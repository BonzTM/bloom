package runtime

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/BonzTM/bloom/internal/config"
	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/db"
	"github.com/BonzTM/bloom/internal/telemetry"
)

const (
	bootstrapTestPassword     = "bootstrap-secret"
	existingBootstrapPassword = "known-existing-password"
)

type bootstrapTestClock struct{}

func (bootstrapTestClock) Now() time.Time {
	return time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)
}

func TestBootstrapAdminSQLite(t *testing.T) {
	dsn := "file:" + t.TempDir() +
		"/bloom.db?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"
	pool := openBootstrapTestDatabase(t, config.DriverSQLite, dsn)
	runBootstrapAdminEngineSuite(t, pool, config.DriverSQLite, dsn)
}

func runBootstrapAdminEngineSuite(t *testing.T, pool *sql.DB, driver config.Driver, dsn string) {
	t.Helper()
	t.Run("creates owner in empty store", func(t *testing.T) {
		testBootstrapCreation(t, pool, driver)
	})
	t.Run("configured username already exists", func(t *testing.T) {
		testBootstrapNoOp(t, pool, driver)
	})
	t.Run("missing role rolls back", func(t *testing.T) {
		testBootstrapMissingRoleRollback(t, pool, driver)
	})
	t.Run("independent contenders converge", func(t *testing.T) {
		testBootstrapRace(t, pool, driver, dsn)
	})
	t.Run("weak password fails", func(t *testing.T) {
		testBootstrapWeakPassword(t, pool, driver)
	})
	t.Run("unset password disables bootstrap", func(t *testing.T) {
		testBootstrapDisabled(t, pool, driver)
	})
	t.Run("custom username", func(t *testing.T) {
		testBootstrapCustomUsername(t, pool, driver)
	})
}

func testBootstrapCreation(t *testing.T, pool *sql.DB, driver config.Driver) {
	t.Helper()
	resetBootstrapAccounts(t, pool)
	logs, audits := runBootstrap(t, pool, driver, bootstrapConfig("admin", bootstrapTestPassword))
	account := assertBootstrapAccount(t, pool, driver, "admin", bootstrapTestPassword)
	assertOwnerRole(t, pool, driver, account.ID)
	assertBootstrapOutputs(t, logs, audits, "admin", bootstrapTestPassword)
}

func testBootstrapNoOp(t *testing.T, pool *sql.DB, driver config.Driver) {
	t.Helper()
	resetBootstrapAccounts(t, pool)
	want := createExistingAccount(t, pool, driver, "admin", existingBootstrapPassword)
	logs, audits := runBootstrap(t, pool, driver, bootstrapConfig("admin", bootstrapTestPassword))
	got := getBootstrapAccount(t, pool, driver, "admin")
	if got.ID != want.ID || got.PasswordHash == nil || *got.PasswordHash != *want.PasswordHash {
		t.Fatalf("configured account changed: got=%+v want=%+v", got, want)
	}
	assertNoRoles(t, pool, driver, got.ID)
	if audits != "" || strings.Count(logs, "remove BLOOM_BOOTSTRAP_PASSWORD") != 1 {
		t.Fatalf("no-op outputs: logs=%s audits=%s", logs, audits)
	}
}

func testBootstrapMissingRoleRollback(t *testing.T, pool *sql.DB, driver config.Driver) {
	t.Helper()
	resetBootstrapAccounts(t, pool)
	account := newBootstrapTestAccount(t, "admin")
	created, err := newBootstrapStore(t, pool, driver).CreateFirstAccountWithRole(
		t.Context(), account, "missing-role",
	)
	if created || !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("CreateFirstAccountWithRole = %t, %v; want false, ErrNotFound", created, err)
	}
	assertBootstrapRowCount(t, pool, "SELECT COUNT(*) FROM accounts", 0)
	assertBootstrapRowCount(t, pool, "SELECT COUNT(*) FROM account_roles", 0)
}

func testBootstrapRace(t *testing.T, pool *sql.DB, driver config.Driver, dsn string) {
	t.Helper()
	resetBootstrapAccounts(t, pool)
	assertBootstrapRace(t, pool, driver, dsn)
}

func testBootstrapWeakPassword(t *testing.T, pool *sql.DB, driver config.Driver) {
	t.Helper()
	resetBootstrapAccounts(t, pool)
	logs, audits, err := callBootstrap(t, pool, driver, bootstrapConfig("admin", "too-short"))
	if !errors.Is(err, core.ErrPasswordTooShort) || err.Error() != core.ErrPasswordTooShort.Error() {
		t.Fatalf("bootstrap weak password = %v, want %q", err, core.ErrPasswordTooShort)
	}
	if strings.Contains(logs+audits+err.Error(), "too-short") {
		t.Fatalf("weak-password failure leaked password: logs=%s audits=%s error=%v", logs, audits, err)
	}
	assertBootstrapFailureAudit(t, audits, "invalid_password")
	assertAccountMissing(t, pool, driver, "admin")
}

func testBootstrapDisabled(t *testing.T, pool *sql.DB, driver config.Driver) {
	t.Helper()
	resetBootstrapAccounts(t, pool)
	logs, audits := runBootstrap(t, pool, driver, bootstrapConfig("admin", ""))
	assertAccountMissing(t, pool, driver, "admin")
	if logs != "" || audits != "" {
		t.Fatalf("disabled outputs: logs=%s audits=%s", logs, audits)
	}
}

func testBootstrapCustomUsername(t *testing.T, pool *sql.DB, driver config.Driver) {
	t.Helper()
	resetBootstrapAccounts(t, pool)
	runBootstrap(t, pool, driver, bootstrapConfig("operator", bootstrapTestPassword))
	assertBootstrapAccount(t, pool, driver, "operator", bootstrapTestPassword)
}

func openBootstrapTestDatabase(t *testing.T, driver config.Driver, dsn string) *sql.DB {
	t.Helper()
	pool, err := db.Open(t.Context(), config.DatabaseConfig{
		Driver: driver, DSN: dsn, MaxOpenConns: 5, MaxIdleConns: 5,
		ConnMaxLifetime: time.Minute, ConnMaxIdleTime: time.Minute,
	}, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = pool.Close() })
	if err := db.Migrate(t.Context(), pool, driver); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return pool
}

func resetBootstrapAccounts(t *testing.T, pool *sql.DB) {
	t.Helper()
	if _, err := pool.ExecContext(t.Context(), "DELETE FROM accounts"); err != nil {
		t.Fatalf("delete accounts: %v", err)
	}
}

func bootstrapConfig(username, password string) config.BootstrapConfig {
	return config.BootstrapConfig{Username: username, Password: config.NewSecret([]byte(password))}
}

func runBootstrap(t *testing.T, pool *sql.DB, driver config.Driver, cfg config.BootstrapConfig) (string, string) {
	t.Helper()
	logs, audits, err := callBootstrap(t, pool, driver, cfg)
	if err != nil {
		t.Fatalf("bootstrapAdmin: %v", err)
	}
	return logs, audits
}

func callBootstrap(
	t *testing.T,
	pool *sql.DB,
	driver config.Driver,
	cfg config.BootstrapConfig,
) (string, string, error) {
	t.Helper()
	store, err := db.NewBootstrapAccountStore(pool, driver)
	if err != nil {
		t.Fatalf("NewBootstrapAccountStore: %v", err)
	}
	var logs, audits strings.Builder
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	audit := telemetry.NewAuditLogger(&audits, bootstrapTestClock{})
	err = bootstrapAdmin(t.Context(), cfg, store, logger, audit, bootstrapTestClock{})
	return logs.String(), audits.String(), err
}

func createExistingAccount(
	t *testing.T, pool *sql.DB, driver config.Driver, username, password string,
) core.Account {
	t.Helper()
	store, _, err := db.NewAccountStores(pool, driver)
	if err != nil {
		t.Fatalf("NewAccountStores: %v", err)
	}
	account := newBootstrapTestAccountWithPassword(t, username, password)
	if err := store.CreateAccount(t.Context(), account); err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	return account
}

func newBootstrapTestAccount(t *testing.T, username string) core.Account {
	t.Helper()
	return newBootstrapTestAccountWithPassword(t, username, bootstrapTestPassword)
}

func newBootstrapTestAccountWithPassword(t *testing.T, username, password string) core.Account {
	t.Helper()
	id, err := core.NewID()
	if err != nil {
		t.Fatalf("NewID: %v", err)
	}
	hash, err := core.HashPassword(password)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	return core.Account{ID: id, Username: username, PasswordHash: &hash, CreatedAt: bootstrapTestClock{}.Now()}
}

func assertBootstrapAccount(t *testing.T, pool *sql.DB, driver config.Driver, username, password string) core.Account {
	t.Helper()
	account := getBootstrapAccount(t, pool, driver, username)
	if account.PasswordHash == nil {
		t.Fatalf("GetAccountByUsername(%q) has no password hash", username)
	}
	matched, err := core.VerifyPassword(*account.PasswordHash, password)
	if err != nil || !matched {
		t.Fatalf("VerifyPassword = %v, %v", matched, err)
	}
	return account
}

func getBootstrapAccount(t *testing.T, pool *sql.DB, driver config.Driver, username string) core.Account {
	t.Helper()
	store, _, err := db.NewAccountStores(pool, driver)
	if err != nil {
		t.Fatalf("NewAccountStores: %v", err)
	}
	account, err := store.GetAccountByUsername(t.Context(), username)
	if err != nil {
		t.Fatalf("GetAccountByUsername(%q) = %+v, %v", username, account, err)
	}
	return account
}

func assertOwnerRole(t *testing.T, pool *sql.DB, driver config.Driver, accountID string) {
	t.Helper()
	authorizer, _, err := db.NewAuthorizationStores(pool, driver)
	if err != nil {
		t.Fatalf("NewAuthorizationStores: %v", err)
	}
	snapshot, err := authorizer.Snapshot(t.Context(), accountID)
	if err != nil || !slices.Equal(snapshot.RoleNames, []string{"owner"}) {
		t.Fatalf("owner snapshot = %+v, %v", snapshot, err)
	}
}

func assertNoRoles(t *testing.T, pool *sql.DB, driver config.Driver, accountID string) {
	t.Helper()
	authorizer, _, err := db.NewAuthorizationStores(pool, driver)
	if err != nil {
		t.Fatalf("NewAuthorizationStores: %v", err)
	}
	snapshot, err := authorizer.Snapshot(t.Context(), accountID)
	if err != nil || len(snapshot.RoleNames) != 0 {
		t.Fatalf("roleless snapshot = %+v, %v", snapshot, err)
	}
}

func assertAccountMissing(t *testing.T, pool *sql.DB, driver config.Driver, username string) {
	t.Helper()
	store, _, err := db.NewAccountStores(pool, driver)
	if err != nil {
		t.Fatalf("NewAccountStores: %v", err)
	}
	if _, err := store.GetAccountByUsername(t.Context(), username); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("GetAccountByUsername(%q) = %v, want ErrNotFound", username, err)
	}
}

func assertBootstrapOutputs(t *testing.T, logs, audits, username, password string) {
	t.Helper()
	if strings.Count(logs, `"msg":"admin account created"`) != 1 || !strings.Contains(logs, `"username":"`+username+`"`) ||
		strings.Count(logs, "remove BLOOM_BOOTSTRAP_PASSWORD") != 1 {
		t.Errorf("bootstrap logs = %s", logs)
	}
	if !strings.Contains(audits, `"action":"account.create_admin"`) ||
		!strings.Contains(audits, `"action":"role.assign"`) || !strings.Contains(audits, `"result":"success"`) ||
		!strings.Contains(audits, `"source":"startup"`) {
		t.Errorf("bootstrap audits = %s", audits)
	}
	if strings.Contains(logs+audits, password) {
		t.Fatalf("bootstrap output leaked password: logs=%s audits=%s", logs, audits)
	}
}

func assertBootstrapFailureAudit(t *testing.T, audits, reason string) {
	t.Helper()
	for _, field := range []string{
		`"action":"account.create_admin"`, `"actor":"system"`, `"result":"failure"`,
		`"reason":"` + reason + `"`, `"source":"startup"`,
	} {
		if !strings.Contains(audits, field) {
			t.Errorf("bootstrap failure audit lacks %s: %s", field, audits)
		}
	}
}

func assertBootstrapRace(t *testing.T, pool *sql.DB, driver config.Driver, dsn string) {
	t.Helper()
	ready := make(chan struct{}, 2)
	start := make(chan struct{})
	pools := []*sql.DB{
		openBootstrapReplica(t, driver, dsn),
		openBootstrapReplica(t, driver, dsn),
	}
	stores := []core.BootstrapAccountStore{
		newBootstrapStore(t, pools[0], driver),
		newBootstrapStore(t, pools[1], driver),
	}
	errs := runConcurrentBootstrap(t.Context(), stores, ready, start)
	for err := range errs {
		if err != nil {
			t.Errorf("concurrent bootstrap = %v", err)
		}
	}
	var count int
	if err := pool.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM accounts").Scan(&count); err != nil || count != 1 {
		t.Fatalf("account count = %d, %v; want 1", count, err)
	}
	if err := pool.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM account_roles ar JOIN roles r ON r.id = ar.role_id WHERE r.name = 'owner'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("owner assignment count = %d, %v; want 1", count, err)
	}
}

func assertBootstrapRowCount(t *testing.T, pool *sql.DB, query string, want int) {
	t.Helper()
	var got int
	if err := pool.QueryRowContext(t.Context(), query).Scan(&got); err != nil || got != want {
		t.Fatalf("row count = %d, %v; want %d", got, err, want)
	}
}

func newBootstrapStore(t *testing.T, pool *sql.DB, driver config.Driver) core.BootstrapAccountStore {
	t.Helper()
	store, err := db.NewBootstrapAccountStore(pool, driver)
	if err != nil {
		t.Fatalf("NewBootstrapAccountStore: %v", err)
	}
	return store
}

func openBootstrapReplica(t *testing.T, driver config.Driver, dsn string) *sql.DB {
	t.Helper()
	pool, err := db.Open(t.Context(), config.DatabaseConfig{
		Driver: driver, DSN: dsn, MaxOpenConns: 2, MaxIdleConns: 2,
		ConnMaxLifetime: time.Minute, ConnMaxIdleTime: time.Minute,
	}, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("open bootstrap replica: %v", err)
	}
	t.Cleanup(func() { _ = pool.Close() })
	return pool
}

func runConcurrentBootstrap(
	ctx context.Context, stores []core.BootstrapAccountStore, ready, start chan struct{},
) <-chan error {
	usernames := []string{"admin-one", "admin-two"}
	errs := make(chan error, len(stores))
	var wait sync.WaitGroup
	for index := range 2 {
		wait.Go(func() {
			ready <- struct{}{}
			<-start
			logger := slog.New(slog.DiscardHandler)
			audit := telemetry.NewAuditLogger(&strings.Builder{}, bootstrapTestClock{})
			errs <- bootstrapAdmin(ctx, bootstrapConfig(usernames[index], bootstrapTestPassword), stores[index], logger, audit, bootstrapTestClock{})
		})
	}
	<-ready
	<-ready
	close(start)
	wait.Wait()
	close(errs)
	return errs
}

type failingBootstrapStore struct{ err error }

func (s failingBootstrapStore) CreateFirstAccountWithRole(context.Context, core.Account, string) (bool, error) {
	return false, s.err
}

type failingBootstrapAudit struct{ err error }

func (a failingBootstrapAudit) Emit(context.Context, telemetry.AuditEvent) error { return a.err }

func TestBootstrapAdminAuditFailureDoesNotReplaceStartupError(t *testing.T) {
	startupErr := errors.New("database unavailable")
	auditErr := errors.New("audit unavailable")
	var logs strings.Builder
	err := bootstrapAdmin(t.Context(), bootstrapConfig("admin", bootstrapTestPassword),
		failingBootstrapStore{err: startupErr}, slog.New(slog.NewJSONHandler(&logs, nil)),
		failingBootstrapAudit{err: auditErr}, bootstrapTestClock{})
	if !errors.Is(err, startupErr) || errors.Is(err, auditErr) {
		t.Fatalf("bootstrap error = %v, want startup error only", err)
	}
	if strings.Count(logs.String(), `"msg":"write audit event"`) != 1 ||
		!strings.Contains(logs.String(), `"action":["account.create_admin"]`) {
		t.Fatalf("audit sink failure log = %s", logs.String())
	}
}

func TestBootstrapAdminPersistenceFailureAudit(t *testing.T) {
	startupErr := errors.New("count accounts")
	var logs, audits strings.Builder
	err := bootstrapAdmin(t.Context(), bootstrapConfig("admin", bootstrapTestPassword),
		failingBootstrapStore{err: startupErr}, slog.New(slog.NewJSONHandler(&logs, nil)),
		telemetry.NewAuditLogger(&audits, bootstrapTestClock{}), bootstrapTestClock{})
	if !errors.Is(err, startupErr) {
		t.Fatalf("bootstrap error = %v, want storage error", err)
	}
	assertBootstrapFailureAudit(t, audits.String(), "internal_error")
}
