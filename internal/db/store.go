package db

import (
	"database/sql"
	"fmt"

	"github.com/BonzTM/bloom/internal/config"
	"github.com/BonzTM/bloom/internal/core"
)

// NewAccountStore returns the core.AccountStore adapter for the configured
// engine, bound to pool. Open the pool with Open so the limits and ping are
// applied consistently. The two adapters share the SAME query file
// (internal/db/queries/accounts.sql); they differ only where the dialects do,
// which today is the created_at representation and the unique-violation error
// shape (ADR 0004).
func NewAccountStore(pool *sql.DB, d config.Driver) (core.AccountStore, error) {
	switch d {
	case config.DriverSQLite:
		return newSQLiteAccounts(pool), nil
	case config.DriverPostgres:
		return newPostgresAccounts(pool), nil
	default:
		return nil, fmt.Errorf("unsupported database driver %q", d)
	}
}
