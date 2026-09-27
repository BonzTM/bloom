package db_test

import (
	"archive/zip"
	"bytes"
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"os"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/BonzTM/bloom/internal/config"
	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/db"
	"github.com/BonzTM/bloom/internal/importer"
	"github.com/BonzTM/bloom/internal/telemetry"
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
	t.Run("Jellystat backup imports and reruns idempotently", func(t *testing.T) {
		testDatabaseJellystatImport(t, pool, driver, newImportFixture(t, pool, driver))
	})
	t.Run("Playback Reporting deduplicates Jellystat plugin rows", func(t *testing.T) {
		testPlaybackReportingJellystatCrossDedup(t, newImportFixture(t, pool, driver))
	})
	t.Run("database upload chunks and terminal cleanup", func(t *testing.T) {
		testDatabaseUploadLifecycle(t, newImportFixture(t, pool, driver))
	})
	t.Run("database ZIP upload round trip", func(t *testing.T) {
		testDatabaseZIPUploadRoundTrip(t, pool, driver, newImportFixture(t, pool, driver))
	})
	t.Run("cancel removes database upload", func(t *testing.T) {
		testCancelledUploadCleanup(t, newImportFixture(t, pool, driver))
	})
	t.Run("failure removes database upload", func(t *testing.T) {
		testFailedUploadCleanup(t, newImportFixture(t, pool, driver))
	})
	t.Run("orphan upload cleanup is bounded", func(t *testing.T) {
		testOrphanUploadCleanup(t, newImportFixture(t, pool, driver))
	})
	t.Run("large orphan cleanup commits chunk batches", func(t *testing.T) {
		testLargeOrphanUploadCleanup(t, newImportFixture(t, pool, driver))
	})
	t.Run("Jellyfin user data is per-user and yields to every watch source", func(t *testing.T) {
		testJellyfinUserDataDeduplication(t, pool, driver, newImportFixture(t, pool, driver))
	})
	t.Run("richer imports supersede Jellyfin user data in either order", func(t *testing.T) {
		testRicherImportPrecedence(t, pool, newImportFixture(t, pool, driver))
	})
	t.Run("import supersession maintains catalog rollups", func(t *testing.T) {
		testImportCatalogRollup(t, pool, driver, newImportFixture(t, pool, driver))
	})
}

func testDatabaseJellystatImport(
	t *testing.T, pool *sql.DB, driver config.Driver, fixture importFixture,
) {
	t.Helper()
	payload, err := os.ReadFile("../importer/testdata/jellystat-backup.jsonl")
	if err != nil {
		t.Fatalf("read Jellystat fixture: %v", err)
	}
	service, staging := newDatabaseImportDependencies(t, fixture, fixture.now)
	first := createDatabaseJellystatJob(t, service, fixture, payload)
	runDatabaseImportWorker(t, fixture, staging)
	assertJellystatJobCounters(t, fixture.store, first.ID, 4, 17, 0)
	second := createDatabaseJellystatJob(t, service, fixture, payload)
	runDatabaseImportWorker(t, fixture, staging)
	assertJellystatJobCounters(t, fixture.store, second.ID, 0, 17, 4)
	watches, err := newPlaybackTestStore(t, pool, driver).ListWatches(t.Context(), core.PlaybackQuery{
		Mode: core.PlaybackQueryHistory, MediaServerID: fixture.serverID, PageSize: 10,
	})
	if err != nil || len(watches) != 4 {
		t.Fatalf("Jellystat watches = %d, %v", len(watches), err)
	}
}

func createDatabaseJellystatJob(
	t *testing.T, service *importer.Service, fixture importFixture, payload []byte,
) core.ImportJob {
	t.Helper()
	uploadID, err := service.StageBloomExport(t.Context(), bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("stage Jellystat fixture: %v", err)
	}
	job, err := service.CreateJellystat(t.Context(), fixture.serverID, fixture.ownerID, uploadID)
	if err != nil {
		t.Fatalf("CreateJellystat: %v", err)
	}
	return job
}

func assertJellystatJobCounters(
	t *testing.T, store core.ImportStore, id string, imported, skipped, duplicate int64,
) {
	t.Helper()
	job, err := store.GetImport(t.Context(), id)
	if err != nil || job.State != core.ImportCompleted || job.Read != 21 ||
		job.Imported != imported || job.Skipped != skipped || job.Duplicate != duplicate {
		t.Fatalf("Jellystat job = %+v, %v", job, err)
	}
}

func testPlaybackReportingJellystatCrossDedup(t *testing.T, fixture importFixture) {
	t.Helper()
	jellystat := createClaimedImport(t, fixture, core.ImportSourceJellystat, `{"id":"upload","offset":0}`)
	record := importedRecord("plugin:77", "cross-source", fixture.now)
	result := commitSingleImport(t, fixture, jellystat, record)
	if result.Imported != 1 {
		t.Fatalf("Jellystat plugin insert = %+v", result)
	}
	if err := fixture.store.FinishImport(
		t.Context(), jellystat.ID, jellystat.LeaseToken, core.ImportCompleted, "", fixture.now,
	); err != nil {
		t.Fatalf("finish Jellystat import: %v", err)
	}
	reporting := createClaimedImport(t, fixture, core.ImportSourcePlaybackReporting, "0")
	record.RecordID = "77"
	result = commitSingleImport(t, fixture, reporting, record)
	if result.Imported != 0 || result.Duplicate != 1 {
		t.Fatalf("Playback Reporting cross-source counters = %+v", result)
	}
}

type importFixtureServer struct{}

func (importFixtureServer) Get(context.Context, string) (core.MediaServerConnection, error) {
	return core.MediaServerConnection{}, nil
}

type importFixtureClock struct{ now time.Time }

func (c importFixtureClock) Now() time.Time { return c.now }

type importFixtureReporting struct{}

func (importFixtureReporting) PlaybackReporting(
	context.Context, string, int64, int,
) (core.PlaybackReportingPage, error) {
	return core.PlaybackReportingPage{}, nil
}

func testDatabaseUploadLifecycle(t *testing.T, fixture importFixture) {
	t.Helper()
	payload := make([]byte, 3*core.ImportUploadChunkBytes+17)
	for index := range payload {
		payload[index] = byte(index % 251)
	}
	service := newDatabaseImportService(t, fixture, fixture.now)
	uploadID, err := service.StageBloomExport(t.Context(), bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("StageBloomExport: %v", err)
	}
	info, err := fixture.store.ImportUploadInfo(t.Context(), uploadID)
	if err != nil || info.Size != int64(len(payload)) || info.ChunkCount != 4 {
		t.Fatalf("ImportUploadInfo = %+v, %v", info, err)
	}
	chunk, err := fixture.store.ReadImportUploadChunk(t.Context(), uploadID, 2)
	if err != nil || !bytes.Equal(chunk, payload[2*core.ImportUploadChunkBytes:3*core.ImportUploadChunkBytes]) {
		t.Fatalf("ReadImportUploadChunk = %d bytes, %v", len(chunk), err)
	}
	job, err := service.CreateBloomExport(t.Context(), fixture.serverID, fixture.ownerID, uploadID)
	if err != nil {
		t.Fatalf("CreateBloomExport: %v", err)
	}
	claimed := claimImport(t, fixture.store, fixture.now, "upload-cleanup")
	if claimed.ID != job.ID {
		t.Fatalf("claimed job = %s, want %s", claimed.ID, job.ID)
	}
	if err := fixture.store.FinishImport(t.Context(), job.ID, claimed.LeaseToken,
		core.ImportCompleted, "", fixture.now.Add(time.Minute)); err != nil {
		t.Fatalf("FinishImport: %v", err)
	}
	if _, err := fixture.store.ImportUploadInfo(t.Context(), uploadID); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("terminal upload remains: %v", err)
	}
}

func testDatabaseZIPUploadRoundTrip(
	t *testing.T, pool *sql.DB, driver config.Driver, fixture importFixture,
) {
	t.Helper()
	payload := databaseImportZIP(t, fixture)
	service, staging := newDatabaseImportDependencies(t, fixture, fixture.now)
	uploadID, err := service.StageBloomExport(t.Context(), bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("StageBloomExport: %v", err)
	}
	job, err := service.CreateBloomExport(t.Context(), fixture.serverID, fixture.ownerID, uploadID)
	if err != nil {
		t.Fatalf("CreateBloomExport: %v", err)
	}
	jobs, err := fixture.store.ListImports(t.Context(), core.ImportListQuery{
		MediaServerID: fixture.serverID, PageSize: 501,
	})
	if err != nil || len(jobs) != 1 || jobs[0].ID != job.ID {
		t.Fatalf("export-sized ListImports = %+v, %v", jobs, err)
	}
	runDatabaseImportWorker(t, fixture, staging)
	completed, err := fixture.store.GetImport(t.Context(), job.ID)
	if err != nil || completed.State != core.ImportCompleted || completed.Imported != 1 {
		t.Fatalf("completed import = %+v, %v", completed, err)
	}
	watches, err := newPlaybackTestStore(t, pool, driver).ListWatches(t.Context(), core.PlaybackQuery{
		Mode: core.PlaybackQueryHistory, MediaServerID: fixture.serverID, PageSize: 10,
	})
	if err != nil || len(watches) != 1 || watches[0].ItemID != "zip-round-trip" {
		t.Fatalf("round-trip watches = %+v, %v", watches, err)
	}
}

func databaseImportZIP(t *testing.T, fixture importFixture) []byte {
	t.Helper()
	watch := playbackStoreWatch(t, fixture.serverID, fixture.now)
	watch.MediaServerName = "Round-trip source"
	watch.ItemID, watch.State = "zip-round-trip", core.WatchStopped
	ended := fixture.now.Add(time.Minute)
	watch.EndedAt, watch.ActiveTime = &ended, time.Minute
	var payload bytes.Buffer
	archive := zip.NewWriter(&payload)
	manifest, err := archive.Create("manifest.json")
	if err != nil {
		t.Fatalf("create manifest.json: %v", err)
	}
	if _, writeErr := manifest.Write([]byte(`{}`)); writeErr != nil {
		t.Fatalf("write manifest.json: %v", writeErr)
	}
	entry, err := archive.CreateHeader(&zip.FileHeader{Name: "watches.jsonl", Method: zip.Store})
	if err != nil {
		t.Fatalf("create watches.jsonl: %v", err)
	}
	if encodeErr := importer.EncodeWatchJSONL(entry, watch); encodeErr != nil {
		t.Fatalf("EncodeWatchJSONL: %v", encodeErr)
	}
	summary, err := archive.Create("summary.json")
	if err != nil {
		t.Fatalf("create summary.json: %v", err)
	}
	if _, writeErr := summary.Write([]byte(`{"watch_records":1}`)); writeErr != nil {
		t.Fatalf("write summary.json: %v", writeErr)
	}
	if closeErr := archive.Close(); closeErr != nil {
		t.Fatalf("close import ZIP: %v", closeErr)
	}
	return payload.Bytes()
}

func runDatabaseImportWorker(t *testing.T, fixture importFixture, staging *importer.Staging) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	var logs bytes.Buffer
	worker, err := importer.NewWorker(importer.WorkerConfig{
		Interval: time.Second, LeaseDuration: 30 * time.Second, ResumeWindow: 5 * time.Minute,
	}, importer.WorkerDependencies{
		Store: fixture.store, Reporting: importFixtureReporting{}, Clock: importFixtureClock{fixture.now},
		Metrics: telemetry.NopMetrics{}, Logger: slog.New(slog.NewTextHandler(&logs, nil)), Staging: staging,
		Wait: func(context.Context, time.Duration) error { cancel(); return context.Canceled },
	})
	if err != nil {
		t.Fatalf("NewWorker: %v", err)
	}
	if err := worker.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if logs.Len() > 0 {
		t.Log(logs.String())
	}
}

func testCancelledUploadCleanup(t *testing.T, fixture importFixture) {
	t.Helper()
	service := newDatabaseImportService(t, fixture, fixture.now)
	uploadID, err := service.StageBloomExport(t.Context(), bytes.NewReader([]byte("upload")))
	if err != nil {
		t.Fatalf("StageBloomExport: %v", err)
	}
	job, err := service.CreateBloomExport(t.Context(), fixture.serverID, fixture.ownerID, uploadID)
	if err != nil {
		t.Fatalf("CreateBloomExport: %v", err)
	}
	if _, err := service.Cancel(t.Context(), job.ID); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if _, err := fixture.store.ImportUploadInfo(t.Context(), uploadID); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("cancelled upload remains: %v", err)
	}
}

func testFailedUploadCleanup(t *testing.T, fixture importFixture) {
	t.Helper()
	service := newDatabaseImportService(t, fixture, fixture.now)
	uploadID, err := service.StageBloomExport(t.Context(), bytes.NewReader([]byte("invalid")))
	if err != nil {
		t.Fatalf("StageBloomExport: %v", err)
	}
	job, err := service.CreateBloomExport(t.Context(), fixture.serverID, fixture.ownerID, uploadID)
	if err != nil {
		t.Fatalf("CreateBloomExport: %v", err)
	}
	claimed := claimImport(t, fixture.store, fixture.now, "failed-upload-cleanup")
	if err := fixture.store.FinishImport(
		t.Context(), job.ID, claimed.LeaseToken, core.ImportFailed, "invalid", fixture.now,
	); err != nil {
		t.Fatalf("FinishImport: %v", err)
	}
	if _, err := fixture.store.ImportUploadInfo(t.Context(), uploadID); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("failed upload remains: %v", err)
	}
}

func testOrphanUploadCleanup(t *testing.T, fixture importFixture) {
	t.Helper()
	oldService := newDatabaseImportService(t, fixture, fixture.now.Add(-11*time.Minute))
	oldID, err := oldService.StageBloomExport(t.Context(), bytes.NewReader([]byte("old")))
	if err != nil {
		t.Fatalf("stage old orphan: %v", err)
	}
	linkedID, err := oldService.StageBloomExport(t.Context(), bytes.NewReader([]byte("linked")))
	if err != nil {
		t.Fatalf("stage linked upload: %v", err)
	}
	linkedJob, linkErr := oldService.CreateBloomExport(
		t.Context(), fixture.serverID, fixture.ownerID, linkedID,
	)
	if linkErr != nil {
		t.Fatalf("link old upload: %v", linkErr)
	}
	if discardErr := fixture.store.DeleteImportUpload(t.Context(), linkedID); discardErr != nil {
		t.Fatalf("discard linked upload: %v", discardErr)
	}
	recentService := newDatabaseImportService(t, fixture, fixture.now)
	recentID, err := recentService.StageBloomExport(t.Context(), bytes.NewReader([]byte("recent")))
	if err != nil {
		t.Fatalf("stage recent orphan: %v", err)
	}
	mixedID := mustID(t)
	writeUploadChunks(t, fixture.store, mixedID, []time.Time{
		fixture.now.Add(-11 * time.Minute), fixture.now,
	})
	deleted, err := fixture.store.DeleteOrphanImportUploads(t.Context(), fixture.now.Add(-10*time.Minute), 1)
	if err != nil || deleted != 1 {
		t.Fatalf("DeleteOrphanImportUploads = %d, %v", deleted, err)
	}
	if _, err := fixture.store.ImportUploadInfo(t.Context(), oldID); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("old orphan remains: %v", err)
	}
	if _, err := fixture.store.ImportUploadInfo(t.Context(), recentID); err != nil {
		t.Fatalf("recent orphan removed: %v", err)
	}
	if _, err := fixture.store.ImportUploadInfo(t.Context(), linkedID); err != nil {
		t.Fatalf("linked upload removed: %v", err)
	}
	if info, err := fixture.store.ImportUploadInfo(t.Context(), mixedID); err != nil || info.ChunkCount != 2 {
		t.Fatalf("recently extended upload = %+v, %v", info, err)
	}
	if _, err := fixture.store.CancelImport(t.Context(), linkedJob.ID, fixture.now); err != nil {
		t.Fatalf("cancel linked upload import: %v", err)
	}
}

func testLargeOrphanUploadCleanup(t *testing.T, fixture importFixture) {
	t.Helper()
	id := mustID(t)
	chunkCount := core.MaxOrphanImportUploadChunks + 1
	timestamps := make([]time.Time, chunkCount)
	for index := range timestamps {
		timestamps[index] = fixture.now.Add(-11 * time.Minute)
	}
	writeUploadChunks(t, fixture.store, id, timestamps)
	before := fixture.now.Add(-10 * time.Minute)
	deleted, err := fixture.store.DeleteOrphanImportUploads(
		t.Context(), before, core.MaxOrphanImportUploadChunks,
	)
	if err != nil || deleted != core.MaxOrphanImportUploadChunks {
		t.Fatalf("first orphan sweep = %d, %v", deleted, err)
	}
	if info, infoErr := fixture.store.ImportUploadInfo(t.Context(), id); infoErr != nil || info.ChunkCount != 1 {
		t.Fatalf("partially deleted orphan = %+v, %v", info, infoErr)
	}
	deleted, err = fixture.store.DeleteOrphanImportUploads(
		t.Context(), before, core.MaxOrphanImportUploadChunks,
	)
	if err != nil || deleted != 1 {
		t.Fatalf("second orphan sweep = %d, %v", deleted, err)
	}
	if _, err := fixture.store.ImportUploadInfo(t.Context(), id); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("large orphan remains: %v", err)
	}
}

func writeUploadChunks(
	t *testing.T, store core.ImportStore, id string, timestamps []time.Time,
) {
	t.Helper()
	for start := 0; start < len(timestamps); start += core.MaxImportUploadWriteChunks {
		end := min(start+core.MaxImportUploadWriteChunks, len(timestamps))
		chunks := make([]core.ImportUploadChunk, 0, end-start)
		for index := start; index < end; index++ {
			chunks = append(chunks, core.ImportUploadChunk{
				ID: id, Index: int64(index), Bytes: []byte{byte(index)}, CreatedAt: timestamps[index],
			})
		}
		if err := store.WriteImportUploadChunks(t.Context(), chunks); err != nil {
			t.Fatalf("write upload chunks %d-%d: %v", start, end, err)
		}
	}
}

func newDatabaseImportService(t *testing.T, fixture importFixture, now time.Time) *importer.Service {
	t.Helper()
	service, _ := newDatabaseImportDependencies(t, fixture, now)
	return service
}

func newDatabaseImportDependencies(
	t *testing.T, fixture importFixture, now time.Time,
) (*importer.Service, *importer.Staging) {
	t.Helper()
	staging, err := importer.NewStaging(fixture.store, 10*time.Minute)
	if err != nil {
		t.Fatalf("NewStaging: %v", err)
	}
	service, err := importer.NewService(
		fixture.store, importFixtureServer{}, importFixtureClock{now: now}, staging,
		slog.New(slog.DiscardHandler),
	)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return service, staging
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
	runtime := 42 * time.Minute
	ended := fixture.now.Add(95 * time.Second)
	exported := core.PlaybackWatch{
		ID: mustID(t), MediaServerID: fixture.serverID, MediaServerName: "Import source",
		MediaUserID: "snapshot-user", Username: "Snapshot",
		DeviceID: "device-id", DeviceName: "TV", Client: "Web", ItemID: "snapshot-item",
		ItemName: "Episode", ItemType: "Episode", SeriesID: "series-id", SeriesName: "Series",
		LibraryID: "library", LibraryName: "Shows", SeasonNumber: &season, EpisodeNumber: &episode,
		PlayMethod: core.PlayMethodDirectStream, State: core.WatchStopped,
		Source: core.WatchSourcePoll, StartedAt: fixture.now, EndedAt: &ended,
		Runtime: &runtime, ActiveTime: 90 * time.Second, LastPosition: 45 * time.Second,
		Stream: &core.StreamDetails{
			Container: "mkv", VideoCodec: "h264", AudioCodec: "aac",
			Bitrate: 1000, Width: 1920, Height: 1080, Framerate: 24, AudioChannels: 2,
			IsVideoDirect: &direct, IsAudioDirect: &direct, TranscodeReasons: []string{"reason"},
		},
	}
	var payload bytes.Buffer
	if err := importer.EncodeWatchJSONL(&payload, exported); err != nil {
		t.Fatalf("EncodeWatchJSONL: %v", err)
	}
	record, err := importer.DecodeWatchJSONL(payload.Bytes())
	if err != nil {
		t.Fatalf("DecodeWatchJSONL: %v", err)
	}
	claimed := createClaimedImport(t, fixture, core.ImportSourceBloomExport, `{"id":"placeholder","offset":0}`)
	commitSingleImport(t, fixture, claimed, record)
	watches, err := newPlaybackTestStore(t, pool, driver).ListWatches(t.Context(), core.PlaybackQuery{
		Mode: core.PlaybackQueryHistory, MediaServerID: fixture.serverID, PageSize: 10,
	})
	if err != nil || len(watches) != 1 {
		t.Fatalf("ListWatches = %+v, %v", watches, err)
	}
	assertBloomSnapshot(t, watches[0], exported)
}

func testJellyfinUserDataDeduplication(
	t *testing.T, pool *sql.DB, driver config.Driver, fixture importFixture,
) {
	t.Helper()
	prior := createClaimedImport(t, fixture, core.ImportSourcePlaybackReporting, "0")
	existing := importedRecord("prior-record", "existing-item", fixture.now)
	existing.MediaUserID = "existing-user"
	commitSingleImport(t, fixture, prior, existing)
	if err := fixture.store.FinishImport(t.Context(), prior.ID, prior.LeaseToken,
		core.ImportCompleted, "", fixture.now.Add(time.Second)); err != nil {
		t.Fatalf("FinishImport(prior): %v", err)
	}

	job := createClaimedImport(t, fixture, core.ImportSourceJellyfinUserData, "{}")
	records := []core.ImportedWatch{
		userDataRecord("existing-user", "existing-item", fixture.now.Add(24*time.Hour)),
		userDataRecord("user-a", "shared-item", fixture.now.Add(24*time.Hour)),
		userDataRecord("user-b", "shared-item", fixture.now.Add(24*time.Hour)),
	}
	result, err := fixture.store.CommitImportBatch(t.Context(), userDataBatch(fixture, job, records))
	if err != nil || result.Imported != 2 || result.Duplicate != 1 {
		t.Fatalf("first user-data batch = %+v, %v", result, err)
	}
	result, err = fixture.store.CommitImportBatch(t.Context(), userDataBatch(fixture, job, records))
	if err != nil || result.Imported != 2 || result.Duplicate != 4 {
		t.Fatalf("replayed user-data batch = %+v, %v", result, err)
	}

	store := newPlaybackTestStore(t, pool, driver)
	collected := playbackStoreWatch(t, fixture.serverID, fixture.now.Add(48*time.Hour))
	collected.MediaUserID, collected.ItemID = "user-a", "shared-item"
	if err := store.SaveWatches(t.Context(), []core.PlaybackMutation{{Watch: collected}}); err != nil {
		t.Fatalf("SaveWatches(collected winner): %v", err)
	}
	assertUserDataWatchCounts(t, pool, fixture.serverID)
}

func userDataRecord(userID, itemID string, started time.Time) core.ImportedWatch {
	record := importedRecord(itemID, itemID, started)
	record.MediaUserID = userID
	return record
}

func userDataBatch(
	fixture importFixture, job core.ImportJob, records []core.ImportedWatch,
) core.ImportBatch {
	return core.ImportBatch{
		JobID: job.ID, LeaseToken: job.LeaseToken, Cursor: "{}", Source: job.Source,
		MediaServerID: fixture.serverID, Records: records, ResumeWindow: 5 * time.Minute,
		Now: fixture.now.Add(time.Minute), LeaseExpiresAt: fixture.now.Add(2 * time.Minute),
	}
}

func assertUserDataWatchCounts(t *testing.T, pool *sql.DB, serverID string) {
	t.Helper()
	var collected, imported int
	err := pool.QueryRowContext(t.Context(), `SELECT
        SUM(CASE WHEN source <> 'import' THEN 1 ELSE 0 END),
        SUM(CASE WHEN import_source = 'jellyfin_userdata' THEN 1 ELSE 0 END)
        FROM watches WHERE media_server_id = $1 AND item_id = 'shared-item'`, serverID).
		Scan(&collected, &imported)
	if err != nil || collected != 1 || imported != 1 {
		t.Fatalf("user-data watch counts = collected %d imported %d, %v; want 1/1", collected, imported, err)
	}
}

func testRicherImportPrecedence(t *testing.T, pool *sql.DB, fixture importFixture) {
	t.Helper()
	userFirst := createClaimedImport(t, fixture, core.ImportSourceJellyfinUserData, "{}")
	record := userDataRecord("unlinked-user", "user-first-item", fixture.now)
	if result := commitSingleImport(t, fixture, userFirst, record); result.Imported != 1 || result.Duplicate != 0 {
		t.Fatalf("user-data first result = %+v", result)
	}
	finishImportFixture(t, fixture, userFirst)
	richSecond := createClaimedImport(t, fixture, core.ImportSourcePlaybackReporting, "0")
	rich := importedRecord("rich-record", record.ItemID, fixture.now)
	rich.MediaUserID = record.MediaUserID
	if result := commitSingleImport(t, fixture, richSecond, rich); result.Imported != 1 || result.Duplicate != 0 {
		t.Fatalf("rich second result = %+v", result)
	}
	assertSingleImportSource(t, pool, fixture.serverID, record.MediaUserID, record.ItemID, "playback_reporting")

	finishImportFixture(t, fixture, richSecond)
	richFirst := createClaimedImport(t, fixture, core.ImportSourceBloomExport, `{"id":"precedence","offset":0}`)
	other := importedRecord("export-record", "rich-first-item", fixture.now)
	other.MediaUserID = "another-unlinked-user"
	if result := commitSingleImport(t, fixture, richFirst, other); result.Imported != 1 {
		t.Fatalf("rich first result = %+v", result)
	}
	finishImportFixture(t, fixture, richFirst)
	userSecond := createClaimedImport(t, fixture, core.ImportSourceJellyfinUserData, "{}")
	if result := commitSingleImport(t, fixture, userSecond,
		userDataRecord(other.MediaUserID, other.ItemID, fixture.now)); result.Imported != 0 || result.Duplicate != 1 {
		t.Fatalf("user-data second result = %+v", result)
	}
	assertSingleImportSource(t, pool, fixture.serverID, other.MediaUserID, other.ItemID, "bloom_export")
}

func testImportCatalogRollup(
	t *testing.T, pool *sql.DB, driver config.Driver, fixture importFixture,
) {
	t.Helper()
	const itemID = "rollup-item"
	seedImportCatalogItem(t, pool, driver, fixture, itemID)
	userJob := createClaimedImport(t, fixture, core.ImportSourceJellyfinUserData, "{}")
	record := userDataRecord("rollup-user", itemID, fixture.now)
	commitSingleImport(t, fixture, userJob, record)
	assertLibraryItemRollup(t, pool, driver, fixture.serverID, itemID, 1, 90, 1, fixture.now, fixture.now)
	finishImportFixture(t, fixture, userJob)

	richJob := createClaimedImport(t, fixture, core.ImportSourcePlaybackReporting, "0")
	rich := importedRecord("rollup-rich", itemID, fixture.now)
	rich.MediaUserID = record.MediaUserID
	rich.Duration = 2 * time.Minute
	commitSingleImport(t, fixture, richJob, rich)
	assertLibraryItemRollup(t, pool, driver, fixture.serverID, itemID, 1, 120, 1, fixture.now, fixture.now)
}

func seedImportCatalogItem(
	t *testing.T, pool *sql.DB, driver config.Driver, fixture importFixture, itemID string,
) {
	t.Helper()
	store, err := db.NewLibraryCatalogStore(pool, driver)
	if err != nil {
		t.Fatalf("NewLibraryCatalogStore: %v", err)
	}
	sync := claimCatalogSync(t, store, fixture.serverID, fixture.now)
	item := catalogSuiteItem(fixture.serverID, itemID, "Rollup item", fixture.now)
	sync, err = store.CommitLibrarySyncPage(t.Context(), sync, []core.LibraryItem{item}, "", fixture.now)
	if err != nil {
		t.Fatalf("CommitLibrarySyncPage: %v", err)
	}
	if _, err = store.FinishLibrarySync(t.Context(), sync, fixture.now); err != nil {
		t.Fatalf("FinishLibrarySync: %v", err)
	}
}

func finishImportFixture(t *testing.T, fixture importFixture, job core.ImportJob) {
	t.Helper()
	if err := fixture.store.FinishImport(t.Context(), job.ID, job.LeaseToken,
		core.ImportCompleted, "", fixture.now.Add(time.Second)); err != nil {
		t.Fatalf("FinishImport: %v", err)
	}
}

func assertSingleImportSource(t *testing.T, pool *sql.DB, serverID, userID, itemID, source string) {
	t.Helper()
	var count int
	var got string
	err := pool.QueryRowContext(t.Context(), `SELECT COUNT(*), MIN(import_source) FROM watches
        WHERE media_server_id = $1 AND media_user_id = $2 AND item_id = $3`, serverID, userID, itemID).Scan(&count, &got)
	if err != nil || count != 1 || got != source {
		t.Fatalf("watch precedence = %d/%q, %v; want 1/%q", count, got, err, source)
	}
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

func assertBloomSnapshot(t *testing.T, watch, exported core.PlaybackWatch) {
	t.Helper()
	if watch.MediaServerID != exported.MediaServerID || watch.MediaUserID != exported.MediaUserID ||
		watch.Username != exported.Username || watch.DeviceID != exported.DeviceID ||
		watch.DeviceName != exported.DeviceName || watch.Client != exported.Client ||
		watch.ItemID != exported.ItemID || watch.ItemName != exported.ItemName || watch.ItemType != exported.ItemType ||
		watch.SeriesID != exported.SeriesID || watch.SeriesName != exported.SeriesName || watch.PlayMethod != exported.PlayMethod ||
		watch.LibraryID != exported.LibraryID || watch.LibraryName != exported.LibraryName ||
		watch.SeasonNumber == nil || *watch.SeasonNumber != *exported.SeasonNumber ||
		watch.EpisodeNumber == nil || *watch.EpisodeNumber != *exported.EpisodeNumber ||
		watch.ActiveTime != exported.ActiveTime || watch.LastPosition != exported.LastPosition ||
		watch.Runtime == nil || exported.Runtime == nil || *watch.Runtime != *exported.Runtime ||
		!watch.StartedAt.Equal(exported.StartedAt) || watch.EndedAt == nil ||
		!watch.EndedAt.Equal(*exported.EndedAt) || !reflect.DeepEqual(watch.Stream, exported.Stream) ||
		watch.Source != core.WatchSourceImport || watch.ImportSource != core.ImportSourceBloomExport ||
		watch.ImportRecordID != exported.ID || watch.State != core.WatchStopped {
		t.Fatalf("Bloom snapshot = %+v, want exported %+v", watch, exported)
	}
}
