//go:build integration

package db

import (
	"database/sql"
	"errors"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/BonzTM/bloom/internal/config"
	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/db/postgres"
)

func TestPostgresOIDCSignInsSerializeIdentityReconciliation(t *testing.T) {
	pool := openOIDCPostgresTest(t)
	store := newPostgresOIDCStore(pool)
	base := core.OIDCSignIn{
		Provider: oidcProviderName, Issuer: "https://concurrent.example", Subject: "subject",
		UsernameClaim: "concurrent-user", MappedRoles: []string{"owner"}, DefaultRole: "member",
		AutoProvision: true, Now: time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC),
	}
	created, err := store.SignInOIDC(t.Context(), base)
	if err != nil {
		t.Fatalf("provision identity: %v", err)
	}
	first := base
	first.MappedRoles = []string{"member"}
	first.Now = first.Now.Add(time.Minute)
	tx := beginPostgresTestTx(t, pool)
	if _, err := store.signInTx(t.Context(), tx, postgres.New(tx), first, 0); err != nil {
		t.Fatalf("first sign-in transaction: %v", err)
	}

	second := base
	second.Now = second.Now.Add(2 * time.Minute)
	assertPostgresSignInWaitsForIdentityLock(t, pool, store, second)
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit first sign-in: %v", err)
	}
	if _, err := store.SignInOIDC(t.Context(), second); err != nil {
		t.Fatalf("second sign-in: %v", err)
	}
	assertPostgresOIDCState(t, pool, created.Account.ID, `["owner"]`, "owner")
}

func openOIDCPostgresTest(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("BLOOM_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("BLOOM_TEST_POSTGRES_DSN not set; skipping live PostgreSQL concurrency test")
	}
	pool, err := Open(t.Context(), config.DatabaseConfig{
		Driver: config.DriverPostgres, DSN: dsn, MaxOpenConns: 5, MaxIdleConns: 5,
		ConnMaxLifetime: time.Minute, ConnMaxIdleTime: time.Minute,
	}, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("open postgres: %v", err)
	}
	t.Cleanup(func() { _ = pool.Close() })
	if err := MigrateDownAll(t.Context(), pool, config.DriverPostgres); err != nil {
		t.Fatalf("clean postgres: %v", err)
	}
	if err := Migrate(t.Context(), pool, config.DriverPostgres); err != nil {
		t.Fatalf("migrate postgres: %v", err)
	}
	return pool
}

func beginPostgresTestTx(t *testing.T, pool *sql.DB) *sql.Tx {
	t.Helper()
	tx, err := pool.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatalf("begin postgres transaction: %v", err)
	}
	t.Cleanup(func() { _ = tx.Rollback() })
	return tx
}

func assertPostgresSignInWaitsForIdentityLock(
	t *testing.T, pool *sql.DB, store *postgresOIDCStore, signIn core.OIDCSignIn,
) {
	t.Helper()
	tx := beginPostgresTestTx(t, pool)
	if _, err := tx.ExecContext(t.Context(), "SET LOCAL lock_timeout = '1ms'"); err != nil {
		t.Fatalf("set postgres lock timeout: %v", err)
	}
	_, err := store.signInTx(t.Context(), tx, postgres.New(tx), signIn, 0)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "55P03" {
		t.Fatalf("second sign-in error = %v, want PostgreSQL lock timeout 55P03", err)
	}
	if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
		t.Fatalf("roll back blocked sign-in: %v", err)
	}
}

func assertPostgresOIDCState(t *testing.T, pool *sql.DB, accountID, mappedRoles, role string) {
	t.Helper()
	var gotMapped, gotRole string
	var count int
	err := pool.QueryRowContext(t.Context(), `
SELECT ai.mapped_roles, string_agg(r.name, ',' ORDER BY r.name), count(*)
FROM account_identities ai
JOIN account_roles ar ON ar.account_id = ai.account_id AND ar.source = 'oidc'
JOIN roles r ON r.id = ar.role_id
WHERE ai.account_id = $1
GROUP BY ai.mapped_roles`, accountID).Scan(&gotMapped, &gotRole, &count)
	if err != nil || gotMapped != mappedRoles || gotRole != role || count != 1 {
		t.Fatalf("OIDC snapshot and grant = %q %q, %v; want %q %q", gotMapped, gotRole, err, mappedRoles, role)
	}
}
