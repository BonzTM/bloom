package db

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/BonzTM/bloom/internal/config"
	"github.com/BonzTM/bloom/internal/core"
)

// NewRecentlyAddedStore returns the engine's recently added catalog reader.
func NewRecentlyAddedStore(pool *sql.DB, driver config.Driver) (core.RecentlyAddedStore, error) {
	if pool == nil {
		return nil, fmt.Errorf("recently added store: %w", core.ErrInvalidArgument)
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

func (s *sqliteCatalogStore) ListRecentlyAddedTitles(ctx context.Context, limit int) ([]core.CatalogItemStats, error) {
	if !core.ValidRecentlyAddedLimit(limit) {
		return nil, core.ErrInvalidArgument
	}
	rows, err := s.q.ListRecentlyAddedTitles(ctx, int64(limit))
	if err != nil {
		return nil, catalogStoreError("list recently added titles", err)
	}
	result := make([]core.CatalogItemStats, 0, len(rows))
	for _, row := range rows {
		stats, mapErr := sqliteCatalogStats(row)
		if mapErr != nil {
			return nil, mapErr
		}
		result = append(result, stats)
	}
	return result, nil
}

func (s *postgresCatalogStore) ListRecentlyAddedTitles(ctx context.Context, limit int) ([]core.CatalogItemStats, error) {
	if !core.ValidRecentlyAddedLimit(limit) {
		return nil, core.ErrInvalidArgument
	}
	rows, err := s.q.ListRecentlyAddedTitles(ctx, int64(limit))
	if err != nil {
		return nil, catalogStoreError("list recently added titles", err)
	}
	result := make([]core.CatalogItemStats, 0, len(rows))
	for _, row := range rows {
		stats, mapErr := postgresCatalogStats(row)
		if mapErr != nil {
			return nil, mapErr
		}
		result = append(result, stats)
	}
	return result, nil
}
