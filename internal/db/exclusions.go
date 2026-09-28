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

const (
	exclusionKindUser    = "media_user"
	exclusionKindLibrary = "library"
)

// NewExclusionStore returns the per-server exclusion settings store.
func NewExclusionStore(pool *sql.DB, driver config.Driver) (core.ExclusionStore, error) {
	if pool == nil {
		return nil, fmt.Errorf("build exclusion store: %w", core.ErrInvalidArgument)
	}
	switch driver {
	case config.DriverSQLite:
		return &sqliteExclusionStore{pool: pool, q: sqlite.New(pool)}, nil
	case config.DriverPostgres:
		return &postgresExclusionStore{pool: pool, q: postgres.New(pool)}, nil
	default:
		return nil, fmt.Errorf("unsupported database driver %q", driver)
	}
}

type sqliteExclusionStore struct {
	pool *sql.DB
	q    *sqlite.Queries
}

func (s *sqliteExclusionStore) GetExclusions(
	ctx context.Context, serverID string,
) (core.MediaServerExclusions, error) {
	if !core.ValidID(serverID) {
		return core.MediaServerExclusions{}, core.ErrInvalidArgument
	}
	rows, err := s.q.ListMediaServerExclusions(ctx, serverID)
	if err != nil {
		return core.MediaServerExclusions{}, fmt.Errorf("list media server exclusions: %w", err)
	}
	result := core.MediaServerExclusions{MediaServerID: serverID}
	for _, row := range rows {
		appendExclusion(&result, row.Kind, row.ExternalID)
	}
	return result, nil
}

func (s *sqliteExclusionStore) ReplaceExclusions(
	ctx context.Context, value core.MediaServerExclusions,
) error {
	if !value.Valid() {
		return core.ErrInvalidArgument
	}
	err := withSQLiteWriteTransaction(ctx, s.pool, func(conn *sql.Conn) error {
		queries := sqlite.New(conn)
		if deleteErr := queries.DeleteMediaServerExclusions(ctx, value.MediaServerID); deleteErr != nil {
			return deleteErr
		}
		if insertErr := insertSQLiteExclusions(ctx, queries, value); insertErr != nil {
			return insertErr
		}
		return queries.RebuildLibraryItemRollups(ctx, value.MediaServerID)
	})
	if err != nil {
		return fmt.Errorf("replace media server exclusions: %w", err)
	}
	return nil
}

func insertSQLiteExclusions(
	ctx context.Context, queries *sqlite.Queries, value core.MediaServerExclusions,
) error {
	for _, id := range value.MediaUserIDs {
		if err := queries.InsertMediaServerExclusion(ctx, sqlite.InsertMediaServerExclusionParams{
			MediaServerID: value.MediaServerID, Kind: exclusionKindUser, ExternalID: id,
		}); err != nil {
			return err
		}
	}
	for _, id := range value.LibraryIDs {
		if err := queries.InsertMediaServerExclusion(ctx, sqlite.InsertMediaServerExclusionParams{
			MediaServerID: value.MediaServerID, Kind: exclusionKindLibrary, ExternalID: id,
		}); err != nil {
			return err
		}
	}
	return nil
}

type postgresExclusionStore struct {
	pool *sql.DB
	q    *postgres.Queries
}

func (s *postgresExclusionStore) GetExclusions(
	ctx context.Context, serverID string,
) (core.MediaServerExclusions, error) {
	if !core.ValidID(serverID) {
		return core.MediaServerExclusions{}, core.ErrInvalidArgument
	}
	rows, err := s.q.ListMediaServerExclusions(ctx, serverID)
	if err != nil {
		return core.MediaServerExclusions{}, fmt.Errorf("list media server exclusions: %w", err)
	}
	result := core.MediaServerExclusions{MediaServerID: serverID}
	for _, row := range rows {
		appendExclusion(&result, row.Kind, row.ExternalID)
	}
	return result, nil
}

func (s *postgresExclusionStore) ReplaceExclusions(
	ctx context.Context, value core.MediaServerExclusions,
) (result error) {
	if !value.Valid() {
		return core.ErrInvalidArgument
	}
	tx, err := s.pool.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin exclusion replacement: %w", err)
	}
	defer func() {
		if rollbackErr := tx.Rollback(); rollbackErr != nil && !errors.Is(rollbackErr, sql.ErrTxDone) {
			result = errors.Join(result, rollbackErr)
		}
	}()
	queries := s.q.WithTx(tx)
	if _, err := queries.LockMediaServerForExclusionReplace(ctx, value.MediaServerID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return core.ErrNotFound
		}
		return fmt.Errorf("lock media server for exclusion replacement: %w", err)
	}
	if err := queries.DeleteMediaServerExclusions(ctx, value.MediaServerID); err != nil {
		return fmt.Errorf("delete media server exclusions: %w", err)
	}
	if err := insertPostgresExclusions(ctx, queries, value); err != nil {
		return fmt.Errorf("insert media server exclusions: %w", err)
	}
	if err := queries.RebuildLibraryItemRollups(ctx, value.MediaServerID); err != nil {
		return fmt.Errorf("rebuild catalog rollups: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit exclusion replacement: %w", err)
	}
	return nil
}

func insertPostgresExclusions(
	ctx context.Context, queries *postgres.Queries, value core.MediaServerExclusions,
) error {
	for _, id := range value.MediaUserIDs {
		if err := queries.InsertMediaServerExclusion(ctx, postgres.InsertMediaServerExclusionParams{
			MediaServerID: value.MediaServerID, Kind: exclusionKindUser, ExternalID: id,
		}); err != nil {
			return err
		}
	}
	for _, id := range value.LibraryIDs {
		if err := queries.InsertMediaServerExclusion(ctx, postgres.InsertMediaServerExclusionParams{
			MediaServerID: value.MediaServerID, Kind: exclusionKindLibrary, ExternalID: id,
		}); err != nil {
			return err
		}
	}
	return nil
}

func appendExclusion(value *core.MediaServerExclusions, kind, id string) {
	if kind == exclusionKindUser {
		value.MediaUserIDs = append(value.MediaUserIDs, id)
		return
	}
	value.LibraryIDs = append(value.LibraryIDs, id)
}
