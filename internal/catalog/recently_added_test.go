package catalog

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/BonzTM/bloom/internal/core"
)

type recentlyAddedStoreStub struct {
	rows  []core.CatalogItemStats
	err   error
	limit int
}

func (s *recentlyAddedStoreStub) ListRecentlyAddedTitles(_ context.Context, limit int) ([]core.CatalogItemStats, error) {
	s.limit = limit
	return s.rows, s.err
}

func TestRecentlyAddedListMapsRowsAndDropsUnknownTypes(t *testing.T) {
	added := time.Date(2026, 10, 20, 8, 0, 0, 0, time.UTC)
	store := &recentlyAddedStoreStub{rows: []core.CatalogItemStats{
		{Item: core.LibraryItem{MediaServerID: "server", ItemID: "movie", ItemType: "Movie", Name: "Film", DateCreated: &added}},
		{Item: core.LibraryItem{MediaServerID: "server", ItemID: "folder", ItemType: "Folder", Name: "Folder", DateCreated: &added}},
	}}
	reader, err := NewRecentlyAdded(store)
	if err != nil {
		t.Fatalf("NewRecentlyAdded: %v", err)
	}
	titles, err := reader.List(t.Context(), 20)
	if err != nil || len(titles) != 1 || titles[0].ItemID != "movie" || titles[0].Kind != core.MediaKindMovie {
		t.Fatalf("List = %+v, %v", titles, err)
	}
	if store.limit != 20 {
		t.Fatalf("store limit = %d, want 20", store.limit)
	}
}

func TestRecentlyAddedListRejectsLimitAndWrapsStoreFailure(t *testing.T) {
	store := &recentlyAddedStoreStub{err: errors.New("database down")}
	reader, err := NewRecentlyAdded(store)
	if err != nil {
		t.Fatalf("NewRecentlyAdded: %v", err)
	}
	if _, err := reader.List(t.Context(), core.MaxRecentlyAddedLimit+1); !errors.Is(err, core.ErrInvalidArgument) || store.limit != 0 {
		t.Fatalf("over-limit List error = %v, store limit %d", err, store.limit)
	}
	if _, err := reader.List(t.Context(), 1); !errors.Is(err, store.err) {
		t.Fatalf("List error = %v, want wrapped store failure", err)
	}
	if _, err := NewRecentlyAdded(nil); !errors.Is(err, core.ErrInvalidArgument) {
		t.Fatalf("NewRecentlyAdded(nil) error = %v", err)
	}
}
