package http

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/importer"
	"github.com/BonzTM/bloom/internal/testutil"
)

const (
	exportMemoryRecords = 20_000
	// exportPeakHeapMargin allows GC and allocator noise under -race while
	// remaining far below the memory required to retain 140,000 extra records.
	exportPeakHeapMargin = 8 << 20
	// uploadPeakHeapLimit covers the eight 1 MiB reusable staging buffers plus
	// allocator and multipart parsing noise under the race detector.
	uploadPeakHeapLimit = 24 << 20
)

func TestWatchExportPeakHeapIsIndependentOfRecordCount(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping multi-second export memory proof in short mode")
	}
	measureWatchExportHeap(t, 1) // Initialize codecs before comparing live heap peaks.
	small := measureWatchExportHeap(t, exportMemoryRecords)
	large := measureWatchExportHeap(t, 8*exportMemoryRecords)
	t.Logf("export peak heap growth: N=%d: %d bytes; 8N=%d: %d bytes", exportMemoryRecords, small, 8*exportMemoryRecords, large)
	if large > small+exportPeakHeapMargin {
		t.Fatalf("export peak heap grew by %d bytes, limit %d", large-small, exportPeakHeapMargin)
	}
}

func measureWatchExportHeap(t *testing.T, records int) uint64 {
	t.Helper()
	reader := &generatedExportPlaybackReader{
		records: records,
		first:   time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC),
	}
	harness := newAuthHarness(t, nil)
	harness.server.playbackReader = reader
	harness.server.imports = &fakeImportManager{}
	harness.server.importTransferTimeout = 10 * time.Minute
	cookie := sessionCookie(t, harness.login(t, "alice", "secret-password"))
	request := httptest.NewRequest(http.MethodGet, "https://bloom.test/api/v1/exports/watches", nil)
	request.RemoteAddr = "192.0.2.10:4321"
	request.Header.Set("X-Request-ID", "memory-export")
	request.AddCookie(cookie)
	response := newDiscardResponseWriter()
	peak, err := testutil.PeakHeapGrowth(func() error {
		harness.h.ServeHTTP(response, request)
		return nil
	})
	if err != nil {
		t.Fatalf("stream %d-record export: %v", records, err)
	}
	if response.status != http.StatusOK {
		t.Fatalf("stream %d-record export status = %d, want %d; logs: %s", records, response.status, http.StatusOK, harness.logs.String())
	}
	t.Logf("streamed %d-record export: %d ZIP bytes", records, response.written)
	return peak
}

type discardResponseWriter struct {
	header  http.Header
	status  int
	written int64
}

func newDiscardResponseWriter() *discardResponseWriter {
	return &discardResponseWriter{header: make(http.Header)}
}

func (w *discardResponseWriter) Header() http.Header { return w.header }

func (w *discardResponseWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
}

func (w *discardResponseWriter) Write(data []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	w.written += int64(len(data))
	return len(data), nil
}

type generatedExportPlaybackReader struct {
	records int
	first   time.Time
}

func (*generatedExportPlaybackReader) GetWatch(context.Context, string) (core.PlaybackWatch, error) {
	return core.PlaybackWatch{}, core.ErrNotFound
}

func (*generatedExportPlaybackReader) ListWatchPositions(context.Context, string) ([]core.PlaybackPosition, error) {
	return nil, nil
}

func (r *generatedExportPlaybackReader) ListWatches(
	ctx context.Context, query core.PlaybackQuery,
) ([]core.PlaybackWatch, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	start := 0
	if !query.BeforeStartedAt.IsZero() {
		start = int(r.first.Sub(query.BeforeStartedAt)/time.Second) + 1
	}
	end := min(start+query.PageSize, r.records)
	page := make([]core.PlaybackWatch, 0, max(0, end-start))
	for index := start; index < end; index++ {
		page = append(page, generatedExportWatch(r.first, index))
	}
	return page, nil
}

func generatedExportWatch(first time.Time, index int) core.PlaybackWatch {
	started := first.Add(-time.Duration(index) * time.Second)
	ended := started.Add(time.Minute)
	watch := exportHTTPWatch(ended)
	watch.ID = fmt.Sprintf("33333333-3333-4333-8333-%012d", index)
	watch.ItemName = generatedExportEntropy(uint64(index), 0x9e3779b97f4a7c15)
	watch.DeviceName = generatedExportEntropy(uint64(index), 0xbf58476d1ce4e5b9)
	watch.Client = generatedExportEntropy(uint64(index), 0x94d049bb133111eb)
	watch.StartedAt = started
	return watch
}

func generatedExportEntropy(index, salt uint64) string {
	const entropyBytes = 96
	data := make([]byte, entropyBytes)
	state := index ^ salt
	for position := range entropyBytes {
		state ^= state << 13
		state ^= state >> 7
		state ^= state << 17
		data[position] = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"[state%62]
	}
	return string(data)
}

func TestMultipartUploadStagingHasFixedPeakHeap(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping multi-second multipart memory proof in short mode")
	}
	const uploadBytes = 32 << 20
	peak := measureMultipartUploadHeap(t, uploadBytes)
	t.Logf("multipart staging peak heap growth for %d bytes: %d bytes", uploadBytes, peak)
	if peak > uploadPeakHeapLimit {
		t.Fatalf("multipart staging peak heap = %d bytes, limit %d", peak, uploadPeakHeapLimit)
	}
}

func measureMultipartUploadHeap(t *testing.T, size int64) uint64 {
	t.Helper()
	file, err := os.CreateTemp(t.TempDir(), "bloom-memory-upload-*.bin")
	if err != nil {
		t.Fatalf("create staged upload file: %v", err)
	}
	t.Cleanup(func() { _ = file.Close() })
	uploads, err := newFileUploadStore(file)
	if err != nil {
		t.Fatalf("create file upload store: %v", err)
	}
	store := &fileImportStore{exportImportStore: &exportImportStore{}, uploads: uploads}
	staging, err := importer.NewStaging(store, time.Minute)
	if err != nil {
		t.Fatalf("create import staging: %v", err)
	}
	service, err := importer.NewService(
		store, exportImportServer{}, exportImportClock{now: time.Now()}, staging, slog.New(slog.DiscardHandler),
	)
	if err != nil {
		t.Fatalf("create import service: %v", err)
	}
	server, _, _ := importHandlerServer(t)
	server.imports = service
	var status int
	peak, err := testutil.PeakHeapGrowth(func() error {
		body, contentType, writeDone := generatedMultipartBody(size)
		request := requestWithAccount(t, http.MethodPost, "/api/v1/imports", "", core.PermissionAdminSettings)
		request.Body = body
		request.Header.Set("Content-Type", contentType)
		recorder := httptest.NewRecorder()
		server.handleCreateImport(recorder, request)
		status = recorder.Code
		return errors.Join(body.Close(), <-writeDone)
	})
	if err != nil {
		t.Fatalf("stream multipart upload: %v", err)
	}
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("invalid streamed archive status = %d, want %d", status, http.StatusUnprocessableEntity)
	}
	return peak
}

func generatedMultipartBody(size int64) (io.ReadCloser, string, <-chan error) {
	reader, writer := io.Pipe()
	form := multipart.NewWriter(writer)
	done := make(chan error, 1)
	go func() {
		err := writeGeneratedMultipart(form, size)
		done <- errors.Join(err, writer.CloseWithError(err))
	}()
	return reader, form.FormDataContentType(), done
}

func writeGeneratedMultipart(form *multipart.Writer, size int64) error {
	if err := form.WriteField("media_server_id", importTestServerID); err != nil {
		return err
	}
	if err := form.WriteField("source", "bloom_export"); err != nil {
		return err
	}
	part, err := form.CreateFormFile("file", "large.zip")
	if err != nil {
		return err
	}
	if _, err := io.CopyN(part, zeroReader{}, size); err != nil {
		return err
	}
	return form.Close()
}

type zeroReader struct{}

func (zeroReader) Read(destination []byte) (int, error) {
	clear(destination)
	return len(destination), nil
}

type fileImportStore struct {
	*exportImportStore
	uploads *fileUploadStore
}

func (s *fileImportStore) WriteImportUploadChunks(
	ctx context.Context, chunks []core.ImportUploadChunk,
) error {
	return s.uploads.WriteImportUploadChunks(ctx, chunks)
}

func (s *fileImportStore) ImportUploadInfo(ctx context.Context, id string) (core.ImportUploadInfo, error) {
	return s.uploads.ImportUploadInfo(ctx, id)
}

func (s *fileImportStore) ReadImportUploadChunk(ctx context.Context, id string, index int64) ([]byte, error) {
	return s.uploads.ReadImportUploadChunk(ctx, id, index)
}

func (s *fileImportStore) DeleteImportUpload(ctx context.Context, id string) error {
	return s.uploads.DeleteImportUpload(ctx, id)
}

func (s *fileImportStore) DeleteOrphanImportUploads(
	ctx context.Context, before time.Time, limit int,
) (int64, error) {
	return s.uploads.DeleteOrphanImportUploads(ctx, before, limit)
}

type fileUploadStore struct {
	file    *os.File
	info    core.ImportUploadInfo
	deleted bool
	mu      sync.Mutex
}

func newFileUploadStore(file *os.File) (*fileUploadStore, error) {
	if file == nil {
		return nil, core.ErrInvalidArgument
	}
	return &fileUploadStore{file: file}, nil
}

func (s *fileUploadStore) WriteImportUploadChunks(
	_ context.Context, chunks []core.ImportUploadChunk,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, chunk := range chunks {
		if err := s.writeChunk(chunk); err != nil {
			return err
		}
	}
	return nil
}

func (s *fileUploadStore) writeChunk(chunk core.ImportUploadChunk) error {
	if s.info.ID != "" && chunk.ID != s.info.ID {
		return core.ErrInvalidArgument
	}
	offset := chunk.Index * int64(core.ImportUploadChunkBytes)
	written, err := s.file.WriteAt(chunk.Bytes, offset)
	if err != nil {
		return fmt.Errorf("write test upload: %w", err)
	}
	if written != len(chunk.Bytes) {
		return fmt.Errorf("write test upload: %w", io.ErrShortWrite)
	}
	s.info.ID = chunk.ID
	s.info.Size = max(s.info.Size, offset+int64(written))
	s.info.ChunkCount = max(s.info.ChunkCount, chunk.Index+1)
	s.deleted = false
	return nil
}

func (s *fileUploadStore) ImportUploadInfo(_ context.Context, id string) (core.ImportUploadInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.deleted || id != s.info.ID || s.info.ID == "" {
		return core.ImportUploadInfo{}, core.ErrNotFound
	}
	return s.info, nil
}

func (s *fileUploadStore) ReadImportUploadChunk(
	_ context.Context, id string, index int64,
) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.deleted || id != s.info.ID || index < 0 || index >= s.info.ChunkCount {
		return nil, core.ErrNotFound
	}
	return s.readChunk(index)
}

func (s *fileUploadStore) readChunk(index int64) ([]byte, error) {
	size := core.ImportUploadChunkBytes
	if index == s.info.ChunkCount-1 {
		size = int(s.info.Size - index*int64(core.ImportUploadChunkBytes))
	}
	chunk := make([]byte, size)
	read, err := s.file.ReadAt(chunk, index*int64(core.ImportUploadChunkBytes))
	if err != nil && (!errors.Is(err, io.EOF) || read != len(chunk)) {
		return nil, fmt.Errorf("read test upload: %w", err)
	}
	if read != len(chunk) {
		return nil, io.ErrUnexpectedEOF
	}
	return chunk, nil
}

func (s *fileUploadStore) DeleteImportUpload(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id == s.info.ID {
		s.deleted = true
	}
	return nil
}

func (*fileUploadStore) DeleteOrphanImportUploads(context.Context, time.Time, int) (int64, error) {
	return 0, nil
}
