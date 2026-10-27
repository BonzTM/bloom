package catalog

import (
	"context"
	"fmt"

	"github.com/BonzTM/bloom/internal/core"
)

// RecentlyAdded reads the newest movies and series the media servers added.
type RecentlyAdded struct {
	store core.RecentlyAddedStore
}

// NewRecentlyAdded validates and constructs a recently added reader.
func NewRecentlyAdded(store core.RecentlyAddedStore) (*RecentlyAdded, error) {
	if store == nil {
		return nil, fmt.Errorf("recently added: %w", core.ErrInvalidArgument)
	}
	return &RecentlyAdded{store: store}, nil
}

// List returns up to limit titles, newest first.
func (r *RecentlyAdded) List(ctx context.Context, limit int) ([]core.RecentlyAddedTitle, error) {
	if !core.ValidRecentlyAddedLimit(limit) {
		return nil, core.ErrInvalidArgument
	}
	rows, err := r.store.ListRecentlyAddedTitles(ctx, limit)
	if err != nil {
		return nil, fmt.Errorf("list recently added titles: %w", err)
	}
	titles := make([]core.RecentlyAddedTitle, 0, len(rows))
	for _, row := range rows {
		if title, ok := core.RecentlyAddedFromCatalog(row.Item); ok {
			titles = append(titles, title)
		}
	}
	return titles, nil
}
