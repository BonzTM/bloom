package db

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"

	"github.com/pressly/goose/v3"

	"github.com/BonzTM/bloom/internal/config"
	"github.com/BonzTM/bloom/internal/core"
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
	p, err := goose.NewProvider(dialect, pool, sub,
		goose.WithGoMigrations(canonicalUsernameMigration()),
		goose.WithDisableGlobalRegistry(true),
	)
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

const (
	canonicalUsernameMigrationVersion = 4
	canonicalUsernameMigrationMaxRows = 1_000_000
)

type usernameMigrationRow struct {
	id, original, canonical string
}

func canonicalUsernameMigration() *goose.Migration {
	return goose.NewGoMigration(canonicalUsernameMigrationVersion,
		&goose.GoFunc{RunTx: migrateCanonicalUsernamesUp},
		&goose.GoFunc{RunTx: migrateCanonicalUsernamesDown},
	)
}

func migrateCanonicalUsernamesUp(ctx context.Context, tx *sql.Tx) error {
	rows, err := loadCanonicalUsernames(ctx, tx)
	if err != nil {
		return err
	}
	return rewriteCanonicalUsernames(ctx, tx, rows)
}

func migrateCanonicalUsernamesDown(ctx context.Context, tx *sql.Tx) error {
	statements := [...]string{
		`UPDATE accounts SET username = (SELECT original_username FROM account_username_migration_backup WHERE account_id = accounts.id) WHERE id IN (SELECT account_id FROM account_username_migration_backup)`,
		"UPDATE accounts SET username_key = NULL",
		"DELETE FROM account_username_migration_backup",
	}
	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("roll back canonical username migration: %w", err)
		}
	}
	return nil
}

func loadCanonicalUsernames(ctx context.Context, tx *sql.Tx) (loaded []usernameMigrationRow, retErr error) {
	query := fmt.Sprintf("SELECT id, username FROM accounts ORDER BY id LIMIT %d", canonicalUsernameMigrationMaxRows+1)
	result, err := tx.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list usernames: %w", err)
	}
	defer func() {
		if closeErr := result.Close(); closeErr != nil {
			retErr = errors.Join(retErr, fmt.Errorf("close username rows: %w", closeErr))
		}
	}()
	loaded = make([]usernameMigrationRow, 0)
	seen := make(map[string]string)
	for range canonicalUsernameMigrationMaxRows + 1 {
		if !result.Next() {
			break
		}
		row, err := scanCanonicalUsername(result, seen)
		if err != nil {
			return nil, err
		}
		loaded = append(loaded, row)
	}
	if err := result.Err(); err != nil {
		return nil, fmt.Errorf("iterate usernames: %w", err)
	}
	if len(loaded) > canonicalUsernameMigrationMaxRows {
		return nil, fmt.Errorf("username migration exceeds %d accounts", canonicalUsernameMigrationMaxRows)
	}
	return loaded, nil
}

func scanCanonicalUsername(rows *sql.Rows, seen map[string]string) (usernameMigrationRow, error) {
	var row usernameMigrationRow
	if err := rows.Scan(&row.id, &row.original); err != nil {
		return row, fmt.Errorf("scan username: %w", err)
	}
	canonical, err := core.UsernameKey(row.original)
	if err != nil {
		return row, fmt.Errorf("account %q has invalid username %q: %w", row.id, row.original, err)
	}
	if priorID, exists := seen[canonical]; exists {
		return row, fmt.Errorf("canonical username collision %q between accounts %q and %q", canonical, priorID, row.id)
	}
	seen[canonical] = row.id
	row.canonical = canonical
	return row, nil
}

func rewriteCanonicalUsernames(ctx context.Context, tx *sql.Tx, rows []usernameMigrationRow) error {
	for _, row := range rows {
		if _, err := tx.ExecContext(ctx,
			"INSERT INTO account_username_migration_backup (account_id, original_username) VALUES ($1, $2)",
			row.id, row.original,
		); err != nil {
			return fmt.Errorf("back up username for account %q: %w", row.id, err)
		}
		if _, err := tx.ExecContext(ctx, "UPDATE accounts SET username = $1, username_key = $1 WHERE id = $2", row.canonical, row.id); err != nil {
			return fmt.Errorf("canonicalize username for account %q: %w", row.id, err)
		}
	}
	return nil
}

// MigrateDownAll rolls every applied migration back to version 0. It exists for
// the parity test's up/down/up proof; production never rolls back
// automatically (a contracted change is forward-only, per the handbook's
// recipes/add-migration.md).
func MigrateDownAll(ctx context.Context, pool *sql.DB, d config.Driver) error {
	return MigrateDownTo(ctx, pool, d, 0)
}

// MigrateDownTo rolls migrations back to target for the database parity proof.
// Production rollout code never invokes this helper automatically.
func MigrateDownTo(ctx context.Context, pool *sql.DB, d config.Driver, target int64) error {
	p, err := newProvider(d, pool)
	if err != nil {
		return err
	}
	if _, err := p.DownTo(ctx, target); err != nil {
		return fmt.Errorf("roll back %s migrations: %w", d, err)
	}
	return nil
}
