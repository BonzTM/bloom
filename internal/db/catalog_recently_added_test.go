package db_test

import (
	"database/sql"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/BonzTM/bloom/internal/config"
	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/db"
)

const recentlyAddedServerID = "8a000000-0000-4000-8000-000000000001"

func runRecentlyAddedEngineTests(t *testing.T, pool *sql.DB, driver config.Driver) {
	t.Helper()
	t.Run("recently added lists newest active movies and series outside excluded libraries", func(t *testing.T) {
		testRecentlyAddedTitles(t, pool, driver)
	})
}

func testRecentlyAddedTitles(t *testing.T, pool *sql.DB, driver config.Driver) {
	t.Helper()
	now := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	createCatalogServer(t, pool, driver, recentlyAddedServerID, "Recently Added Suite", now)
	seedRecentlyAddedItems(t, pool, driver, now)
	exclusions, err := db.NewExclusionStore(pool, driver)
	if err != nil {
		t.Fatalf("NewExclusionStore: %v", err)
	}
	err = exclusions.ReplaceExclusions(t.Context(), core.MediaServerExclusions{
		MediaServerID: recentlyAddedServerID, LibraryIDs: []string{"library-hidden"},
	})
	if err != nil {
		t.Fatalf("ReplaceExclusions: %v", err)
	}
	store, err := db.NewRecentlyAddedStore(pool, driver)
	if err != nil {
		t.Fatalf("NewRecentlyAddedStore: %v", err)
	}
	rows, err := store.ListRecentlyAddedTitles(t.Context(), 3)
	if err != nil {
		t.Fatalf("ListRecentlyAddedTitles: %v", err)
	}
	got := make([]string, 0, len(rows))
	for _, row := range rows {
		got = append(got, row.Item.ItemID)
	}
	// Other suites share the database, so the fixture is dated far ahead of
	// theirs and only its own newest rows can fill the first three places.
	if want := []string{"recent-series", "recent-movie-new", "recent-movie-old"}; !slices.Equal(got, want) {
		t.Fatalf("recently added = %v, want %v", got, want)
	}
	_, err = store.ListRecentlyAddedTitles(t.Context(), core.MaxRecentlyAddedLimit+1)
	if !errors.Is(err, core.ErrInvalidArgument) {
		t.Fatalf("over-limit error = %v, want invalid argument", err)
	}
}

func seedRecentlyAddedItems(t *testing.T, pool *sql.DB, driver config.Driver, now time.Time) {
	t.Helper()
	future := time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC)
	item := func(id, itemType, library string, created time.Time) core.LibraryItem {
		value := catalogSuiteItem(recentlyAddedServerID, id, id, now)
		value.ItemType, value.LibraryID, value.DateCreated = itemType, library, &created
		return value
	}
	items := []core.LibraryItem{
		item("recent-movie-old", "Movie", "library-1", future.Add(time.Hour)),
		item("recent-movie-new", "Movie", "library-1", future.Add(2*time.Hour)),
		item("recent-series", "Series", "library-2", future.Add(3*time.Hour)),
		item("recent-episode", "Episode", "library-2", future.Add(4*time.Hour)),
		item("recent-hidden", "Movie", "library-hidden", future.Add(5*time.Hour)),
		item("recent-archived", "Movie", "library-1", future.Add(6*time.Hour)),
	}
	catalog, err := db.NewLibraryCatalogStore(pool, driver)
	if err != nil {
		t.Fatalf("NewLibraryCatalogStore: %v", err)
	}
	commitRecentlyAddedSync(t, catalog, now, items, false)
	// A second walk that no longer sees the last item archives it.
	commitRecentlyAddedSync(t, catalog, now.Add(time.Hour), items[:len(items)-1], true)
}

func commitRecentlyAddedSync(
	t *testing.T, catalog core.LibraryCatalogStore, now time.Time, items []core.LibraryItem, archive bool,
) {
	t.Helper()
	seen := make([]core.LibraryItem, 0, len(items))
	for _, item := range items {
		item.LastSeenAt, item.UpdatedAt = now, now
		seen = append(seen, item)
	}
	sync := claimCatalogSync(t, catalog, recentlyAddedServerID, now)
	sync, err := catalog.CommitLibrarySyncPage(t.Context(), sync, seen, "", now.Add(time.Minute))
	if err != nil {
		t.Fatalf("CommitLibrarySyncPage: %v", err)
	}
	if archive {
		missing, missingErr := catalog.ListLibrarySyncMissingIDs(t.Context(), sync, "", core.CatalogUserDataBatchSize)
		if missingErr != nil || !slices.Equal(missing, []string{"recent-archived"}) {
			t.Fatalf("missing ids = %v, %v", missing, missingErr)
		}
		sync, err = catalog.CommitLibrarySyncArchives(t.Context(), sync, missing, "", now.Add(2*time.Minute))
		if err != nil {
			t.Fatalf("CommitLibrarySyncArchives: %v", err)
		}
	}
	if _, err := catalog.FinishLibrarySync(t.Context(), sync, now.Add(3*time.Minute)); err != nil {
		t.Fatalf("FinishLibrarySync: %v", err)
	}
}
