package db

import (
	"database/sql"
	"fmt"

	"github.com/BonzTM/bloom/internal/config"
	"github.com/BonzTM/bloom/internal/core"
)

const maxMediaServerQueryPageSize = 101

// NewMediaServerStores returns the media-server read and write seams.
func NewMediaServerStores(pool *sql.DB, driver config.Driver) (core.MediaServerReader, core.MediaServerWriter, error) {
	switch driver {
	case config.DriverSQLite:
		adapter := newSQLiteMediaServers(pool)
		return adapter, adapter, nil
	case config.DriverPostgres:
		adapter := newPostgresMediaServers(pool)
		return adapter, adapter, nil
	default:
		return nil, nil, fmt.Errorf("unsupported database driver %q", driver)
	}
}

func validateMediaServerRecord(record core.MediaServerRecord) error {
	if record.ID == "" || !record.Kind.Valid() || len(record.CredentialCiphertext) == 0 {
		return core.ErrInvalidArgument
	}
	if err := core.ValidateMediaServerName(record.Name); err != nil {
		return err
	}
	baseURL, err := core.ValidateMediaServerURL(record.BaseURL, record.AllowInsecure)
	if err != nil || baseURL != record.BaseURL {
		return core.ErrInvalidArgument
	}
	return nil
}

func validateMediaServerPageSize(pageSize int) error {
	if pageSize < 1 || pageSize > maxMediaServerQueryPageSize {
		return core.ErrInvalidArgument
	}
	return nil
}
