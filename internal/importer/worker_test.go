package importer

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
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
	active    []string
	listErr   error
	commitErr error
}

func (*workerStore) CreateImport(context.Context, core.ImportJob) error { return nil }
func (*workerStore) ListImports(context.Context, core.ImportListQuery) ([]core.ImportJob, error) {
	return nil, nil
}

func (s *workerStore) ListActiveBloomImportCursors(context.Context) ([]string, error) {
	return s.active, s.listErr
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

func (s *workerStore) FinishImport(_ context.Context, _, _ string, state core.ImportState, message string, _ time.Time) error {
	s.finished, s.errorText = state, message
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
	id, err := worker.deps.Staging.stage(strings.NewReader("upload"))
	if err != nil {
		t.Fatalf("stage upload: %v", err)
	}
	job.Cursor, err = encodeFileCursor(fileCursor{ID: id})
	if err != nil {
		t.Fatalf("encode cursor: %v", err)
	}
	if err := worker.complete(t.Context(), job); err != nil {
		t.Fatalf("complete: %v", err)
	}
	assertStagingPresence(t, worker.deps.Staging, id, false)
}

func TestWorkerCompletionIgnoresCleanupFailure(t *testing.T) {
	job := pendingWorkerJob(t)
	job.Source, job.Cursor = core.ImportSourceBloomExport, "invalid"
	store := workerStore{job: job}
	worker := newTestWorker(t, &store, reportingStub{})
	if err := worker.complete(t.Context(), job); err != nil || store.finished != core.ImportCompleted {
		t.Fatalf("complete = %v, state %q", err, store.finished)
	}
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

func TestBloomJobRecoversAfterStagingBecomesAvailable(t *testing.T) {
	job := pendingWorkerJob(t)
	job.Source = core.ImportSourceBloomExport
	store := workerStore{job: job}
	worker := newTestWorker(t, &store, reportingStub{})
	id, err := worker.deps.Staging.stage(strings.NewReader(validJSONLFixture() + "\n"))
	if err != nil {
		t.Fatalf("stage upload: %v", err)
	}
	store.job.Cursor, err = encodeFileCursor(fileCursor{ID: id})
	if err != nil {
		t.Fatalf("encode cursor: %v", err)
	}
	mounted := worker.deps.Staging.root + ".mounted"
	if err := os.Rename(worker.deps.Staging.root, mounted); err != nil {
		t.Fatalf("move staging root: %v", err)
	}
	if err := worker.deps.Staging.markUnavailable(errors.New("mount unavailable")); !errors.Is(err, errStagingUnavailable) {
		t.Fatalf("mark unavailable: %v", err)
	}
	if err := worker.runOnce(t.Context()); !errors.Is(err, errStagingUnavailable) || store.finished != "" {
		t.Fatalf("unavailable run = %v, finished %q", err, store.finished)
	}
	if err := os.Rename(mounted, worker.deps.Staging.root); err != nil {
		t.Fatalf("restore staging root: %v", err)
	}
	if err := worker.runOnce(t.Context()); err != nil || store.finished != core.ImportCompleted || store.result.Imported != 1 {
		t.Fatalf("recovered run = %v, state %q, counters %+v", err, store.finished, store.result)
	}
}

func TestWorkerSweepKeepsOnlyActiveUploads(t *testing.T) {
	store := workerStore{job: pendingWorkerJob(t)}
	worker := newTestWorker(t, &store, reportingStub{})
	activeID, err := worker.deps.Staging.stage(strings.NewReader("active"))
	if err != nil {
		t.Fatalf("stage active: %v", err)
	}
	orphanID, err := worker.deps.Staging.stage(strings.NewReader("orphan"))
	if err != nil {
		t.Fatalf("stage orphan: %v", err)
	}
	cursor, err := encodeFileCursor(fileCursor{ID: activeID})
	if err != nil {
		t.Fatalf("encode cursor: %v", err)
	}
	store.active = []string{cursor}
	partialID := "44444444-4444-4444-8444-444444444444"
	partialPath := filepath.Join(worker.deps.Staging.root, partialID+partialSuffix)
	if err := os.WriteFile(partialPath, []byte("partial"), 0o600); err != nil {
		t.Fatalf("write partial staging file: %v", err)
	}
	if err := worker.sweep(t.Context()); err != nil {
		t.Fatalf("sweep: %v", err)
	}
	assertStagingPresence(t, worker.deps.Staging, activeID, true)
	assertStagingPresence(t, worker.deps.Staging, orphanID, false)
	if _, err := os.Stat(partialPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("partial staging file survived sweep: %v", err)
	}
}

func assertStagingPresence(t *testing.T, staging *Staging, id string, want bool) {
	t.Helper()
	file, err := staging.open(id)
	if (err == nil) != want {
		t.Fatalf("staging %s exists = %t, %v; want %t", id, err == nil, err, want)
	}
	if file != nil {
		_ = file.Close()
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
	staging, err := NewStaging(t.TempDir())
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
