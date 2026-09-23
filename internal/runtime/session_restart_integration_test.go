//go:build integration

package runtime_test

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/BonzTM/bloom/internal/config"
	"github.com/BonzTM/bloom/internal/db"
)

func TestRunSessionDurabilityAcrossRestartPostgres(t *testing.T) {
	dsn := os.Getenv("BLOOM_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("BLOOM_TEST_POSTGRES_DSN not set; skipping live PostgreSQL session restart test")
	}
	cfg := baseConfig(t, "127.0.0.1:0")
	cfg.Database.Driver = config.DriverPostgres
	cfg.Database.DSN = isolatedSessionDSN(t, dsn)
	cfg.Database.MigrateOnStartup = true
	runSessionRestartSuite(t, cfg)
}

func isolatedSessionDSN(t *testing.T, dsn string) string {
	t.Helper()
	schema := fmt.Sprintf("bloom_session_runtime_%d", os.Getpid())
	pool, err := db.Open(t.Context(), config.DatabaseConfig{
		Driver: config.DriverPostgres, DSN: dsn, MaxOpenConns: 1, MaxIdleConns: 1,
		ConnMaxLifetime: time.Minute, ConnMaxIdleTime: time.Minute,
	}, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("open PostgreSQL schema administrator: %v", err)
	}
	if _, err := pool.ExecContext(t.Context(), "DROP SCHEMA IF EXISTS "+schema+" CASCADE; CREATE SCHEMA "+schema); err != nil {
		_ = pool.Close()
		t.Fatalf("prepare PostgreSQL session schema: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := pool.ExecContext(ctx, "DROP SCHEMA IF EXISTS "+schema+" CASCADE"); err != nil {
			t.Errorf("drop PostgreSQL session schema: %v", err)
		}
		if err := pool.Close(); err != nil {
			t.Errorf("close PostgreSQL schema administrator: %v", err)
		}
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
