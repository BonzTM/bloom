//go:build integration

// The PostgreSQL half of the ADR 0004 parity proof runs the SAME engine suite
// as the SQLite test against a LIVE database. It is gated behind the
// `integration` build tag so the default `make verify` stays green offline;
// run it with:
//
//	BLOOM_TEST_POSTGRES_DSN='postgres://bloom:bloom@localhost:5432/bloom?sslmode=disable' \
//		go test -tags=integration ./internal/db/...
//
// CI sets the DSN against a service container (.github/workflows/ci.yml).
package db_test

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"

	"github.com/BonzTM/bloom/internal/config"
	"github.com/BonzTM/bloom/internal/db"
)

func TestPostgresEngineSuite(t *testing.T) {
	dsn := os.Getenv("BLOOM_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("BLOOM_TEST_POSTGRES_DSN not set; skipping live PostgreSQL parity test")
	}
	ctx := context.Background()
	pool, err := db.Open(ctx, config.DatabaseConfig{
		Driver:          config.DriverPostgres,
		DSN:             dsn,
		MaxOpenConns:    5,
		MaxIdleConns:    5,
		ConnMaxLifetime: time.Minute,
		ConnMaxIdleTime: time.Minute,
	}, discardLogger())
	if err != nil {
		t.Fatalf("Open postgres: %v", err)
	}
	t.Cleanup(func() { _ = pool.Close() })

	// Start from a clean slate so a previous run's rows cannot collide; the
	// suite's own up/down/up then proves the migrations.
	if err := db.MigrateDownAll(ctx, pool, config.DriverPostgres); err != nil {
		t.Fatalf("MigrateDownAll (pre-clean): %v", err)
	}

	runEngineSuite(t, pool, config.DriverPostgres)
	assertColumns(t, pool, postgresColumns, expectedAccountColumns)
	assertColumns(t, pool, postgresSessionColumns, expectedSessionColumns)
	assertColumns(t, pool, postgresRoleColumns, expectedRoleColumns)
	assertColumns(t, pool, postgresRolePermissionColumns, expectedRolePermissionColumns)
	assertColumns(t, pool, postgresAccountRoleColumns, expectedAccountRoleColumns)
	assertColumns(t, pool, postgresMediaServerColumns, expectedMediaServerColumns)
	assertColumns(t, pool, postgresIdentityColumns, expectedIdentityColumns)
	assertColumns(t, pool, func(ctx context.Context, pool *sql.DB) ([]string, error) {
		return postgresTableColumns(ctx, pool, "invites")
	}, expectedInviteColumns)
	assertColumns(t, pool, func(ctx context.Context, pool *sql.DB) ([]string, error) {
		return postgresTableColumns(ctx, pool, "invite_libraries")
	}, expectedInviteLibraryColumns)
	assertColumns(t, pool, func(ctx context.Context, pool *sql.DB) ([]string, error) {
		return postgresTableColumns(ctx, pool, "invite_redemptions")
	}, expectedInviteRedemptionColumns)
	assertColumns(t, pool, func(ctx context.Context, pool *sql.DB) ([]string, error) {
		return postgresTableColumns(ctx, pool, "invite_provisioning_failures")
	}, expectedInviteProvisioningFailureColumns)
}

func postgresSessionColumns(ctx context.Context, pool *sql.DB) ([]string, error) {
	rows, err := pool.QueryContext(ctx,
		"SELECT column_name FROM information_schema.columns WHERE table_schema = current_schema() AND table_name = 'sessions' ORDER BY column_name")
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanStrings(rows)
}

func postgresRoleColumns(ctx context.Context, pool *sql.DB) ([]string, error) {
	return postgresTableColumns(ctx, pool, "roles")
}

func postgresRolePermissionColumns(ctx context.Context, pool *sql.DB) ([]string, error) {
	return postgresTableColumns(ctx, pool, "role_permissions")
}

func postgresAccountRoleColumns(ctx context.Context, pool *sql.DB) ([]string, error) {
	return postgresTableColumns(ctx, pool, "account_roles")
}

func postgresMediaServerColumns(ctx context.Context, pool *sql.DB) ([]string, error) {
	return postgresTableColumns(ctx, pool, "media_servers")
}

func postgresIdentityColumns(ctx context.Context, pool *sql.DB) ([]string, error) {
	return postgresTableColumns(ctx, pool, "account_identities")
}

func postgresTableColumns(ctx context.Context, pool *sql.DB, table string) ([]string, error) {
	rows, err := pool.QueryContext(ctx,
		"SELECT column_name FROM information_schema.columns WHERE table_schema = current_schema() AND table_name = $1 ORDER BY column_name", table)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanStrings(rows)
}

// postgresColumns lists the accounts columns via information_schema.
func postgresColumns(ctx context.Context, pool *sql.DB) ([]string, error) {
	rows, err := pool.QueryContext(ctx,
		"SELECT column_name FROM information_schema.columns WHERE table_schema = current_schema() AND table_name = 'accounts' ORDER BY column_name")
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanStrings(rows)
}
