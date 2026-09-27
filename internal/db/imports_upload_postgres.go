package db

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/db/postgres"
)

func (s *postgresImportStore) CreateUploadedImport(
	ctx context.Context, job core.ImportJob, uploadID string,
) (result error) {
	if !job.Valid() || job.State != core.ImportPending || !core.ValidID(uploadID) {
		return core.ErrInvalidArgument
	}
	tx, err := s.pool.BeginTx(ctx, nil)
	if err != nil {
		return importStoreError("begin uploaded import", err)
	}
	defer rollbackImport(tx, &result)
	q := s.q.WithTx(tx)
	if createErr := q.CreateImport(ctx, postgresCreateImportParams(job)); createErr != nil {
		if isPostgresUnique(createErr) {
			return core.ErrImportInProgress
		}
		return importStoreError("create uploaded import", createErr)
	}
	rows, err := q.LinkImportUpload(ctx, postgres.LinkImportUploadParams{
		ImportID: sql.NullString{String: job.ID, Valid: true}, ID: uploadID,
	})
	if err != nil {
		return importStoreError("link import upload", err)
	}
	if rows < 1 {
		return core.ErrInvalidArgument
	}
	return importStoreError("commit uploaded import", tx.Commit())
}

func (s *postgresImportStore) WriteImportUploadChunks(
	ctx context.Context, chunks []core.ImportUploadChunk,
) (result error) {
	if err := validateUploadChunks(chunks); err != nil {
		return err
	}
	tx, err := s.pool.BeginTx(ctx, nil)
	if err != nil {
		return importStoreError("begin import upload chunks", err)
	}
	defer rollbackImport(tx, &result)
	q := s.q.WithTx(tx)
	for _, chunk := range chunks {
		params := postgres.InsertImportUploadChunkParams{
			ID: chunk.ID, ChunkIndex: chunk.Index, Bytes: chunk.Bytes,
			CreatedAt: core.NormalizeTime(chunk.CreatedAt),
		}
		if err := q.InsertImportUploadChunk(ctx, params); err != nil {
			return importStoreError("write import upload chunk", err)
		}
	}
	return importStoreError("commit import upload chunks", tx.Commit())
}

func (s *postgresImportStore) ImportUploadInfo(ctx context.Context, id string) (core.ImportUploadInfo, error) {
	if !core.ValidID(id) {
		return core.ImportUploadInfo{}, core.ErrInvalidArgument
	}
	row, err := s.q.GetImportUploadInfo(ctx, id)
	if err != nil {
		return core.ImportUploadInfo{}, importStoreError("get import upload info", importNotFound(err))
	}
	return core.ImportUploadInfo{ID: row.ID, Size: row.SizeBytes, ChunkCount: row.ChunkCount}, nil
}

func (s *postgresImportStore) ReadImportUploadChunk(ctx context.Context, id string, index int64) ([]byte, error) {
	if !core.ValidID(id) || index < 0 {
		return nil, core.ErrInvalidArgument
	}
	data, err := s.q.GetImportUploadChunk(ctx, postgres.GetImportUploadChunkParams{ID: id, ChunkIndex: index})
	if err != nil {
		return nil, importStoreError("get import upload chunk", importNotFound(err))
	}
	return data, nil
}

func (s *postgresImportStore) DeleteImportUpload(ctx context.Context, id string) error {
	if !core.ValidID(id) {
		return core.ErrInvalidArgument
	}
	return importStoreError("delete import upload", s.q.DeleteImportUpload(ctx, id))
}

func (s *postgresImportStore) DeleteOrphanImportUploads(
	ctx context.Context, before time.Time, limit int,
) (deleted int64, result error) {
	if before.IsZero() || limit < 1 || limit > core.MaxOrphanImportUploadChunks {
		return 0, core.ErrInvalidArgument
	}
	tx, err := s.pool.BeginTx(ctx, nil)
	if err != nil {
		return 0, importStoreError("begin orphan import upload cleanup", err)
	}
	defer rollbackImport(tx, &result)
	q := s.q.WithTx(tx)
	deleted, err = q.DeleteOrphanImportUploadChunks(ctx, postgres.DeleteOrphanImportUploadChunksParams{
		Before: core.NormalizeTime(before), PageSize: int32(limit),
	})
	if err != nil {
		return 0, importStoreError("delete orphan import upload chunks", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, importStoreError("commit orphan import upload cleanup", err)
	}
	return deleted, nil
}

func postgresDeleteJobUpload(ctx context.Context, q *postgres.Queries, importID string) error {
	err := q.DeleteImportUploadForJob(ctx, sql.NullString{String: importID, Valid: true})
	if err != nil {
		return fmt.Errorf("delete import upload for job: %w", err)
	}
	return nil
}
