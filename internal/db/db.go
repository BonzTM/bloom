// Package db owns Bloom's SQL: the database/sql pool, the embedded goose
// migrations for both engines, and the adapters that map the sqlc-generated
// query packages (internal/db/sqlite, internal/db/postgres) onto the
// consumer-defined store interfaces in internal/core. It implements ADR 0004:
// SQLite by default, PostgreSQL at full parity, portable SQL, dialect
// differences confined to the per-engine migration directories and the two
// adapters.
package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"time"

	// Blank imports register the two supported database/sql drivers. Both are
	// pure Go, so the binary stays a static CGO_ENABLED=0 build (ADR 0004
	// item 7). They are registered here, in the package that owns storage,
	// rather than in main, because the engine choice is a config value resolved
	// by Open and the parity tests need both drivers linked.
	_ "github.com/jackc/pgx/v5/stdlib" // registers "pgx"
	_ "modernc.org/sqlite"             // registers "sqlite"

	"github.com/BonzTM/bloom/internal/config"
)

// pingTimeout bounds the startup connectivity check so a service that cannot
// reach its store fails fast instead of hanging.
const pingTimeout = 5 * time.Second

// ErrDatabaseLoggerRequired is returned when Open receives a nil logger.
var ErrDatabaseLoggerRequired = errors.New("database logger is required")

// driverName maps the configured engine to the registered database/sql driver
// name. It is the single place that knowledge lives.
func driverName(d config.Driver) (string, error) {
	switch d {
	case config.DriverSQLite:
		return "sqlite", nil
	case config.DriverPostgres:
		return "pgx", nil
	default:
		return "", fmt.Errorf("unsupported database driver %q", d)
	}
}

// Open opens a *sql.DB for the configured engine and DSN. The logger must be
// non-nil; Open returns ErrDatabaseLoggerRequired otherwise. It applies all
// four pool limits from config instead of the database/sql defaults. For
// SQLite, it appends the foreign-key compatibility pragma when the configured
// DSN has no foreign-key directive and logs that action without logging the
// DSN. Open verifies connectivity with PingContext so an unreachable store
// fails fast, and it verifies that SQLite foreign-key enforcement is enabled.
func Open(ctx context.Context, cfg config.DatabaseConfig, logger *slog.Logger) (*sql.DB, error) {
	if logger == nil {
		return nil, ErrDatabaseLoggerRequired
	}
	name, err := driverName(cfg.Driver)
	if err != nil {
		return nil, err
	}
	dsn := cfg.DSN
	foreignKeysDefaultApplied := false
	if cfg.Driver == config.DriverSQLite {
		dsn, foreignKeysDefaultApplied = sqliteDSNWithForeignKeys(dsn)
		if foreignKeysDefaultApplied {
			logger.InfoContext(ctx, "added SQLite foreign key pragma")
		}
	}
	pool, err := sql.Open(name, dsn)
	if err != nil {
		return nil, fmt.Errorf("open %s database: %w", cfg.Driver, err)
	}
	configurePool(pool, cfg)
	if err := verifyPool(ctx, pool, cfg.Driver, foreignKeysDefaultApplied); err != nil {
		_ = pool.Close()
		return nil, err
	}
	return pool, nil
}

func sqliteDSNWithForeignKeys(dsn string) (string, bool) {
	const parameter = "_pragma=foreign_keys(1)"
	dsn = sqliteDSNFileURI(dsn)
	query, hasQuery := sqliteDSNQuery(dsn)
	if hasQuery && !sqliteQueryNeedsForeignKeys(query) {
		return dsn, false
	}
	delimiter := "?"
	if hasQuery {
		delimiter = "&"
	}
	return dsn + delimiter + parameter, true
}

func sqliteDSNFileURI(dsn string) string {
	if len(dsn) == 0 || dsn[0] != '?' {
		return dsn
	}
	return "file:" + url.PathEscape(dsn)
}

// sqliteDSNQuery mirrors the driver's first-question-mark split. A question
// mark at byte zero leaves the DSN unsplit because it belongs to the filename.
func sqliteDSNQuery(dsn string) (string, bool) {
	position := strings.IndexRune(dsn, '?')
	if position < 1 {
		return "", false
	}
	return dsn[position+1:], true
}

func sqliteQueryNeedsForeignKeys(query string) bool {
	values, err := url.ParseQuery(query)
	if err != nil {
		// The driver rejects the same malformed query; preserve operator input.
		return false
	}
	if _, ok := values["_foreign_keys"]; ok {
		return false
	}
	if _, ok := values["_fk"]; ok {
		return false
	}
	for _, pragma := range values["_pragma"] {
		if strings.EqualFold(sqlitePragmaName(pragma), "foreign_keys") {
			return false
		}
	}
	return true
}

func sqlitePragmaName(pragma string) string {
	pragma = strings.TrimSpace(pragma)
	if end := strings.IndexAny(pragma, "(="); end >= 0 {
		pragma = pragma[:end]
	}
	return strings.TrimSpace(pragma)
}

func configurePool(pool *sql.DB, cfg config.DatabaseConfig) {
	pool.SetMaxOpenConns(cfg.MaxOpenConns)       // cap total open connections
	pool.SetMaxIdleConns(cfg.MaxIdleConns)       // idle floor; must be <= MaxOpenConns
	pool.SetConnMaxLifetime(cfg.ConnMaxLifetime) // bound age (required behind LB/proxy/failover)
	pool.SetConnMaxIdleTime(cfg.ConnMaxIdleTime) // reap idle connections under low load
}

func verifyPool(ctx context.Context, pool *sql.DB, driver config.Driver, foreignKeysDefaultApplied bool) error {
	pingCtx, cancel := context.WithTimeout(ctx, pingTimeout)
	defer cancel()
	if err := pool.PingContext(pingCtx); err != nil {
		return fmt.Errorf("ping %s database: %w", driver, err)
	}
	if driver == config.DriverSQLite {
		return verifySQLiteForeignKeys(pingCtx, pool, foreignKeysDefaultApplied)
	}
	return nil
}

func verifySQLiteForeignKeys(ctx context.Context, pool *sql.DB, compatibilityDefaultApplied bool) error {
	conn, err := pool.Conn(ctx)
	if err != nil {
		return fmt.Errorf("verify SQLite foreign keys: acquire connection: %w", err)
	}
	defer func() { _ = conn.Close() }()
	var enabled int
	if err := conn.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&enabled); err != nil {
		return fmt.Errorf("verify SQLite foreign keys: %w", err)
	}
	if enabled != 1 {
		if compatibilityDefaultApplied {
			return errors.New("verify SQLite foreign keys: disabled after compatibility default was applied to BLOOM_DB_DSN without a foreign-key directive")
		}
		return errors.New("verify SQLite foreign keys: disabled; BLOOM_DB_DSN contains a foreign-key directive that disables foreign keys and must be enabled or removed")
	}
	return nil
}
