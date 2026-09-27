package importer

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/BonzTM/bloom/internal/core"
)

type serviceStore struct {
	*workerStore
	createErr error
}

func (s *serviceStore) CreateImport(_ context.Context, job core.ImportJob) error {
	s.job = job
	return s.createErr
}

func (s *serviceStore) CancelImport(_ context.Context, _ string, now time.Time) (core.ImportJob, error) {
	s.job.State, s.job.FinishedAt, s.job.UpdatedAt = core.ImportCancelled, &now, now
	return s.job, nil
}

type serviceServer struct{ err error }

func (s serviceServer) Get(context.Context, string) (core.MediaServerConnection, error) {
	return core.MediaServerConnection{}, s.err
}

func TestBloomUploadIsRemovedAfterCancellation(t *testing.T) {
	store := &serviceStore{workerStore: &workerStore{}}
	service, staging := newService(t, store)
	stagingID, err := service.StageBloomExport(t.Context(), strings.NewReader("{}\n"))
	if err != nil {
		t.Fatalf("StageBloomExport: %v", err)
	}
	job, err := service.CreateBloomExport(
		t.Context(), importServiceServerID, importServiceAccountID, stagingID,
	)
	if err != nil {
		t.Fatalf("CreateBloomExport: %v", err)
	}
	if file, err := staging.open(stagingID); err != nil {
		t.Fatalf("staged upload: %v", err)
	} else if closeErr := file.Close(); closeErr != nil {
		t.Fatalf("close staged upload: %v", closeErr)
	}
	if _, err := service.Cancel(t.Context(), job.ID); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if _, err := staging.open(stagingID); err == nil {
		t.Fatalf("staged upload still exists: %v", err)
	}
}

func TestBloomUploadIsRemovedWhenJobCreationConflicts(t *testing.T) {
	store := &serviceStore{workerStore: &workerStore{}, createErr: core.ErrImportInProgress}
	service, staging := newService(t, store)
	stagingID, err := service.StageBloomExport(t.Context(), strings.NewReader("{}\n"))
	if err != nil {
		t.Fatalf("StageBloomExport: %v", err)
	}
	_, err = service.CreateBloomExport(t.Context(), importServiceServerID, importServiceAccountID, stagingID)
	if !errors.Is(err, core.ErrImportInProgress) {
		t.Fatalf("CreateBloomExport = %v, want conflict", err)
	}
	if _, statErr := staging.open(stagingID); statErr == nil {
		t.Fatalf("failed upload still exists: %v", statErr)
	}
}

func TestCancellationSucceedsWhenStagingCleanupFails(t *testing.T) {
	store := &serviceStore{workerStore: &workerStore{job: core.ImportJob{
		ID: importServiceServerID, MediaServerID: importServiceServerID,
		RequestedBy: importServiceAccountID, Source: core.ImportSourceBloomExport,
		State: core.ImportPending, Cursor: "not-a-cursor",
	}}}
	service, _ := newService(t, store)
	job, err := service.Cancel(t.Context(), store.job.ID)
	if err != nil || job.State != core.ImportCancelled {
		t.Fatalf("Cancel = %+v, %v", job, err)
	}
}

func TestBloomUploadStagingIsSerialized(t *testing.T) {
	service, _ := newService(t, &serviceStore{workerStore: &workerStore{}})
	first := newBlockingReader()
	second := newBlockingReader()
	done := make(chan error, 2)
	go func() { _, err := service.StageBloomExport(t.Context(), first); done <- err }()
	<-first.started
	go func() { _, err := service.StageBloomExport(t.Context(), second); done <- err }()
	select {
	case <-second.started:
		t.Fatal("second upload started before the staging slot was released")
	case <-time.After(20 * time.Millisecond):
	}
	close(first.release)
	if err := <-done; err != nil {
		t.Fatalf("first StageBloomExport: %v", err)
	}
	<-second.started
	close(second.release)
	if err := <-done; err != nil {
		t.Fatalf("second StageBloomExport: %v", err)
	}
}

func TestBloomUploadUnavailableDoesNotReadUpload(t *testing.T) {
	dataDirectory := filepath.Join(t.TempDir(), "blocked")
	if err := os.WriteFile(dataDirectory, []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("block data directory: %v", err)
	}
	service, staging := newServiceWithDataDirectory(t, dataDirectory)
	wantRoot, err := filepath.Abs(filepath.Join(dataDirectory, stagingDirectory))
	if err != nil || staging.root != wantRoot {
		t.Fatalf("staging root = %q, %v; want %q", staging.root, err, wantRoot)
	}
	upload := &failOnRead{t: t}
	_, err = service.StageBloomExport(t.Context(), upload)
	assertUploadUnavailable(t, err)
	if staging.UnavailableCause() == nil {
		t.Fatal("staging is available, want unavailable cause")
	}
	if err := staging.sweep(nil); err != nil {
		t.Fatalf("unavailable sweep = %v, want no-op", err)
	}
}

func TestBloomUploadStagingRecoversLazily(t *testing.T) {
	dataDirectory := filepath.Join(t.TempDir(), "blocked")
	if err := os.WriteFile(dataDirectory, []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("block data directory: %v", err)
	}
	service, staging := newServiceWithDataDirectory(t, dataDirectory)
	if _, err := service.StageBloomExport(t.Context(), &failOnRead{t: t}); err == nil {
		t.Fatal("StageBloomExport succeeded while staging was unavailable")
	}
	if err := os.Remove(dataDirectory); err != nil {
		t.Fatalf("remove staging blocker: %v", err)
	}
	id, err := service.StageBloomExport(t.Context(), strings.NewReader("{}\n"))
	if err != nil {
		t.Fatalf("StageBloomExport after recovery: %v", err)
	}
	if staging.UnavailableCause() != nil {
		t.Fatalf("staging remained unavailable: %v", staging.UnavailableCause())
	}
	file, err := staging.open(id)
	if err != nil {
		t.Fatalf("open recovered upload: %v", err)
	}
	if closeErr := file.Close(); closeErr != nil {
		t.Fatalf("close recovered upload: %v", closeErr)
	}
}

func TestBloomUploadDetectsLaterStagingLoss(t *testing.T) {
	service, staging := newService(t, &serviceStore{workerStore: &workerStore{}})
	mounted := staging.root + ".mounted"
	if err := os.Rename(staging.root, mounted); err != nil {
		t.Fatalf("move staging root: %v", err)
	}
	_, err := service.StageBloomExport(t.Context(), &failOnRead{t: t})
	assertUploadUnavailable(t, err)
	if _, err := os.Lstat(staging.root); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("lost mount was replaced by a shadow staging root: %v", err)
	}
	if err := os.Rename(mounted, staging.root); err != nil {
		t.Fatalf("restore staging root: %v", err)
	}
	if _, err := service.StageBloomExport(t.Context(), strings.NewReader("{}\n")); err != nil {
		t.Fatalf("StageBloomExport after later recovery: %v", err)
	}
}

type failOnRead struct{ t *testing.T }

func (r *failOnRead) Read([]byte) (int, error) {
	r.t.Fatal("upload body was read while staging was unavailable")
	return 0, io.EOF
}

func assertUploadUnavailable(t *testing.T, err error) {
	t.Helper()
	if !errors.Is(err, core.ErrInvalidArgument) {
		t.Fatalf("StageBloomExport error = %v, want ErrInvalidArgument", err)
	}
	detail, ok := errors.AsType[*core.InvalidArgumentError](err)
	if !ok || detail.Field != "source" || detail.Code != "unavailable" ||
		detail.Message != uploadUnavailableMessage {
		t.Fatalf("StageBloomExport detail = %+v, %v", detail, err)
	}
}

type blockingReader struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func newBlockingReader() *blockingReader {
	return &blockingReader{started: make(chan struct{}), release: make(chan struct{})}
}

func (r *blockingReader) Read([]byte) (int, error) {
	r.once.Do(func() {
		close(r.started)
		<-r.release
	})
	return 0, io.EOF
}

func newService(t *testing.T, store core.ImportStore) (*Service, *Staging) {
	t.Helper()
	return newServiceWithStoreAndDataDirectory(t, store, t.TempDir())
}

func newServiceWithDataDirectory(t *testing.T, dataDirectory string) (*Service, *Staging) {
	t.Helper()
	return newServiceWithStoreAndDataDirectory(t, &serviceStore{workerStore: &workerStore{}}, dataDirectory)
}

func newServiceWithStoreAndDataDirectory(
	t *testing.T, store core.ImportStore, dataDirectory string,
) (*Service, *Staging) {
	t.Helper()
	staging, err := NewStaging(dataDirectory)
	if err != nil {
		t.Fatalf("NewStaging: %v", err)
	}
	service, err := NewService(
		store, serviceServer{}, fixedImportClock{}, staging, slog.New(slog.DiscardHandler),
	)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return service, staging
}

type fixedImportClock struct{}

func (fixedImportClock) Now() time.Time {
	return time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
}

const (
	importServiceServerID  = "11111111-1111-4111-8111-111111111111"
	importServiceAccountID = "22222222-2222-4222-8222-222222222222"
)
