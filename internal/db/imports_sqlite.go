package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/db/sqlite"
)

type sqliteImportStore struct {
	pool *sql.DB
	q    *sqlite.Queries
}

func newSQLiteImportStore(pool *sql.DB) *sqliteImportStore {
	return &sqliteImportStore{pool: pool, q: sqlite.New(pool)}
}

func (s *sqliteImportStore) CreateImport(ctx context.Context, job core.ImportJob) error {
	if !job.Valid() || job.State != core.ImportPending {
		return fmt.Errorf("create import: %w", core.ErrInvalidArgument)
	}
	err := s.q.CreateImport(ctx, sqliteCreateImportParams(job))
	if isSQLiteUnique(err) {
		return fmt.Errorf("create import: %w", core.ErrImportInProgress)
	}
	return importStoreError("create import", err)
}

func (s *sqliteImportStore) GetImport(ctx context.Context, id string) (core.ImportJob, error) {
	if !core.ValidID(id) {
		return core.ImportJob{}, core.ErrInvalidArgument
	}
	row, err := s.q.GetImport(ctx, id)
	if err != nil {
		return core.ImportJob{}, importStoreError("get import", importNotFound(err))
	}
	return sqliteImport(row)
}

func (s *sqliteImportStore) ListImports(ctx context.Context, query core.ImportListQuery) ([]core.ImportJob, error) {
	if err := validateImportList(query); err != nil {
		return nil, err
	}
	before, id := importListCursor(query)
	rows, err := s.q.ListImports(ctx, sqlite.ListImportsParams{
		BeforeCreatedAt: formatSQLiteTime(before), BeforeID: id, PageSize: int64(query.PageSize),
	})
	if err != nil {
		return nil, importStoreError("list imports", err)
	}
	jobs := make([]core.ImportJob, 0, len(rows))
	for _, row := range rows {
		job, mapErr := sqliteImport(row)
		if mapErr != nil {
			return nil, importStoreError("map import", mapErr)
		}
		jobs = append(jobs, job)
	}
	return jobs, nil
}

func (s *sqliteImportStore) ListActiveBloomImportCursors(ctx context.Context) ([]string, error) {
	cursors, err := s.q.ListActiveBloomImportCursors(ctx)
	if err != nil {
		return nil, importStoreError("list active Bloom import cursors", err)
	}
	if len(cursors) > core.MaxActiveImportUploads {
		return nil, importStoreError("list active Bloom import cursors", errors.New("active upload count exceeds safety bound"))
	}
	return cursors, nil
}

func (s *sqliteImportStore) CancelImport(ctx context.Context, id string, now time.Time) (core.ImportJob, error) {
	if !core.ValidID(id) || now.IsZero() {
		return core.ImportJob{}, core.ErrInvalidArgument
	}
	rows, err := s.q.CancelImport(ctx, sqlite.CancelImportParams{ID: id, Now: sqliteNullableTime(&now)})
	if err != nil {
		return core.ImportJob{}, importStoreError("cancel import", err)
	}
	if rows == 0 {
		_, getErr := s.GetImport(ctx, id)
		if errors.Is(getErr, core.ErrNotFound) {
			return core.ImportJob{}, core.ErrNotFound
		}
		if getErr != nil {
			return core.ImportJob{}, getErr
		}
		return core.ImportJob{}, core.ErrInvalidTransition
	}
	return s.GetImport(ctx, id)
}

func (s *sqliteImportStore) ClaimImport(ctx context.Context, lease core.ImportLease, now time.Time) (job core.ImportJob, result error) {
	if lease.Token == "" || !lease.ExpiresAt.After(now) || now.IsZero() {
		return job, core.ErrInvalidArgument
	}
	tx, err := s.pool.BeginTx(ctx, nil)
	if err != nil {
		return job, importStoreError("begin import claim", err)
	}
	defer rollbackImport(tx, &result)
	q := s.q.WithTx(tx)
	row, err := q.SelectClaimableImport(ctx, sqliteNullableTime(&now))
	if err != nil {
		return job, importStoreError("select import claim", importNotFound(err))
	}
	rows, err := q.ClaimImport(ctx, sqlite.ClaimImportParams{
		ID: row.ID, Token: lease.Token, ExpiresAt: sqliteNullableTime(&lease.ExpiresAt), Now: formatSQLiteTime(now),
	})
	if claimErr := importRowsError("claim import", rows, err, core.ErrNotFound); claimErr != nil {
		return job, claimErr
	}
	claimed, err := q.GetImport(ctx, row.ID)
	if err != nil {
		return job, importStoreError("read claimed import", err)
	}
	if err := tx.Commit(); err != nil {
		return job, importStoreError("commit import claim", err)
	}
	return sqliteImport(claimed)
}

func (s *sqliteImportStore) RenewImportLease(ctx context.Context, id string, lease core.ImportLease, now time.Time) error {
	rows, err := s.q.RenewImportLease(ctx, sqlite.RenewImportLeaseParams{
		ID: id, Token: lease.Token, ExpiresAt: sqliteNullableTime(&lease.ExpiresAt), Now: formatSQLiteTime(now),
	})
	if err != nil {
		return importStoreError("renew import lease", err)
	}
	if rows != 1 {
		return core.ErrImportLeaseLost
	}
	return nil
}

func (s *sqliteImportStore) CommitImportBatch(
	ctx context.Context, batch core.ImportBatch,
) (core.ImportBatchResult, error) {
	if err := validateImportBatch(batch); err != nil {
		return core.ImportBatchResult{}, err
	}
	var out core.ImportBatchResult
	err := withSQLiteWriteTransaction(ctx, s.pool, func(conn *sql.Conn) error {
		result, txErr := commitSQLiteImportBatch(ctx, sqlite.New(conn), batch)
		out = result
		return txErr
	})
	if err != nil {
		return core.ImportBatchResult{}, importStoreError("commit import batch", err)
	}
	return out, nil
}

func commitSQLiteImportBatch(
	ctx context.Context, q *sqlite.Queries, batch core.ImportBatch,
) (core.ImportBatchResult, error) {
	rows, err := q.FenceImportBatch(ctx, sqlite.FenceImportBatchParams{ID: batch.JobID, Token: batch.LeaseToken})
	if fenceErr := importRowsError("fence import batch", rows, err, core.ErrImportLeaseLost); fenceErr != nil {
		return core.ImportBatchResult{}, fenceErr
	}
	imported, duplicate, err := insertSQLiteImportRecords(ctx, q, batch)
	if err != nil {
		return core.ImportBatchResult{}, err
	}
	rows, err = q.CheckpointImport(ctx, sqlite.CheckpointImportParams{
		ID: batch.JobID, Token: batch.LeaseToken, Cursor: batch.Cursor,
		ReadDelta: int64(len(batch.Records)) + batch.Skipped, ImportedDelta: imported,
		SkippedDelta: batch.Skipped, DuplicateDelta: duplicate,
		ExpiresAt: sqliteNullableTime(&batch.LeaseExpiresAt), Now: formatSQLiteTime(batch.Now),
	})
	if checkpointErr := importRowsError("checkpoint import", rows, err, core.ErrImportLeaseLost); checkpointErr != nil {
		return core.ImportBatchResult{}, checkpointErr
	}
	checkpoint, err := q.GetImport(ctx, batch.JobID)
	if err != nil {
		return core.ImportBatchResult{}, importStoreError("read import checkpoint", err)
	}
	outJob, err := sqliteImport(checkpoint)
	if err != nil {
		return core.ImportBatchResult{}, importStoreError("map import checkpoint", err)
	}
	return importCounters(outJob), nil
}

func insertSQLiteImportRecords(ctx context.Context, q *sqlite.Queries, batch core.ImportBatch) (int64, int64, error) {
	var imported, duplicate int64
	for _, record := range batch.Records {
		dupe, err := q.FindCollectedImportDuplicate(ctx, sqlite.FindCollectedImportDuplicateParams{
			MediaServerID: batch.MediaServerID, MediaUserID: record.MediaUserID, ItemID: record.ItemID,
			StartAfter:  formatSQLiteTime(record.StartedAt.Add(-batch.ResumeWindow)),
			StartBefore: formatSQLiteTime(record.StartedAt.Add(batch.ResumeWindow)),
		})
		if err != nil {
			return 0, 0, importStoreError("find collected import duplicate", err)
		}
		if dupe {
			duplicate++
			continue
		}
		id, err := core.NewID()
		if err != nil {
			return 0, 0, importStoreError("create imported watch id", err)
		}
		params, err := sqliteImportedWatchParams(id, batch, record)
		if err != nil {
			return 0, 0, importStoreError("encode imported watch", err)
		}
		inserted, err := q.InsertImportedWatch(ctx, params)
		if err != nil {
			return 0, 0, importStoreError("insert imported watch", err)
		}
		if inserted == 1 {
			imported++
		} else {
			duplicate++
		}
	}
	return imported, duplicate, nil
}

func sqliteImportedWatchParams(
	id string, batch core.ImportBatch, record core.ImportedWatch,
) (sqlite.InsertImportedWatchParams, error) {
	stream, err := encodeStreamDetails(record.Stream)
	if err != nil {
		return sqlite.InsertImportedWatchParams{}, err
	}
	return sqlite.InsertImportedWatchParams{
		ID: id, MediaServerID: batch.MediaServerID, MediaUserID: record.MediaUserID,
		Username: record.Username, DeviceID: record.DeviceID, DeviceName: record.DeviceName, Client: record.Client,
		ItemID: record.ItemID, ItemName: record.ItemName, ItemType: record.ItemType,
		SeriesName: record.SeriesName, LibraryID: record.LibraryID, LibraryName: record.LibraryName,
		SeasonNumber: sqliteNullableInt32(record.SeasonNumber), EpisodeNumber: sqliteNullableInt32(record.EpisodeNumber),
		PlayMethod: string(record.PlayMethod), StartedAt: formatSQLiteTime(record.StartedAt),
		EndedAt: formatSQLiteTime(importEndedAt(record)), ActiveSeconds: int64(record.Duration / time.Second),
		LastPositionMs: int64(record.LastPosition / time.Millisecond), RuntimeMs: nullableDurationMilliseconds(record.Runtime),
		Now:             formatSQLiteTime(batch.Now),
		StreamContainer: stream.container, StreamVideoCodec: stream.videoCodec,
		StreamAudioCodec: stream.audioCodec, StreamBitrate: stream.bitrate,
		StreamWidth: stream.width, StreamHeight: stream.height,
		StreamFramerateHundredths: stream.framerate, StreamAudioChannels: stream.audioChannels,
		StreamIsVideoDirect: sqliteNullBool(stream.videoDirect), StreamIsAudioDirect: sqliteNullBool(stream.audioDirect),
		StreamTranscodeReasons: stream.reasons, ImportSource: sql.NullString{String: string(batch.Source), Valid: true},
		ImportRecordID: sql.NullString{String: record.RecordID, Valid: true},
	}, nil
}

func (s *sqliteImportStore) FinishImport(ctx context.Context, id, token string, state core.ImportState, lastError string, now time.Time) error {
	if !core.ValidID(id) || token == "" || !state.Terminal() || len(lastError) > core.MaxImportErrorBytes || now.IsZero() {
		return core.ErrInvalidArgument
	}
	rows, err := s.q.FinishImport(ctx, sqlite.FinishImportParams{
		ID: id, Token: token, State: string(state), LastError: lastError, Now: sqliteNullableTime(&now),
	})
	if err != nil {
		return importStoreError("finish import", err)
	}
	if rows != 1 {
		return core.ErrImportLeaseLost
	}
	return nil
}

func sqliteCreateImportParams(job core.ImportJob) sqlite.CreateImportParams {
	return sqlite.CreateImportParams{
		ID: job.ID, MediaServerID: job.MediaServerID, Source: string(job.Source), State: string(job.State),
		Cursor: job.Cursor, ReadCount: job.Read, ImportedCount: job.Imported,
		SkippedCount: job.Skipped, DuplicateCount: job.Duplicate, LastError: job.LastError,
		LeaseToken: job.LeaseToken, LeaseExpiresAt: sqliteNullableTime(job.LeaseExpiresAt), RequestedBy: job.RequestedBy,
		CreatedAt: formatSQLiteTime(job.CreatedAt), StartedAt: sqliteNullableTime(job.StartedAt),
		FinishedAt: sqliteNullableTime(job.FinishedAt), UpdatedAt: formatSQLiteTime(job.UpdatedAt),
	}
}

func sqliteImport(row sqlite.Import) (core.ImportJob, error) {
	created, err := parseSQLiteTime(row.CreatedAt)
	if err != nil {
		return core.ImportJob{}, err
	}
	updated, err := parseSQLiteTime(row.UpdatedAt)
	if err != nil {
		return core.ImportJob{}, err
	}
	lease, err := sqliteOptionalTime(row.LeaseExpiresAt)
	if err != nil {
		return core.ImportJob{}, err
	}
	started, err := sqliteOptionalTime(row.StartedAt)
	if err != nil {
		return core.ImportJob{}, err
	}
	finished, err := sqliteOptionalTime(row.FinishedAt)
	if err != nil {
		return core.ImportJob{}, err
	}
	job := core.ImportJob{
		ID: row.ID, MediaServerID: row.MediaServerID, Source: core.ImportSource(row.Source), State: core.ImportState(row.State),
		Cursor: row.Cursor, Read: row.ReadCount, Imported: row.ImportedCount, Skipped: row.SkippedCount,
		Duplicate: row.DuplicateCount, LastError: row.LastError, LeaseToken: row.LeaseToken,
		LeaseExpiresAt: lease, RequestedBy: row.RequestedBy, CreatedAt: created,
		StartedAt: started, FinishedAt: finished, UpdatedAt: updated,
	}
	if !job.Valid() {
		return core.ImportJob{}, errors.New("persisted import is invalid")
	}
	return job, nil
}

func importCounters(job core.ImportJob) core.ImportBatchResult {
	return core.ImportBatchResult{Read: job.Read, Imported: job.Imported, Skipped: job.Skipped, Duplicate: job.Duplicate}
}
