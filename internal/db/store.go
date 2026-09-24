package db

import (
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/BonzTM/bloom/internal/config"
	"github.com/BonzTM/bloom/internal/core"
)

const maxAccountUsernameLookup = 101

// NewAccountStores returns the two consumer-owned account persistence
// boundaries for the configured
// engine, bound to pool. Open the pool with Open so the limits and ping are
// applied consistently. The two adapters share the SAME query file
// (internal/db/queries/accounts.sql); they differ only where the dialects do,
// which today is the created_at representation and the unique-violation error
// shape (ADR 0004).
func NewAccountStores(pool *sql.DB, d config.Driver) (core.AccountStore, core.LocalIdentityStore, error) {
	switch d {
	case config.DriverSQLite:
		adapter := newSQLiteAccounts(pool)
		return adapter, adapter, nil
	case config.DriverPostgres:
		adapter := newPostgresAccounts(pool)
		return adapter, adapter, nil
	default:
		return nil, nil, fmt.Errorf("unsupported database driver %q", d)
	}
}

func accountIDsJSON(accountIDs []string) (string, error) {
	if len(accountIDs) > maxAccountUsernameLookup {
		return "", core.ErrInvalidArgument
	}
	encoded, err := json.Marshal(accountIDs)
	if err != nil {
		return "", fmt.Errorf("encode account ids: %w", err)
	}
	return string(encoded), nil
}
