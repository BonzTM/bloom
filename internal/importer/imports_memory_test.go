package importer

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/BonzTM/bloom/internal/config"
	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/db"
	"github.com/BonzTM/bloom/internal/testutil"
)

const (
	importMemoryRecords = 20_000
	// importPeakHeapMargin allows GC and allocator noise under -race while
	// remaining far below the memory required to retain 140,000 extra records.
	importPeakHeapMargin = 16 << 20
)

func TestCompressedBloomImportPeakHeapIsIndependentOfRecordCount(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping multi-second import memory proof in short mode")
	}
	t.Run("warmup", func(t *testing.T) {
		measureCompressedImportHeap(t, 1)
	})
	small := measureCompressedImportHeap(t, importMemoryRecords)
	large := measureCompressedImportHeap(t, 8*importMemoryRecords)
	t.Logf("import peak heap growth: N=%d: %d bytes; 8N=%d: %d bytes", importMemoryRecords, small, 8*importMemoryRecords, large)
	if large > small+importPeakHeapMargin {
		t.Fatalf("import peak heap grew by %d bytes, limit %d", large-small, importPeakHeapMargin)
	}
}

func measureCompressedImportHeap(t *testing.T, records int) uint64 {
	t.Helper()
	uploads := openMemoryTestUploadStore(t)
	job := pendingWorkerJob(t)
	job.Source = core.ImportSourceBloomExport
	store := &sqliteWorkerStore{workerStore: workerStore{job: job}, uploads: uploads}
	worker := newTestWorker(t, store, reportingStub{})
	peak, err := testutil.PeakHeapGrowth(func() error {
		id, stageErr := stageCompressedWatchArchive(t.Context(), worker.deps.Staging, records)
		if stageErr != nil {
			return stageErr
		}
		store.job.Cursor, stageErr = encodeFileCursor(fileCursor{ID: id})
		if stageErr != nil {
			return stageErr
		}
		return worker.runOnce(t.Context())
	})
	if err != nil {
		t.Fatalf("run %d-record compressed import: %v", records, err)
	}
	if store.finished != core.ImportCompleted || store.result.Imported != int64(records) {
		t.Fatalf("imported %d records: state %q, result %+v", records, store.finished, store.result)
	}
	return peak
}

func openMemoryTestUploadStore(t *testing.T) core.ImportStore {
	t.Helper()
	cfg := config.DatabaseConfig{
		Driver:          config.DriverSQLite,
		DSN:             filepath.Join(t.TempDir(), "upload.db"),
		MaxOpenConns:    1,
		MaxIdleConns:    1,
		ConnMaxLifetime: time.Hour,
		ConnMaxIdleTime: time.Hour,
	}
	pool, err := db.Open(t.Context(), cfg, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("open SQLite upload store: %v", err)
	}
	t.Cleanup(func() { _ = pool.Close() })
	if migrateErr := db.Migrate(t.Context(), pool, config.DriverSQLite); migrateErr != nil {
		t.Fatalf("migrate SQLite upload store: %v", migrateErr)
	}
	store, err := db.NewImportStore(pool, config.DriverSQLite)
	if err != nil {
		t.Fatalf("create SQLite upload store: %v", err)
	}
	return store
}

func stageCompressedWatchArchive(ctx context.Context, staging *Staging, records int) (string, error) {
	reader, writer := io.Pipe()
	archiveDone := make(chan error, 1)
	go func() {
		err := writeCompressedWatchArchive(writer, records)
		archiveDone <- errors.Join(err, writer.CloseWithError(err))
	}()
	id, err := staging.stage(ctx, reader, staticClock{time.Now()})
	closeErr := reader.CloseWithError(err)
	return id, errors.Join(err, closeErr, <-archiveDone)
}

type sqliteWorkerStore struct {
	workerStore
	uploads core.ImportStore
}

func (s *sqliteWorkerStore) WriteImportUploadChunks(
	ctx context.Context, chunks []core.ImportUploadChunk,
) error {
	return s.uploads.WriteImportUploadChunks(ctx, chunks)
}

func (s *sqliteWorkerStore) ImportUploadInfo(ctx context.Context, id string) (core.ImportUploadInfo, error) {
	return s.uploads.ImportUploadInfo(ctx, id)
}

func (s *sqliteWorkerStore) ReadImportUploadChunk(ctx context.Context, id string, index int64) ([]byte, error) {
	return s.uploads.ReadImportUploadChunk(ctx, id, index)
}

func (s *sqliteWorkerStore) DeleteImportUpload(ctx context.Context, id string) error {
	return s.uploads.DeleteImportUpload(ctx, id)
}

func (s *sqliteWorkerStore) DeleteOrphanImportUploads(
	ctx context.Context, before time.Time, limit int,
) (int64, error) {
	return s.uploads.DeleteOrphanImportUploads(ctx, before, limit)
}
