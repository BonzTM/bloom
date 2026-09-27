package db_test

import (
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/BonzTM/bloom/internal/config"
	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/db"
)

const catalogPagingServerID = "82000000-0000-4000-8000-000000000001"

func runCatalogEngineTests(t *testing.T, pool *sql.DB, driver config.Driver) {
	t.Helper()
	t.Run("catalog upsert archive and paging", func(t *testing.T) {
		testCatalogUpsertArchivePaging(t, pool, driver)
	})
	t.Run("every catalog sort uses stable keyset pages", func(t *testing.T) {
		testCatalogKeysetPages(t, pool, driver)
	})
	t.Run("catalog lease fencing rejects reclaimed workers", func(t *testing.T) {
		testCatalogLeaseFencing(t, pool, driver)
	})
	t.Run("genre aggregation is not bounded by library size", func(t *testing.T) {
		testLargeCatalogGenres(t, pool, driver)
	})
	t.Run("every catalog sort plan is index backed", func(t *testing.T) {
		testCatalogPlans(t, pool, driver)
	})
	t.Run("catalog detail and history plans seek target watches", func(t *testing.T) {
		testCatalogDetailHistoryPlans(t, pool, driver)
	})
	t.Run("catalog detail and history include scoped descendants", func(t *testing.T) {
		testCatalogDescendantReads(t, pool, driver)
	})
	t.Run("sync completion rebuilds catalog rollups", func(t *testing.T) {
		testCatalogRollupRebuild(t, pool, driver)
	})
}

func testCatalogUpsertArchivePaging(t *testing.T, pool *sql.DB, driver config.Driver) {
	t.Helper()
	reader, writer, storesErr := db.NewMediaServerStores(pool, driver)
	if storesErr != nil || reader == nil {
		t.Fatalf("NewMediaServerStores: %v", storesErr)
	}
	now := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	serverID := "81000000-0000-4000-8000-000000000001"
	record := core.MediaServerRecord{MediaServer: core.MediaServer{
		ID: serverID, Kind: core.MediaServerKindJellyfin, Name: "Catalog Suite", BaseURL: "https://catalog.invalid",
		CreatedAt: now, UpdatedAt: now,
	}, CredentialCiphertext: []byte("encrypted")}
	if err := writer.CreateMediaServer(t.Context(), record); err != nil {
		t.Fatalf("CreateMediaServer: %v", err)
	}
	store, storeErr := db.NewLibraryCatalogStore(pool, driver)
	if storeErr != nil {
		t.Fatalf("NewLibraryCatalogStore: %v", storeErr)
	}
	first := claimCatalogSync(t, store, serverID, now)
	if _, requestErr := store.RequestLibrarySync(t.Context(), serverID); !errors.Is(requestErr, core.ErrLibrarySyncInProgress) {
		t.Fatalf("overlapping RequestLibrarySync = %v, want sync in progress", requestErr)
	}
	items := []core.LibraryItem{
		catalogSuiteItem(serverID, "item-b", "Beta", now),
		catalogSuiteItem(serverID, "item-a", "Alpha", now),
	}
	first, commitErr := store.CommitLibrarySyncPage(t.Context(), first, items, "", now.Add(time.Minute))
	if commitErr != nil {
		t.Fatalf("CommitLibrarySyncPage: %v", commitErr)
	}
	if _, err := store.FinishLibrarySync(t.Context(), first, now.Add(2*time.Minute)); err != nil {
		t.Fatalf("FinishLibrarySync: %v", err)
	}
	assertCatalogPage(t, store, serverID, []string{"item-a", "item-b"}, false)
	archiveMissingCatalogItem(t, store, serverID, now)
}

func archiveMissingCatalogItem(t *testing.T, store core.LibraryCatalogStore, serverID string, now time.Time) {
	t.Helper()
	if _, err := store.RequestLibrarySync(t.Context(), serverID); err != nil {
		t.Fatalf("second RequestLibrarySync: %v", err)
	}
	sync := claimScheduledCatalogSync(t, store, serverID, now.Add(2*time.Hour))
	item := catalogSuiteItem(serverID, "item-a", "Alpha Updated", now.Add(2*time.Hour))
	sync, err := store.CommitLibrarySyncPage(t.Context(), sync, []core.LibraryItem{item}, "", now.Add(2*time.Hour))
	if err != nil {
		t.Fatalf("second CommitLibrarySyncPage: %v", err)
	}
	missing, err := store.ListLibrarySyncMissingIDs(t.Context(), sync, "", core.CatalogUserDataBatchSize)
	if err != nil || !slices.Equal(missing, []string{"item-b"}) {
		t.Fatalf("missing ids = %v, %v", missing, err)
	}
	sync, err = store.CommitLibrarySyncArchives(t.Context(), sync, missing, `{"phase":"revalidation","after_item_id":"item-b"}`, now.Add(2*time.Hour+time.Minute))
	if err != nil {
		t.Fatalf("CommitLibrarySyncArchives: %v", err)
	}
	finished, err := store.FinishLibrarySync(t.Context(), sync, now.Add(2*time.Hour+time.Minute))
	if err != nil || finished.Archived != 1 {
		t.Fatalf("second FinishLibrarySync = %+v, %v", finished, err)
	}
	assertCatalogPage(t, store, serverID, []string{"item-a"}, false)
	assertCatalogPage(t, store, serverID, []string{"item-b"}, true)
}

func testCatalogLeaseFencing(t *testing.T, pool *sql.DB, driver config.Driver) {
	t.Helper()
	now := time.Date(2026, 9, 27, 20, 0, 0, 0, time.UTC)
	serverID := "84000000-0000-4000-8000-000000000001"
	createCatalogServer(t, pool, driver, serverID, "Catalog Fencing", now)
	store, err := db.NewLibraryCatalogStore(pool, driver)
	if err != nil {
		t.Fatalf("NewLibraryCatalogStore: %v", err)
	}
	seed := claimCatalogSync(t, store, serverID, now)
	seed, err = store.CommitLibrarySyncPage(t.Context(), seed,
		[]core.LibraryItem{catalogSuiteItem(serverID, "old-item", "Old", now)}, "", now)
	if err != nil {
		t.Fatalf("seed catalog: %v", err)
	}
	if _, err = store.FinishLibrarySync(t.Context(), seed, now); err != nil {
		t.Fatalf("finish seed catalog: %v", err)
	}
	workerA, workerB := reclaimCatalogLease(t, store, serverID, now.Add(time.Hour))
	assertStaleCatalogWorkerRejected(t, store, workerA, now.Add(2*time.Hour))
	workerB, err = store.CommitLibrarySyncArchives(t.Context(), workerB, []string{"old-item"}, `{"phase":"revalidation","after_item_id":"old-item"}`, now.Add(2*time.Hour))
	if err != nil {
		t.Fatalf("worker B archive: %v", err)
	}
	if _, err = store.FinishLibrarySync(t.Context(), workerB, now.Add(2*time.Hour)); err != nil {
		t.Fatalf("worker B finish: %v", err)
	}
	assertCatalogPage(t, store, serverID, []string{"old-item"}, true)
}

func testCatalogRollupRebuild(t *testing.T, pool *sql.DB, driver config.Driver) {
	t.Helper()
	now := time.Date(2026, 9, 27, 21, 0, 0, 0, time.UTC)
	serverID := "85000000-0000-4000-8000-000000000001"
	createCatalogServer(t, pool, driver, serverID, "Catalog Rollup", now)
	store, err := db.NewLibraryCatalogStore(pool, driver)
	if err != nil {
		t.Fatalf("NewLibraryCatalogStore: %v", err)
	}
	sync := claimCatalogSync(t, store, serverID, now)
	item := catalogSuiteItem(serverID, "rollup-item", "Rollup", now)
	sync, err = store.CommitLibrarySyncPage(t.Context(), sync, []core.LibraryItem{item}, "", now)
	if err != nil {
		t.Fatalf("CommitLibrarySyncPage: %v", err)
	}
	if _, err = store.FinishLibrarySync(t.Context(), sync, now); err != nil {
		t.Fatalf("FinishLibrarySync: %v", err)
	}
	watch := playbackStoreWatch(t, serverID, now.Add(time.Hour))
	watch.ItemID, watch.ActiveTime = item.ItemID, 3*time.Minute
	if err = newPlaybackTestStore(t, pool, driver).SaveWatches(t.Context(), []core.PlaybackMutation{{Watch: watch}}); err != nil {
		t.Fatalf("SaveWatches: %v", err)
	}
	if _, err = pool.ExecContext(t.Context(), `UPDATE library_items SET plays=0,watch_seconds=0,unique_users=0,
first_played_at=NULL,last_played_at=NULL WHERE media_server_id=$1 AND item_id=$2`, serverID, item.ItemID); err != nil {
		t.Fatalf("corrupt rollup fixture: %v", err)
	}
	rebuild := claimCatalogSync(t, store, serverID, now.Add(2*time.Hour))
	if _, err = store.FinishLibrarySync(t.Context(), rebuild, now.Add(2*time.Hour)); err != nil {
		t.Fatalf("FinishLibrarySync(rebuild): %v", err)
	}
	assertLibraryItemRollup(t, pool, driver, serverID, item.ItemID, 1, 180, 1, now.Add(time.Hour), now.Add(time.Hour))
}

func testCatalogDescendantReads(t *testing.T, pool *sql.DB, driver config.Driver) {
	t.Helper()
	now := time.Date(2026, 9, 27, 22, 0, 0, 0, time.UTC)
	serverID := "86000000-0000-4000-8000-000000000001"
	createCatalogServer(t, pool, driver, serverID, "Catalog Descendants", now)
	store, err := db.NewLibraryCatalogStore(pool, driver)
	if err != nil {
		t.Fatalf("NewLibraryCatalogStore: %v", err)
	}
	sync := claimCatalogSync(t, store, serverID, now)
	items := catalogDescendantItems(serverID, now)
	sync, err = store.CommitLibrarySyncPage(t.Context(), sync, items, "", now)
	if err != nil {
		t.Fatalf("seed descendants: %v", err)
	}
	if _, err = store.FinishLibrarySync(t.Context(), sync, now); err != nil {
		t.Fatalf("finish descendants: %v", err)
	}
	seedCatalogDescendantWatches(t, pool, driver, serverID, now)
	for _, itemID := range []string{"series", "season"} {
		detail, detailErr := store.GetCatalogItem(t.Context(), serverID, itemID)
		history, historyErr := store.ListCatalogHistory(t.Context(), core.CatalogHistoryQuery{
			MediaServerID: serverID, ItemID: itemID, Limit: 10,
		})
		if detailErr != nil || historyErr != nil || detail.Item.Plays != 2 || len(history) != 2 {
			t.Errorf("%s detail/history = %d/%d, %v/%v; want 2/2",
				itemID, detail.Item.Plays, len(history), detailErr, historyErr)
		}
	}
}

func catalogDescendantItems(serverID string, now time.Time) []core.LibraryItem {
	series := catalogSuiteItem(serverID, "series", "Series", now)
	series.ItemType = "Series"
	season := catalogSuiteItem(serverID, "season", "Season", now)
	season.ItemType, season.ParentID, season.SeriesID = "Season", series.ItemID, series.ItemID
	first := catalogSuiteItem(serverID, "episode-1", "Episode 1", now)
	first.ItemType, first.SeriesID, first.SeasonID = "Episode", series.ItemID, season.ItemID
	second := catalogSuiteItem(serverID, "episode-2", "Episode 2", now)
	second.ItemType, second.SeriesID, second.SeasonID = "Episode", series.ItemID, season.ItemID
	return []core.LibraryItem{series, season, first, second}
}

func seedCatalogDescendantWatches(
	t *testing.T, pool *sql.DB, driver config.Driver, serverID string, now time.Time,
) {
	t.Helper()
	playback := newPlaybackTestStore(t, pool, driver)
	mutations := make([]core.PlaybackMutation, 0, 2)
	for index, itemID := range []string{"episode-1", "episode-2"} {
		watch := playbackStoreWatch(t, serverID, now.Add(time.Duration(index)*time.Minute))
		watch.ItemID, watch.SeriesID = itemID, "series"
		watch.ServerSessionID = fmt.Sprintf("descendant-session-%d", index)
		mutations = append(mutations, core.PlaybackMutation{Watch: watch})
	}
	if err := playback.SaveWatches(t.Context(), mutations); err != nil {
		t.Fatalf("seed descendant watches: %v", err)
	}
}

func assertLibraryItemRollup(
	t *testing.T, pool *sql.DB, driver config.Driver, serverID, itemID string,
	plays, seconds, users int64, first, last time.Time,
) {
	t.Helper()
	store, err := db.NewLibraryCatalogStore(pool, driver)
	if err != nil {
		t.Fatalf("NewLibraryCatalogStore: %v", err)
	}
	items, err := store.ListCatalogItems(t.Context(), core.CatalogItemQuery{
		MediaServerID: serverID, LibraryID: "library-1", Sort: core.CatalogSortName,
		Order: core.SortAscending, Limit: 100,
	})
	if err != nil || len(items) != 1 || items[0].Item.ItemID != itemID {
		t.Fatalf("catalog rollup item = %+v, %v", items, err)
	}
	got := items[0]
	if got.Plays != plays || got.WatchSeconds != seconds || got.UniqueUsers != users ||
		got.FirstPlayedAt == nil || got.LastPlayedAt == nil ||
		!got.FirstPlayedAt.Equal(core.NormalizeTime(first)) || !got.LastPlayedAt.Equal(core.NormalizeTime(last)) {
		t.Fatalf("catalog rollup = %d/%d/%d/%s/%s, %v; want %d/%d/%d/%s/%s",
			got.Plays, got.WatchSeconds, got.UniqueUsers, got.FirstPlayedAt, got.LastPlayedAt,
			err, plays, seconds, users, first, last)
	}
}

func reclaimCatalogLease(
	t *testing.T, store core.LibraryCatalogStore, serverID string, now time.Time,
) (core.LibrarySync, core.LibrarySync) {
	t.Helper()
	if _, err := store.RequestLibrarySync(t.Context(), serverID); err != nil {
		t.Fatalf("request fenced sync: %v", err)
	}
	workerA := claimCatalogLease(t, store, serverID, "worker-a", now)
	reclaimedAt := now.Add(2 * time.Minute)
	workerB := claimCatalogLease(t, store, serverID, "worker-b", reclaimedAt)
	return workerA, workerB
}

func claimCatalogLease(
	t *testing.T, store core.LibraryCatalogStore, serverID, token string, now time.Time,
) core.LibrarySync {
	t.Helper()
	for range 1000 {
		sync, err := store.ClaimLibrarySync(t.Context(), core.LibrarySyncLease{
			Token: token, ExpiresAt: now.Add(time.Minute),
		}, now, now.Add(-time.Hour))
		if err != nil {
			t.Fatalf("claim %s: %v", token, err)
		}
		if sync.MediaServerID == serverID {
			return sync
		}
		if err := store.FailLibrarySync(t.Context(), sync, "test fixture", now); err != nil {
			t.Fatalf("release unrelated catalog lease: %v", err)
		}
	}
	t.Fatal("catalog lease target was not claimed within bound")
	return core.LibrarySync{}
}

func assertStaleCatalogWorkerRejected(
	t *testing.T, store core.LibraryCatalogStore, sync core.LibrarySync, now time.Time,
) {
	t.Helper()
	item := catalogSuiteItem(sync.MediaServerID, "late-item", "Late", *sync.StartedAt)
	if _, err := store.CommitLibrarySyncPage(t.Context(), sync, []core.LibraryItem{item}, "", now); !errors.Is(err, core.ErrLibrarySyncLeaseLost) {
		t.Fatalf("worker A page commit = %v", err)
	}
	if _, err := store.CommitLibrarySyncArchives(t.Context(), sync, []string{"old-item"}, `{"phase":"revalidation"}`, now); !errors.Is(err, core.ErrLibrarySyncLeaseLost) {
		t.Fatalf("worker A archive = %v", err)
	}
	if _, err := store.FinishLibrarySync(t.Context(), sync, now); !errors.Is(err, core.ErrLibrarySyncLeaseLost) {
		t.Fatalf("worker A finish = %v", err)
	}
}

func testCatalogKeysetPages(t *testing.T, pool *sql.DB, driver config.Driver) {
	t.Helper()
	store, serverID, now := seedCatalogPageFixture(t, pool, driver)
	for _, sort := range []core.CatalogSort{
		core.CatalogSortName, core.CatalogSortDateAdded, core.CatalogSortPremiereDate,
		core.CatalogSortPlays, core.CatalogSortWatchTime, core.CatalogSortLastPlayed,
	} {
		for _, order := range []core.SortOrder{core.SortAscending, core.SortDescending} {
			assertCatalogSortPages(t, store, serverID, sort, order)
		}
	}
	assertCatalogQueryPages(t, store, core.CatalogItemQuery{
		MediaServerID: serverID, LibraryID: "library-1", Sort: core.CatalogSortLastPlayed,
		Order: core.SortDescending, Limit: 100,
		Window: core.CatalogWindow{Enabled: true, Start: now.Add(-time.Hour), End: now},
	})
	assertCatalogNullOrdering(t, store, serverID)
	assertCatalogHistoryPages(t, store, serverID)
	assertCatalogStalePages(t, store, serverID, now)
}

func seedCatalogPageFixture(
	t *testing.T, pool *sql.DB, driver config.Driver,
) (core.LibraryCatalogStore, string, time.Time) {
	t.Helper()
	now := time.Date(2026, 9, 27, 16, 0, 0, 0, time.UTC)
	serverID := catalogPagingServerID
	createCatalogServer(t, pool, driver, serverID, "Catalog Paging", now)
	store, err := db.NewLibraryCatalogStore(pool, driver)
	if err != nil {
		t.Fatalf("NewLibraryCatalogStore: %v", err)
	}
	sync := claimCatalogSync(t, store, serverID, now)
	older, newer := now.AddDate(-1, 0, 0), now.AddDate(0, -1, 0)
	items := []core.LibraryItem{
		catalogDatedItem("item-a", "Alpha", nil, nil, now),
		catalogDatedItem("item-b", "Beta", &older, &newer, now),
		catalogDatedItem("item-c", "Gamma", &newer, nil, now),
		catalogDatedItem("item-d", "Delta", &older, &older, now),
	}
	sync, err = store.CommitLibrarySyncPage(t.Context(), sync, items, "", now)
	if err != nil {
		t.Fatalf("CommitLibrarySyncPage: %v", err)
	}
	if _, err = store.FinishLibrarySync(t.Context(), sync, now.Add(time.Minute)); err != nil {
		t.Fatalf("FinishLibrarySync: %v", err)
	}
	seedCatalogWatches(t, pool, driver, serverID, now)
	return store, serverID, now
}

func createCatalogServer(
	t *testing.T, pool *sql.DB, driver config.Driver, serverID, name string, now time.Time,
) {
	t.Helper()
	_, writer, err := db.NewMediaServerStores(pool, driver)
	if err != nil {
		t.Fatalf("NewMediaServerStores: %v", err)
	}
	record := core.MediaServerRecord{MediaServer: core.MediaServer{
		ID: serverID, Kind: core.MediaServerKindJellyfin, Name: name, BaseURL: "https://catalog-page.invalid",
		CreatedAt: now, UpdatedAt: now,
	}, CredentialCiphertext: []byte("encrypted")}
	if err := writer.CreateMediaServer(t.Context(), record); err != nil {
		t.Fatalf("CreateMediaServer: %v", err)
	}
}

func catalogDatedItem(
	itemID, name string, created, premiere *time.Time, now time.Time,
) core.LibraryItem {
	item := catalogSuiteItem(catalogPagingServerID, itemID, name, now)
	item.DateCreated, item.PremiereDate = created, premiere
	item.Genres = []string{"Drama", "Shared"}
	return item
}

func seedCatalogWatches(t *testing.T, pool *sql.DB, driver config.Driver, serverID string, now time.Time) {
	t.Helper()
	store := newPlaybackTestStore(t, pool, driver)
	items := []string{"item-b", "item-b", "item-c", "item-d"}
	mutations := make([]core.PlaybackMutation, 0, len(items))
	for index, itemID := range items {
		watch := playbackStoreWatch(t, serverID, now.Add(time.Duration(index-8)*time.Hour))
		watch.ItemID, watch.ServerSessionID = itemID, fmt.Sprintf("catalog-session-%d", index)
		watch.MediaUserID = fmt.Sprintf("catalog-user-%d", index%3)
		watch.ActiveTime = time.Duration(index+1) * time.Minute
		mutations = append(mutations, core.PlaybackMutation{Watch: watch})
	}
	if err := store.SaveWatches(t.Context(), mutations); err != nil {
		t.Fatalf("SaveWatches: %v", err)
	}
}

func assertCatalogSortPages(
	t *testing.T, store core.LibraryCatalogStore, serverID string, sort core.CatalogSort, order core.SortOrder,
) {
	t.Helper()
	assertCatalogQueryPages(t, store, core.CatalogItemQuery{
		MediaServerID: serverID, LibraryID: "library-1", Sort: sort, Order: order, Limit: 100,
	})
}

func assertCatalogQueryPages(
	t *testing.T, store core.LibraryCatalogStore, base core.CatalogItemQuery,
) {
	t.Helper()
	want := catalogItemIDs(t, store, base)
	got := make([]string, 0, len(want))
	base.Limit = 2
	for range 3 {
		page, err := store.ListCatalogItems(t.Context(), base)
		if err != nil {
			t.Fatalf("ListCatalogItems(%s/%s): %v", base.Sort, base.Order, err)
		}
		for _, item := range page {
			got = append(got, item.Item.ItemID)
		}
		if len(page) < base.Limit {
			break
		}
		base.After = catalogTestPosition(base.Sort, page[len(page)-1])
	}
	if !slices.Equal(got, want) {
		t.Fatalf("paged %s/%s = %v, want %v", base.Sort, base.Order, got, want)
	}
}

func catalogItemIDs(
	t *testing.T, store core.LibraryCatalogStore, query core.CatalogItemQuery,
) []string {
	t.Helper()
	items, err := store.ListCatalogItems(t.Context(), query)
	if err != nil {
		t.Fatalf("ListCatalogItems(%s/%s): %v", query.Sort, query.Order, err)
	}
	result := make([]string, 0, len(items))
	for _, item := range items {
		result = append(result, item.Item.ItemID)
	}
	return result
}

func catalogTestPosition(sort core.CatalogSort, value core.CatalogItemStats) *core.CatalogItemPosition {
	if value.PagePosition.ItemID != "" {
		position := value.PagePosition
		return &position
	}
	position := &core.CatalogItemPosition{ItemID: value.Item.ItemID}
	switch sort {
	case core.CatalogSortName:
		position.TextValue = value.Item.Name
	case core.CatalogSortDateAdded:
		position.TimeValue = value.Item.DateCreated
	case core.CatalogSortPremiereDate:
		position.TimeValue = value.Item.PremiereDate
	case core.CatalogSortPlays:
		position.NumberValue = value.Plays
	case core.CatalogSortWatchTime:
		position.NumberValue = value.WatchSeconds
	case core.CatalogSortLastPlayed:
		position.TimeValue = value.LastPlayedAt
	}
	return position
}

func assertCatalogNullOrdering(t *testing.T, store core.LibraryCatalogStore, serverID string) {
	t.Helper()
	for _, sort := range []core.CatalogSort{
		core.CatalogSortDateAdded, core.CatalogSortPremiereDate, core.CatalogSortLastPlayed,
	} {
		items, err := store.ListCatalogItems(t.Context(), core.CatalogItemQuery{
			MediaServerID: serverID, LibraryID: "library-1", Sort: sort, Order: core.SortDescending, Limit: 100,
		})
		if err != nil {
			t.Fatalf("ListCatalogItems(%s): %v", sort, err)
		}
		seenNull := false
		for _, item := range items {
			value := catalogSortTime(sort, item)
			if value == nil {
				seenNull = true
			} else if seenNull {
				t.Fatalf("%s placed a known date after an unknown date", sort)
			}
		}
	}
}

func catalogSortTime(sort core.CatalogSort, item core.CatalogItemStats) *time.Time {
	switch sort {
	case core.CatalogSortDateAdded:
		return item.Item.DateCreated
	case core.CatalogSortPremiereDate:
		return item.Item.PremiereDate
	default:
		return item.LastPlayedAt
	}
}

func assertCatalogHistoryPages(t *testing.T, store core.LibraryCatalogStore, serverID string) {
	t.Helper()
	first, err := store.ListCatalogHistory(t.Context(), core.CatalogHistoryQuery{
		MediaServerID: serverID, ItemID: "item-b", Limit: 1,
	})
	if err != nil || len(first) != 1 {
		t.Fatalf("first history page = %+v, %v", first, err)
	}
	second, err := store.ListCatalogHistory(t.Context(), core.CatalogHistoryQuery{
		MediaServerID: serverID, ItemID: "item-b", Limit: 1,
		After: &core.CatalogHistoryPosition{StartedAt: first[0].StartedAt, ID: first[0].ID},
	})
	if err != nil || len(second) != 1 || second[0].ID == first[0].ID {
		t.Fatalf("second history page = %+v, %v", second, err)
	}
}

func assertCatalogStalePages(
	t *testing.T, store core.LibraryCatalogStore, serverID string, now time.Time,
) {
	t.Helper()
	query := core.CatalogStaleQuery{
		MediaServerID: serverID, LibraryID: "library-1", Before: now.Add(time.Hour), Limit: 2,
	}
	first, err := store.ListStaleCatalogItems(t.Context(), query)
	if err != nil || len(first) != 2 || first[0].LastPlayedAt != nil {
		t.Fatalf("first stale page = %+v, %v", first, err)
	}
	last := first[len(first)-1]
	query.After = &core.CatalogStalePosition{
		LastPlayedAt: last.LastPlayedAt, Name: last.Item.Name, ItemID: last.Item.ItemID,
	}
	second, err := store.ListStaleCatalogItems(t.Context(), query)
	if err != nil || len(second) != 2 || second[0].Item.ItemID == first[0].Item.ItemID {
		t.Fatalf("second stale page = %+v, %v", second, err)
	}
}

func testLargeCatalogGenres(t *testing.T, pool *sql.DB, driver config.Driver) {
	t.Helper()
	now := time.Date(2026, 9, 27, 18, 0, 0, 0, time.UTC)
	serverID := "83000000-0000-4000-8000-000000000001"
	createCatalogServer(t, pool, driver, serverID, "Large Catalog", now)
	query := `WITH RECURSIVE seq(n) AS (SELECT 1 UNION ALL SELECT n + 1 FROM seq WHERE n < 100001)
INSERT INTO library_items (media_server_id,item_id,library_id,item_type,name,genres,first_seen_at,last_seen_at,updated_at)
SELECT $1, printf('bulk-%06d',n), 'library-large', 'Movie', printf('Movie %06d',n), '["Large"]',$2,$2,$2 FROM seq`
	if driver == config.DriverPostgres {
		query = `INSERT INTO library_items (media_server_id,item_id,library_id,item_type,name,genres,first_seen_at,last_seen_at,updated_at)
SELECT $1, 'bulk-' || lpad(n::text,6,'0'), 'library-large', 'Movie', 'Movie ' || lpad(n::text,6,'0'), '["Large"]',$2,$2,$2
FROM generate_series(1,100001) AS n`
	}
	if _, err := pool.ExecContext(t.Context(), query, serverID, now); err != nil {
		t.Fatalf("seed large catalog: %v", err)
	}
	if _, err := pool.ExecContext(t.Context(), `INSERT INTO library_item_genres (media_server_id,item_id,genre)
SELECT media_server_id,item_id,'Large' FROM library_items WHERE media_server_id=$1`, serverID); err != nil {
		t.Fatalf("seed large catalog genres: %v", err)
	}
	store, err := db.NewLibraryCatalogStore(pool, driver)
	if err != nil {
		t.Fatalf("NewLibraryCatalogStore: %v", err)
	}
	genres, err := store.ListCatalogGenres(t.Context(), serverID, "library-large", core.CatalogWindow{})
	if err != nil || len(genres) != 1 || genres[0].Items != 100001 {
		t.Fatalf("ListCatalogGenres = %+v, %v", genres, err)
	}
}

func testCatalogPlans(t *testing.T, pool *sql.DB, driver config.Driver) {
	t.Helper()
	fields := []catalogPlanField{
		{name: "name", column: "name", kind: "text"},
		{name: "date", column: "date_created", kind: "nullable"},
		{name: "premiere", column: "premiere_date", kind: "nullable"},
		{name: "plays", column: "plays", kind: "number"},
		{name: "watch", column: "watch_seconds", kind: "number"},
		{name: "last_played", column: "last_played_at", kind: "nullable"},
	}
	for _, field := range fields {
		for _, filtered := range []bool{false, true} {
			for _, direction := range []string{"ASC", "DESC"} {
				assertCatalogPlan(t, pool, driver, field, direction, filtered)
			}
		}
	}
}

const catalogTargetPlanCTE = `WITH root AS (
    SELECT item_id,item_type FROM library_items WHERE media_server_id=$1 AND item_id=$2
), target_items AS (
    SELECT item_id,item_type AS root_type FROM root
    UNION ALL
    SELECT li.item_id,root.item_type FROM root
    JOIN library_items li ON li.media_server_id=$1 AND li.season_id=root.item_id AND li.item_id<>root.item_id
    WHERE root.item_type='Season'
    UNION ALL
    SELECT li.item_id,root.item_type FROM root
    JOIN library_items li ON li.media_server_id=$1 AND li.series_id=root.item_id AND li.item_id<>root.item_id
    WHERE root.item_type='Series'
), target_watches AS (
    SELECT w.id,w.media_server_id,w.item_id,w.started_at FROM target_items ti
    JOIN watches w ON w.media_server_id=$1 AND w.item_id=ti.item_id WHERE ti.root_type<>'Series'
    UNION ALL
    SELECT w.id,w.media_server_id,w.item_id,w.started_at FROM root
    JOIN watches w ON w.media_server_id=$1 AND w.item_id=root.item_id WHERE root.item_type='Series'
    UNION ALL
    SELECT w.id,w.media_server_id,w.item_id,w.started_at FROM root
    JOIN watches w ON w.media_server_id=$1 AND w.series_id=root.item_id
    JOIN target_items ti ON ti.item_id=w.item_id AND ti.root_type='Series'
    WHERE root.item_type='Series' AND w.item_id<>root.item_id
)
`

func testCatalogDetailHistoryPlans(t *testing.T, pool *sql.DB, driver config.Driver) {
	t.Helper()
	detail := explainCatalogQuery(t, pool, driver, catalogTargetPlanCTE+`SELECT COUNT(*) FROM target_watches`,
		catalogPagingServerID, "item-b")
	assertCatalogWatchSeeks(t, detail, "detail")
	history := explainCatalogQuery(t, pool, driver, catalogTargetPlanCTE+`SELECT * FROM target_watches
WHERE started_at<$3 OR (started_at=$3 AND id<$4)
ORDER BY started_at DESC,id DESC LIMIT 10`, catalogPagingServerID, "item-b",
		time.Date(2026, 9, 27, 23, 0, 0, 0, time.UTC), "z")
	assertCatalogWatchSeeks(t, history, "history")
}

// assertCatalogWatchSeeks accepts either watches index that leads with
// (media_server_id, item_id); the planner picks between them by table size,
// and the union of the three branches sorts before LIMIT either way.
func assertCatalogWatchSeeks(t *testing.T, plan, query string) {
	t.Helper()
	itemSeek := strings.Contains(plan, "watches_server_item_started_idx") ||
		strings.Contains(plan, "watches_catalog_aggregate_idx")
	if !itemSeek || !strings.Contains(plan, "watches_server_series_started_idx") {
		t.Errorf("%s plan = %q; want item and series index seeks", query, plan)
	}
	if strings.Contains(plan, "SCAN w") || strings.Contains(plan, "Seq Scan on watches") {
		t.Errorf("%s plan scans watches: %q", query, plan)
	}
}

type catalogPlanField struct{ name, column, kind string }

func assertCatalogPlan(
	t *testing.T, pool *sql.DB, driver config.Driver, field catalogPlanField, direction string, filtered bool,
) {
	t.Helper()
	filter, prefix := "", ""
	args := []any{catalogPagingServerID, "library-1", false}
	if filtered {
		filter, prefix = " AND item_type=$4", "type_"
		args = append(args, "UnexpectedType")
	}
	after, afterArgs := catalogPlanAfter(field, direction, len(args)+1)
	args = append(args, afterArgs...)
	innerOrder := catalogPlanOrder(field, direction)
	query := fmt.Sprintf(`SELECT li.* FROM library_items li
WHERE li.media_server_id=$1 AND li.library_id=$2 AND li.archived=$3%s AND %s
ORDER BY %s LIMIT 10`, filter, after, innerOrder)
	plan := explainCatalogQuery(t, pool, driver, query, args...)
	want := "library_items_" + prefix + field.name + "_" + strings.ToLower(direction) + "_idx"
	if !strings.Contains(plan, want) || strings.Contains(plan, "TEMP B-TREE") || strings.Contains(plan, "Sort") {
		t.Fatalf("%s/%s filtered=%t plan = %q; want %s without sort", field.name, direction, filtered, plan, want)
	}
}

func catalogPlanAfter(field catalogPlanField, direction string, first int) (string, []any) {
	comparison := ">"
	if direction == "DESC" {
		comparison = "<"
	}
	switch field.kind {
	case "text":
		return fmt.Sprintf("(li.%s %s $%d OR (li.%s=$%d AND li.item_id>$%d))",
			field.column, comparison, first, field.column, first, first+1), []any{"", ""}
	case "number":
		return fmt.Sprintf("(li.%s %s $%d OR (li.%s=$%d AND li.item_id>$%d))",
			field.column, comparison, first, field.column, first, first+1), []any{int64(-1), ""}
	default:
		condition := fmt.Sprintf(`(($%d=0 AND (li.%s IS NULL OR li.%s %s $%d OR
(li.%s=$%d AND li.item_id>$%d))) OR ($%d<>0 AND li.%s IS NULL AND li.item_id>$%d))`,
			first, field.column, field.column, comparison, first+1, field.column, first+1, first+2,
			first, field.column, first+2)
		return condition, []any{0, time.Unix(0, 0).UTC(), ""}
	}
}

func catalogPlanOrder(field catalogPlanField, direction string) string {
	if field.kind == "nullable" {
		return fmt.Sprintf("(li.%s IS NULL),li.%s %s,li.item_id", field.column, field.column, direction)
	}
	return fmt.Sprintf("li.%s %s,li.item_id", field.column, direction)
}

func explainCatalogQuery(t *testing.T, pool *sql.DB, driver config.Driver, query string, args ...any) string {
	t.Helper()
	if driver == config.DriverPostgres {
		return explainPostgresCatalogQuery(t, pool, query, args...)
	}
	rows, err := pool.QueryContext(t.Context(), "EXPLAIN QUERY PLAN "+query, args...)
	if err != nil {
		t.Fatalf("EXPLAIN QUERY PLAN: %v", err)
	}
	defer func() { _ = rows.Close() }()
	var plan strings.Builder
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatalf("scan query plan: %v", err)
		}
		plan.WriteString(detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("catalog query plan = %q, %v", plan.String(), err)
	}
	return plan.String()
}

func explainPostgresCatalogQuery(t *testing.T, pool *sql.DB, query string, args ...any) string {
	t.Helper()
	conn, err := pool.Conn(t.Context())
	if err != nil {
		t.Fatalf("catalog EXPLAIN connection: %v", err)
	}
	defer func() { _ = conn.Close() }()
	if _, err = conn.ExecContext(t.Context(), "SET enable_seqscan=off"); err != nil {
		t.Fatalf("disable sequential scan: %v", err)
	}
	rows, err := conn.QueryContext(t.Context(), "EXPLAIN "+query, args...)
	if err != nil {
		t.Fatalf("EXPLAIN: %v", err)
	}
	defer func() { _ = rows.Close() }()
	var plan strings.Builder
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			t.Fatalf("scan PostgreSQL plan: %v", err)
		}
		plan.WriteString(line)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("PostgreSQL catalog query plan = %q, %v", plan.String(), err)
	}
	return plan.String()
}

func claimCatalogSync(
	t *testing.T, store core.LibraryCatalogStore, serverID string, now time.Time,
) core.LibrarySync {
	t.Helper()
	if _, err := store.RequestLibrarySync(t.Context(), serverID); err != nil {
		t.Fatalf("RequestLibrarySync: %v", err)
	}
	return claimScheduledCatalogSync(t, store, serverID, now)
}

func claimScheduledCatalogSync(
	t *testing.T, store core.LibraryCatalogStore, serverID string, now time.Time,
) core.LibrarySync {
	t.Helper()
	for range 1000 {
		sync, err := store.ClaimLibrarySync(t.Context(), core.LibrarySyncLease{
			Token: "lease-" + serverID, ExpiresAt: now.Add(time.Minute),
		}, now, now.Add(-time.Hour))
		if err != nil {
			t.Fatalf("ClaimLibrarySync = %+v, %v", sync, err)
		}
		if sync.MediaServerID == serverID {
			return sync
		}
		if err := store.FailLibrarySync(t.Context(), sync, "test fixture", now); err != nil {
			t.Fatalf("FailLibrarySync fixture: %v", err)
		}
	}
	t.Fatal("catalog target was not claimed within bound")
	return core.LibrarySync{}
}

func catalogSuiteItem(serverID, itemID, name string, now time.Time) core.LibraryItem {
	return core.LibraryItem{
		MediaServerID: serverID, ItemID: itemID, LibraryID: "library-1", ItemType: "UnexpectedType",
		Name: name, Genres: []string{"Drama"}, FirstSeenAt: now, LastSeenAt: now, UpdatedAt: now,
	}
}

func assertCatalogPage(
	t *testing.T, store core.LibraryCatalogStore, serverID string, want []string, archived bool,
) {
	t.Helper()
	items, err := store.ListCatalogItems(t.Context(), core.CatalogItemQuery{
		MediaServerID: serverID, LibraryID: "library-1", Sort: core.CatalogSortName,
		Order: core.SortAscending, Archived: &archived, Limit: 100,
	})
	if err != nil || len(items) != len(want) {
		t.Fatalf("ListCatalogItems = %+v, %v; want %v", items, err, want)
	}
	for index, item := range items {
		if item.Item.ItemID != want[index] {
			t.Fatalf("item %d = %q, want %q", index, item.Item.ItemID, want[index])
		}
	}
}
