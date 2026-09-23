package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/BonzTM/bloom/internal/config"
	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/db/postgres"
	"github.com/BonzTM/bloom/internal/db/sqlite"
)

type metadataProviders struct {
	driver   config.Driver
	sqlite   *sqlite.Queries
	postgres *postgres.Queries
}

var (
	_ core.MetadataProviderReader = (*metadataProviders)(nil)
	_ core.MetadataProviderWriter = (*metadataProviders)(nil)
)

// NewMetadataProviderStores constructs the encrypted provider configuration stores.
func NewMetadataProviderStores(pool *sql.DB, driver config.Driver) (core.MetadataProviderReader, core.MetadataProviderWriter, error) {
	if pool == nil {
		return nil, nil, fmt.Errorf("metadata provider stores: %w", core.ErrInvalidArgument)
	}
	store := &metadataProviders{driver: driver}
	switch driver {
	case config.DriverSQLite:
		store.sqlite = sqlite.New(pool)
	case config.DriverPostgres:
		store.postgres = postgres.New(pool)
	default:
		return nil, nil, fmt.Errorf("unsupported database driver %q", driver)
	}
	return store, store, nil
}

func (s *metadataProviders) GetMetadataProvider(ctx context.Context, kind core.MetadataProviderKind) (core.MetadataProviderRecord, error) {
	if !kind.Valid() {
		return core.MetadataProviderRecord{}, core.ErrInvalidArgument
	}
	if s.sqlite != nil {
		row, err := s.sqlite.GetMetadataProvider(ctx, string(kind))
		if err != nil {
			return core.MetadataProviderRecord{}, mapNotFound("select metadata provider", err)
		}
		created, err := parseSQLiteTime(row.CreatedAt)
		if err != nil {
			return core.MetadataProviderRecord{}, err
		}
		updated, err := parseSQLiteTime(row.UpdatedAt)
		if err != nil {
			return core.MetadataProviderRecord{}, err
		}
		return core.MetadataProviderRecord{Kind: core.MetadataProviderKind(row.Kind), CredentialCiphertext: row.CredentialCiphertext, KeyID: row.KeyID, CreatedAt: created, UpdatedAt: updated}, nil
	}
	row, err := s.postgres.GetMetadataProvider(ctx, string(kind))
	if err != nil {
		return core.MetadataProviderRecord{}, mapNotFound("select metadata provider", err)
	}
	return core.MetadataProviderRecord{
		Kind: core.MetadataProviderKind(row.Kind), CredentialCiphertext: row.CredentialCiphertext, KeyID: row.KeyID,
		CreatedAt: core.NormalizeTime(row.CreatedAt), UpdatedAt: core.NormalizeTime(row.UpdatedAt),
	}, nil
}

func (s *metadataProviders) UpsertMetadataProvider(ctx context.Context, record core.MetadataProviderRecord) error {
	if !record.Kind.Valid() || len(record.CredentialCiphertext) == 0 || record.KeyID == "" {
		return core.ErrInvalidArgument
	}
	if s.sqlite != nil {
		err := s.sqlite.UpsertMetadataProvider(ctx, sqlite.UpsertMetadataProviderParams{
			Kind: string(record.Kind), CredentialCiphertext: record.CredentialCiphertext,
			KeyID: record.KeyID, CreatedAt: formatSQLiteTime(record.CreatedAt), UpdatedAt: formatSQLiteTime(record.UpdatedAt),
		})
		return wrapDB("upsert metadata provider", err)
	}
	err := s.postgres.UpsertMetadataProvider(ctx, postgres.UpsertMetadataProviderParams{
		Kind: string(record.Kind), CredentialCiphertext: record.CredentialCiphertext,
		KeyID: record.KeyID, CreatedAt: core.NormalizeTime(record.CreatedAt), UpdatedAt: core.NormalizeTime(record.UpdatedAt),
	})
	return wrapDB("upsert metadata provider", err)
}

func (s *metadataProviders) DeleteMetadataProvider(ctx context.Context, kind core.MetadataProviderKind) error {
	var rows int64
	var err error
	if s.sqlite != nil {
		rows, err = s.sqlite.DeleteMetadataProvider(ctx, string(kind))
	} else {
		rows, err = s.postgres.DeleteMetadataProvider(ctx, string(kind))
	}
	if err != nil {
		return fmt.Errorf("delete metadata provider: %w", err)
	}
	if rows != 1 {
		return core.ErrNotFound
	}
	return nil
}

func mapNotFound(operation string, err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return core.ErrNotFound
	}
	return fmt.Errorf("%s: %w", operation, err)
}

func wrapDB(operation string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", operation, err)
}
