package db

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"

	"github.com/pressly/goose/v3"

	"github.com/BonzTM/bloom/internal/config"
)

// migrationsFS embeds the goose-tagged SQL migrations for BOTH engines so a
// built binary carries its own schema and can apply it without shipping loose
// .sql files, per the handbook's services/database.md. Each engine has its own
// directory (ADR 0004 item 2); the parity test asserts the two directories
// carry identical file lists.
//
//go:embed migrations/sqlite/*.sql migrations/postgres/*.sql
var migrationsFS embed.FS

// MigrationsFS exposes the embedded migration tree read-only so tests can
// compare the per-engine directories without reaching into the package.
func MigrationsFS() fs.FS { return migrationsFS }

// migrationSet resolves the goose dialect and the embedded sub-directory for a
// configured engine. It is the single place the engine-to-migrations mapping
// lives.
func migrationSet(d config.Driver) (goose.Dialect, fs.FS, error) {
	var dialect goose.Dialect
	switch d {
	case config.DriverSQLite:
		dialect = goose.DialectSQLite3
	case config.DriverPostgres:
		dialect = goose.DialectPostgres
	default:
		return "", nil, fmt.Errorf("unsupported database driver %q", d)
	}
	sub, err := fs.Sub(migrationsFS, "migrations/"+string(d))
	if err != nil {
		return "", nil, fmt.Errorf("locate %s migrations: %w", d, err)
	}
	return dialect, sub, nil
}

// newProvider builds a goose Provider for the engine. The Provider API is used
// instead of goose's package-level SetBaseFS/SetDialect because that global
// state cannot serve two engines in one process, which the parity tests need.
// Callers must NOT call Provider.Close: it closes the underlying *sql.DB, which
// the caller owns and keeps serving from.
func newProvider(d config.Driver, pool *sql.DB) (*goose.Provider, error) {
	dialect, sub, err := migrationSet(d)
	if err != nil {
		return nil, err
	}
	p, err := goose.NewProvider(dialect, pool, sub)
	if err != nil {
		return nil, fmt.Errorf("goose provider for %s: %w", d, err)
	}
	return p, nil
}

// Migrate applies all pending goose migrations for the configured engine to
// pool, bringing the schema up to the latest version. The caller gates it
// (BLOOM_DB_MIGRATE_ON_STARTUP or the one-shot -migrate mode). The driver
// behind pool must already be registered; Migrate does not open the pool.
func Migrate(ctx context.Context, pool *sql.DB, d config.Driver) error {
	p, err := newProvider(d, pool)
	if err != nil {
		return err
	}
	if _, err := p.Up(ctx); err != nil {
		return fmt.Errorf("apply %s migrations: %w", d, err)
	}
	return nil
}

// MigrateDownAll rolls every applied migration back to version 0. It exists for
// the parity test's up/down/up proof; production never rolls back
// automatically (a contracted change is forward-only, per the handbook's
// recipes/add-migration.md).
func MigrateDownAll(ctx context.Context, pool *sql.DB, d config.Driver) error {
	p, err := newProvider(d, pool)
	if err != nil {
		return err
	}
	if _, err := p.DownTo(ctx, 0); err != nil {
		return fmt.Errorf("roll back %s migrations: %w", d, err)
	}
	return nil
}
