package importer

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/telemetry"
	"github.com/BonzTM/bloom/internal/testutil"
)

type workerStore struct {
	job       core.ImportJob
	result    core.ImportBatchResult
	finished  core.ImportState
	errorText string
	commitErr error
	uploads   *memoryUploadStore
	infoHook  func(context.Context, string) (core.ImportUploadInfo, error)
}

func (*workerStore) CreateImport(context.Context, core.ImportJob) error { return nil }
func (*workerStore) ListImports(context.Context, core.ImportListQuery) ([]core.ImportJob, error) {
	return nil, nil
}

func (s *workerStore) CreateUploadedImport(_ context.Context, job core.ImportJob, uploadID string) error {
	s.job = job
	s.uploadStore().linkUpload(uploadID)
	return nil
}

func (s *workerStore) uploadStore() *memoryUploadStore {
	if s.uploads == nil {
		s.uploads = newMemoryUploadStore()
	}
	return s.uploads
}

func (s *workerStore) WriteImportUploadChunks(ctx context.Context, chunks []core.ImportUploadChunk) error {
	return s.uploadStore().WriteImportUploadChunks(ctx, chunks)
}

func (s *workerStore) ImportUploadInfo(ctx context.Context, id string) (core.ImportUploadInfo, error) {
	if s.infoHook != nil {
		return s.infoHook(ctx, id)
	}
	return s.uploadStore().ImportUploadInfo(ctx, id)
}

func (s *workerStore) ReadImportUploadChunk(ctx context.Context, id string, index int64) ([]byte, error) {
	return s.uploadStore().ReadImportUploadChunk(ctx, id, index)
}

func (s *workerStore) DeleteImportUpload(ctx context.Context, id string) error {
	return s.uploadStore().DeleteImportUpload(ctx, id)
}

func (s *workerStore) DeleteOrphanImportUploads(
	ctx context.Context, before time.Time, limit int,
) (int64, error) {
	return s.uploadStore().DeleteOrphanImportUploads(ctx, before, limit)
}

func (s *workerStore) GetImport(context.Context, string) (core.ImportJob, error) { return s.job, nil }

func (s *workerStore) CancelImport(context.Context, string, time.Time) (core.ImportJob, error) {
	return core.ImportJob{}, nil
}

func (s *workerStore) ClaimImport(_ context.Context, lease core.ImportLease, now time.Time) (core.ImportJob, error) {
	s.job.State, s.job.LeaseToken, s.job.LeaseExpiresAt = core.ImportRunning, lease.Token, &lease.ExpiresAt
	s.job.StartedAt, s.job.UpdatedAt = &now, now
	return s.job, nil
}

func (*workerStore) RenewImportLease(context.Context, string, core.ImportLease, time.Time) error {
	return nil
}

func (s *workerStore) CommitImportBatch(_ context.Context, batch core.ImportBatch) (core.ImportBatchResult, error) {
	if s.commitErr != nil {
		return core.ImportBatchResult{}, s.commitErr
	}
	s.result.Read += int64(len(batch.Records)) + batch.Skipped
	s.result.Imported += int64(len(batch.Records))
	s.result.Skipped += batch.Skipped
	s.job.Cursor = batch.Cursor
	return s.result, nil
}

func (s *workerStore) FinishImport(ctx context.Context, _, _ string, state core.ImportState, message string, _ time.Time) error {
	s.finished, s.errorText = state, message
	if s.job.Source == core.ImportSourceBloomExport {
		cursor, err := decodeFileCursor(s.job.Cursor)
		if err == nil {
			uploads := s.uploadStore()
			uploads.mu.Lock()
			uploads.deleteUpload(cursor.ID)
			uploads.mu.Unlock()
		}
	}
	return nil
}

type reportingStub struct {
	records []core.ImportedWatch
	err     error
}

func (s reportingStub) PlaybackReporting(context.Context, string, int64, int) (core.PlaybackReportingPage, error) {
	page := core.PlaybackReportingPage{Records: s.records}
	if len(s.records) > 0 {
		page.Cursor = 1
	}
	return page, s.err
}

func TestWorkerCommitsBatchAndCompletes(t *testing.T) {
	store := workerStore{job: pendingWorkerJob(t)}
	record := core.ImportedWatch{
		RecordID: "1", MediaUserID: "user", Username: "Alice", ItemID: "item",
		ItemName: "Film", ItemType: "Movie", PlayMethod: core.PlayMethodDirectPlay,
		StartedAt: time.Date(2026, 9, 25, 11, 0, 0, 0, time.UTC), Duration: time.Minute,
	}
	worker := newTestWorker(t, &store, reportingStub{records: []core.ImportedWatch{record}})
	if err := worker.runOnce(t.Context()); err != nil {
		t.Fatalf("runOnce: %v", err)
	}
	if store.finished != core.ImportCompleted || store.result.Imported != 1 || store.job.Cursor != "1" {
		t.Fatalf("worker result = state %q counters %+v cursor %q", store.finished, store.result, store.job.Cursor)
	}
}

func TestWorkerPersistsSafePluginMissingFailure(t *testing.T) {
	store := workerStore{job: pendingWorkerJob(t)}
	worker := newTestWorker(t, &store, reportingStub{err: core.ErrImportPluginMissing})
	err := worker.runOnce(t.Context())
	if !errors.Is(err, core.ErrImportPluginMissing) || store.finished != core.ImportFailed ||
		store.errorText != "Playback Reporting plugin is not installed" {
		t.Fatalf("runOnce = %v, state %q, last error %q", err, store.finished, store.errorText)
	}
}

func TestWorkerCompletionRemovesBloomUpload(t *testing.T) {
	job := pendingWorkerJob(t)
	job.Source = core.ImportSourceBloomExport
	store := workerStore{job: job}
	worker := newTestWorker(t, &store, reportingStub{})
	id, err := worker.deps.Staging.stage(t.Context(), strings.NewReader("upload"), staticClock{time.Now()})
	if err != nil {
		t.Fatalf("stage upload: %v", err)
	}
	job.Cursor, err = encodeFileCursor(fileCursor{ID: id})
	if err != nil {
		t.Fatalf("encode cursor: %v", err)
	}
	store.job = job
	if err := worker.complete(t.Context(), job); err != nil {
		t.Fatalf("complete: %v", err)
	}
	assertStagingPresence(t, worker.deps.Staging, id, false)
}

func TestWorkerLogsAndBacksOffOnStoreFailure(t *testing.T) {
	storeErr := errors.Join(core.ErrImportStore, errors.New("database unavailable"))
	store := workerStore{job: pendingWorkerJob(t), commitErr: storeErr}
	record := core.ImportedWatch{
		RecordID: "1", MediaUserID: "user", ItemID: "item", PlayMethod: core.PlayMethodUnknown,
		StartedAt: time.Date(2026, 9, 25, 11, 0, 0, 0, time.UTC),
	}
	worker := newTestWorker(t, &store, reportingStub{records: []core.ImportedWatch{record}})
	var logs strings.Builder
	worker.deps.Logger = slog.New(slog.NewTextHandler(&logs, nil))
	ctx, cancel := context.WithCancel(t.Context())
	worker.deps.Wait = func(context.Context, time.Duration) error {
		cancel()
		return ctx.Err()
	}
	if err := worker.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if store.finished != "" || !strings.Contains(logs.String(), "import worker pass failed") {
		t.Fatalf("finished = %q logs = %q", store.finished, logs.String())
	}
}

func TestBloomJobReadsDatabaseStagingAndCompletes(t *testing.T) {
	job := pendingWorkerJob(t)
	job.Source = core.ImportSourceBloomExport
	store := workerStore{job: job}
	worker := newTestWorker(t, &store, reportingStub{})
	id, err := worker.deps.Staging.stage(
		t.Context(), strings.NewReader(validJSONLFixture()+"\n"), staticClock{time.Now()},
	)
	if err != nil {
		t.Fatalf("stage upload: %v", err)
	}
	store.job.Cursor, err = encodeFileCursor(fileCursor{ID: id})
	if err != nil {
		t.Fatalf("encode cursor: %v", err)
	}
	if err := worker.runOnce(t.Context()); err != nil || store.finished != core.ImportCompleted || store.result.Imported != 1 {
		t.Fatalf("database upload run = %v, state %q, counters %+v", err, store.finished, store.result)
	}
}

func TestWorkerSweepDeletesExpiredOrphanUpload(t *testing.T) {
	store := workerStore{job: pendingWorkerJob(t)}
	worker := newTestWorker(t, &store, reportingStub{})
	orphanID, err := worker.deps.Staging.stage(
		t.Context(), strings.NewReader("orphan"),
		staticClock{time.Date(2026, 9, 25, 11, 40, 0, 0, time.UTC)},
	)
	if err != nil {
		t.Fatalf("stage orphan: %v", err)
	}
	if err := worker.sweep(t.Context()); err != nil {
		t.Fatalf("sweep: %v", err)
	}
	assertStagingPresence(t, worker.deps.Staging, orphanID, false)
}

func TestWorkerKeepsCompressedWatchEntryOpenAcrossBatches(t *testing.T) {
	job := pendingWorkerJob(t)
	job.Source = core.ImportSourceBloomExport
	store := workerStore{job: job}
	worker := newTestWorker(t, &store, reportingStub{})
	payload := compressedWatchArchive(t, core.ImportBatchSize+1)
	id, err := worker.deps.Staging.stage(t.Context(), bytes.NewReader(payload), staticClock{time.Now()})
	if err != nil {
		t.Fatalf("stage compressed archive: %v", err)
	}
	store.job.Cursor, err = encodeFileCursor(fileCursor{ID: id})
	if err != nil {
		t.Fatalf("encode cursor: %v", err)
	}
	opened := 0
	worker.sources.openWatch = func(upload *UploadReader, offset int64) (io.ReadCloser, error) {
		return openWatchUploadWith(upload, offset, func(entry *zip.File) (io.ReadCloser, error) {
			opened++
			return entry.Open()
		})
	}
	if err := worker.runOnce(t.Context()); err != nil {
		t.Fatalf("run compressed import: %v", err)
	}
	if opened != 1 || store.result.Imported != core.ImportBatchSize+1 {
		t.Fatalf("entry opens = %d, imported = %d", opened, store.result.Imported)
	}
}

func compressedWatchArchive(t *testing.T, records int) []byte {
	t.Helper()
	var output bytes.Buffer
	archive := zip.NewWriter(&output)
	entry, err := archive.Create("watches.jsonl")
	if err != nil {
		t.Fatalf("create compressed watches.jsonl: %v", err)
	}
	if _, err := io.WriteString(entry, strings.Repeat(validJSONLFixture()+"\n", records)); err != nil {
		t.Fatalf("write compressed watches.jsonl: %v", err)
	}
	if err := archive.Close(); err != nil {
		t.Fatalf("close compressed archive: %v", err)
	}
	return output.Bytes()
}

func TestWorkerTimesOutStalledUploadInfo(t *testing.T) {
	job := pendingWorkerJob(t)
	job.Source = core.ImportSourceBloomExport
	store := workerStore{job: job}
	worker := newTestWorker(t, &store, reportingStub{})
	worker.config.StoreTimeout = 20 * time.Millisecond
	worker.sources.storeTimeout = worker.config.StoreTimeout
	id, err := worker.deps.Staging.stage(
		t.Context(), strings.NewReader(validJSONLFixture()+"\n"), staticClock{time.Now()},
	)
	if err != nil {
		t.Fatalf("stage upload: %v", err)
	}
	store.job.Cursor, err = encodeFileCursor(fileCursor{ID: id})
	if err != nil {
		t.Fatalf("encode cursor: %v", err)
	}
	store.infoHook = func(ctx context.Context, _ string) (core.ImportUploadInfo, error) {
		<-ctx.Done()
		return core.ImportUploadInfo{}, ctx.Err()
	}
	started := time.Now()
	err = worker.runOnce(t.Context())
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(started) > time.Second {
		t.Fatalf("runOnce = %v after %s", err, time.Since(started))
	}
}

func assertStagingPresence(t *testing.T, staging *Staging, id string, want bool) {
	t.Helper()
	_, err := staging.open(t.Context(), id)
	if (err == nil) != want {
		t.Fatalf("staging %s exists = %t, %v; want %t", id, err == nil, err, want)
	}
}

func pendingWorkerJob(t *testing.T) core.ImportJob {
	t.Helper()
	now := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	return core.ImportJob{
		ID:            "11111111-1111-4111-8111-111111111111",
		MediaServerID: "22222222-2222-4222-8222-222222222222",
		RequestedBy:   "33333333-3333-4333-8333-333333333333",
		Source:        core.ImportSourcePlaybackReporting, State: core.ImportPending, Cursor: "0",
		CreatedAt: now, UpdatedAt: now,
	}
}

func newTestWorker(t *testing.T, store core.ImportStore, reporting PlaybackReportingService) *Worker {
	t.Helper()
	staging, err := NewStaging(store, 10*time.Minute)
	if err != nil {
		t.Fatalf("NewStaging: %v", err)
	}
	worker, err := NewWorker(WorkerConfig{
		Interval: time.Second, LeaseDuration: 30 * time.Second, ResumeWindow: 5 * time.Minute,
	}, WorkerDependencies{
		Store: store, Reporting: reporting,
		Clock:   testutil.NewFakeClock(time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)),
		Metrics: telemetry.NopMetrics{}, Logger: slog.New(slog.DiscardHandler),
		Staging: staging,
	})
	if err != nil {
		t.Fatalf("NewWorker: %v", err)
	}
	return worker
}
