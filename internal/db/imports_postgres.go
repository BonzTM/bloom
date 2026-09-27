package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/db/postgres"
)

type postgresImportStore struct {
	pool *sql.DB
	q    *postgres.Queries
}

func newPostgresImportStore(pool *sql.DB) *postgresImportStore {
	return &postgresImportStore{pool: pool, q: postgres.New(pool)}
}

func (s *postgresImportStore) CreateImport(ctx context.Context, job core.ImportJob) error {
	if !job.Valid() || job.State != core.ImportPending {
		return fmt.Errorf("create import: %w", core.ErrInvalidArgument)
	}
	err := s.q.CreateImport(ctx, postgresCreateImportParams(job))
	if isPostgresUnique(err) {
		return fmt.Errorf("create import: %w", core.ErrImportInProgress)
	}
	return importStoreError("create import", err)
}

func (s *postgresImportStore) GetImport(ctx context.Context, id string) (core.ImportJob, error) {
	if !core.ValidID(id) {
		return core.ImportJob{}, core.ErrInvalidArgument
	}
	row, err := s.q.GetImport(ctx, id)
	if err != nil {
		return core.ImportJob{}, importStoreError("get import", importNotFound(err))
	}
	job, err := postgresImport(row)
	if err != nil {
		return core.ImportJob{}, importStoreError("map import", err)
	}
	return job, nil
}

func (s *postgresImportStore) ListImports(ctx context.Context, query core.ImportListQuery) ([]core.ImportJob, error) {
	if err := validateImportList(query); err != nil {
		return nil, err
	}
	before, id := importListCursor(query)
	rows, err := s.q.ListImports(ctx, postgres.ListImportsParams{
		MediaServerID: query.MediaServerID, BeforeCreatedAt: before,
		BeforeID: id, PageSize: int32(query.PageSize), //nolint:gosec // bounded above.
	})
	if err != nil {
		return nil, importStoreError("list imports", err)
	}
	jobs := make([]core.ImportJob, 0, len(rows))
	for _, row := range rows {
		job, mapErr := postgresImport(row)
		if mapErr != nil {
			return nil, importStoreError("map import", mapErr)
		}
		jobs = append(jobs, job)
	}
	return jobs, nil
}

func (s *postgresImportStore) CancelImport(
	ctx context.Context, id string, now time.Time,
) (job core.ImportJob, result error) {
	if !core.ValidID(id) || now.IsZero() {
		return core.ImportJob{}, core.ErrInvalidArgument
	}
	tx, err := s.pool.BeginTx(ctx, nil)
	if err != nil {
		return core.ImportJob{}, importStoreError("begin import cancellation", err)
	}
	defer rollbackImport(tx, &result)
	q := s.q.WithTx(tx)
	rows, err := q.CancelImport(ctx, postgres.CancelImportParams{ID: id, Now: nullableTime(&now)})
	if err != nil || rows == 0 {
		return core.ImportJob{}, importStoreError("cancel import", postgresCancelError(ctx, q, id, rows, err))
	}
	if cleanupErr := postgresDeleteJobUpload(ctx, q, id); cleanupErr != nil {
		return core.ImportJob{}, importStoreError("cancel import", cleanupErr)
	}
	row, err := q.GetImport(ctx, id)
	if err != nil {
		return core.ImportJob{}, importStoreError("read cancelled import", err)
	}
	if err := tx.Commit(); err != nil {
		return core.ImportJob{}, importStoreError("commit import cancellation", err)
	}
	return postgresImport(row)
}

func postgresCancelError(ctx context.Context, q *postgres.Queries, id string, rows int64, queryErr error) error {
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

func (s *postgresImportStore) ClaimImport(ctx context.Context, lease core.ImportLease, now time.Time) (job core.ImportJob, result error) {
	if lease.Token == "" || !lease.ExpiresAt.After(now) || now.IsZero() {
		return job, core.ErrInvalidArgument
	}
	tx, err := s.pool.BeginTx(ctx, nil)
	if err != nil {
		return job, importStoreError("begin import claim", err)
	}
	defer rollbackImport(tx, &result)
	q := s.q.WithTx(tx)
	row, err := q.SelectClaimableImport(ctx, nullableTime(&now))
	if err != nil {
		return job, importStoreError("select import claim", importNotFound(err))
	}
	rows, err := q.ClaimImport(ctx, postgres.ClaimImportParams{
		ID: row.ID, Token: lease.Token, ExpiresAt: nullableTime(&lease.ExpiresAt), Now: core.NormalizeTime(now),
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
	return postgresImport(claimed)
}

func (s *postgresImportStore) RenewImportLease(ctx context.Context, id string, lease core.ImportLease, now time.Time) error {
	rows, err := s.q.RenewImportLease(ctx, postgres.RenewImportLeaseParams{
		ID: id, Token: lease.Token, ExpiresAt: nullableTime(&lease.ExpiresAt), Now: core.NormalizeTime(now),
	})
	if err != nil {
		return importStoreError("renew import lease", err)
	}
	if rows != 1 {
		return core.ErrImportLeaseLost
	}
	return nil
}

func (s *postgresImportStore) CommitImportBatch(ctx context.Context, batch core.ImportBatch) (out core.ImportBatchResult, result error) {
	if err := validateImportBatch(batch); err != nil {
		return out, err
	}
	tx, err := s.pool.BeginTx(ctx, nil)
	if err != nil {
		return out, importStoreError("begin import batch", err)
	}
	defer rollbackImport(tx, &result)
	q := s.q.WithTx(tx)
	if fenceErr := fencePostgresImport(ctx, q, batch); fenceErr != nil {
		return out, fenceErr
	}
	if lockErr := lockPostgresImportKeys(ctx, q, batch); lockErr != nil {
		return out, lockErr
	}
	imported, duplicate, err := insertPostgresImportRecords(ctx, q, batch)
	if err != nil {
		return out, err
	}
	rows, err := q.CheckpointImport(ctx, postgres.CheckpointImportParams{
		ID: batch.JobID, Token: batch.LeaseToken, Cursor: batch.Cursor,
		ReadDelta: int64(len(batch.Records)) + batch.Skipped, ImportedDelta: imported,
		SkippedDelta: batch.Skipped, DuplicateDelta: duplicate,
		ExpiresAt: nullableTime(&batch.LeaseExpiresAt), Now: core.NormalizeTime(batch.Now),
	})
	if checkpointErr := importRowsError("checkpoint import", rows, err, core.ErrImportLeaseLost); checkpointErr != nil {
		return out, checkpointErr
	}
	checkpoint, err := q.GetImport(ctx, batch.JobID)
	if err != nil {
		return out, importStoreError("read import checkpoint", err)
	}
	if commitErr := tx.Commit(); commitErr != nil {
		return out, importStoreError("commit import batch", commitErr)
	}
	outJob, err := postgresImport(checkpoint)
	if err != nil {
		return out, importStoreError("map import checkpoint", err)
	}
	return importCounters(outJob), nil
}

func fencePostgresImport(ctx context.Context, q *postgres.Queries, batch core.ImportBatch) error {
	_, err := q.FenceImportBatch(ctx, postgres.FenceImportBatchParams{ID: batch.JobID, Token: batch.LeaseToken})
	if errors.Is(err, sql.ErrNoRows) {
		return core.ErrImportLeaseLost
	}
	return importStoreError("fence import batch", err)
}

func lockPostgresImportKeys(ctx context.Context, q *postgres.Queries, batch core.ImportBatch) error {
	keys := make([]string, 0, len(batch.Records))
	for _, record := range batch.Records {
		keys = append(keys, watchDedupKey(batch.MediaServerID, record.MediaUserID, record.ItemID))
	}
	slices.Sort(keys)
	keys = slices.Compact(keys)
	for _, key := range keys {
		if err := q.LockWatchDedup(ctx, key); err != nil {
			return importStoreError("lock watch deduplication key", err)
		}
	}
	return nil
}

func insertPostgresImportRecords(ctx context.Context, q *postgres.Queries, batch core.ImportBatch) (int64, int64, error) {
	var imported, duplicate int64
	for _, record := range batch.Records {
		if err := supersedePostgresUserData(ctx, q, batch, record); err != nil {
			return 0, 0, importStoreError("supersede Jellyfin user-data watch", err)
		}
		dupe, err := postgresImportDuplicate(ctx, q, batch, record)
		if err != nil {
			return 0, 0, importStoreError("find collected import duplicate", err)
		}
		if dupe {
			duplicate++
			if rebuildErr := rebuildPostgresImportRollup(ctx, q, batch.MediaServerID, record.ItemID); rebuildErr != nil {
				return 0, 0, rebuildErr
			}
			continue
		}
		crossDuplicate, err := postgresCrossSourceDuplicate(ctx, q, batch, record.RecordID)
		if err != nil {
			return 0, 0, err
		}
		if crossDuplicate {
			duplicate++
			continue
		}
		id, err := core.NewID()
		if err != nil {
			return 0, 0, importStoreError("create imported watch id", err)
		}
		params, err := postgresImportedWatchParams(id, batch, record)
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
		if err := rebuildPostgresImportRollup(ctx, q, batch.MediaServerID, record.ItemID); err != nil {
			return 0, 0, err
		}
	}
	return imported, duplicate, nil
}

func rebuildPostgresImportRollup(ctx context.Context, q *postgres.Queries, serverID, itemID string) error {
	err := q.RebuildLibraryItemRollup(ctx, postgres.RebuildLibraryItemRollupParams{
		MediaServerID: serverID, ItemID: itemID,
	})
	return importStoreError("rebuild catalog item rollup", err)
}

func supersedePostgresUserData(
	ctx context.Context, q *postgres.Queries, batch core.ImportBatch, record core.ImportedWatch,
) error {
	if batch.Source == core.ImportSourceJellyfinUserData {
		return nil
	}
	return q.DeleteJellyfinUserDataDuplicate(ctx, postgres.DeleteJellyfinUserDataDuplicateParams{
		MediaServerID: batch.MediaServerID, MediaUserID: record.MediaUserID, ItemID: record.ItemID,
	})
}

func postgresImportDuplicate(
	ctx context.Context, q *postgres.Queries, batch core.ImportBatch, record core.ImportedWatch,
) (bool, error) {
	if batch.Source == core.ImportSourceJellyfinUserData {
		return q.FindAnyWatchForUserItem(ctx, postgres.FindAnyWatchForUserItemParams{
			MediaServerID: batch.MediaServerID, MediaUserID: record.MediaUserID, ItemID: record.ItemID,
		})
	}
	return q.FindCollectedImportDuplicate(ctx, postgres.FindCollectedImportDuplicateParams{
		MediaServerID: batch.MediaServerID, MediaUserID: record.MediaUserID, ItemID: record.ItemID,
		StartAfter: record.StartedAt.Add(-batch.ResumeWindow), StartBefore: record.StartedAt.Add(batch.ResumeWindow),
	})
}

func postgresCrossSourceDuplicate(
	ctx context.Context, q *postgres.Queries, batch core.ImportBatch, recordID string,
) (bool, error) {
	source, alternateID, ok := crossSourceImportRecord(batch.Source, recordID)
	if !ok {
		return false, nil
	}
	duplicate, err := q.FindCrossSourceImportDuplicate(ctx, postgres.FindCrossSourceImportDuplicateParams{
		MediaServerID:  batch.MediaServerID,
		ImportSource:   sql.NullString{String: string(source), Valid: true},
		ImportRecordID: sql.NullString{String: alternateID, Valid: true},
	})
	return duplicate, importStoreError("find cross-source import duplicate", err)
}

func postgresImportedWatchParams(
	id string, batch core.ImportBatch, record core.ImportedWatch,
) (postgres.InsertImportedWatchParams, error) {
	stream, err := encodeStreamDetails(record.Stream)
	if err != nil {
		return postgres.InsertImportedWatchParams{}, err
	}
	return postgres.InsertImportedWatchParams{
		ID: id, MediaServerID: batch.MediaServerID, MediaUserID: record.MediaUserID,
		Username: record.Username, DeviceID: record.DeviceID, DeviceName: record.DeviceName, Client: record.Client,
		ItemID: record.ItemID, ItemName: record.ItemName, ItemType: record.ItemType,
		SeriesID: optionalStreamString(record.SeriesID), SeriesName: record.SeriesName, LibraryID: record.LibraryID, LibraryName: record.LibraryName,
		SeasonNumber: nullableInt32(record.SeasonNumber), EpisodeNumber: nullableInt32(record.EpisodeNumber),
		PlayMethod: string(record.PlayMethod), StartedAt: core.NormalizeTime(record.StartedAt),
		EndedAt: importEndedAt(record), ActiveSeconds: int64(record.Duration / time.Second),
		LastPositionMs: int64(record.LastPosition / time.Millisecond), RuntimeMs: nullableDurationMilliseconds(record.Runtime),
		Now:             core.NormalizeTime(batch.Now),
		StreamContainer: stream.container, StreamVideoCodec: stream.videoCodec,
		StreamAudioCodec: stream.audioCodec, StreamBitrate: stream.bitrate,
		StreamWidth: postgresNullInt32(stream.width), StreamHeight: postgresNullInt32(stream.height),
		StreamFramerateHundredths: postgresNullInt32(stream.framerate),
		StreamAudioChannels:       postgresNullInt32(stream.audioChannels), StreamIsVideoDirect: stream.videoDirect,
		StreamIsAudioDirect: stream.audioDirect, StreamTranscodeReasons: stream.reasons,
		ImportSource:   sql.NullString{String: string(batch.Source), Valid: true},
		ImportRecordID: sql.NullString{String: record.RecordID, Valid: true},
	}, nil
}

func (s *postgresImportStore) FinishImport(
	ctx context.Context, id, token string, state core.ImportState, lastError string, now time.Time,
) (result error) {
	if !core.ValidID(id) || token == "" || !state.Terminal() || len(lastError) > core.MaxImportErrorBytes || now.IsZero() {
		return core.ErrInvalidArgument
	}
	tx, err := s.pool.BeginTx(ctx, nil)
	if err != nil {
		return importStoreError("begin import finish", err)
	}
	defer rollbackImport(tx, &result)
	q := s.q.WithTx(tx)
	rows, err := q.FinishImport(ctx, postgres.FinishImportParams{
		ID: id, Token: token, State: string(state), LastError: lastError, Now: nullableTime(&now),
	})
	if finishErr := importRowsError("finish import", rows, err, core.ErrImportLeaseLost); finishErr != nil {
		return finishErr
	}
	if err := postgresDeleteJobUpload(ctx, q, id); err != nil {
		return importStoreError("finish import", err)
	}
	return importStoreError("commit import finish", tx.Commit())
}

func postgresCreateImportParams(job core.ImportJob) postgres.CreateImportParams {
	return postgres.CreateImportParams{
		ID: job.ID, MediaServerID: job.MediaServerID, Source: string(job.Source), State: string(job.State),
		Cursor: job.Cursor, ReadCount: job.Read, ImportedCount: job.Imported,
		SkippedCount: job.Skipped, DuplicateCount: job.Duplicate, LastError: job.LastError,
		LeaseToken: job.LeaseToken, LeaseExpiresAt: nullableTime(job.LeaseExpiresAt), RequestedBy: job.RequestedBy,
		CreatedAt: core.NormalizeTime(job.CreatedAt), StartedAt: nullableTime(job.StartedAt),
		FinishedAt: nullableTime(job.FinishedAt), UpdatedAt: core.NormalizeTime(job.UpdatedAt),
	}
}

func postgresImport(row postgres.Import) (core.ImportJob, error) {
	job := core.ImportJob{
		ID: row.ID, MediaServerID: row.MediaServerID, Source: core.ImportSource(row.Source), State: core.ImportState(row.State),
		Cursor: row.Cursor, Read: row.ReadCount, Imported: row.ImportedCount, Skipped: row.SkippedCount,
		Duplicate: row.DuplicateCount, LastError: row.LastError, LeaseToken: row.LeaseToken,
		LeaseExpiresAt: timeFromNull(row.LeaseExpiresAt), RequestedBy: row.RequestedBy,
		CreatedAt: core.NormalizeTime(row.CreatedAt), StartedAt: timeFromNull(row.StartedAt),
		FinishedAt: timeFromNull(row.FinishedAt), UpdatedAt: core.NormalizeTime(row.UpdatedAt),
	}
	if !job.Valid() {
		return core.ImportJob{}, errors.New("persisted import is invalid")
	}
	return job, nil
}
