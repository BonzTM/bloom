package runtime

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/BonzTM/bloom/internal/config"
	"github.com/BonzTM/bloom/internal/db"
)

const adminTestSecret = "0123456789abcdef0123456789abcdef"

type failingWriter struct{ err error }

func (w failingWriter) Write([]byte) (int, error) { return 0, w.err }

func prepareAdminDatabase(t *testing.T) string {
	t.Helper()
	dsn := "file:" + filepath.Join(t.TempDir(), "bloom.db") + "?_pragma=foreign_keys(1)"
	pool, err := db.Open(context.Background(), adminDatabaseConfig(dsn))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := db.Migrate(context.Background(), pool, config.DriverSQLite); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if err := pool.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	return dsn
}

func adminDatabaseConfig(dsn string) config.DatabaseConfig {
	return config.DatabaseConfig{
		Driver: config.DriverSQLite, DSN: dsn, MaxOpenConns: 1, MaxIdleConns: 1,
		ConnMaxLifetime: time.Hour, ConnMaxIdleTime: time.Hour,
	}
}

func setAdminEnvironment(t *testing.T, dsn string) {
	t.Helper()
	t.Setenv("BLOOM_SECRET_KEY", adminTestSecret)
	t.Setenv("BLOOM_DB_DRIVER", "sqlite")
	t.Setenv("BLOOM_DB_DSN", dsn)
	t.Setenv("BLOOM_DB_MAX_OPEN_CONNS", "1")
	t.Setenv("BLOOM_DB_MAX_IDLE_CONNS", "1")
}
