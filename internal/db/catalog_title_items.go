package db

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/BonzTM/bloom/internal/config"
	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/db/postgres"
	"github.com/BonzTM/bloom/internal/db/sqlite"
)

// NewCatalogTitleStore returns the engine's catalog title resolver.
func NewCatalogTitleStore(pool *sql.DB, driver config.Driver) (core.CatalogTitleItems, error) {
	if pool == nil {
		return nil, fmt.Errorf("catalog title store: %w", core.ErrInvalidArgument)
	}
	switch driver {
	case config.DriverSQLite:
		return newSQLiteCatalogStore(pool), nil
	case config.DriverPostgres:
		return newPostgresCatalogStore(pool), nil
	default:
		return nil, fmt.Errorf("unsupported database driver %q", driver)
	}
}

func (s *sqliteCatalogStore) ListCatalogTitleItemIDs(ctx context.Context, query core.CatalogTitleQuery) ([]string, error) {
	if !query.Valid() {
		return nil, core.ErrInvalidArgument
	}
	ids, err := s.q.ListCatalogTitleItemIDs(ctx, sqlite.ListCatalogTitleItemIDsParams{
		MediaServerID: query.MediaServerID, ItemType: query.ItemType(),
		TmdbID: sql.NullInt64{Int64: query.TMDBID, Valid: true}, RowLimit: int64(query.Limit),
	})
	if err != nil {
		return nil, catalogStoreError("list catalog title items", err)
	}
	return ids, nil
}

func (s *postgresCatalogStore) ListCatalogTitleItemIDs(ctx context.Context, query core.CatalogTitleQuery) ([]string, error) {
	if !query.Valid() {
		return nil, core.ErrInvalidArgument
	}
	ids, err := s.q.ListCatalogTitleItemIDs(ctx, postgres.ListCatalogTitleItemIDsParams{
		MediaServerID: query.MediaServerID, ItemType: query.ItemType(),
		TmdbID: sql.NullInt64{Int64: query.TMDBID, Valid: true}, RowLimit: int64(query.Limit),
	})
	if err != nil {
		return nil, catalogStoreError("list catalog title items", err)
	}
	return ids, nil
}
