package db

import (
	"database/sql"
	"fmt"

	"github.com/BonzTM/bloom/internal/config"
	"github.com/BonzTM/bloom/internal/core"
)

// NewAccountMediaUserStores returns account-to-media-user persistence for one engine.
func NewAccountMediaUserStores(
	pool *sql.DB, driver config.Driver,
) (core.AccountMediaUserReader, core.AccountMediaUserWriter, error) {
	if pool == nil {
		return nil, nil, fmt.Errorf("account media user stores: %w", core.ErrInvalidArgument)
	}
	switch driver {
	case config.DriverSQLite:
		adapter := newSQLiteAccountMediaUsers(pool)
		return adapter, adapter, nil
	case config.DriverPostgres:
		adapter := newPostgresAccountMediaUsers(pool)
		return adapter, adapter, nil
	default:
		return nil, nil, fmt.Errorf("unsupported database driver %q", driver)
	}
}

func validateAccountMediaUserQuery(accountID, mediaServerID string) error {
	if !core.ValidID(accountID) || !core.ValidID(mediaServerID) {
		return core.ErrInvalidArgument
	}
	return nil
}

func validateAccountMediaUserList(accountID string, limit int) error {
	if !core.ValidID(accountID) || limit < 1 || limit > core.MaxAccountMediaUsers {
		return core.ErrInvalidArgument
	}
	return nil
}
