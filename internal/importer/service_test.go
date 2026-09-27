package importer

import (
	"context"
	"errors"
	"io"
	"log/slog"
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

func (s *serviceStore) CreateUploadedImport(
	_ context.Context, job core.ImportJob, _ string,
) error {
	if s.createErr != nil {
		return s.createErr
	}
	s.job = job
	return nil
}

func (s *serviceStore) CancelImport(ctx context.Context, _ string, now time.Time) (core.ImportJob, error) {
	s.job.State, s.job.FinishedAt, s.job.UpdatedAt = core.ImportCancelled, &now, now
	if s.job.Source == core.ImportSourceBloomExport {
		cursor, err := decodeFileCursor(s.job.Cursor)
		if err != nil {
			return core.ImportJob{}, err
		}
		if err := s.DeleteImportUpload(ctx, cursor.ID); err != nil {
			return core.ImportJob{}, err
		}
	}
	return s.job, nil
}

type serviceServer struct{ err error }

func (s serviceServer) Get(context.Context, string) (core.MediaServerConnection, error) {
	return core.MediaServerConnection{}, s.err
}

func TestBloomUploadIsRemovedAfterCancellation(t *testing.T) {
	store := newServiceStore()
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
	if _, err := service.Cancel(t.Context(), job.ID); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if _, err := staging.open(t.Context(), stagingID); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("cancelled upload still exists: %v", err)
	}
}

func TestBloomUploadIsRemovedWhenJobCreationConflicts(t *testing.T) {
	store := newServiceStore()
	store.createErr = core.ErrImportInProgress
	service, staging := newService(t, store)
	stagingID, err := service.StageBloomExport(t.Context(), strings.NewReader("{}\n"))
	if err != nil {
		t.Fatalf("StageBloomExport: %v", err)
	}
	_, err = service.CreateBloomExport(t.Context(), importServiceServerID, importServiceAccountID, stagingID)
	if !errors.Is(err, core.ErrImportInProgress) {
		t.Fatalf("CreateBloomExport = %v, want conflict", err)
	}
	if _, err := staging.open(t.Context(), stagingID); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("failed upload still exists: %v", err)
	}
}

func TestBloomUploadStagingIsSerialized(t *testing.T) {
	service, _ := newService(t, newServiceStore())
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

func newServiceStore() *serviceStore {
	return &serviceStore{workerStore: &workerStore{uploads: newMemoryUploadStore()}}
}

func newService(t *testing.T, store core.ImportStore) (*Service, *Staging) {
	t.Helper()
	staging, err := NewStaging(store, 10*time.Minute)
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
