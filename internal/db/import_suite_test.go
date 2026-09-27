package db_test

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/BonzTM/bloom/internal/config"
	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/db"
)

type importFixture struct {
	store    core.ImportStore
	serverID string
	ownerID  string
	now      time.Time
}

func runImportEngineTests(t *testing.T, pool *sql.DB, driver config.Driver) {
	t.Helper()
	t.Run("active uniqueness and atomic resumable batch", func(t *testing.T) {
		testImportBatchAndResume(t, pool, driver, newImportFixture(t, pool, driver))
	})
	t.Run("cancel and missing job", func(t *testing.T) {
		testImportCancel(t, newImportFixture(t, pool, driver))
	})
	t.Run("collector first deduplicates import", func(t *testing.T) {
		testCollectorFirstDeduplication(t, pool, driver, newImportFixture(t, pool, driver))
	})
	t.Run("import first yields to collector", func(t *testing.T) {
		testImportFirstDeduplication(t, pool, driver, newImportFixture(t, pool, driver))
	})
	t.Run("concurrent import and collector converge", func(t *testing.T) {
		testConcurrentDeduplication(t, pool, driver, newImportFixture(t, pool, driver))
	})
	t.Run("expired worker is fenced after reclaim", func(t *testing.T) {
		testExpiredImportWorkerFence(t, newImportFixture(t, pool, driver))
	})
	t.Run("Bloom export snapshot is preserved", func(t *testing.T) {
		testBloomExportSnapshot(t, pool, driver, newImportFixture(t, pool, driver))
	})
}

func newImportFixture(t *testing.T, pool *sql.DB, driver config.Driver) importFixture {
	t.Helper()
	ctx := context.Background()
	now := core.NormalizeTime(time.Date(2026, 9, 25, 12, 0, 0, 123456789, time.UTC))
	accounts, _, err := db.NewAccountStores(pool, driver)
	if err != nil {
		t.Fatalf("NewAccountStores: %v", err)
	}
	owner := core.Account{ID: mustID(t), Username: "import-owner-" + mustID(t), CreatedAt: now}
	if createErr := accounts.CreateAccount(ctx, owner); createErr != nil {
		t.Fatalf("create import owner: %v", createErr)
	}
	_, mediaWriter, err := db.NewMediaServerStores(pool, driver)
	if err != nil {
		t.Fatalf("NewMediaServerStores: %v", err)
	}
	server := mediaServerRecord(t, "Import "+mustID(t), "https://import.example.test", now)
	if createErr := mediaWriter.CreateMediaServer(ctx, server); createErr != nil {
		t.Fatalf("create import media server: %v", createErr)
	}
	store, err := db.NewImportStore(pool, driver)
	if err != nil {
		t.Fatalf("NewImportStore: %v", err)
	}
	return importFixture{store: store, serverID: server.ID, ownerID: owner.ID, now: now}
}

func testImportBatchAndResume(t *testing.T, pool *sql.DB, driver config.Driver, fixture importFixture) {
	t.Helper()
	ctx := context.Background()
	job := fixture.job(t, core.ImportSourcePlaybackReporting, "0", fixture.now)
	if err := fixture.store.CreateImport(ctx, job); err != nil {
		t.Fatalf("CreateImport: %v", err)
	}
	conflict := fixture.job(t, job.Source, "0", fixture.now.Add(time.Second))
	if err := fixture.store.CreateImport(ctx, conflict); !errors.Is(err, core.ErrImportInProgress) {
		t.Fatalf("CreateImport(active duplicate) = %v, want import in progress", err)
	}
	claimed := claimImport(t, fixture.store, fixture.now, "lease-one")
	seedCollectedDuplicate(t, pool, driver, fixture.serverID, fixture.now)
	result := commitImportFixture(t, fixture, claimed)
	if result.Read != 3 || result.Imported != 1 || result.Duplicate != 2 {
		t.Fatalf("first counters = %+v, want read 3 imported 1 duplicate 2", result)
	}
	result = commitImportFixture(t, fixture, claimed)
	if result.Read != 6 || result.Imported != 1 || result.Duplicate != 5 {
		t.Fatalf("replayed counters = %+v, want read 6 imported 1 duplicate 5", result)
	}
	assertImportedWatchShape(t, pool, fixture.serverID)
	assertImportLeaseResume(t, fixture, claimed)
}

func (fixture importFixture) job(t *testing.T, source core.ImportSource, cursor string, now time.Time) core.ImportJob {
	t.Helper()
	return core.ImportJob{
		ID: mustID(t), MediaServerID: fixture.serverID, RequestedBy: fixture.ownerID,
		Source: source, State: core.ImportPending, Cursor: cursor,
		CreatedAt: core.NormalizeTime(now), UpdatedAt: core.NormalizeTime(now),
	}
}

func claimImport(t *testing.T, store core.ImportStore, now time.Time, token string) core.ImportJob {
	t.Helper()
	job, err := store.ClaimImport(t.Context(), core.ImportLease{
		Token: token, ExpiresAt: now.Add(30 * time.Second),
	}, now)
	if err != nil {
		t.Fatalf("ClaimImport: %v", err)
	}
	return job
}

func seedCollectedDuplicate(t *testing.T, pool *sql.DB, driver config.Driver, serverID string, now time.Time) {
	t.Helper()
	store := newPlaybackTestStore(t, pool, driver)
	watch := playbackStoreWatch(t, serverID, now)
	watch.MediaUserID, watch.ItemID = "media-user", "item-duplicate"
	if err := store.SaveWatches(t.Context(), []core.PlaybackMutation{{Watch: watch}}); err != nil {
		t.Fatalf("seed collected watch: %v", err)
	}
}

func commitImportFixture(t *testing.T, fixture importFixture, job core.ImportJob) core.ImportBatchResult {
	t.Helper()
	records := []core.ImportedWatch{
		importedRecord("source-duplicate", "item-duplicate", fixture.now.Add(time.Minute)),
		importedRecord("source-inserted", "item-imported", fixture.now.Add(10*time.Minute)),
		importedRecord("source-inserted", "item-imported", fixture.now.Add(10*time.Minute)),
	}
	result, err := fixture.store.CommitImportBatch(t.Context(), core.ImportBatch{
		JobID: job.ID, LeaseToken: job.LeaseToken, Cursor: "2", Source: job.Source,
		MediaServerID: fixture.serverID, Records: records, ResumeWindow: 5 * time.Minute,
		Now: fixture.now.Add(2 * time.Second), LeaseExpiresAt: fixture.now.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("CommitImportBatch: %v", err)
	}
	return result
}

func importedRecord(recordID, itemID string, started time.Time) core.ImportedWatch {
	return core.ImportedWatch{
		RecordID: recordID, MediaUserID: "media-user", Username: "source-user",
		DeviceName: "device", Client: "client", ItemID: itemID, ItemName: "Source title",
		ItemType: "Movie", PlayMethod: core.PlayMethodDirectPlay,
		StartedAt: started, Duration: 90 * time.Second,
	}
}

func assertImportedWatchShape(t *testing.T, pool *sql.DB, serverID string) {
	t.Helper()
	var source, importSource, recordID, state string
	var segments, positions int
	err := pool.QueryRowContext(t.Context(), `SELECT source, import_source, import_record_id, state,
        (SELECT COUNT(*) FROM watch_segments WHERE watch_id = watches.id),
        (SELECT COUNT(*) FROM watch_positions WHERE watch_id = watches.id)
        FROM watches WHERE media_server_id = $1 AND import_record_id = 'source-inserted'`, serverID).
		Scan(&source, &importSource, &recordID, &state, &segments, &positions)
	if err != nil || source != "import" || importSource != "playback_reporting" ||
		recordID != "source-inserted" || state != "stopped" || segments != 0 || positions != 0 {
		t.Fatalf("imported watch = %q %q %q %q segments=%d positions=%d, %v",
			source, importSource, recordID, state, segments, positions, err)
	}
}

func assertImportLeaseResume(t *testing.T, fixture importFixture, job core.ImportJob) {
	t.Helper()
	_, err := fixture.store.ClaimImport(t.Context(), core.ImportLease{
		Token: "too-early", ExpiresAt: fixture.now.Add(2 * time.Minute),
	}, fixture.now.Add(20*time.Second))
	if !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("ClaimImport(before expiry) = %v, want not found", err)
	}
	resumed := claimImport(t, fixture.store, fixture.now.Add(2*time.Minute), "lease-two")
	if resumed.ID != job.ID || resumed.Cursor != "2" || resumed.Read != 6 {
		t.Fatalf("resumed job = %+v", resumed)
	}
	if err := fixture.store.FinishImport(t.Context(), resumed.ID, resumed.LeaseToken,
		core.ImportCompleted, "", fixture.now.Add(3*time.Minute)); err != nil {
		t.Fatalf("FinishImport: %v", err)
	}
}

func testImportCancel(t *testing.T, fixture importFixture) {
	t.Helper()
	job := fixture.job(t, core.ImportSourcePlaybackReporting, "0", fixture.now.Add(4*time.Minute))
	if err := fixture.store.CreateImport(t.Context(), job); err != nil {
		t.Fatalf("CreateImport(cancel): %v", err)
	}
	cancelled, err := fixture.store.CancelImport(t.Context(), job.ID, fixture.now.Add(5*time.Minute))
	if err != nil || cancelled.State != core.ImportCancelled || cancelled.FinishedAt == nil {
		t.Fatalf("CancelImport = %+v, %v", cancelled, err)
	}
	if _, err := fixture.store.CancelImport(t.Context(), mustID(t), fixture.now); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("CancelImport(missing) = %v, want not found", err)
	}
}

func testCollectorFirstDeduplication(
	t *testing.T, pool *sql.DB, driver config.Driver, fixture importFixture,
) {
	t.Helper()
	started := fixture.now.Add(time.Minute)
	saveCollectedWatch(t, pool, driver, fixture.serverID, "collector-first", started)
	claimed := createClaimedImport(t, fixture, core.ImportSourcePlaybackReporting, "0")
	result := commitSingleImport(t, fixture, claimed, importedRecord("collector-first", "collector-first", started))
	if result.Imported != 0 || result.Duplicate != 1 {
		t.Fatalf("collector-first counters = %+v", result)
	}
	assertWatchSourceCounts(t, pool, fixture.serverID, "collector-first", 1, 0)
}

func testImportFirstDeduplication(
	t *testing.T, pool *sql.DB, driver config.Driver, fixture importFixture,
) {
	t.Helper()
	started := fixture.now.Add(time.Minute)
	claimed := createClaimedImport(t, fixture, core.ImportSourcePlaybackReporting, "0")
	result := commitSingleImport(t, fixture, claimed, importedRecord("import-first", "import-first", started))
	if result.Imported != 1 {
		t.Fatalf("import-first counters = %+v", result)
	}
	saveCollectedWatch(t, pool, driver, fixture.serverID, "import-first", started)
	assertWatchSourceCounts(t, pool, fixture.serverID, "import-first", 1, 0)
}

func testConcurrentDeduplication(
	t *testing.T, pool *sql.DB, driver config.Driver, fixture importFixture,
) {
	t.Helper()
	started := fixture.now.Add(time.Minute)
	claimed := createClaimedImport(t, fixture, core.ImportSourcePlaybackReporting, "0")
	start := make(chan struct{})
	errorsFound := make(chan error, 2)
	var ready sync.WaitGroup
	ready.Add(2)
	go func() {
		ready.Done()
		<-start
		_, err := fixture.store.CommitImportBatch(t.Context(), singleImportBatch(
			fixture, claimed, importedRecord("concurrent", "concurrent", started),
		))
		errorsFound <- err
	}()
	go func() {
		ready.Done()
		<-start
		errorsFound <- saveCollectedWatchError(t, pool, driver, fixture.serverID, "concurrent", started)
	}()
	ready.Wait()
	close(start)
	for range 2 {
		if err := <-errorsFound; err != nil {
			t.Fatalf("concurrent deduplication: %v", err)
		}
	}
	assertWatchSourceCounts(t, pool, fixture.serverID, "concurrent", 1, 0)
}

func testExpiredImportWorkerFence(t *testing.T, fixture importFixture) {
	t.Helper()
	job := fixture.job(t, core.ImportSourcePlaybackReporting, "0", fixture.now)
	if err := fixture.store.CreateImport(t.Context(), job); err != nil {
		t.Fatalf("CreateImport: %v", err)
	}
	first := claimImport(t, fixture.store, fixture.now, "expired-worker")
	second := claimImport(t, fixture.store, fixture.now.Add(31*time.Second), "replacement-worker")
	record := importedRecord("fenced", "fenced", fixture.now)
	if _, err := fixture.store.CommitImportBatch(t.Context(), singleImportBatch(fixture, first, record)); !errors.Is(err, core.ErrImportLeaseLost) {
		t.Fatalf("expired worker commit = %v, want lease lost", err)
	}
	result := commitSingleImport(t, fixture, second, record)
	if result.Imported != 1 || result.Read != 1 {
		t.Fatalf("replacement worker counters = %+v", result)
	}
}

func testBloomExportSnapshot(
	t *testing.T, pool *sql.DB, driver config.Driver, fixture importFixture,
) {
	t.Helper()
	season, episode, direct := int32(3), int32(8), true
	ended := fixture.now.Add(95 * time.Second)
	record := core.ImportedWatch{
		RecordID: "snapshot", MediaUserID: "snapshot-user", Username: "Snapshot",
		DeviceID: "device-id", DeviceName: "TV", Client: "Web", ItemID: "snapshot-item",
		ItemName: "Episode", ItemType: "Episode", SeriesName: "Series",
		LibraryID: "library", LibraryName: "Shows", SeasonNumber: &season, EpisodeNumber: &episode,
		PlayMethod: core.PlayMethodDirectStream, StartedAt: fixture.now, EndedAt: &ended,
		Duration: 90 * time.Second, LastPosition: 45 * time.Second,
		Stream: &core.StreamDetails{
			Container: "mkv", VideoCodec: "h264", AudioCodec: "aac",
			Bitrate: 1000, Width: 1920, Height: 1080, Framerate: 24, AudioChannels: 2,
			IsVideoDirect: &direct, IsAudioDirect: &direct, TranscodeReasons: []string{"reason"},
		},
	}
	claimed := createClaimedImport(t, fixture, core.ImportSourceBloomExport, `{"id":"placeholder","offset":0}`)
	commitSingleImport(t, fixture, claimed, record)
	watches, err := newPlaybackTestStore(t, pool, driver).ListWatches(t.Context(), core.PlaybackQuery{
		Mode: core.PlaybackQueryHistory, MediaServerID: fixture.serverID, PageSize: 10,
	})
	if err != nil || len(watches) != 1 {
		t.Fatalf("ListWatches = %+v, %v", watches, err)
	}
	assertBloomSnapshot(t, watches[0], record)
}

func createClaimedImport(
	t *testing.T, fixture importFixture, source core.ImportSource, cursor string,
) core.ImportJob {
	t.Helper()
	job := fixture.job(t, source, cursor, fixture.now)
	if err := fixture.store.CreateImport(t.Context(), job); err != nil {
		t.Fatalf("CreateImport: %v", err)
	}
	return claimImport(t, fixture.store, fixture.now, "lease-"+job.ID)
}

func singleImportBatch(
	fixture importFixture, job core.ImportJob, record core.ImportedWatch,
) core.ImportBatch {
	return core.ImportBatch{
		JobID: job.ID, LeaseToken: job.LeaseToken, Cursor: "next", Source: job.Source,
		MediaServerID: fixture.serverID, Records: []core.ImportedWatch{record},
		ResumeWindow: 5 * time.Minute, Now: fixture.now.Add(time.Second),
		LeaseExpiresAt: fixture.now.Add(time.Minute),
	}
}

func commitSingleImport(
	t *testing.T, fixture importFixture, job core.ImportJob, record core.ImportedWatch,
) core.ImportBatchResult {
	t.Helper()
	result, err := fixture.store.CommitImportBatch(t.Context(), singleImportBatch(fixture, job, record))
	if err != nil {
		t.Fatalf("CommitImportBatch: %v", err)
	}
	return result
}

func saveCollectedWatch(
	t *testing.T, pool *sql.DB, driver config.Driver, serverID, itemID string, started time.Time,
) {
	t.Helper()
	if err := saveCollectedWatchError(t, pool, driver, serverID, itemID, started); err != nil {
		t.Fatalf("SaveWatches: %v", err)
	}
}

func saveCollectedWatchError(
	t *testing.T, pool *sql.DB, driver config.Driver, serverID, itemID string, started time.Time,
) error {
	t.Helper()
	watch := playbackStoreWatch(t, serverID, started)
	watch.MediaUserID, watch.ItemID = "media-user", itemID
	return newPlaybackTestStore(t, pool, driver).SaveWatches(t.Context(), []core.PlaybackMutation{{Watch: watch}})
}

func assertWatchSourceCounts(
	t *testing.T, pool *sql.DB, serverID, itemID string, collected, imported int,
) {
	t.Helper()
	var gotCollected, gotImported int
	err := pool.QueryRowContext(t.Context(), `SELECT
        SUM(CASE WHEN source <> 'import' THEN 1 ELSE 0 END),
        SUM(CASE WHEN source = 'import' THEN 1 ELSE 0 END)
        FROM watches WHERE media_server_id = $1 AND item_id = $2`, serverID, itemID).
		Scan(&gotCollected, &gotImported)
	if err != nil || gotCollected != collected || gotImported != imported {
		t.Fatalf("watch counts = collected %d imported %d, %v; want %d/%d",
			gotCollected, gotImported, err, collected, imported)
	}
}

func assertBloomSnapshot(t *testing.T, watch core.PlaybackWatch, record core.ImportedWatch) {
	t.Helper()
	if watch.DeviceID != record.DeviceID || watch.SeriesName != record.SeriesName ||
		watch.LibraryID != record.LibraryID || watch.LibraryName != record.LibraryName ||
		watch.SeasonNumber == nil || *watch.SeasonNumber != *record.SeasonNumber ||
		watch.EpisodeNumber == nil || *watch.EpisodeNumber != *record.EpisodeNumber ||
		watch.LastPosition != record.LastPosition || watch.EndedAt == nil ||
		!watch.EndedAt.Equal(*record.EndedAt) || !reflect.DeepEqual(watch.Stream, record.Stream) {
		t.Fatalf("Bloom snapshot = %+v, want %+v", watch, record)
	}
}
