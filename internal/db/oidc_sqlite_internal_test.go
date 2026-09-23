package db

import (
	"database/sql"
	"log/slog"
	"testing"
	"time"

	"github.com/BonzTM/bloom/internal/config"
	"github.com/BonzTM/bloom/internal/core"
)

func TestSQLiteOIDCSignInsSerializeIdentityReconciliation(t *testing.T) {
	pool := openConcurrentSQLiteTest(t)
	store := newSQLiteOIDCStore(pool)
	base := core.OIDCSignIn{
		Provider: oidcProviderName, Issuer: "https://sqlite-concurrent.example", Subject: "subject",
		UsernameClaim: "sqlite-user", MappedRoles: []string{"owner"}, DefaultRole: "member",
		AutoProvision: true, Now: time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC),
	}
	created, err := store.SignInOIDC(t.Context(), base)
	if err != nil {
		t.Fatalf("provision identity: %v", err)
	}
	first, second := base, base
	first.MappedRoles = []string{"member"}
	first.Now = first.Now.Add(time.Minute)
	second.Now = second.Now.Add(2 * time.Minute)
	start := make(chan struct{})
	done := make(chan error, 2)
	ctx := t.Context()
	for _, signIn := range []core.OIDCSignIn{first, second} {
		go func() {
			<-start
			_, signInErr := store.SignInOIDC(ctx, signIn)
			done <- signInErr
		}()
	}
	close(start)
	for range 2 {
		if err := <-done; err != nil {
			t.Fatalf("concurrent sign-in: %v", err)
		}
	}
	assertSQLiteOIDCState(t, pool, created.Account.ID)
}

func openConcurrentSQLiteTest(t *testing.T) *sql.DB {
	t.Helper()
	pool, err := Open(t.Context(), config.DatabaseConfig{
		Driver:       config.DriverSQLite,
		DSN:          "file:oidc-concurrency?mode=memory&cache=shared&_pragma=busy_timeout(5000)",
		MaxOpenConns: 4, MaxIdleConns: 4, ConnMaxLifetime: time.Hour, ConnMaxIdleTime: time.Hour,
	}, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("open SQLite: %v", err)
	}
	t.Cleanup(func() { _ = pool.Close() })
	if err := Migrate(t.Context(), pool, config.DriverSQLite); err != nil {
		t.Fatalf("migrate SQLite: %v", err)
	}
	return pool
}

func assertSQLiteOIDCState(t *testing.T, pool *sql.DB, accountID string) {
	t.Helper()
	var mappedRoles, role string
	var count int
	err := pool.QueryRowContext(t.Context(), `
SELECT ai.mapped_roles, group_concat(r.name, ','), count(*)
FROM account_identities ai
JOIN account_roles ar ON ar.account_id = ai.account_id AND ar.source = 'oidc'
JOIN roles r ON r.id = ar.role_id
WHERE ai.account_id = ?
GROUP BY ai.mapped_roles`, accountID).Scan(&mappedRoles, &role, &count)
	wantMapped := `["` + role + `"]`
	if err != nil || count != 1 || (role != "member" && role != "owner") || mappedRoles != wantMapped {
		t.Fatalf("OIDC snapshot and grant = %q %q, %v", mappedRoles, role, err)
	}
}
