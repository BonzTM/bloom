package importer

import (
	"archive/zip"
	"bytes"
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
	createErr       error
	linkBeforeError bool
}

func (s *serviceStore) CreateImport(_ context.Context, job core.ImportJob) error {
	s.job = job
	return s.createErr
}

func (s *serviceStore) CreateUploadedImport(
	_ context.Context, job core.ImportJob, uploadID string,
) error {
	if s.createErr == nil || s.linkBeforeError {
		s.job = job
		s.uploadStore().linkUpload(uploadID)
	}
	if s.createErr != nil {
		return s.createErr
	}
	return nil
}

func (s *serviceStore) CancelImport(ctx context.Context, _ string, now time.Time) (core.ImportJob, error) {
	s.job.State, s.job.FinishedAt, s.job.UpdatedAt = core.ImportCancelled, &now, now
	if s.job.Source == core.ImportSourceBloomExport || s.job.Source == core.ImportSourceJellystat {
		cursor, err := decodeFileCursor(s.job.Cursor)
		if err != nil {
			return core.ImportJob{}, err
		}
		s.uploadStore().mu.Lock()
		s.uploadStore().deleteUpload(cursor.ID)
		s.uploadStore().mu.Unlock()
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
	stagingID, err := service.StageBloomExport(t.Context(), bytes.NewReader(validBloomUpload(t)))
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

func TestCreateJellystatValidatesFirstLineAndRemovesRejectedUpload(t *testing.T) {
	store := newServiceStore()
	service, staging := newService(t, store)
	invalidID, err := service.StageBloomExport(t.Context(), strings.NewReader(`{"id":"watch"}`+"\n"))
	if err != nil {
		t.Fatalf("stage invalid upload: %v", err)
	}
	_, err = service.CreateJellystat(t.Context(), importServiceServerID, importServiceAccountID, invalidID)
	if !errors.Is(err, core.ErrInvalidArgument) {
		t.Fatalf("CreateJellystat(invalid) = %v, want invalid argument", err)
	}
	if _, openErr := staging.open(t.Context(), invalidID); !errors.Is(openErr, core.ErrNotFound) {
		t.Fatalf("rejected Jellystat upload remains: %v", openErr)
	}
	validID, err := service.StageBloomExport(
		t.Context(), strings.NewReader(`{"type":"table","table":"jf_libraries"}`+"\n"),
	)
	if err != nil {
		t.Fatalf("stage valid upload: %v", err)
	}
	job, err := service.CreateJellystat(t.Context(), importServiceServerID, importServiceAccountID, validID)
	if err != nil || job.Source != core.ImportSourceJellystat {
		t.Fatalf("CreateJellystat(valid) = %+v, %v", job, err)
	}
}

func TestBloomUploadIsRemovedWhenJobCreationConflicts(t *testing.T) {
	store := newServiceStore()
	store.createErr = core.ErrImportInProgress
	service, staging := newService(t, store)
	stagingID, err := service.StageBloomExport(t.Context(), bytes.NewReader(validBloomUpload(t)))
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

func TestBloomUploadSurvivesCommittedLinkReportedAsError(t *testing.T) {
	store := newServiceStore()
	store.createErr = errors.Join(core.ErrImportStore, errors.New("commit outcome unknown"))
	store.linkBeforeError = true
	service, staging := newService(t, store)
	stagingID, err := service.StageBloomExport(t.Context(), bytes.NewReader(validBloomUpload(t)))
	if err != nil {
		t.Fatalf("StageBloomExport: %v", err)
	}
	_, err = service.CreateBloomExport(t.Context(), importServiceServerID, importServiceAccountID, stagingID)
	if !errors.Is(err, core.ErrImportStore) {
		t.Fatalf("CreateBloomExport = %v, want store error", err)
	}
	if _, err := staging.open(t.Context(), stagingID); err != nil {
		t.Fatalf("linked upload was discarded: %v", err)
	}
}

func TestCreateBloomExportRejectsInvalidArchiveBeforeCreatingJob(t *testing.T) {
	tests := map[string][]byte{
		"bare JSONL":          []byte(validJSONLFixture() + "\n"),
		"stored entry":        bloomArchiveWithMethod(t, zip.Store),
		"decompression ratio": excessiveRatioArchive(t),
		"entry over 4 GiB":    oversizedZipEntry(t),
	}
	for name, payload := range tests {
		t.Run(name, func(t *testing.T) {
			store := newServiceStore()
			service, staging := newService(t, store)
			stagingID, err := service.StageBloomExport(t.Context(), bytes.NewReader(payload))
			if err != nil {
				t.Fatalf("StageBloomExport: %v", err)
			}
			_, err = service.CreateBloomExport(
				t.Context(), importServiceServerID, importServiceAccountID, stagingID,
			)
			var invalid *core.InvalidArgumentError
			if !errors.As(err, &invalid) || invalid.Field != "file" || store.job.ID != "" {
				t.Fatalf("CreateBloomExport = %v, job %+v", err, store.job)
			}
			if _, openErr := staging.open(t.Context(), stagingID); !errors.Is(openErr, core.ErrNotFound) {
				t.Fatalf("rejected upload remains: %v", openErr)
			}
		})
	}
}

func TestCreateBloomExportRejectsAggregateSizeBeforeCreatingJob(t *testing.T) {
	payload := aggregateOversizedArchive(t)
	if err := openStagedWatch(t, payload); !errors.Is(err, errImportArchiveAggregateSize) {
		t.Fatalf("openWatchUpload = %v, want aggregate size limit", err)
	}
	store := newServiceStore()
	service, staging := newService(t, store)
	stagingID, err := service.StageBloomExport(t.Context(), bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("StageBloomExport: %v", err)
	}
	_, err = service.CreateBloomExport(t.Context(), importServiceServerID, importServiceAccountID, stagingID)
	var invalid *core.InvalidArgumentError
	if !errors.As(err, &invalid) || invalid.Field != "file" || store.job.ID != "" {
		t.Fatalf("CreateBloomExport = %v, job %+v", err, store.job)
	}
	if _, openErr := staging.open(t.Context(), stagingID); !errors.Is(openErr, core.ErrNotFound) {
		t.Fatalf("rejected upload remains: %v", openErr)
	}
}

func TestCreateBloomExportAcceptsCompressedEntryLargerThanUploadCap(t *testing.T) {
	store := newServiceStore()
	service, staging := newService(t, store)
	watches := strings.Repeat(validJSONLFixture()+"\n", 8)
	payload := zipFixture(t, map[string]string{
		"manifest.json": validManifestFixture(), "watches.jsonl": watches,
		"summary.json": validSummaryFixture(8),
	})
	if len(payload) >= len(watches) {
		t.Fatalf("fixture did not compress: archive %d, watches %d", len(payload), len(watches))
	}
	staging.maxBytes = int64(len(payload))
	if int64(len(watches)) <= staging.maxBytes {
		t.Fatalf("watches %d must exceed upload cap %d", len(watches), staging.maxBytes)
	}
	stagingID, err := service.StageBloomExport(t.Context(), bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("StageBloomExport: %v", err)
	}
	job, err := service.CreateBloomExport(
		t.Context(), importServiceServerID, importServiceAccountID, stagingID,
	)
	if err != nil || job.Source != core.ImportSourceBloomExport {
		t.Fatalf("CreateBloomExport = %+v, %v", job, err)
	}
}

func validBloomUpload(t *testing.T) []byte {
	t.Helper()
	return zipFixture(t, map[string]string{
		"manifest.json": validManifestFixture(), "watches.jsonl": validJSONLFixture() + "\n",
		"summary.json": validSummaryFixture(1),
	})
}

func bloomArchiveWithMethod(t *testing.T, method uint16) []byte {
	t.Helper()
	var output bytes.Buffer
	archive := zip.NewWriter(&output)
	entries := map[string]string{
		"manifest.json": validManifestFixture(), "watches.jsonl": validJSONLFixture() + "\n",
		"summary.json": validSummaryFixture(1),
	}
	for _, name := range []string{"manifest.json", "watches.jsonl", "summary.json"} {
		header := &zip.FileHeader{Name: name, Method: zip.Deflate}
		if name == "watches.jsonl" {
			header.Method = method
		}
		entry, err := archive.CreateHeader(header)
		if err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
		if _, err := io.WriteString(entry, entries[name]); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatalf("close Bloom archive: %v", err)
	}
	return output.Bytes()
}

func aggregateOversizedArchive(t *testing.T) []byte {
	t.Helper()
	metadataSize := uint64(maxImportMetadataBytes)
	jsonlSize := uint64(core.MaxImportArchiveEntryBytes / 2)
	return rawZipEntries(t, []rawZipFixture{
		{name: "manifest.json", uncompressed: metadataSize, compressed: compressedAtLimit(metadataSize)},
		{name: "watches.jsonl", uncompressed: jsonlSize, compressed: compressedAtLimit(jsonlSize)},
		{name: "imports.jsonl", uncompressed: jsonlSize + 1, compressed: compressedAtLimit(jsonlSize + 1)},
		{name: "summary.json", uncompressed: metadataSize, compressed: compressedAtLimit(metadataSize)},
	})
}

type rawZipFixture struct {
	name                     string
	uncompressed, compressed uint64
}

func compressedAtLimit(uncompressed uint64) uint64 {
	ratio := uint64(core.MaxImportDecompressionRatio)
	return (uncompressed + ratio - 1) / ratio
}

func rawZipEntries(t *testing.T, entries []rawZipFixture) []byte {
	t.Helper()
	var output bytes.Buffer
	archive := zip.NewWriter(&output)
	for _, fixture := range entries {
		header := &zip.FileHeader{Name: fixture.name, Method: zip.Deflate}
		header.UncompressedSize64 = fixture.uncompressed
		header.CompressedSize64 = fixture.compressed
		entry, err := archive.CreateRaw(header)
		if err != nil {
			t.Fatalf("create raw %s: %v", fixture.name, err)
		}
		if _, err := entry.Write(make([]byte, int(fixture.compressed))); err != nil {
			t.Fatalf("write raw %s: %v", fixture.name, err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatalf("close raw archive: %v", err)
	}
	return output.Bytes()
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
