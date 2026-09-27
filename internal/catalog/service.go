// Package catalog coordinates the durable, type-agnostic media library catalog.
package catalog

import (
	"context"
	"fmt"
	"time"

	"github.com/BonzTM/bloom/internal/core"
)

// Service provides catalog commands and bounded reads to the HTTP layer.
type Service struct {
	store   core.LibraryCatalogStore
	servers interface {
		Get(context.Context, string) (core.MediaServerConnection, error)
	}
	wake  chan<- struct{}
	cache *resultCache
}

// NewService validates and constructs a catalog service.
func NewService(
	store core.LibraryCatalogStore,
	servers interface {
		Get(context.Context, string) (core.MediaServerConnection, error)
	},
	wake chan<- struct{}, clock core.Clock, cacheTTL time.Duration,
) (*Service, error) {
	if store == nil || servers == nil || clock == nil || cacheTTL < 0 {
		return nil, fmt.Errorf("catalog service: %w", core.ErrInvalidArgument)
	}
	return &Service{store: store, servers: servers, wake: wake, cache: newResultCache(clock, cacheTTL)}, nil
}

// RequestSync durably schedules an immediate full walk.
func (s *Service) RequestSync(ctx context.Context, serverID string) (core.LibrarySync, error) {
	if _, err := s.servers.Get(ctx, serverID); err != nil {
		return core.LibrarySync{}, err
	}
	sync, err := s.store.RequestLibrarySync(ctx, serverID)
	if err != nil {
		return core.LibrarySync{}, err
	}
	s.cache.clear()
	if s.wake != nil {
		select {
		case s.wake <- struct{}{}:
		default:
		}
	}
	return sync, nil
}

// ListLibraries returns active item and play totals grouped by library and type.
func (s *Service) ListLibraries(
	ctx context.Context, serverID string, window core.CatalogWindow,
) ([]core.CatalogLibrarySummary, error) {
	window = canonicalWindow(window, s.cache.ttl)
	key := catalogCacheKey{kind: cacheLibraries, serverID: serverID, window: window}
	if value, ok := s.cache.getLibraries(key); ok {
		return value, nil
	}
	value, err := s.store.ListCatalogLibraries(ctx, serverID, window)
	if err == nil {
		s.cache.putLibraries(key, value)
	}
	return value, err
}

// ListItems returns one bounded catalog item page.
func (s *Service) ListItems(
	ctx context.Context, query core.CatalogItemQuery,
) ([]core.CatalogItemStats, error) {
	return s.store.ListCatalogItems(ctx, query)
}

// Item returns catalog metadata, descendants, and play totals for one item.
func (s *Service) Item(ctx context.Context, serverID, itemID string) (core.CatalogItemDetail, error) {
	return s.store.GetCatalogItem(ctx, serverID, itemID)
}

// History returns one bounded item-or-descendant watch page.
func (s *Service) History(
	ctx context.Context, query core.CatalogHistoryQuery,
) ([]core.PlaybackWatch, error) {
	return s.store.ListCatalogHistory(ctx, query)
}

// Recent returns the most recently created active catalog items.
func (s *Service) Recent(
	ctx context.Context, serverID, libraryID string, limit int,
) ([]core.CatalogItemStats, error) {
	return s.store.ListRecentCatalogItems(ctx, serverID, libraryID, limit)
}

// Genres returns active item and play totals grouped by genre.
func (s *Service) Genres(
	ctx context.Context, serverID, libraryID string, window core.CatalogWindow,
) ([]core.CatalogGenreSummary, error) {
	window = canonicalWindow(window, s.cache.ttl)
	key := catalogCacheKey{kind: cacheGenres, serverID: serverID, libraryID: libraryID, window: window}
	if value, ok := s.cache.getGenres(key); ok {
		return value, nil
	}
	value, err := s.store.ListCatalogGenres(ctx, serverID, libraryID, window)
	if err == nil {
		s.cache.putGenres(key, value)
	}
	return value, err
}

// Stale returns active items without a play on or after the cutoff.
func (s *Service) Stale(
	ctx context.Context, query core.CatalogStaleQuery,
) ([]core.CatalogItemStats, error) {
	return s.store.ListStaleCatalogItems(ctx, query)
}
