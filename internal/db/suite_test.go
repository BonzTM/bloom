package db_test

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alexedwards/scs/v2"
	"github.com/pressly/goose/v3"

	"github.com/BonzTM/bloom/internal/config"
	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/db"
	"github.com/BonzTM/bloom/internal/testutil"
)

// runEngineSuite is the ONE parity suite both engines must pass (ADR 0004
// item 6): migrate up, down to zero, up again, then exercise every
// core.AccountStore method against the freshly migrated schema. The SQLite
// test runs it in-memory on every `make verify`; the PostgreSQL test runs it
// under the integration build tag against a live server.
func runEngineSuite(t *testing.T, pool *sql.DB, driver config.Driver) {
	t.Helper()
	ctx := context.Background()
	t.Run("username migration", func(t *testing.T) {
		testUsernameMigration(t, pool, driver)
	})
	t.Run("role migration preserves existing role-less accounts", func(t *testing.T) {
		testRoleMigrationNotice(t, pool, driver)
	})

	// up / down / up: forward, reverse, and re-apply all succeed.
	if err := db.Migrate(ctx, pool, driver); err != nil {
		t.Fatalf("Migrate (first up): %v", err)
	}
	assertUsernameMigrationVersions(t, pool, 3)
	if err := db.MigrateDownAll(ctx, pool, driver); err != nil {
		t.Fatalf("MigrateDownAll: %v", err)
	}
	assertUsernameMigrationVersions(t, pool, 0)
	if err := db.Migrate(ctx, pool, driver); err != nil {
		t.Fatalf("Migrate (second up): %v", err)
	}
	assertUsernameMigrationVersions(t, pool, 3)

	store, localIdentities, err := db.NewAccountStores(pool, driver)
	if err != nil {
		t.Fatalf("NewAccountStores: %v", err)
	}
	authorizer, roles, adminStore := newAuthorizationTestStores(t, pool, driver)
	cleanupMetrics := &sessionCleanupMetrics{}
	clock := testutil.NewFakeClock(time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC))
	logger := slog.New(slog.DiscardHandler)
	sessionStore, err := db.NewSessionStore(pool, driver, cleanupMetrics, logger, clock)
	if err != nil {
		t.Fatalf("NewSessionStore: %v", err)
	}

	t.Run("create and get round-trip", func(t *testing.T) { testCreateGet(t, store) })
	t.Run("duplicate id", func(t *testing.T) { testDuplicateID(t, store) })
	t.Run("duplicate username", func(t *testing.T) { testDuplicateUsername(t, store) })
	t.Run("PRECIS username identity", func(t *testing.T) { testPRECISUsernameIdentity(t, store) })
	t.Run("raw username constraints", func(t *testing.T) { testRawUsernameConstraints(t, pool, driver) })
	t.Run("not found", func(t *testing.T) { testNotFound(t, store) })
	t.Run("timestamp normalized to utc microseconds", func(t *testing.T) { testTimestampNormalization(t, store) })
	t.Run("session store round-trip", func(t *testing.T) { testSessionStore(t, pool, driver, sessionStore, clock) })
	t.Run("stale session commits cannot recreate deleted tokens", func(t *testing.T) {
		testStaleSessionCommits(t, pool, sessionStore)
	})
	t.Run("password hash update", func(t *testing.T) { testPasswordHashUpdate(t, store, localIdentities) })
	t.Run("password hash length constraint", func(t *testing.T) { testPasswordHashLengthConstraint(t, store) })
	runAuthorizationEngineTests(t, pool, driver, store, adminStore, authorizer, roles)
}

func runAuthorizationEngineTests(
	t *testing.T,
	pool *sql.DB,
	driver config.Driver,
	store core.AccountStore,
	adminStore core.AdminAccountStore,
	authorizer core.Authorizer,
	roles core.RoleReader,
) {
	t.Helper()
	t.Run("built-in role authorization", func(t *testing.T) {
		testBuiltInRoleAuthorization(t, store, adminStore, authorizer, roles)
	})
	t.Run("role foreign keys and cascades", func(t *testing.T) {
		testRoleForeignKeysAndCascades(t, pool, driver, store)
	})
	t.Run("foreign key schema contract", func(t *testing.T) { testForeignKeySchema(t, pool, driver) })
}

func newAuthorizationTestStores(
	t *testing.T,
	pool *sql.DB,
	driver config.Driver,
) (core.Authorizer, core.RoleReader, core.AdminAccountStore) {
	t.Helper()
	authorizer, roles, err := db.NewAuthorizationStores(pool, driver)
	if err != nil {
		t.Fatalf("NewAuthorizationStores: %v", err)
	}
	adminStore, err := db.NewAdminAccountStore(pool, driver)
	if err != nil {
		t.Fatalf("NewAdminAccountStore: %v", err)
	}
	return authorizer, roles, adminStore
}

func testRoleMigrationNotice(t *testing.T, pool *sql.DB, driver config.Driver) {
	t.Helper()
	ctx := context.Background()
	if err := db.Migrate(ctx, pool, driver); err != nil {
		t.Fatalf("prepare role migration: %v", err)
	}
	if err := db.MigrateDownTo(ctx, pool, driver, 5); err != nil {
		t.Fatalf("roll back role migration: %v", err)
	}
	store, _, err := db.NewAccountStores(pool, driver)
	if err != nil {
		t.Fatalf("NewAccountStores: %v", err)
	}
	account := core.Account{ID: mustID(t), Username: "legacy-" + mustID(t), CreatedAt: authorizationFixtureTime()}
	if err := store.CreateAccount(ctx, account); err != nil {
		t.Fatalf("create legacy role-less account: %v", err)
	}
	var logs strings.Builder
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	if err := db.MigrateWithLogger(ctx, pool, driver, logger); err != nil {
		t.Fatalf("apply role migration: %v", err)
	}
	if !strings.Contains(logs.String(), "existing accounts were left without roles") {
		t.Fatalf("migration notice = %q", logs.String())
	}
	var assignments int
	if err := pool.QueryRowContext(ctx, "SELECT COUNT(*) FROM account_roles WHERE account_id = $1", account.ID).Scan(&assignments); err != nil {
		t.Fatalf("count legacy role assignments: %v", err)
	}
	if assignments != 0 {
		t.Fatalf("legacy account received %d guessed roles", assignments)
	}
	logs.Reset()
	if err := db.MigrateWithLogger(ctx, pool, driver, logger); err != nil {
		t.Fatalf("idempotent role migration: %v", err)
	}
	if logs.Len() != 0 {
		t.Fatalf("already-applied migration logged notice: %q", logs.String())
	}
	assertSeedCount(t, pool, "roles", 2)
	assertSeedCount(t, pool, "role_permissions", 13)
	if _, err := pool.ExecContext(ctx, "DELETE FROM accounts WHERE id = $1", account.ID); err != nil {
		t.Fatalf("delete legacy role-less account: %v", err)
	}
	if err := db.MigrateDownAll(ctx, pool, driver); err != nil {
		t.Fatalf("clean role migration fixture: %v", err)
	}
}

func assertSeedCount(t *testing.T, pool *sql.DB, table string, want int) {
	t.Helper()
	query := map[string]string{
		"roles":            "SELECT COUNT(*) FROM roles",
		"role_permissions": "SELECT COUNT(*) FROM role_permissions",
	}[table]
	var got int
	if err := pool.QueryRowContext(context.Background(), query).Scan(&got); err != nil || got != want {
		t.Fatalf("%s seed count = %d, %v; want %d", table, got, err, want)
	}
}

func testBuiltInRoleAuthorization(
	t *testing.T,
	accounts core.AccountStore,
	admins core.AdminAccountStore,
	authorizer core.Authorizer,
	roles core.RoleReader,
) {
	t.Helper()
	ctx := context.Background()
	owner := core.Account{ID: mustID(t), Username: "owner-" + mustID(t), CreatedAt: authorizationFixtureTime()}
	member := core.Account{ID: mustID(t), Username: "member-" + mustID(t), CreatedAt: authorizationFixtureTime()}
	roleless := core.Account{ID: mustID(t), Username: "roleless-" + mustID(t), CreatedAt: authorizationFixtureTime()}
	if err := admins.CreateAccountWithRole(ctx, owner, "owner"); err != nil {
		t.Fatalf("create owner: %v", err)
	}
	if err := admins.CreateAccountWithRole(ctx, member, "member"); err != nil {
		t.Fatalf("create member: %v", err)
	}
	if err := accounts.CreateAccount(ctx, roleless); err != nil {
		t.Fatalf("create role-less account: %v", err)
	}
	failed := core.Account{ID: mustID(t), Username: "failed-" + mustID(t), CreatedAt: authorizationFixtureTime()}
	if err := admins.CreateAccountWithRole(ctx, failed, "missing-role"); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("create with missing role = %v, want ErrNotFound", err)
	}
	if _, err := accounts.GetAccount(ctx, failed.ID); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("failed role assignment persisted account: %v", err)
	}
	wantOwner := ownerPermissions()
	assertBuiltInPermissions(t, authorizer, owner.ID, member.ID, roleless.ID, wantOwner)
	assertSeededRoles(t, roles, wantOwner)
	assertRolePagination(t, roles)
	assertRoleGrant(t, admins, roleless.ID)
}

func ownerPermissions() []core.Permission {
	wantOwner := make([]core.Permission, 0, len(core.PermissionCatalog()))
	for _, definition := range core.PermissionCatalog() {
		wantOwner = append(wantOwner, definition.ID)
	}
	slices.Sort(wantOwner)
	return wantOwner
}

func authorizationFixtureTime() time.Time {
	return time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
}

func assertBuiltInPermissions(
	t *testing.T,
	authorizer core.Authorizer,
	ownerID, memberID, rolelessID string,
	wantOwner []core.Permission,
) {
	t.Helper()
	assertAccountPermissions(t, authorizer, ownerID, wantOwner)
	assertAccountPermissions(t, authorizer, memberID, []core.Permission{
		core.PermissionRequestsCreate, core.PermissionRequestsReadOwn, core.PermissionStatsReadOwn,
	})
	assertAccountPermissions(t, authorizer, rolelessID, nil)
	assertAuthorizationSnapshot(t, authorizer, ownerID, []string{"owner"}, wantOwner)
	assertAuthorizationSnapshot(t, authorizer, memberID, []string{"member"}, []core.Permission{
		core.PermissionRequestsCreate, core.PermissionRequestsReadOwn, core.PermissionStatsReadOwn,
	})
	assertAuthorizationSnapshot(t, authorizer, rolelessID, nil, nil)
}

func assertAuthorizationSnapshot(
	t *testing.T,
	authorizer core.Authorizer,
	accountID string,
	wantRoles []string,
	wantPermissions []core.Permission,
) {
	t.Helper()
	got, err := authorizer.Snapshot(context.Background(), accountID)
	if err != nil {
		t.Fatalf("Snapshot(%q): %v", accountID, err)
	}
	if len(wantRoles) == 0 {
		wantRoles = []string{}
	}
	if len(wantPermissions) == 0 {
		wantPermissions = []core.Permission{}
	}
	if !slices.Equal(got.RoleNames, wantRoles) || !slices.Equal(got.Permissions, wantPermissions) {
		t.Fatalf("Snapshot(%q) = %+v, want roles %v permissions %v",
			accountID, got, wantRoles, wantPermissions)
	}
}

func assertSeededRoles(t *testing.T, roles core.RoleReader, wantOwner []core.Permission) {
	t.Helper()
	listed, err := roles.ListRoles(context.Background(), "", 10)
	if err != nil {
		t.Fatalf("ListRoles: %v", err)
	}
	wantRoles := []core.Role{
		{
			ID: "00000000-0000-4000-8000-000000000002", Name: "member",
			Description: "Access to owned data and creating requests.", BuiltIn: true,
			CreatedAt: time.Unix(0, 0).UTC(),
			Permissions: []core.Permission{
				core.PermissionRequestsCreate, core.PermissionRequestsReadOwn, core.PermissionStatsReadOwn,
			},
		},
		{
			ID: "00000000-0000-4000-8000-000000000001", Name: "owner",
			Description: "Full access to every Bloom permission.", BuiltIn: true,
			CreatedAt: time.Unix(0, 0).UTC(), Permissions: wantOwner,
		},
	}
	if !reflect.DeepEqual(listed, wantRoles) {
		t.Fatalf("roles = %+v, want %+v", listed, wantRoles)
	}
}

func assertRolePagination(t *testing.T, roles core.RoleReader) {
	t.Helper()
	firstPage, err := roles.ListRoles(context.Background(), "", 1)
	if err != nil || len(firstPage) != 1 || firstPage[0].Name != "member" {
		t.Fatalf("first role page = %+v, %v", firstPage, err)
	}
	secondPage, err := roles.ListRoles(context.Background(), firstPage[0].Name, 1)
	if err != nil || len(secondPage) != 1 || secondPage[0].Name != "owner" {
		t.Fatalf("second role page = %+v, %v", secondPage, err)
	}
}

func assertRoleGrant(t *testing.T, admins core.AdminAccountStore, rolelessID string) {
	t.Helper()
	assigned, err := admins.GrantRole(context.Background(), rolelessID, "member")
	if err != nil || !assigned {
		t.Fatalf("GrantRole(first) = %v, %v; want assigned", assigned, err)
	}
	assigned, err = admins.GrantRole(context.Background(), rolelessID, "member")
	if err != nil || assigned {
		t.Fatalf("GrantRole(idempotent) = %v, %v; want already held", assigned, err)
	}
	if _, err := admins.GrantRole(context.Background(), mustID(t), "member"); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("GrantRole(unknown account) = %v, want ErrNotFound", err)
	}
	if _, err := admins.GrantRole(context.Background(), rolelessID, "missing-role"); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("GrantRole(unknown role) = %v, want ErrNotFound", err)
	}
}

func assertAccountPermissions(t *testing.T, authorizer core.Authorizer, accountID string, want []core.Permission) {
	t.Helper()
	got, err := authorizer.Permissions(context.Background(), accountID)
	if err != nil {
		t.Fatalf("Permissions(%q): %v", accountID, err)
	}
	if len(want) == 0 {
		want = []core.Permission{}
	}
	if !slices.Equal(got, want) {
		t.Fatalf("Permissions(%q) = %v, want %v", accountID, got, want)
	}
}

func testRoleForeignKeysAndCascades(
	t *testing.T,
	pool *sql.DB,
	driver config.Driver,
	accounts core.AccountStore,
) {
	t.Helper()
	ctx := context.Background()
	account := core.Account{ID: mustID(t), Username: "fk-" + mustID(t), CreatedAt: authorizationFixtureTime()}
	if err := accounts.CreateAccount(ctx, account); err != nil {
		t.Fatalf("create foreign-key account: %v", err)
	}
	ownerRoleID := "00000000-0000-4000-8000-000000000001"
	assertConstraintFailure(t, pool,
		"INSERT INTO account_roles (account_id, role_id) VALUES ($1, $2)", mustID(t), ownerRoleID)
	assertConstraintFailure(t, pool,
		"INSERT INTO account_roles (account_id, role_id) VALUES ($1, $2)", account.ID, mustID(t))
	assertConstraintFailure(t, pool,
		"INSERT INTO role_permissions (role_id, permission) VALUES ($1, $2)", mustID(t), "users.read")

	if _, err := pool.ExecContext(ctx,
		"INSERT INTO account_roles (account_id, role_id) VALUES ($1, $2)", account.ID, ownerRoleID); err != nil {
		t.Fatalf("assign role for account cascade: %v", err)
	}
	if _, err := pool.ExecContext(ctx, "DELETE FROM accounts WHERE id = $1", account.ID); err != nil {
		t.Fatalf("delete account for cascade: %v", err)
	}
	assertRowCount(t, pool, "SELECT COUNT(*) FROM account_roles WHERE account_id = $1", account.ID, 0)

	testRoleDeleteCascade(t, pool, driver, accounts)
}

func testRoleDeleteCascade(t *testing.T, pool *sql.DB, driver config.Driver, accounts core.AccountStore) {
	t.Helper()
	ctx := context.Background()
	roleID := mustID(t)
	account := core.Account{ID: mustID(t), Username: "cascade-" + mustID(t), CreatedAt: authorizationFixtureTime()}
	if err := accounts.CreateAccount(ctx, account); err != nil {
		t.Fatalf("create role-cascade account: %v", err)
	}
	if _, err := pool.ExecContext(ctx,
		"INSERT INTO roles (id, name, description, built_in, created_at) VALUES ($1, $2, $3, $4, $5)",
		roleID, "cascade-"+roleID, "cascade fixture", false, migrationCreatedAt(driver)); err != nil {
		t.Fatalf("create role cascade fixture: %v", err)
	}
	if _, err := pool.ExecContext(ctx,
		"INSERT INTO role_permissions (role_id, permission) VALUES ($1, $2)", roleID, "users.read"); err != nil {
		t.Fatalf("create permission cascade fixture: %v", err)
	}
	if _, err := pool.ExecContext(ctx,
		"INSERT INTO account_roles (account_id, role_id) VALUES ($1, $2)", account.ID, roleID); err != nil {
		t.Fatalf("create assignment cascade fixture: %v", err)
	}
	if _, err := pool.ExecContext(ctx, "DELETE FROM roles WHERE id = $1", roleID); err != nil {
		t.Fatalf("delete role for cascade: %v", err)
	}
	assertRowCount(t, pool, "SELECT COUNT(*) FROM role_permissions WHERE role_id = $1", roleID, 0)
	assertRowCount(t, pool, "SELECT COUNT(*) FROM account_roles WHERE role_id = $1", roleID, 0)
}

func assertConstraintFailure(t *testing.T, pool *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := pool.ExecContext(context.Background(), query, args...); err == nil {
		t.Fatalf("constraint query succeeded: %s", query)
	}
}

func assertRowCount(t *testing.T, pool *sql.DB, query string, arg any, want int) {
	t.Helper()
	var got int
	if err := pool.QueryRowContext(context.Background(), query, arg).Scan(&got); err != nil || got != want {
		t.Fatalf("row count = %d, %v; want %d", got, err, want)
	}
}

func testForeignKeySchema(t *testing.T, pool *sql.DB, driver config.Driver) {
	t.Helper()
	query := `SELECT m.name, fk."from", fk."table", fk."to", fk.on_delete
FROM sqlite_master m JOIN pragma_foreign_key_list(m.name) fk
WHERE m.type = 'table' ORDER BY m.name, fk."from"`
	if driver == config.DriverPostgres {
		query = `SELECT tc.table_name, kcu.column_name, ccu.table_name, ccu.column_name, rc.delete_rule
FROM information_schema.table_constraints tc
JOIN information_schema.key_column_usage kcu ON tc.constraint_name = kcu.constraint_name AND tc.constraint_schema = kcu.constraint_schema
JOIN information_schema.referential_constraints rc ON tc.constraint_name = rc.constraint_name AND tc.constraint_schema = rc.constraint_schema
JOIN information_schema.constraint_column_usage ccu ON rc.unique_constraint_name = ccu.constraint_name AND rc.unique_constraint_schema = ccu.constraint_schema
WHERE tc.constraint_type = 'FOREIGN KEY' AND tc.table_schema = current_schema()
ORDER BY tc.table_name, kcu.column_name`
	}
	rows, err := pool.QueryContext(context.Background(), query)
	if err != nil {
		t.Fatalf("list foreign keys: %v", err)
	}
	defer func() { _ = rows.Close() }()
	got := make([]string, 0, 3)
	for rows.Next() {
		var table, column, target, targetColumn, action string
		if err := rows.Scan(&table, &column, &target, &targetColumn, &action); err != nil {
			t.Fatalf("scan foreign key: %v", err)
		}
		got = append(got, strings.Join([]string{table, column, target, targetColumn, action}, ":"))
	}
	want := []string{
		"account_roles:account_id:accounts:id:CASCADE",
		"account_roles:role_id:roles:id:CASCADE",
		"role_permissions:role_id:roles:id:CASCADE",
	}
	if err := rows.Err(); err != nil || !slices.Equal(got, want) {
		t.Fatalf("foreign keys = %v, %v; want %v (accounts and sessions define none)", got, err, want)
	}
}

func testUsernameMigration(t *testing.T, pool *sql.DB, driver config.Driver) {
	t.Helper()
	t.Run("canonicalizes and restores display values", func(t *testing.T) {
		testUsernameMigrationRoundTrip(t, pool, driver)
	})
	t.Run("rejects PRECIS key collisions atomically", func(t *testing.T) {
		testUsernameMigrationCollision(t, pool, driver)
	})
	t.Run("rejects the shared invalid username set", func(t *testing.T) {
		testUsernameMigrationRejections(t, pool, driver)
	})
	t.Run("rolls data back when version recording fails", func(t *testing.T) {
		testUsernameMigrationVersionFailure(t, pool, driver)
	})
	if err := db.MigrateDownAll(context.Background(), pool, driver); err != nil {
		t.Fatalf("clean up username migration fixture: %v", err)
	}
}

func testUsernameMigrationRoundTrip(t *testing.T, pool *sql.DB, driver config.Driver) {
	t.Helper()
	ctx := context.Background()
	prepareUsernameMigration(t, pool, driver)
	legacy := map[string]string{
		mustID(t): "ＡLICE", mustID(t): "Σigma", mustID(t): "ςigma",
		mustID(t): "Straße", mustID(t): "STRASSE",
	}
	for id, username := range legacy {
		insertLegacyAccount(t, pool, driver, id, username)
	}
	if err := db.Migrate(ctx, pool, driver); err != nil {
		t.Fatalf("canonical username migration: %v", err)
	}
	assertCanonicalUsernameMigration(t, pool, driver, legacy)
	if err := db.MigrateDownTo(ctx, pool, driver, 2); err != nil {
		t.Fatalf("roll back username migration: %v", err)
	}
	for id, original := range legacy {
		if got := rawUsername(t, pool, id); got != original {
			t.Fatalf("restored username = %q, want %q", got, original)
		}
	}
	if err := db.Migrate(ctx, pool, driver); err != nil {
		t.Fatalf("re-apply canonical username migration: %v", err)
	}
	assertCanonicalUsernameMigration(t, pool, driver, legacy)
}

func assertCanonicalUsernameMigration(t *testing.T, pool *sql.DB, driver config.Driver, legacy map[string]string) {
	t.Helper()
	assertMigrationVersion(t, pool, 6)
	assertUsernameMigrationVersions(t, pool, 3)
	for id, original := range legacy {
		want, err := core.UsernameKey(original)
		if err != nil {
			t.Fatalf("UsernameKey(%q): %v", original, err)
		}
		username, key := rawUsernameIdentity(t, pool, id)
		if username != want || key != want {
			t.Fatalf("migrated identity = (%q, %q), want (%q, %q)", username, key, want, want)
		}
	}
	assertUsernameMigrationBackups(t, pool, legacy)
	assertCanonicalUsernameConstraints(t, pool, driver, legacy)
}

func assertUsernameMigrationBackups(t *testing.T, pool *sql.DB, legacy map[string]string) {
	t.Helper()
	var count int
	if err := pool.QueryRowContext(context.Background(),
		"SELECT COUNT(*) FROM account_username_migration_backup",
	).Scan(&count); err != nil || count != len(legacy) {
		t.Fatalf("username backup count = %d, %v; want %d", count, err, len(legacy))
	}
	for id, original := range legacy {
		var got string
		err := pool.QueryRowContext(context.Background(),
			"SELECT original_username FROM account_username_migration_backup WHERE account_id = $1", id,
		).Scan(&got)
		if err != nil || got != original {
			t.Fatalf("username backup = %q, %v; want %q", got, err, original)
		}
	}
}

func assertCanonicalUsernameConstraints(t *testing.T, pool *sql.DB, driver config.Driver, legacy map[string]string) {
	t.Helper()
	created := migrationCreatedAt(driver)
	for id, original := range legacy {
		key, err := core.UsernameKey(original)
		if err != nil {
			t.Fatalf("UsernameKey(%q): %v", original, err)
		}
		assertRawUsernameInsertFails(t, pool, mustID(t), "unique-display-"+id, key, created)
		assertRawUsernameInsertFails(t, pool, mustID(t), key, "unique-key-"+id, created)
		break
	}
	assertRawUsernameInsertFails(t, pool, mustID(t), "missing-key", nil, created)
}

func assertRawUsernameInsertFails(t *testing.T, pool *sql.DB, id, username string, key, created any) {
	t.Helper()
	_, err := pool.ExecContext(context.Background(),
		"INSERT INTO accounts (id, username, username_key, created_at) VALUES ($1, $2, $3, $4)",
		id, username, key, created,
	)
	if err == nil {
		t.Fatalf("raw username insert (%q, %v) bypassed canonical constraints", username, key)
	}
}

func testUsernameMigrationCollision(t *testing.T, pool *sql.DB, driver config.Driver) {
	t.Helper()
	ctx := context.Background()
	prepareUsernameMigration(t, pool, driver)
	firstID := mustID(t)
	secondID := mustID(t)
	insertLegacyAccount(t, pool, driver, firstID, "ＡLICE")
	insertLegacyAccount(t, pool, driver, secondID, "Alice")
	if err := db.Migrate(ctx, pool, driver); err == nil || !strings.Contains(err.Error(), "canonical username collision") {
		t.Fatalf("colliding username migration = %v, want clear collision error", err)
	}
	if first, second := rawUsername(t, pool, firstID), rawUsername(t, pool, secondID); first != "ＡLICE" || second != "Alice" {
		t.Fatalf("failed migration changed usernames to %q and %q", first, second)
	}
	assertMigrationVersion(t, pool, 3)
	assertUsernameMigrationUnchanged(t, pool, firstID)
	assertUsernameMigrationUnchanged(t, pool, secondID)
}

func testUsernameMigrationRejections(t *testing.T, pool *sql.DB, driver config.Driver) {
	t.Helper()
	ctx := context.Background()
	prepareUsernameMigration(t, pool, driver)
	invalid := [...]string{"ali²ce", "שalice", "alice smith", "-alice"}
	for _, username := range invalid {
		if _, err := pool.ExecContext(ctx, "DELETE FROM accounts"); err != nil {
			t.Fatalf("clear migration accounts: %v", err)
		}
		id := mustID(t)
		insertLegacyAccount(t, pool, driver, id, username)
		if err := db.Migrate(ctx, pool, driver); err == nil {
			t.Errorf("migration accepted invalid username %q", username)
		}
		assertMigrationVersion(t, pool, 3)
		assertUsernameMigrationUnchanged(t, pool, id)
		if got := rawUsername(t, pool, id); got != username {
			t.Errorf("failed migration changed username %q to %q", username, got)
		}
	}
}

func testUsernameMigrationVersionFailure(t *testing.T, pool *sql.DB, driver config.Driver) {
	t.Helper()
	ctx := context.Background()
	prepareUsernameMigration(t, pool, driver)
	id := mustID(t)
	insertLegacyAccount(t, pool, driver, id, "ÉLODIE")
	removeFailure := installVersionFailure(t, pool, driver)
	if err := db.Migrate(ctx, pool, driver); err == nil {
		t.Fatal("migration succeeded when version recording was forced to fail")
	}
	assertMigrationVersion(t, pool, 3)
	assertUsernameMigrationUnchanged(t, pool, id)
	if got := rawUsername(t, pool, id); got != "ÉLODIE" {
		t.Fatalf("failed migration changed username to %q", got)
	}
	removeFailure()
	if err := db.Migrate(ctx, pool, driver); err != nil {
		t.Fatalf("migration after removing version failure: %v", err)
	}
	assertMigrationVersion(t, pool, 6)
	if username, key := rawUsernameIdentity(t, pool, id); username != "élodie" || key != "élodie" {
		t.Fatalf("committed identity = (%q, %q), want (élodie, élodie)", username, key)
	}
}

func prepareUsernameMigration(t *testing.T, pool *sql.DB, driver config.Driver) {
	t.Helper()
	ctx := context.Background()
	if err := db.MigrateDownTo(ctx, pool, driver, 2); err != nil {
		t.Fatalf("migrate down to version 2: %v", err)
	}
	if _, err := migrationProvider(t, pool, driver).UpTo(ctx, 3); err != nil {
		t.Fatalf("apply username expansion: %v", err)
	}
	if _, err := pool.ExecContext(ctx, "DELETE FROM accounts"); err != nil {
		t.Fatalf("clear migration accounts: %v", err)
	}
}

func migrationProvider(t *testing.T, pool *sql.DB, driver config.Driver) *goose.Provider {
	t.Helper()
	sub, err := fs.Sub(db.MigrationsFS(), "migrations/"+string(driver))
	if err != nil {
		t.Fatalf("migration filesystem: %v", err)
	}
	dialect := goose.DialectSQLite3
	if driver == config.DriverPostgres {
		dialect = goose.DialectPostgres
	}
	provider, err := goose.NewProvider(dialect, pool, sub)
	if err != nil {
		t.Fatalf("migration provider: %v", err)
	}
	return provider
}

func insertLegacyAccount(t *testing.T, pool *sql.DB, driver config.Driver, id, username string) {
	t.Helper()
	if _, err := pool.ExecContext(context.Background(),
		"INSERT INTO accounts (id, username, created_at) VALUES ($1, $2, $3)", id, username, migrationCreatedAt(driver),
	); err != nil {
		t.Fatalf("insert legacy account %q: %v", username, err)
	}
}

func migrationCreatedAt(driver config.Driver) any {
	createdAt := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	if driver == config.DriverSQLite {
		return createdAt.Format("2006-01-02T15:04:05.000000Z")
	}
	return createdAt
}

func rawUsername(t *testing.T, pool *sql.DB, id string) string {
	t.Helper()
	var username string
	if err := pool.QueryRowContext(context.Background(), "SELECT username FROM accounts WHERE id = $1", id).Scan(&username); err != nil {
		t.Fatalf("select raw username: %v", err)
	}
	return username
}

func rawUsernameIdentity(t *testing.T, pool *sql.DB, id string) (string, string) {
	t.Helper()
	var username, key string
	err := pool.QueryRowContext(context.Background(),
		"SELECT username, username_key FROM accounts WHERE id = $1", id,
	).Scan(&username, &key)
	if err != nil {
		t.Fatalf("select raw username identity: %v", err)
	}
	return username, key
}

func assertMigrationVersion(t *testing.T, pool *sql.DB, want int64) {
	t.Helper()
	var got int64
	err := pool.QueryRowContext(context.Background(),
		"SELECT COALESCE(MAX(version_id), 0) FROM goose_db_version WHERE is_applied = TRUE",
	).Scan(&got)
	if err != nil || got != want {
		t.Fatalf("migration version = %d, %v; want %d", got, err, want)
	}
}

func assertUsernameMigrationVersions(t *testing.T, pool *sql.DB, want int) {
	t.Helper()
	var got int
	err := pool.QueryRowContext(context.Background(),
		"SELECT COUNT(*) FROM goose_db_version WHERE is_applied = TRUE AND version_id IN (3, 4, 5)",
	).Scan(&got)
	if err != nil || got != want {
		t.Fatalf("applied username migration versions = %d, %v; want %d", got, err, want)
	}
}

func assertUsernameMigrationUnchanged(t *testing.T, pool *sql.DB, id string) {
	t.Helper()
	var key sql.NullString
	if err := pool.QueryRowContext(context.Background(), "SELECT username_key FROM accounts WHERE id = $1", id).Scan(&key); err != nil {
		t.Fatalf("select username key: %v", err)
	}
	if key.Valid {
		t.Errorf("failed migration stored username key %q", key.String)
	}
	var backups int
	if err := pool.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM account_username_migration_backup").Scan(&backups); err != nil {
		t.Fatalf("count username backups: %v", err)
	}
	if backups != 0 {
		t.Errorf("failed migration stored %d username backups", backups)
	}
}

func installVersionFailure(t *testing.T, pool *sql.DB, driver config.Driver) func() {
	t.Helper()
	if driver == config.DriverSQLite {
		execTestSQL(t, pool, `CREATE TRIGGER fail_migration_version BEFORE INSERT ON goose_db_version
WHEN NEW.version_id = 4 BEGIN SELECT RAISE(FAIL, 'version insert failed'); END`)
		return func() { execTestSQL(t, pool, "DROP TRIGGER fail_migration_version") }
	}
	execTestSQL(t, pool, `CREATE FUNCTION fail_migration_version() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.version_id = 4 THEN RAISE EXCEPTION 'version insert failed'; END IF;
  RETURN NEW;
END $$`)
	execTestSQL(t, pool, `CREATE TRIGGER fail_migration_version BEFORE INSERT ON goose_db_version
FOR EACH ROW EXECUTE FUNCTION fail_migration_version()`)
	return func() {
		execTestSQL(t, pool, "DROP TRIGGER fail_migration_version ON goose_db_version")
		execTestSQL(t, pool, "DROP FUNCTION fail_migration_version()")
	}
}

func execTestSQL(t *testing.T, pool *sql.DB, query string) {
	t.Helper()
	if _, err := pool.ExecContext(context.Background(), query); err != nil {
		t.Fatalf("execute test SQL: %v", err)
	}
}

type sessionCleanupMetrics struct {
	failures atomic.Int64
}

func (m *sessionCleanupMetrics) IncSessionCleanupFailure() { m.failures.Add(1) }

func testPasswordHashUpdate(t *testing.T, store core.AccountStore, identities core.LocalIdentityStore) {
	t.Helper()
	id := mustID(t)
	account := core.Account{ID: id, Username: "rehash-" + id, PasswordHash: new("old"), CreatedAt: time.Now()}
	if err := store.CreateAccount(context.Background(), account); err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	if err := identities.UpdateAccountPasswordHash(context.Background(), id, "new"); err != nil {
		t.Fatalf("UpdateAccountPasswordHash: %v", err)
	}
	got, err := store.GetAccount(context.Background(), id)
	if err != nil || got.PasswordHash == nil || *got.PasswordHash != "new" {
		t.Fatalf("GetAccount after update = %+v, %v", got, err)
	}
}

func testPasswordHashLengthConstraint(t *testing.T, store core.AccountStore) {
	t.Helper()
	id := mustID(t)
	hash := strings.Repeat("x", core.MaxPasswordHashBytes+1)
	err := store.CreateAccount(context.Background(), core.Account{
		ID: id, Username: "long-hash-" + id, PasswordHash: &hash, CreatedAt: time.Now(),
	})
	if err == nil {
		t.Fatal("CreateAccount accepted oversized password hash")
	}
}

func testSessionStore(
	t *testing.T,
	pool *sql.DB,
	driver config.Driver,
	store scs.CtxStore,
	clock *testutil.FakeClock,
) {
	t.Helper()
	want := []byte("encoded-session")
	expiry := time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC)
	ctx := context.Background()
	if err := store.CommitCtx(ctx, "token-1", want, expiry); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	got, found, err := store.FindCtx(ctx, "token-1")
	if err != nil || !found || string(got) != string(want) {
		t.Fatalf("Find = %q, %v, %v; want %q, true, nil", got, found, err, want)
	}
	if err := store.DeleteCtx(ctx, "token-1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, found, err := store.FindCtx(ctx, "token-1"); err != nil || found {
		t.Fatalf("Find after Delete = found %v, err %v", found, err)
	}
	testSessionExpiryPrecision(t, pool, driver, store)
	testSessionExpiryBoundary(t, store, clock)
	testSessionContextCancellation(t, store)
	testSessionCleanup(t, pool, driver, clock)
}

func testSessionExpiryBoundary(t *testing.T, store scs.CtxStore, clock *testutil.FakeClock) {
	t.Helper()
	expiry := clock.Now().Add(time.Minute)
	if err := store.CommitCtx(context.Background(), "expiry-boundary", []byte("live"), expiry); err != nil {
		t.Fatalf("CommitCtx expiry boundary: %v", err)
	}
	clock.Advance(time.Minute - time.Microsecond)
	if _, found, err := store.FindCtx(context.Background(), "expiry-boundary"); err != nil || !found {
		t.Fatalf("FindCtx before expiry = found %v, err %v; want true, nil", found, err)
	}
	clock.Advance(time.Microsecond)
	if _, found, err := store.FindCtx(context.Background(), "expiry-boundary"); err != nil || found {
		t.Fatalf("FindCtx at expiry = found %v, err %v; want false, nil", found, err)
	}
}

func testStaleSessionCommits(t *testing.T, pool *sql.DB, store scs.CtxStore) {
	t.Helper()
	manager := testSessionManager(store)
	t.Run("logout deletion wins", func(t *testing.T) {
		testStaleCommitAfterLogout(t, pool, manager)
	})
	t.Run("login renewal wins", func(t *testing.T) {
		testStaleCommitAfterRenewal(t, pool, manager)
	})
}

func testSessionManager(store scs.Store) *scs.SessionManager {
	manager := scs.New()
	manager.Store = store
	manager.HashTokenInStore = true
	manager.Lifetime = time.Hour
	manager.IdleTimeout = time.Minute
	return manager
}

func testStaleCommitAfterLogout(t *testing.T, pool *sql.DB, manager *scs.SessionManager) {
	t.Helper()
	oldToken := seedSession(t, manager)
	stale := loadSessionContext(t, manager, oldToken)
	logout := loadSessionContext(t, manager, oldToken)
	if err := manager.Destroy(logout); err != nil {
		t.Fatalf("logout destroy: %v", err)
	}
	manager.Put(stale, "touch", true)
	if _, _, err := manager.Commit(stale); err != nil {
		t.Fatalf("stale commit after logout: %v", err)
	}
	assertStoredSession(t, pool, oldToken, false)
	assertSessionUnusable(t, manager, oldToken)
}

func testStaleCommitAfterRenewal(t *testing.T, pool *sql.DB, manager *scs.SessionManager) {
	t.Helper()
	oldToken := seedSession(t, manager)
	stale := loadSessionContext(t, manager, oldToken)
	renewal := loadSessionContext(t, manager, oldToken)
	if err := manager.RenewToken(renewal); err != nil {
		t.Fatalf("renew token: %v", err)
	}
	newToken, _, err := manager.Commit(renewal)
	if err != nil {
		t.Fatalf("commit renewed token: %v", err)
	}
	if _, _, err := manager.Commit(stale); err != nil {
		t.Fatalf("stale commit after renewal: %v", err)
	}
	assertStoredSession(t, pool, oldToken, false)
	assertStoredSession(t, pool, newToken, true)
	assertSessionUnusable(t, manager, oldToken)
}

func seedSession(t *testing.T, manager *scs.SessionManager) string {
	t.Helper()
	ctx, err := manager.Load(core.WithSessionLoadTracking(context.Background()), "")
	if err != nil {
		t.Fatalf("load new session: %v", err)
	}
	manager.Put(ctx, "account_id", "acct-1")
	token, _, err := manager.Commit(ctx)
	if err != nil {
		t.Fatalf("commit new session: %v", err)
	}
	return token
}

func loadSessionContext(t *testing.T, manager *scs.SessionManager, token string) context.Context {
	t.Helper()
	ctx, err := manager.Load(core.WithSessionLoadTracking(context.Background()), token)
	if err != nil {
		t.Fatalf("load session: %v", err)
	}
	return ctx
}

func assertStoredSession(t *testing.T, pool *sql.DB, token string, want bool) {
	t.Helper()
	hash := sha256.Sum256([]byte(token))
	storedToken := base64.RawURLEncoding.EncodeToString(hash[:])
	var count int
	if err := pool.QueryRowContext(context.Background(),
		"SELECT COUNT(*) FROM sessions WHERE token = $1", storedToken,
	).Scan(&count); err != nil {
		t.Fatalf("query hashed token: %v", err)
	}
	if got := count == 1; got != want {
		t.Fatalf("hashed token stored = %v, want %v", got, want)
	}
}

func assertSessionUnusable(t *testing.T, manager *scs.SessionManager, token string) {
	t.Helper()
	ctx := loadSessionContext(t, manager, token)
	if accountID := manager.GetString(ctx, "account_id"); accountID != "" {
		t.Fatalf("deleted token restored account %q", accountID)
	}
}

func testSessionContextCancellation(t *testing.T, store scs.CtxStore) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	expiry := time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := store.CommitCtx(ctx, "cancelled", []byte("x"), expiry); !errors.Is(err, context.Canceled) {
		t.Fatalf("CommitCtx(cancelled) = %v, want context.Canceled", err)
	}
	if _, _, err := store.FindCtx(ctx, "cancelled"); !errors.Is(err, context.Canceled) {
		t.Fatalf("FindCtx(cancelled) = %v, want context.Canceled", err)
	}
	if err := store.DeleteCtx(ctx, "cancelled"); !errors.Is(err, context.Canceled) {
		t.Fatalf("DeleteCtx(cancelled) = %v, want context.Canceled", err)
	}
}

func testSessionExpiryPrecision(t *testing.T, pool *sql.DB, driver config.Driver, store scs.CtxStore) {
	t.Helper()
	in := time.Date(2100, 1, 1, 0, 0, 0, 123456789, time.FixedZone("plus2", 2*60*60))
	if err := store.CommitCtx(context.Background(), "precision", []byte("x"), in); err != nil {
		t.Fatalf("CommitCtx precision: %v", err)
	}
	want := core.NormalizeTime(in)
	if driver == config.DriverSQLite {
		var got int64
		if err := pool.QueryRowContext(context.Background(), "SELECT expiry FROM sessions WHERE token = $1", "precision").Scan(&got); err != nil {
			t.Fatalf("query sqlite expiry: %v", err)
		}
		if got != want.UnixMicro() {
			t.Errorf("sqlite expiry = %d, want %d", got, want.UnixMicro())
		}
		return
	}
	var got time.Time
	if err := pool.QueryRowContext(context.Background(), "SELECT expiry FROM sessions WHERE token = $1", "precision").Scan(&got); err != nil {
		t.Fatalf("query postgres expiry: %v", err)
	}
	if !core.NormalizeTime(got).Equal(want) {
		t.Errorf("postgres expiry = %v, want %v", got, want)
	}
}

func testSessionCleanup(t *testing.T, pool *sql.DB, driver config.Driver, clock *testutil.FakeClock) {
	t.Helper()
	metrics := &sessionCleanupMetrics{}
	logger := slog.New(slog.DiscardHandler)
	store, err := db.NewSessionStore(pool, driver, metrics, logger, clock)
	if err != nil {
		t.Fatalf("NewSessionStore cleanup: %v", err)
	}
	past := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	for cycle := range 4 {
		for index := range sessionCleanupCommitCount {
			token := fmt.Sprintf("expired-cleanup-%d-%03d", cycle, index)
			if err := store.Commit(token, []byte("old"), past); err != nil {
				t.Fatalf("cycle %d commit %d: %v", cycle, index, err)
			}
		}
		if count := countExpiredSessions(t, pool); count != 0 {
			t.Fatalf("expired rows after cycle %d = %d, want 0", cycle, count)
		}
	}
	testSessionCleanupBoundary(t, pool, driver, clock)
	if driver == config.DriverSQLite {
		testSessionCleanupFailure(t, pool, past, clock)
	}
}

func testSessionCleanupBoundary(
	t *testing.T,
	pool *sql.DB,
	driver config.Driver,
	clock *testutil.FakeClock,
) {
	t.Helper()
	logger := slog.New(slog.DiscardHandler)
	store, err := db.NewSessionStore(pool, driver, &sessionCleanupMetrics{}, logger, clock)
	if err != nil {
		t.Fatalf("NewSessionStore cleanup boundary: %v", err)
	}
	expiry := clock.Now()
	commitSessions(t, store, "exact-cleanup", sessionCleanupCommitCount, expiry)
	if got := countSessions(t, pool, "exact-cleanup-%"); got != sessionCleanupCommitCount {
		t.Fatalf("sessions at cleanup boundary = %d, want %d", got, sessionCleanupCommitCount)
	}
	clock.Advance(time.Microsecond)
	commitSessions(t, store, "cleanup-trigger", sessionCleanupCommitCount, expiry.Add(time.Hour))
	if got := countSessions(t, pool, "exact-cleanup-%"); got != 0 {
		t.Fatalf("sessions after cleanup boundary = %d, want 0", got)
	}
}

func commitSessions(t *testing.T, store scs.CtxStore, prefix string, count int, expiry time.Time) {
	t.Helper()
	for index := range count {
		token := fmt.Sprintf("%s-%03d", prefix, index)
		if err := store.Commit(token, []byte("data"), expiry); err != nil {
			t.Fatalf("Commit %s: %v", token, err)
		}
	}
}

func countSessions(t *testing.T, pool *sql.DB, pattern string) int {
	t.Helper()
	var count int
	if err := pool.QueryRowContext(context.Background(),
		"SELECT COUNT(*) FROM sessions WHERE token LIKE $1", pattern,
	).Scan(&count); err != nil {
		t.Fatalf("count sessions %q: %v", pattern, err)
	}
	return count
}

func countExpiredSessions(t *testing.T, pool *sql.DB) int {
	t.Helper()
	var count int
	if err := pool.QueryRowContext(context.Background(),
		"SELECT COUNT(*) FROM sessions WHERE token LIKE 'expired-cleanup-%' OR token LIKE 'cleanup-failure-%'",
	).Scan(&count); err != nil {
		t.Fatalf("count expired sessions: %v", err)
	}
	return count
}

func testSessionCleanupFailure(t *testing.T, pool *sql.DB, expiry time.Time, clock *testutil.FakeClock) {
	t.Helper()
	fixture := newCleanupFailureFixture(t, pool, expiry, clock)
	t.Run("backoff transitions", fixture.testBackoffTransitions)
	t.Run("bounded logging", fixture.testLogging)
	t.Run("recovery resets failure streak", fixture.testRecovery)
}

type cleanupFailureFixture struct {
	pool    *sql.DB
	expiry  time.Time
	clock   *testutil.FakeClock
	metrics *sessionCleanupMetrics
	logs    *strings.Builder
	store   scs.CtxStore
}

func newCleanupFailureFixture(
	t *testing.T, pool *sql.DB, expiry time.Time, clock *testutil.FakeClock,
) *cleanupFailureFixture {
	t.Helper()
	createCleanupFailureTrigger(t, pool, "fail_session_cleanup", "cleanup failed")
	t.Cleanup(func() { dropCleanupFailureTrigger(t, pool, "fail_session_cleanup") })
	metrics := &sessionCleanupMetrics{}
	var logs strings.Builder
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	store, err := db.NewSessionStore(pool, config.DriverSQLite, metrics, logger, clock)
	if err != nil {
		t.Fatalf("NewSessionStore failure case: %v", err)
	}
	commitSessions(t, store, "cleanup-failure", sessionCleanupCommitCount, expiry)
	return &cleanupFailureFixture{
		pool: pool, expiry: expiry, clock: clock,
		metrics: metrics, logs: &logs, store: store,
	}
}

func (f *cleanupFailureFixture) testBackoffTransitions(t *testing.T) {
	commitSessions(t, f.store, "cleanup-failure-backoff", sessionCleanupCommitCount*4, f.expiry)
	if got := f.metrics.failures.Load(); got != 1 {
		t.Fatalf("cleanup attempts during backoff = %d, want 1", got)
	}
	f.advanceCommitAndAssert(t, time.Second-time.Microsecond, "before-retry-one", 1)
	f.advanceCommitAndAssert(t, time.Microsecond, "retry-one", 2)
	f.advanceCommitAndAssert(t, 2*time.Second-time.Microsecond, "before-retry-two", 2)
	f.advanceCommitAndAssert(t, time.Microsecond, "retry-two", 3)
}

func (f *cleanupFailureFixture) advanceCommitAndAssert(t *testing.T, advance time.Duration, token string, want int64) {
	t.Helper()
	f.clock.Advance(advance)
	if err := f.store.Commit("cleanup-failure-"+token, []byte("active"), f.expiry); err != nil {
		t.Fatalf("Commit %s: %v", token, err)
	}
	if got := f.metrics.failures.Load(); got != want {
		t.Fatalf("cleanup attempts after %s = %d, want %d", token, got, want)
	}
}

func (f *cleanupFailureFixture) testLogging(t *testing.T) {
	if got := strings.Count(f.logs.String(), `"msg":"session cleanup failed"`); got != 1 {
		t.Fatalf("cleanup failure log count = %d, want 1: %s", got, f.logs.String())
	}
	if !strings.Contains(f.logs.String(), "cleanup expired sessions") || !strings.Contains(f.logs.String(), "cleanup failed") {
		t.Fatalf("cleanup failure log omitted wrapped cause: %s", f.logs.String())
	}
	if strings.Contains(f.logs.String(), "cleanup-failure-") {
		t.Fatalf("cleanup failure log leaked session data: %s", f.logs.String())
	}
}

func (f *cleanupFailureFixture) testRecovery(t *testing.T) {
	dropCleanupFailureTrigger(t, f.pool, "fail_session_cleanup")
	beforeRecovery := countExpiredSessions(t, f.pool)
	f.clock.Advance(4 * time.Second)
	if err := f.store.Commit("cleanup-failure-retry", []byte("old"), f.expiry); err != nil {
		t.Fatalf("commit retry trigger: %v", err)
	}
	if count := countExpiredSessions(t, f.pool); count >= beforeRecovery {
		t.Fatalf("expired rows after retained retry = %d, want less than %d", count, beforeRecovery)
	}
	createCleanupFailureTrigger(t, f.pool, "fail_session_cleanup_again", "cleanup failed again")
	commitSessions(t, f.store, "cleanup-failure-again", sessionCleanupCommitCount, f.expiry)
	if got := strings.Count(f.logs.String(), `"msg":"session cleanup failed"`); got != 2 {
		t.Fatalf("cleanup failure log count after reset = %d, want 2: %s", got, f.logs.String())
	}
	dropCleanupFailureTrigger(t, f.pool, "fail_session_cleanup_again")
}

func createCleanupFailureTrigger(t *testing.T, pool *sql.DB, name, message string) {
	t.Helper()
	query := fmt.Sprintf(`CREATE TRIGGER %s BEFORE DELETE ON sessions
BEGIN SELECT RAISE(FAIL, '%s'); END`, name, message)
	if _, err := pool.ExecContext(context.Background(), query); err != nil {
		t.Fatalf("create cleanup failure trigger: %v", err)
	}
}

func dropCleanupFailureTrigger(t *testing.T, pool *sql.DB, name string) {
	t.Helper()
	if _, err := pool.ExecContext(context.Background(), "DROP TRIGGER IF EXISTS "+name); err != nil {
		t.Fatalf("drop cleanup failure trigger: %v", err)
	}
}

const sessionCleanupCommitCount = 64

func mustID(t *testing.T) string {
	t.Helper()
	id, err := core.NewID()
	if err != nil {
		t.Fatalf("NewID: %v", err)
	}
	return id
}

func testCreateGet(t *testing.T, store core.AccountStore) {
	t.Helper()
	ctx := context.Background()
	want := core.Account{
		ID:           mustID(t),
		Username:     "alice-" + mustID(t),
		PasswordHash: new("$argon2id$test"),
		Disabled:     true,
		CreatedAt:    time.Date(2026, 9, 22, 10, 30, 0, 123456000, time.UTC),
	}
	if err := store.CreateAccount(ctx, want); err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}

	got, err := store.GetAccount(ctx, want.ID)
	if err != nil {
		t.Fatalf("GetAccount: %v", err)
	}
	assertAccount(t, got, want)

	got, err = store.GetAccountByUsername(ctx, want.Username)
	if err != nil {
		t.Fatalf("GetAccountByUsername: %v", err)
	}
	assertAccount(t, got, want)
}

func testDuplicateID(t *testing.T, store core.AccountStore) {
	t.Helper()
	ctx := context.Background()
	id := mustID(t)
	first := core.Account{ID: id, Username: "first-" + id, CreatedAt: time.Now()}
	if err := store.CreateAccount(ctx, first); err != nil {
		t.Fatalf("first CreateAccount: %v", err)
	}
	dup := core.Account{ID: id, Username: "second-" + id, CreatedAt: time.Now()}
	if err := store.CreateAccount(ctx, dup); !errors.Is(err, core.ErrAlreadyExists) {
		t.Fatalf("duplicate id CreateAccount = %v, want ErrAlreadyExists", err)
	}
	// The original row is untouched.
	got, err := store.GetAccount(ctx, id)
	if err != nil || got.Username != first.Username {
		t.Fatalf("GetAccount after duplicate = %+v, %v; want username %q", got, err, first.Username)
	}
}

func testDuplicateUsername(t *testing.T, store core.AccountStore) {
	t.Helper()
	ctx := context.Background()
	suffix := mustID(t)
	username := "shared-" + suffix
	if err := store.CreateAccount(ctx, core.Account{ID: mustID(t), Username: "ＳHARED-" + suffix, CreatedAt: time.Now()}); err != nil {
		t.Fatalf("first CreateAccount: %v", err)
	}
	err := store.CreateAccount(ctx, core.Account{ID: mustID(t), Username: username, CreatedAt: time.Now()})
	if !errors.Is(err, core.ErrAlreadyExists) {
		t.Fatalf("duplicate username CreateAccount = %v, want ErrAlreadyExists", err)
	}
}

func testPRECISUsernameIdentity(t *testing.T, store core.AccountStore) {
	t.Helper()
	ctx := context.Background()
	usernames := []string{"Σigma-user", "ςigma-user", "Straße-user", "STRASSE-user"}
	for _, username := range usernames {
		account := core.Account{ID: mustID(t), Username: username, CreatedAt: time.Now()}
		if err := store.CreateAccount(ctx, account); err != nil {
			t.Fatalf("CreateAccount(%q): %v", username, err)
		}
		key, err := core.UsernameKey(username)
		if err != nil {
			t.Fatalf("UsernameKey(%q): %v", username, err)
		}
		got, err := store.GetAccountByUsername(ctx, username)
		if err != nil || got.Username != key {
			t.Fatalf("GetAccountByUsername(%q) = %+v, %v; want display %q", username, got, err, key)
		}
	}
}

func testRawUsernameConstraints(t *testing.T, pool *sql.DB, driver config.Driver) {
	t.Helper()
	ctx := context.Background()
	created := migrationCreatedAt(driver)
	baseID := mustID(t)
	_, err := pool.ExecContext(ctx,
		"INSERT INTO accounts (id, username, username_key, created_at) VALUES ($1, $2, $3, $4)",
		baseID, "Élodie-raw", "élodie-raw", created,
	)
	if err != nil {
		t.Fatalf("raw non-ASCII insert: %v", err)
	}
	tests := []struct{ username, key string }{
		{username: "Different-display", key: "élodie-raw"},
		{username: "Élodie-raw", key: "different-key"},
	}
	for _, testCase := range tests {
		_, err := pool.ExecContext(ctx,
			"INSERT INTO accounts (id, username, username_key, created_at) VALUES ($1, $2, $3, $4)",
			mustID(t), testCase.username, testCase.key, created,
		)
		if err == nil {
			t.Errorf("raw insert (%q, %q) bypassed exact UNIQUE constraint", testCase.username, testCase.key)
		}
	}
}

func testNotFound(t *testing.T, store core.AccountStore) {
	t.Helper()
	ctx := context.Background()
	if _, err := store.GetAccount(ctx, mustID(t)); !errors.Is(err, core.ErrNotFound) {
		t.Errorf("GetAccount(missing) = %v, want ErrNotFound", err)
	}
	if _, err := store.GetAccountByUsername(ctx, "nobody-"+mustID(t)); !errors.Is(err, core.ErrNotFound) {
		t.Errorf("GetAccountByUsername(missing) = %v, want ErrNotFound", err)
	}
}

// testTimestampNormalization proves ADR 0004 item 5 on this engine: a
// non-UTC, nanosecond-precision input reads back as the same instant in UTC at
// microsecond precision, so a value written on either engine compares Equal.
func testTimestampNormalization(t *testing.T, store core.AccountStore) {
	t.Helper()
	ctx := context.Background()
	loc := time.FixedZone("minus7", -7*60*60)
	in := time.Date(2026, 1, 2, 3, 4, 5, 987654321, loc)
	a := core.Account{ID: mustID(t), Username: "tz-" + mustID(t), CreatedAt: in}
	if err := store.CreateAccount(ctx, a); err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	got, err := store.GetAccount(ctx, a.ID)
	if err != nil {
		t.Fatalf("GetAccount: %v", err)
	}
	want := core.NormalizeTime(in)
	if !got.CreatedAt.Equal(want) {
		t.Errorf("CreatedAt = %v, want %v", got.CreatedAt, want)
	}
	if got.CreatedAt.Location() != time.UTC {
		t.Errorf("CreatedAt location = %v, want UTC", got.CreatedAt.Location())
	}
	if got.CreatedAt.Nanosecond() != 987654000 {
		t.Errorf("CreatedAt nanos = %d, want 987654000 (microsecond truncation)", got.CreatedAt.Nanosecond())
	}
}

func assertAccount(t *testing.T, got, want core.Account) {
	t.Helper()
	if got.ID != want.ID || got.Username != want.Username || got.Disabled != want.Disabled {
		t.Errorf("account = %+v, want id %q username %q", got, want.ID, want.Username)
	}
	if !equalStringPointers(got.PasswordHash, want.PasswordHash) {
		t.Errorf("PasswordHash = %v, want %v", got.PasswordHash, want.PasswordHash)
	}
	if !got.CreatedAt.Equal(want.CreatedAt) {
		t.Errorf("CreatedAt = %v, want %v", got.CreatedAt, want.CreatedAt)
	}
}

func equalStringPointers(left, right *string) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}
