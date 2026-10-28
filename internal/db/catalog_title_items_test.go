package db_test

import (
	"database/sql"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/BonzTM/bloom/internal/config"
	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/db"
)

const (
	catalogTitleServerID = "8a000000-0000-4000-8000-000000000002"
	catalogTitleOtherID  = "8a000000-0000-4000-8000-000000000003"
)

func runCatalogTitleEngineTests(t *testing.T, pool *sql.DB, driver config.Driver) {
	t.Helper()
	t.Run("catalog title items resolve active items by kind and TMDB ID", func(t *testing.T) {
		testCatalogTitleItems(t, pool, driver)
	})
	t.Run("catalog title items read their partial index", func(t *testing.T) {
		testCatalogTitleItemsUsesIndex(t, pool, driver)
	})
}

func testCatalogTitleItems(t *testing.T, pool *sql.DB, driver config.Driver) {
	t.Helper()
	now := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	createCatalogServer(t, pool, driver, catalogTitleServerID, "Catalog Title Suite", now)
	createCatalogServer(t, pool, driver, catalogTitleOtherID, "Catalog Title Other", now)
	seedCatalogTitleItems(t, pool, driver, now)
	store, err := db.NewCatalogTitleStore(pool, driver)
	if err != nil {
		t.Fatalf("NewCatalogTitleStore: %v", err)
	}
	query := core.CatalogTitleQuery{
		MediaServerID: catalogTitleServerID, Kind: core.MediaKindMovie, TMDBID: 603, Limit: core.MaxCatalogTitleItems,
	}
	assertCatalogTitleItems(t, store, query, []string{"matrix-a", "matrix-b"})
	limited := query
	limited.Limit = 1
	assertCatalogTitleItems(t, store, limited, []string{"matrix-a"})
	series := query
	series.Kind = core.MediaKindSeries
	assertCatalogTitleItems(t, store, series, []string{"series-603"})
	missing := query
	missing.TMDBID = 999
	assertCatalogTitleItems(t, store, missing, []string{})
	other := query
	other.MediaServerID = catalogTitleOtherID
	assertCatalogTitleItems(t, store, other, []string{})
	invalid := query
	invalid.Limit = core.MaxCatalogTitleItems + 1
	if _, err := store.ListCatalogTitleItemIDs(t.Context(), invalid); !errors.Is(err, core.ErrInvalidArgument) {
		t.Fatalf("over-limit error = %v, want invalid argument", err)
	}
}

func assertCatalogTitleItems(t *testing.T, store core.CatalogTitleItems, query core.CatalogTitleQuery, want []string) {
	t.Helper()
	got, err := store.ListCatalogTitleItemIDs(t.Context(), query)
	if err != nil {
		t.Fatalf("ListCatalogTitleItemIDs(%+v): %v", query, err)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("ListCatalogTitleItemIDs(%+v) = %v, want %v", query, got, want)
	}
}

func seedCatalogTitleItems(t *testing.T, pool *sql.DB, driver config.Driver, now time.Time) {
	t.Helper()
	item := func(id, itemType, library string, tmdbID int64) core.LibraryItem {
		value := catalogSuiteItem(catalogTitleServerID, id, id, now)
		value.ItemType, value.LibraryID, value.TMDBID = itemType, library, tmdbID
		return value
	}
	items := []core.LibraryItem{
		item("matrix-b", "Movie", "library-2", 603),
		item("matrix-a", "Movie", "library-1", 603),
		item("series-603", "Series", "library-3", 603),
		item("reloaded", "Movie", "library-1", 604),
		item("untagged", "Movie", "library-1", 0),
		item("matrix-gone", "Movie", "library-1", 603),
	}
	catalog, err := db.NewLibraryCatalogStore(pool, driver)
	if err != nil {
		t.Fatalf("NewLibraryCatalogStore: %v", err)
	}
	commitCatalogTitleSync(t, catalog, now, items, "")
	// A second walk that no longer sees the last item archives it.
	commitCatalogTitleSync(t, catalog, now.Add(time.Hour), items[:len(items)-1], "matrix-gone")
}

func commitCatalogTitleSync(
	t *testing.T, catalog core.LibraryCatalogStore, now time.Time, items []core.LibraryItem, archived string,
) {
	t.Helper()
	seen := make([]core.LibraryItem, 0, len(items))
	for _, item := range items {
		item.LastSeenAt, item.UpdatedAt = now, now
		seen = append(seen, item)
	}
	sync := claimCatalogSync(t, catalog, catalogTitleServerID, now)
	sync, err := catalog.CommitLibrarySyncPage(t.Context(), sync, seen, "", now.Add(time.Minute))
	if err != nil {
		t.Fatalf("CommitLibrarySyncPage: %v", err)
	}
	if archived != "" {
		missing, missingErr := catalog.ListLibrarySyncMissingIDs(t.Context(), sync, "", core.CatalogUserDataBatchSize)
		if missingErr != nil || !slices.Equal(missing, []string{archived}) {
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

// A title lookup runs on every title page, so it must reach its rows through
// the partial index instead of scanning every episode of the server.
func testCatalogTitleItemsUsesIndex(t *testing.T, pool *sql.DB, driver config.Driver) {
	t.Helper()
	source, err := os.ReadFile("queries/catalog_title_items.sql")
	if err != nil {
		t.Fatalf("read query: %v", err)
	}
	_, query, found := strings.Cut(string(source), ":many\n")
	if !found {
		t.Fatal("catalog title query has no sqlc header")
	}
	query = strings.NewReplacer(
		"sqlc.arg(media_server_id)", "'"+catalogTitleServerID+"'", "sqlc.arg(item_type)", "'Movie'",
		"sqlc.arg(tmdb_id)", "603", "CAST(sqlc.arg(row_limit) AS BIGINT)", "5",
	).Replace(query)
	query = strings.TrimSuffix(strings.TrimSpace(query), ";")
	tx, err := pool.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() {
		if rollbackErr := tx.Rollback(); rollbackErr != nil && !errors.Is(rollbackErr, sql.ErrTxDone) {
			t.Errorf("rollback: %v", rollbackErr)
		}
	}()
	explain := "EXPLAIN QUERY PLAN "
	if driver == config.DriverPostgres {
		// The suite's table is tiny, so a sequential scan would win on cost.
		if _, err = tx.ExecContext(t.Context(), "SET LOCAL enable_seqscan = off"); err != nil {
			t.Fatalf("disable seqscan: %v", err)
		}
		explain = "EXPLAIN "
	}
	plan := readQueryPlan(t, tx, explain+query)
	if !strings.Contains(plan, "library_items_tmdb_idx") {
		t.Fatalf("catalog title plan does not use its index:\n%s", plan)
	}
}
