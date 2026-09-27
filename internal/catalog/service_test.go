package catalog

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/testutil"
)

func TestServiceCachesAggregateReadsAndReturnsCopies(t *testing.T) {
	t.Parallel()
	clock := testutil.NewFakeClock(time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC))
	store := &fakeCatalogStore{
		libraries: []core.CatalogLibrarySummary{{LibraryID: "library", Types: []core.CatalogTypeCount{{ItemType: "Movie", Items: 1}}}},
		genres:    []core.CatalogGenreSummary{{Genre: "Drama", Items: 1}},
	}
	service, err := NewService(store, catalogServerStub{}, nil, clock, time.Minute)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	window := core.CatalogWindow{Enabled: true, Start: clock.Now().Add(-24 * time.Hour), End: clock.Now()}

	first, err := service.ListLibraries(context.Background(), catalogWorkerServerID, window)
	if err != nil {
		t.Fatalf("first ListLibraries: %v", err)
	}
	first[0].Types[0].Items = 99
	second, err := service.ListLibraries(context.Background(), catalogWorkerServerID, window)
	if err != nil || store.libraryCalls != 1 || second[0].Types[0].Items != 1 {
		t.Fatalf("cached libraries = %+v, calls = %d, err = %v", second, store.libraryCalls, err)
	}

	if _, err = service.Genres(context.Background(), catalogWorkerServerID, "library", window); err != nil {
		t.Fatalf("first Genres: %v", err)
	}
	if _, err = service.Genres(context.Background(), catalogWorkerServerID, "library", window); err != nil {
		t.Fatalf("second Genres: %v", err)
	}
	if store.genreCalls != 1 {
		t.Fatalf("genre calls = %d, want 1", store.genreCalls)
	}
}

type catalogServerStub struct{ err error }

func (s catalogServerStub) Get(context.Context, string) (core.MediaServerConnection, error) {
	return core.MediaServerConnection{}, s.err
}

func TestRequestSyncRejectsUnknownServerBeforePersistence(t *testing.T) {
	t.Parallel()
	store := &fakeCatalogStore{}
	service, err := NewService(store, catalogServerStub{err: core.ErrNotFound}, nil,
		testutil.NewFakeClock(time.Now()), time.Minute)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	if _, err := service.RequestSync(t.Context(), catalogWorkerServerID); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("RequestSync = %v, want ErrNotFound", err)
	}
	if store.requested {
		t.Fatal("RequestLibrarySync was called for an unknown server")
	}
}
