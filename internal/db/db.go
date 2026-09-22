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
	"fmt"
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

// Open opens a *sql.DB for the configured engine and DSN, applies all four pool
// limits from config (never the database/sql defaults, per the handbook's
// services/database.md), and verifies connectivity with PingContext so a
// service that cannot reach its store fails fast instead of reporting ready.
func Open(ctx context.Context, cfg config.DatabaseConfig) (*sql.DB, error) {
	name, err := driverName(cfg.Driver)
	if err != nil {
		return nil, err
	}
	pool, err := sql.Open(name, cfg.DSN)
	if err != nil {
		return nil, fmt.Errorf("open %s database: %w", cfg.Driver, err)
	}

	// Pool sizing is mandatory and explicit. The *sql.DB is a pool, not a
	// connection; the zero-value defaults are wrong for production.
	pool.SetMaxOpenConns(cfg.MaxOpenConns)       // cap total open connections
	pool.SetMaxIdleConns(cfg.MaxIdleConns)       // idle floor; must be <= MaxOpenConns
	pool.SetConnMaxLifetime(cfg.ConnMaxLifetime) // bound age (required behind LB/proxy/failover)
	pool.SetConnMaxIdleTime(cfg.ConnMaxIdleTime) // reap idle connections under low load

	pingCtx, cancel := context.WithTimeout(ctx, pingTimeout)
	defer cancel()
	if err := pool.PingContext(pingCtx); err != nil {
		// Best-effort close; the open failed so the pool is unusable.
		_ = pool.Close()
		return nil, fmt.Errorf("ping %s database: %w", cfg.Driver, err)
	}
	return pool, nil
}
