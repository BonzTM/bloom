package db

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/db/sqlite"
)

func (s *sqliteImportStore) CreateUploadedImport(ctx context.Context, job core.ImportJob, uploadID string) error {
	if !job.Valid() || job.State != core.ImportPending || !core.ValidID(uploadID) {
		return core.ErrInvalidArgument
	}
	err := withSQLiteWriteTransaction(ctx, s.pool, func(conn *sql.Conn) error {
		q := sqlite.New(conn)
		if err := q.CreateImport(ctx, sqliteCreateImportParams(job)); err != nil {
			if isSQLiteUnique(err) {
				return core.ErrImportInProgress
			}
			return err
		}
		rows, err := q.LinkImportUpload(ctx, sqlite.LinkImportUploadParams{
			ImportID: sql.NullString{String: job.ID, Valid: true}, ID: uploadID,
		})
		if err != nil {
			return err
		}
		if rows < 1 {
			return core.ErrInvalidArgument
		}
		return nil
	})
	return importStoreError("create uploaded import", err)
}

func (s *sqliteImportStore) WriteImportUploadChunks(
	ctx context.Context, chunks []core.ImportUploadChunk,
) error {
	if err := validateUploadChunks(chunks); err != nil {
		return err
	}
	err := withSQLiteWriteTransaction(ctx, s.pool, func(conn *sql.Conn) error {
		q := sqlite.New(conn)
		for _, chunk := range chunks {
			params := sqlite.InsertImportUploadChunkParams{
				ID: chunk.ID, ChunkIndex: chunk.Index, Bytes: chunk.Bytes,
				CreatedAt: formatSQLiteTime(chunk.CreatedAt),
			}
			if err := q.InsertImportUploadChunk(ctx, params); err != nil {
				return err
			}
		}
		return nil
	})
	return importStoreError("write import upload chunks", err)
}

func (s *sqliteImportStore) ImportUploadInfo(ctx context.Context, id string) (core.ImportUploadInfo, error) {
	if !core.ValidID(id) {
		return core.ImportUploadInfo{}, core.ErrInvalidArgument
	}
	row, err := s.q.GetImportUploadInfo(ctx, id)
	if err != nil {
		return core.ImportUploadInfo{}, importStoreError("get import upload info", importNotFound(err))
	}
	return core.ImportUploadInfo{ID: row.ID, Size: row.SizeBytes, ChunkCount: row.ChunkCount}, nil
}

func (s *sqliteImportStore) ReadImportUploadChunk(ctx context.Context, id string, index int64) ([]byte, error) {
	if !core.ValidID(id) || index < 0 {
		return nil, core.ErrInvalidArgument
	}
	data, err := s.q.GetImportUploadChunk(ctx, sqlite.GetImportUploadChunkParams{ID: id, ChunkIndex: index})
	if err != nil {
		return nil, importStoreError("get import upload chunk", importNotFound(err))
	}
	return data, nil
}

func (s *sqliteImportStore) DeleteImportUpload(ctx context.Context, id string) error {
	if !core.ValidID(id) {
		return core.ErrInvalidArgument
	}
	return importStoreError("delete import upload", s.q.DeleteImportUpload(ctx, id))
}

func (s *sqliteImportStore) DeleteOrphanImportUploads(
	ctx context.Context, before time.Time, limit int,
) (int64, error) {
	if before.IsZero() || limit < 1 || limit > core.MaxOrphanImportUploads {
		return 0, core.ErrInvalidArgument
	}
	var deleted int64
	err := withSQLiteWriteTransaction(ctx, s.pool, func(conn *sql.Conn) error {
		q := sqlite.New(conn)
		ids, err := q.ListOrphanImportUploadIDs(ctx, sqlite.ListOrphanImportUploadIDsParams{
			Before: formatSQLiteTime(before), PageSize: int64(limit),
		})
		if err != nil {
			return err
		}
		for _, id := range ids {
			rows, err := q.DeleteOrphanImportUpload(ctx, id)
			if err != nil {
				return err
			}
			deleted += rows
		}
		return nil
	})
	if err != nil {
		return 0, importStoreError("delete orphan import uploads", err)
	}
	return deleted, nil
}

func sqliteDeleteJobUpload(ctx context.Context, q *sqlite.Queries, importID string) error {
	err := q.DeleteImportUploadForJob(ctx, sql.NullString{String: importID, Valid: true})
	if err != nil {
		return fmt.Errorf("delete import upload for job: %w", err)
	}
	return nil
}
