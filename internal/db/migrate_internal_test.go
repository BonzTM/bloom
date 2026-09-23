package db

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"github.com/BonzTM/bloom/internal/config"
)

func TestMigrationVersionApplied(t *testing.T) {
	pool := openMigrationStatusTestDatabase(t)

	applied, err := migrationVersionApplied(context.Background(), pool, config.DriverSQLite, rolesMigrationVersion)
	if err != nil || applied {
		t.Fatalf("missing version table = %v, %v; want false, nil", applied, err)
	}

	createMigrationVersionTable(t, pool)
	if _, execErr := pool.Exec("INSERT INTO goose_db_version (version_id, is_applied) VALUES ($1, TRUE)", rolesMigrationVersion); execErr != nil {
		t.Fatalf("insert migration version: %v", execErr)
	}
	applied, err = migrationVersionApplied(context.Background(), pool, config.DriverSQLite, rolesMigrationVersion)
	if err != nil || !applied {
		t.Fatalf("applied version = %v, %v; want true, nil", applied, err)
	}
	applied, err = migrationVersionApplied(context.Background(), pool, config.DriverSQLite, rolesMigrationVersion+1)
	if err != nil || applied {
		t.Fatalf("unapplied version = %v, %v; want false, nil", applied, err)
	}
}

func TestMigrationVersionAppliedReturnsQueryErrors(t *testing.T) {
	t.Run("context cancellation remains matchable", func(t *testing.T) {
		pool := openMigrationStatusTestDatabase(t)
		createMigrationVersionTable(t, pool)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		applied, err := migrationVersionApplied(ctx, pool, config.DriverSQLite, rolesMigrationVersion)
		if applied || !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled query = %v, %v; want false and context.Canceled", applied, err)
		}
	})
	t.Run("malformed version table is not missing", func(t *testing.T) {
		pool := openMigrationStatusTestDatabase(t)
		if _, err := pool.Exec("CREATE TABLE goose_db_version (version_id INTEGER)"); err != nil {
			t.Fatalf("create malformed version table: %v", err)
		}

		applied, err := migrationVersionApplied(context.Background(), pool, config.DriverSQLite, rolesMigrationVersion)
		if applied || err == nil {
			t.Fatalf("malformed table query = %v, %v; want false and error", applied, err)
		}
	})
}

func openMigrationStatusTestDatabase(t *testing.T) *sql.DB {
	t.Helper()
	dsn := "file:" + filepath.Join(t.TempDir(), "migration-status.db")
	pool, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("open migration status database: %v", err)
	}
	pool.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = pool.Close() })
	return pool
}

func createMigrationVersionTable(t *testing.T, pool *sql.DB) {
	t.Helper()
	if _, err := pool.Exec(`CREATE TABLE goose_db_version (
		version_id INTEGER NOT NULL,
		is_applied BOOLEAN NOT NULL
	)`); err != nil {
		t.Fatalf("create migration version table: %v", err)
	}
}
