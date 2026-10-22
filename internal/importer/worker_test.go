package importer

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
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
	job           core.ImportJob
	result        core.ImportBatchResult
	finished      core.ImportState
	errorText     string
	commitErr     error
	uploads       *memoryUploadStore
	infoHook      func(context.Context, string) (core.ImportUploadInfo, error)
	readChunkHook func(int64)
	commitHook    func(core.ImportBatch)
	renewed       int
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
	if s.readChunkHook != nil {
		s.readChunkHook(index)
	}
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

func (s *workerStore) RenewImportLease(
	_ context.Context, id string, lease core.ImportLease, now time.Time,
) error {
	if id != s.job.ID || lease.Token != s.job.LeaseToken ||
		s.job.LeaseExpiresAt != nil && !s.job.LeaseExpiresAt.After(now) {
		return core.ErrImportLeaseLost
	}
	s.renewed++
	s.job.LeaseToken, s.job.LeaseExpiresAt = lease.Token, &lease.ExpiresAt
	return nil
}

func (s *workerStore) CommitImportBatch(_ context.Context, batch core.ImportBatch) (core.ImportBatchResult, error) {
	if s.commitErr != nil {
		return core.ImportBatchResult{}, s.commitErr
	}
	s.result.Read += int64(len(batch.Records)) + batch.Skipped
	s.result.Imported += int64(len(batch.Records))
	s.result.Skipped += batch.Skipped
	s.result.UnresolvedLibrary += batch.UnresolvedLibrary
	s.job.Cursor = batch.Cursor
	s.job.Read, s.job.Imported, s.job.Skipped = s.result.Read, s.result.Imported, s.result.Skipped
	s.job.Duplicate, s.job.UnresolvedLibrary = s.result.Duplicate, s.result.UnresolvedLibrary
	if s.commitHook != nil {
		s.commitHook(batch)
	}
	return s.result, nil
}

func (s *workerStore) FinishImport(ctx context.Context, _, _ string, state core.ImportState, message string, _ time.Time) error {
	s.finished, s.errorText = state, message
	if s.job.Source == core.ImportSourceBloomExport || s.job.Source == core.ImportSourceJellystat {
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

func TestWorkerHandlesLeaseLossDuringJellystatLookup(t *testing.T) {
	start := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	job := pendingWorkerJob(t)
	job.Source = core.ImportSourceJellystat
	store := workerStore{job: job}
	worker := newTestWorker(t, &store, reportingStub{})
	clock := testutil.NewFakeClock(start)
	worker.deps.Clock, worker.sources.clock = clock, clock
	stageJellystatWorkerFixture(t, worker, &store, start)
	store.readChunkHook = func(int64) { clock.Advance(worker.config.LeaseDuration) }

	if err := worker.runOnce(t.Context()); err != nil {
		t.Fatalf("runOnce after lookup lease loss: %v", err)
	}
	if store.finished != "" || store.job.State != core.ImportRunning {
		t.Fatalf("lease-lost lookup finished job as %q from state %q", store.finished, store.job.State)
	}
}

func TestWorkerStopsCleanlyWhenJellystatLookupIsCancelled(t *testing.T) {
	start := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	job := pendingWorkerJob(t)
	job.Source = core.ImportSourceJellystat
	store := workerStore{job: job}
	worker := newTestWorker(t, &store, reportingStub{})
	stageJellystatWorkerFixture(t, worker, &store, start)
	ctx, cancel := context.WithCancel(t.Context())
	store.readChunkHook = func(int64) { cancel() }

	if err := worker.runOnce(ctx); err != nil {
		t.Fatalf("runOnce after lookup cancellation: %v", err)
	}
	if store.finished != "" || store.job.State != core.ImportRunning {
		t.Fatalf("cancelled lookup finished job as %q from state %q", store.finished, store.job.State)
	}
}

func stageJellystatWorkerFixture(t *testing.T, worker *Worker, store *workerStore, now time.Time) {
	t.Helper()
	id, err := worker.deps.Staging.stage(
		t.Context(), strings.NewReader(jellystatMultiChunkLookupFixture()), staticClock{now},
	)
	if err != nil {
		t.Fatalf("stage Jellystat worker fixture: %v", err)
	}
	store.uploadStore().linkUpload(id)
	store.job.Cursor, err = encodeFileCursor(fileCursor{ID: id})
	if err != nil {
		t.Fatalf("encode Jellystat worker cursor: %v", err)
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
		t.Context(), bytes.NewReader(compressedWatchArchive(t, 1)), staticClock{time.Now()},
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

func TestCompressedBloomImportResumesFromCursorExactlyOnce(t *testing.T) {
	const recordCount = core.ImportBatchSize + 3
	staging := newJSONLTestStaging(t)
	id, err := staging.stage(
		t.Context(), bytes.NewReader(compressedWatchArchive(t, recordCount)), staticClock{time.Now()},
	)
	if err != nil {
		t.Fatalf("stage compressed archive: %v", err)
	}
	cursor, err := encodeFileCursor(fileCursor{ID: id})
	if err != nil {
		t.Fatalf("encode cursor: %v", err)
	}
	firstReader := &jsonlReader{staging: staging}
	first, cursor, _, err := firstReader.ReadImportBatch(t.Context(), core.ImportJob{Cursor: cursor})
	if closeErr := firstReader.Close(); err != nil || closeErr != nil {
		t.Fatalf("read first batch: %v; close: %v", err, closeErr)
	}
	checkpoint, err := decodeFileCursor(cursor)
	if err != nil {
		t.Fatalf("decode checkpoint: %v", err)
	}
	legacyCursor := fmt.Sprintf(`{"id":%q,"offset":%d}`, checkpoint.ID, checkpoint.Offset)
	secondReader := &jsonlReader{staging: staging}
	second, _, _, err := secondReader.ReadImportBatch(t.Context(), core.ImportJob{
		Cursor: legacyCursor, Read: int64(len(first)),
	})
	if closeErr := secondReader.Close(); err != nil || closeErr != nil {
		t.Fatalf("read resumed batch: %v; close: %v", err, closeErr)
	}
	seen := make(map[string]struct{}, recordCount)
	for _, record := range append(first, second...) {
		seen[record.RecordID] = struct{}{}
	}
	if len(first) != core.ImportBatchSize || len(second) != 3 || len(seen) != recordCount {
		t.Fatalf("resumed records = first %d second %d unique %d", len(first), len(second), len(seen))
	}
}

func TestCompressedBloomImportResumeCountsSourceRecordsBeforeExclusions(t *testing.T) {
	const recordCount = core.ImportBatchSize + 3
	job := pendingWorkerJob(t)
	job.Source = core.ImportSourceBloomExport
	store := workerStore{job: job}
	firstWorker := newTestWorker(t, &store, reportingStub{})
	id, err := firstWorker.deps.Staging.stage(
		t.Context(), bytes.NewReader(compressedWatchArchive(t, recordCount)), staticClock{time.Now()},
	)
	if err != nil {
		t.Fatalf("stage compressed archive: %v", err)
	}
	store.job.Cursor, err = encodeFileCursor(fileCursor{ID: id})
	if err != nil {
		t.Fatalf("encode cursor: %v", err)
	}
	exclusions := importerExclusions{value: core.MediaServerExclusions{
		MediaServerID: job.MediaServerID, MediaUserIDs: []string{"user"},
	}}
	firstWorker.deps.Exclusions = exclusions
	ctx, cancel := context.WithCancel(t.Context())
	store.commitHook = func(core.ImportBatch) { cancel() }
	if runErr := firstWorker.runOnce(ctx); runErr != nil {
		t.Fatalf("first worker pass: %v", runErr)
	}
	if store.result.Read != core.ImportBatchSize || store.result.Skipped != core.ImportBatchSize || store.finished != "" {
		t.Fatalf("checkpoint counters = %+v, state %q", store.result, store.finished)
	}
	checkpoint, err := decodeFileCursor(store.job.Cursor)
	if err != nil || !checkpoint.recordsKnown || checkpoint.Records != core.ImportBatchSize {
		t.Fatalf("checkpoint cursor = %+v, %v", checkpoint, err)
	}

	store.commitHook = nil
	resumedWorker := newTestWorker(t, &store, reportingStub{})
	resumedWorker.deps.Exclusions = exclusions
	if err := resumedWorker.runOnce(t.Context()); err != nil {
		t.Fatalf("resumed worker pass: %v", err)
	}
	if store.finished != core.ImportCompleted || store.result.Read != recordCount ||
		store.result.Imported != 0 || store.result.Skipped != recordCount {
		t.Fatalf("resumed job = state %q counters %+v", store.finished, store.result)
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
	worker.sources.openWatch = func(ctx context.Context, upload *UploadReader, offset int64) (io.ReadCloser, error) {
		return openWatchUploadWith(ctx, upload, offset, func(entry *zip.File) (io.ReadCloser, error) {
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
	if err := writeCompressedWatchArchive(&output, records); err != nil {
		t.Fatalf("write compressed archive: %v", err)
	}
	return output.Bytes()
}

func writeCompressedWatchArchive(output io.Writer, records int) error {
	archive := zip.NewWriter(output)
	manifest, err := archive.Create("manifest.json")
	if err != nil {
		return fmt.Errorf("create manifest.json: %w", err)
	}
	if _, writeErr := io.WriteString(manifest, validManifestFixture()); writeErr != nil {
		return fmt.Errorf("write manifest.json: %w", writeErr)
	}
	entry, err := archive.Create("watches.jsonl")
	if err != nil {
		return fmt.Errorf("create compressed watches.jsonl: %w", err)
	}
	for index := range records {
		line := strings.Replace(
			validJSONLFixture(), "11111111-1111-4111-8111-111111111111",
			fmt.Sprintf("11111111-1111-4111-8111-%012d", index), 1,
		)
		if _, writeErr := io.WriteString(entry, line+"\n"); writeErr != nil {
			return fmt.Errorf("write compressed watches.jsonl: %w", writeErr)
		}
	}
	summary, err := archive.Create("summary.json")
	if err != nil {
		return fmt.Errorf("create summary.json: %w", err)
	}
	if _, err := io.WriteString(summary, validSummaryFixture(records)); err != nil {
		return fmt.Errorf("write summary.json: %w", err)
	}
	if err := archive.Close(); err != nil {
		return fmt.Errorf("close compressed archive: %w", err)
	}
	return nil
}

func TestWorkerFailsCleanlyWhenWatchHeaderUnderstatesUncompressedSize(t *testing.T) {
	job := pendingWorkerJob(t)
	job.Source = core.ImportSourceBloomExport
	store := workerStore{job: job}
	worker := newTestWorker(t, &store, reportingStub{})
	payload := understatedWatchArchive(t)
	id, err := worker.deps.Staging.stage(t.Context(), bytes.NewReader(payload), staticClock{time.Now()})
	if err != nil {
		t.Fatalf("stage archive: %v", err)
	}
	store.uploadStore().linkUpload(id)
	store.job.Cursor, err = encodeFileCursor(fileCursor{ID: id})
	if err != nil {
		t.Fatalf("encode cursor: %v", err)
	}
	err = worker.runOnce(t.Context())
	if !errors.Is(err, zip.ErrFormat) || store.finished != core.ImportFailed || store.result.Imported != 0 {
		t.Fatalf("understated archive = %v, state %q, imported %d", err, store.finished, store.result.Imported)
	}
}

func understatedWatchArchive(t *testing.T) []byte {
	t.Helper()
	payload := compressedWatchArchive(t, 1)
	nameIndex := bytes.LastIndex(payload, []byte("watches.jsonl"))
	headerIndex := nameIndex - zipDirectoryHeaderBytes
	if headerIndex < 0 || binary.LittleEndian.Uint32(payload[headerIndex:headerIndex+4]) != zipDirectoryHeaderSignature {
		t.Fatal("fixture does not contain the watches.jsonl central-directory header")
	}
	binary.LittleEndian.PutUint32(payload[headerIndex+24:headerIndex+28], 1)
	return payload
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
	store.uploadStore().linkUpload(id)
	store.infoHook = func(ctx context.Context, _ string) (core.ImportUploadInfo, error) {
		<-ctx.Done()
		return core.ImportUploadInfo{}, errors.Join(core.ErrImportStore, ctx.Err())
	}
	started := time.Now()
	err = worker.runOnce(t.Context())
	if !errors.Is(err, core.ErrImportStore) || !errors.Is(err, context.DeadlineExceeded) ||
		time.Since(started) > time.Second {
		t.Fatalf("runOnce = %v after %s", err, time.Since(started))
	}
	if store.finished != "" || store.job.State != core.ImportRunning {
		t.Fatalf("timed-out read finished job as %q from state %q", store.finished, store.job.State)
	}
	store.infoHook = nil
	assertStagingPresence(t, worker.deps.Staging, id, true)
	store.uploads.mu.Lock()
	linked := store.uploads.linked[id]
	store.uploads.mu.Unlock()
	if !linked {
		t.Fatal("timed-out read unlinked its staged upload")
	}
}

func TestWorkerFailsBloomImportWhenSummaryCountDoesNotMatch(t *testing.T) {
	job := pendingWorkerJob(t)
	job.Source = core.ImportSourceBloomExport
	store := workerStore{job: job}
	worker := newTestWorker(t, &store, reportingStub{})
	payload := zipFixture(t, map[string]string{
		"manifest.json": validManifestFixture(),
		"watches.jsonl": validJSONLFixture() + "\n",
		"summary.json":  validSummaryFixture(2),
	})
	id, err := worker.deps.Staging.stage(t.Context(), bytes.NewReader(payload), staticClock{time.Now()})
	if err != nil {
		t.Fatalf("stage archive: %v", err)
	}
	store.uploadStore().linkUpload(id)
	store.job.Cursor, err = encodeFileCursor(fileCursor{ID: id})
	if err != nil {
		t.Fatalf("encode cursor: %v", err)
	}
	err = worker.runOnce(t.Context())
	if !errors.Is(err, core.ErrImportRecordCountMismatch) || store.finished != core.ImportFailed ||
		store.errorText != "Bloom export watch record count does not match summary" {
		t.Fatalf("mismatched summary = %v, state %q, last_error %q", err, store.finished, store.errorText)
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

func TestEveryImportSourceSkipsExcludedRecords(t *testing.T) {
	for _, source := range []core.ImportSource{
		core.ImportSourcePlaybackReporting, core.ImportSourceBloomExport,
		core.ImportSourceJellystat, core.ImportSourceJellyfinUserData,
	} {
		t.Run(string(source), func(t *testing.T) {
			job := pendingWorkerJob(t)
			job.Source = source
			store := &workerStore{job: job}
			worker := newTestWorker(t, store, reportingStub{})
			worker.deps.Exclusions = importerExclusions{value: core.MediaServerExclusions{
				MediaServerID: job.MediaServerID, MediaUserIDs: []string{"excluded-user"},
				LibraryIDs: []string{"excluded-library"},
			}}
			worker.deps.Libraries = &importerLibraries{items: map[string]string{"catalog-item": "excluded-library"}}
			records := []core.ImportedWatch{
				{MediaUserID: "excluded-user", LibraryID: "library"},
				{MediaUserID: "user", LibraryID: "excluded-library"},
				{MediaUserID: "user", ItemID: "catalog-item"},
				{MediaUserID: "user", LibraryID: "library"},
			}
			filtered, skipped, unresolved, err := worker.filterExcluded(t.Context(), job.MediaServerID, records)
			if err != nil || skipped != 3 || unresolved != 0 || len(filtered) != 1 || filtered[0].MediaUserID != "user" {
				t.Fatalf("filterExcluded = %+v, skipped %d unresolved %d, %v", filtered, skipped, unresolved, err)
			}
		})
	}
}

type importerExclusions struct{ value core.MediaServerExclusions }

func (e importerExclusions) GetExclusions(context.Context, string) (core.MediaServerExclusions, error) {
	return e.value, nil
}

type importerLibraries struct {
	items map[string]string
	calls int
}

func (r *importerLibraries) ResolveImportLibrary(_ context.Context, _, itemID string) (string, bool, error) {
	r.calls++
	libraryID, found := r.items[itemID]
	return libraryID, found, nil
}

type importerItems struct {
	items map[string]core.Library
	calls int
}

func (r *importerItems) ResolveLibrary(_ context.Context, _, itemID string) (core.Library, bool, error) {
	r.calls++
	library, found := r.items[itemID]
	return library, found, nil
}

func TestImportLibraryFallsBackToMediaServerAndCachesBatchResolution(t *testing.T) {
	job := pendingWorkerJob(t)
	store := &workerStore{job: job}
	worker := newTestWorker(t, store, reportingStub{})
	worker.deps.Exclusions = importerExclusions{value: core.MediaServerExclusions{
		MediaServerID: job.MediaServerID, LibraryIDs: []string{"excluded-library"},
	}}
	catalog := &importerLibraries{items: map[string]string{}}
	items := &importerItems{items: map[string]core.Library{
		"blank-library-item": {ID: "excluded-library", Name: "Excluded"},
	}}
	worker.deps.Libraries, worker.deps.Items = catalog, items
	records := []core.ImportedWatch{
		{MediaUserID: "user", ItemID: "blank-library-item"},
		{MediaUserID: "user", ItemID: "blank-library-item"},
	}
	filtered, skipped, unresolved, err := worker.filterExcluded(t.Context(), job.MediaServerID, records)
	if err != nil || len(filtered) != 0 || skipped != 2 || unresolved != 0 {
		t.Fatalf("filterExcluded = %+v, skipped %d unresolved %d, %v", filtered, skipped, unresolved, err)
	}
	if catalog.calls != 1 || items.calls != 1 {
		t.Fatalf("resolver calls = catalog %d media server %d, want 1 each", catalog.calls, items.calls)
	}
}

func TestImportLibraryMissFailsClosedAndCountsSkippedRow(t *testing.T) {
	job := pendingWorkerJob(t)
	record := core.ImportedWatch{
		RecordID: "1", MediaUserID: "user", ItemID: "unknown-item", ItemName: "Unknown",
		ItemType: "Movie", PlayMethod: core.PlayMethodDirectPlay,
		StartedAt: time.Date(2026, 9, 25, 11, 0, 0, 0, time.UTC), Duration: time.Minute,
	}
	store := &workerStore{job: job}
	worker := newTestWorker(t, store, reportingStub{records: []core.ImportedWatch{record}})
	worker.deps.Exclusions = importerExclusions{value: core.MediaServerExclusions{
		MediaServerID: job.MediaServerID, LibraryIDs: []string{"excluded-library"},
	}}
	catalog := &importerLibraries{items: map[string]string{}}
	items := &importerItems{items: map[string]core.Library{}}
	worker.deps.Libraries, worker.deps.Items = catalog, items

	if err := worker.runOnce(t.Context()); err != nil {
		t.Fatalf("runOnce: %v", err)
	}
	if store.finished != core.ImportCompleted || store.result.Imported != 0 || store.result.Skipped != 1 ||
		store.job.UnresolvedLibrary != 1 || catalog.calls != 1 || items.calls != 1 {
		t.Fatalf("job = %+v, counters %+v, resolver calls %d/%d", store.job, store.result, catalog.calls, items.calls)
	}
}

func TestImportLibraryMissIsKeptWhenNoLibrariesAreExcluded(t *testing.T) {
	job := pendingWorkerJob(t)
	worker := newTestWorker(t, &workerStore{job: job}, reportingStub{})
	worker.deps.Exclusions = importerExclusions{value: core.MediaServerExclusions{MediaServerID: job.MediaServerID}}
	catalog := &importerLibraries{items: map[string]string{}}
	items := &importerItems{items: map[string]core.Library{}}
	worker.deps.Libraries, worker.deps.Items = catalog, items
	records := []core.ImportedWatch{{MediaUserID: "user", ItemID: "unknown-item"}}

	filtered, skipped, unresolved, err := worker.filterExcluded(t.Context(), job.MediaServerID, records)
	if err != nil || len(filtered) != 1 || skipped != 0 || unresolved != 0 {
		t.Fatalf("filterExcluded = %+v, skipped %d unresolved %d, %v", filtered, skipped, unresolved, err)
	}
	if catalog.calls != 0 || items.calls != 0 {
		t.Fatalf("resolver calls = catalog %d media server %d, want none", catalog.calls, items.calls)
	}
}
