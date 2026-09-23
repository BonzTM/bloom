//go:build integration

package runtime

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/BonzTM/bloom/internal/config"
	"github.com/BonzTM/bloom/internal/db"
)

func TestBootstrapAdminPostgres(t *testing.T) {
	dsn := os.Getenv("BLOOM_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("BLOOM_TEST_POSTGRES_DSN not set; skipping live PostgreSQL bootstrap test")
	}
	testDSN := isolatedBootstrapDSN(t, dsn)
	pool := openBootstrapTestDatabase(t, config.DriverPostgres, testDSN)
	runBootstrapAdminEngineSuite(t, pool, config.DriverPostgres, testDSN)
}

func isolatedBootstrapDSN(t *testing.T, dsn string) string {
	t.Helper()
	schema := fmt.Sprintf("bloom_bootstrap_runtime_%d", os.Getpid())
	pool, err := db.Open(t.Context(), config.DatabaseConfig{
		Driver: config.DriverPostgres, DSN: dsn, MaxOpenConns: 1, MaxIdleConns: 1,
		ConnMaxLifetime: time.Minute, ConnMaxIdleTime: time.Minute,
	}, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("Open PostgreSQL schema administrator: %v", err)
	}
	if _, err := pool.ExecContext(t.Context(), "DROP SCHEMA IF EXISTS "+schema+" CASCADE; CREATE SCHEMA "+schema); err != nil {
		_ = pool.Close()
		t.Fatalf("prepare PostgreSQL bootstrap schema: %v", err)
	}
	t.Cleanup(func() {
		cleanupBootstrapSchema(t, pool, schema)
	})
	parsed, err := url.Parse(dsn)
	if err != nil {
		t.Fatalf("parse PostgreSQL test DSN: %v", err)
	}
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	return parsed.String()
}

func cleanupBootstrapSchema(t *testing.T, pool *sql.DB, schema string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := pool.ExecContext(ctx, "DROP SCHEMA IF EXISTS "+schema+" CASCADE"); err != nil {
		t.Errorf("drop PostgreSQL bootstrap schema: %v", err)
	}
	if err := pool.Close(); err != nil {
		t.Errorf("close PostgreSQL schema administrator: %v", err)
	}
}
