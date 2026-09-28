package db_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/BonzTM/bloom/internal/config"
	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/db"
)

type additiveColumnFixture struct {
	playback core.PlaybackPersistence
	stats    core.StatsReader
	catalog  core.LibraryCatalogStore
	imports  core.ImportStore
	serverID string
	watchID  string
	itemID   string
	userID   string
	importID string
	now      time.Time
}

func runAdditiveColumnCompatibilityTests(t *testing.T, pool *sql.DB, driver config.Driver) {
	t.Helper()
	t.Run("additive columns preserve older read shapes", func(t *testing.T) {
		fixture := newAdditiveColumnFixture(t, pool, driver)
		addCompatibilityColumns(t, pool)

		assertCompatibilityPlayback(t, fixture)
		assertCompatibilityStats(t, fixture)
		assertCompatibilityCatalog(t, fixture)
		assertCompatibilityImports(t, fixture)
	})
}

func newAdditiveColumnFixture(t *testing.T, pool *sql.DB, driver config.Driver) additiveColumnFixture {
	t.Helper()
	now := core.NormalizeTime(time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC))
	server := createPlaybackTestServer(t, pool, driver, "Additive columns")
	playback := newPlaybackTestStore(t, pool, driver)
	watch := seedCompatibilityWatch(t, playback, server.ID, now)
	catalog := seedCompatibilityCatalog(t, pool, driver, server.ID, watch.ItemID, now)
	stats, err := db.NewStatsReader(pool, driver)
	if err != nil {
		t.Fatalf("NewStatsReader: %v", err)
	}
	imports, importID := seedCompatibilityImport(t, pool, driver)
	return additiveColumnFixture{
		playback: playback, stats: stats, catalog: catalog, imports: imports,
		serverID: server.ID, watchID: watch.ID, itemID: watch.ItemID,
		userID: watch.MediaUserID, importID: importID, now: now,
	}
}

func seedCompatibilityWatch(
	t *testing.T, store core.PlaybackPersistence, serverID string, now time.Time,
) core.PlaybackWatch {
	t.Helper()
	watch := playbackStoreWatch(t, serverID, now)
	watch.ItemID = "compatibility-item"
	watch.State = core.WatchStopped
	watch.EndedAt = new(watch.LastSeenAt)
	if err := store.SaveWatches(t.Context(), []core.PlaybackMutation{{Watch: watch}}); err != nil {
		t.Fatalf("seed compatibility watch: %v", err)
	}
	return watch
}

func seedCompatibilityCatalog(
	t *testing.T, pool *sql.DB, driver config.Driver, serverID, itemID string, now time.Time,
) core.LibraryCatalogStore {
	t.Helper()
	store, err := db.NewLibraryCatalogStore(pool, driver)
	if err != nil {
		t.Fatalf("NewLibraryCatalogStore: %v", err)
	}
	sync := claimCatalogSync(t, store, serverID, now)
	sync, err = store.CommitLibrarySyncPage(t.Context(), sync,
		[]core.LibraryItem{catalogSuiteItem(serverID, itemID, "Compatibility item", now)}, "", now)
	if err != nil {
		t.Fatalf("seed compatibility catalog: %v", err)
	}
	if _, err = store.FinishLibrarySync(t.Context(), sync, now); err != nil {
		t.Fatalf("finish compatibility catalog: %v", err)
	}
	return store
}

func seedCompatibilityImport(
	t *testing.T, pool *sql.DB, driver config.Driver,
) (core.ImportStore, string) {
	t.Helper()
	fixture := newImportFixture(t, pool, driver)
	job := fixture.job(t, core.ImportSourcePlaybackReporting, "0", fixture.now)
	if err := fixture.store.CreateImport(t.Context(), job); err != nil {
		t.Fatalf("seed compatibility import: %v", err)
	}
	return fixture.store, job.ID
}

func addCompatibilityColumns(t *testing.T, pool *sql.DB) {
	t.Helper()
	execTestSQL(t, pool, "ALTER TABLE watches ADD COLUMN compatibility_probe TEXT")
	t.Cleanup(func() {
		dropCompatibilityColumn(t, pool, "ALTER TABLE watches DROP COLUMN compatibility_probe")
	})
	execTestSQL(t, pool, "ALTER TABLE library_items ADD COLUMN compatibility_probe TEXT")
	t.Cleanup(func() {
		dropCompatibilityColumn(t, pool, "ALTER TABLE library_items DROP COLUMN compatibility_probe")
	})
}

func dropCompatibilityColumn(t *testing.T, pool *sql.DB, query string) {
	t.Helper()
	if _, err := pool.ExecContext(context.Background(), query); err != nil {
		t.Errorf("drop compatibility column: %v", err)
	}
}

func assertCompatibilityPlayback(t *testing.T, fixture additiveColumnFixture) {
	t.Helper()
	rows, err := fixture.playback.ListWatches(t.Context(), core.PlaybackQuery{
		Mode: core.PlaybackQueryHistory, MediaServerID: fixture.serverID, PageSize: 10,
	})
	if err != nil || len(rows) != 1 || rows[0].ID != fixture.watchID {
		t.Fatalf("playback compatibility read = %+v, %v", rows, err)
	}
}

func assertCompatibilityStats(t *testing.T, fixture additiveColumnFixture) {
	t.Helper()
	window, err := core.NewStatsWindow(1, fixture.serverID, "UTC", fixture.now.Add(time.Hour))
	if err != nil {
		t.Fatalf("NewStatsWindow: %v", err)
	}
	result, err := fixture.stats.ReadStats(t.Context(), core.StatsQuery{
		Window: window, Report: core.StatsReportUser,
		UserServerID: fixture.serverID, MediaUserID: fixture.userID,
	})
	if err != nil || len(result.Watches) != 1 || result.Watches[0].ID != fixture.watchID {
		t.Fatalf("statistics compatibility read = %+v, %v", result.Watches, err)
	}
}

func assertCompatibilityCatalog(t *testing.T, fixture additiveColumnFixture) {
	t.Helper()
	detail, err := fixture.catalog.GetCatalogItem(t.Context(), fixture.serverID, fixture.itemID)
	if err != nil || detail.Item.Item.ItemID != fixture.itemID {
		t.Fatalf("catalog item compatibility read = %+v, %v", detail, err)
	}
	rows, err := fixture.catalog.ListCatalogHistory(t.Context(), core.CatalogHistoryQuery{
		MediaServerID: fixture.serverID, ItemID: fixture.itemID, Limit: 10,
	})
	if err != nil || len(rows) != 1 || rows[0].ID != fixture.watchID {
		t.Fatalf("catalog history compatibility read = %+v, %v", rows, err)
	}
}

func assertCompatibilityImports(t *testing.T, fixture additiveColumnFixture) {
	t.Helper()
	job, err := fixture.imports.GetImport(t.Context(), fixture.importID)
	if err != nil || job.ID != fixture.importID {
		t.Fatalf("import compatibility read = %+v, %v", job, err)
	}
}
