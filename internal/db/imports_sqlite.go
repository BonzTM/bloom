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
		MediaServerID: query.MediaServerID, BeforeCreatedAt: formatSQLiteTime(before),
		BeforeID: id, PageSize: int64(query.PageSize),
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

func (s *sqliteImportStore) CancelImport(ctx context.Context, id string, now time.Time) (core.ImportJob, error) {
	if !core.ValidID(id) || now.IsZero() {
		return core.ImportJob{}, core.ErrInvalidArgument
	}
	var job core.ImportJob
	err := withSQLiteWriteTransaction(ctx, s.pool, func(conn *sql.Conn) error {
		q := sqlite.New(conn)
		rows, err := q.CancelImport(ctx, sqlite.CancelImportParams{ID: id, Now: sqliteNullableTime(&now)})
		if err != nil || rows == 0 {
			return sqliteCancelError(ctx, q, id, rows, err)
		}
		if cleanupErr := sqliteDeleteJobUpload(ctx, q, id); cleanupErr != nil {
			return cleanupErr
		}
		row, err := q.GetImport(ctx, id)
		if err != nil {
			return err
		}
		job, err = sqliteImport(row)
		return err
	})
	if err != nil {
		return core.ImportJob{}, importStoreError("cancel import", err)
	}
	return job, nil
}

func sqliteCancelError(ctx context.Context, q *sqlite.Queries, id string, rows int64, queryErr error) error {
	if queryErr != nil {
		return queryErr
	}
	if rows == 1 {
		return nil
	}
	if _, err := q.GetImport(ctx, id); errors.Is(err, sql.ErrNoRows) {
		return core.ErrNotFound
	} else if err != nil {
		return err
	}
	return core.ErrInvalidTransition
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
		crossDuplicate, err := sqliteCrossSourceDuplicate(ctx, q, batch, record)
		if err != nil {
			return 0, 0, err
		}
		if crossDuplicate {
			duplicate++
			continue
		}
		if supersedeErr := supersedeSQLiteUserData(ctx, q, batch, record); supersedeErr != nil {
			return 0, 0, importStoreError("supersede Jellyfin user-data watch", supersedeErr)
		}
		dupe, err := sqliteImportDuplicate(ctx, q, batch, record)
		if err != nil {
			return 0, 0, importStoreError("find collected import duplicate", err)
		}
		if dupe {
			duplicate++
			if rebuildErr := rebuildSQLiteImportRollup(ctx, q, batch.MediaServerID, record.ItemID); rebuildErr != nil {
				return 0, 0, rebuildErr
			}
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
		if err := rebuildSQLiteImportRollup(ctx, q, batch.MediaServerID, record.ItemID); err != nil {
			return 0, 0, err
		}
	}
	return imported, duplicate, nil
}

func rebuildSQLiteImportRollup(ctx context.Context, q *sqlite.Queries, serverID, itemID string) error {
	err := q.RebuildLibraryItemRollup(ctx, sqlite.RebuildLibraryItemRollupParams{
		MediaServerID: serverID, ItemID: itemID,
	})
	return importStoreError("rebuild catalog item rollup", err)
}

func supersedeSQLiteUserData(
	ctx context.Context, q *sqlite.Queries, batch core.ImportBatch, record core.ImportedWatch,
) error {
	if batch.Source == core.ImportSourceJellyfinUserData {
		return nil
	}
	return q.DeleteJellyfinUserDataDuplicate(ctx, sqlite.DeleteJellyfinUserDataDuplicateParams{
		MediaServerID: batch.MediaServerID, MediaUserID: record.MediaUserID, ItemID: record.ItemID,
	})
}

func sqliteImportDuplicate(
	ctx context.Context, q *sqlite.Queries, batch core.ImportBatch, record core.ImportedWatch,
) (bool, error) {
	if batch.Source == core.ImportSourceJellyfinUserData {
		return q.FindAnyWatchForUserItem(ctx, sqlite.FindAnyWatchForUserItemParams{
			MediaServerID: batch.MediaServerID, MediaUserID: record.MediaUserID, ItemID: record.ItemID,
		})
	}
	return q.FindCollectedImportDuplicate(ctx, sqlite.FindCollectedImportDuplicateParams{
		MediaServerID: batch.MediaServerID, MediaUserID: record.MediaUserID, ItemID: record.ItemID,
		StartAfter:  formatSQLiteTime(record.StartedAt.Add(-batch.ResumeWindow)),
		StartBefore: formatSQLiteTime(record.StartedAt.Add(batch.ResumeWindow)),
	})
}

func sqliteCrossSourceDuplicate(
	ctx context.Context, q *sqlite.Queries, batch core.ImportBatch, record core.ImportedWatch,
) (bool, error) {
	var duplicate bool
	var err error
	switch batch.Source {
	case core.ImportSourceJellystat:
		if record.OriginRecordID == "" {
			return false, nil
		}
		duplicate, err = q.FindPlaybackReportingImportDuplicate(ctx,
			sqlite.FindPlaybackReportingImportDuplicateParams{
				MediaServerID: batch.MediaServerID, ImportRecordID: optionalStreamString(record.OriginRecordID),
			})
	case core.ImportSourcePlaybackReporting:
		duplicate, err = q.FindJellystatImportDuplicate(ctx, sqlite.FindJellystatImportDuplicateParams{
			MediaServerID: batch.MediaServerID, ImportOriginRecordID: optionalStreamString(record.RecordID),
			LegacyImportRecordID: optionalStreamString("plugin:" + record.RecordID),
		})
	case core.ImportSourceBloomExport, core.ImportSourceJellyfinUserData:
		return false, nil
	}
	return duplicate, importStoreError("find cross-source import duplicate", err)
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
		SeriesID: optionalStreamString(record.SeriesID), SeriesName: record.SeriesName, LibraryID: record.LibraryID, LibraryName: record.LibraryName,
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
		ImportRecordID:       sql.NullString{String: record.RecordID, Valid: true},
		ImportOriginRecordID: optionalStreamString(record.OriginRecordID),
	}, nil
}

func (s *sqliteImportStore) FinishImport(ctx context.Context, id, token string, state core.ImportState, lastError string, now time.Time) error {
	if !core.ValidID(id) || token == "" || !state.Terminal() || len(lastError) > core.MaxImportErrorBytes || now.IsZero() {
		return core.ErrInvalidArgument
	}
	err := withSQLiteWriteTransaction(ctx, s.pool, func(conn *sql.Conn) error {
		q := sqlite.New(conn)
		rows, err := q.FinishImport(ctx, sqlite.FinishImportParams{
			ID: id, Token: token, State: string(state), LastError: lastError, Now: sqliteNullableTime(&now),
		})
		if finishErr := importRowsError("finish import", rows, err, core.ErrImportLeaseLost); finishErr != nil {
			return finishErr
		}
		return sqliteDeleteJobUpload(ctx, q, id)
	})
	return importStoreError("finish import", err)
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
