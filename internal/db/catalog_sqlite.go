package db

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/db/sqlite"
)

type sqliteCatalogStore struct {
	pool *sql.DB
	q    *sqlite.Queries
}

func newSQLiteCatalogStore(pool *sql.DB) *sqliteCatalogStore {
	return &sqliteCatalogStore{pool: pool, q: sqlite.New(pool)}
}

func (s *sqliteCatalogStore) RequestLibrarySync(ctx context.Context, serverID string) (core.LibrarySync, error) {
	if !core.ValidID(serverID) {
		return core.LibrarySync{}, core.ErrInvalidArgument
	}
	rows, err := s.q.RequestLibrarySync(ctx, serverID)
	if err != nil {
		return core.LibrarySync{}, catalogStoreError("request library sync", err)
	}
	if rows == 0 {
		return core.LibrarySync{}, core.ErrLibrarySyncInProgress
	}
	return s.getSync(ctx, s.q, serverID)
}

func (s *sqliteCatalogStore) ClaimLibrarySync(
	ctx context.Context, lease core.LibrarySyncLease, now, dueBefore time.Time,
) (sync core.LibrarySync, result error) {
	if err := validateCatalogClaim(lease, now, dueBefore); err != nil {
		return sync, err
	}
	tx, err := s.pool.BeginTx(ctx, nil)
	if err != nil {
		return sync, catalogStoreError("begin library sync claim", err)
	}
	defer rollbackCatalog(tx, &result)
	q := s.q.WithTx(tx)
	if ensureErr := q.EnsureLibrarySyncRows(ctx); ensureErr != nil {
		return sync, catalogStoreError("ensure library sync rows", ensureErr)
	}
	selected, err := q.SelectClaimableLibrarySync(ctx, sqlite.SelectClaimableLibrarySyncParams{
		Now: sqliteNullableTime(&now), DueBefore: sqliteNullableTime(&dueBefore),
	})
	if err != nil {
		return sync, catalogStoreError("select library sync", importNotFound(err))
	}
	rows, err := q.ClaimLibrarySync(ctx, sqlite.ClaimLibrarySyncParams{
		Token: lease.Token, ExpiresAt: sqliteNullableTime(&lease.ExpiresAt), Now: sqliteNullableTime(&now),
		MediaServerID: selected.MediaServerID, DueBefore: sqliteNullableTime(&dueBefore),
	})
	if rows != 1 || err != nil {
		return sync, catalogStoreError("claim library sync", errors.Join(err, core.ErrLibrarySyncLeaseLost))
	}
	sync, err = s.getSync(ctx, q, selected.MediaServerID)
	if err != nil {
		return sync, err
	}
	if commitErr := tx.Commit(); commitErr != nil {
		return sync, catalogStoreError("commit library sync claim", commitErr)
	}
	return sync, nil
}

func (s *sqliteCatalogStore) CommitLibrarySyncPage(
	ctx context.Context, sync core.LibrarySync, items []core.LibraryItem, cursor string, now time.Time,
) (updated core.LibrarySync, result error) {
	if err := validateCatalogCommit(sync, items, cursor, now); err != nil {
		return updated, err
	}
	tx, err := s.pool.BeginTx(ctx, nil)
	if err != nil {
		return updated, catalogStoreError("begin catalog page", err)
	}
	defer rollbackCatalog(tx, &result)
	q := s.q.WithTx(tx)
	if fenceErr := sqliteFenceCatalog(ctx, q, sync); fenceErr != nil {
		return updated, fenceErr
	}
	for _, item := range items {
		if upsertErr := sqliteUpsertCatalogItem(ctx, q, item); upsertErr != nil {
			return updated, upsertErr
		}
	}
	rows, err := q.CheckpointLibrarySync(ctx, sqlite.CheckpointLibrarySyncParams{
		Cursor: cursor, SeenDelta: int64(len(items)), UpsertedDelta: int64(len(items)),
		ExpiresAt: sqliteNullableTime(sync.LeaseExpiresAt), MediaServerID: sync.MediaServerID, Token: sync.LeaseToken,
	})
	if rows != 1 || err != nil {
		return updated, catalogStoreError("checkpoint library sync", errors.Join(err, core.ErrLibrarySyncLeaseLost))
	}
	updated, err = s.getSync(ctx, q, sync.MediaServerID)
	if err != nil {
		return updated, err
	}
	if commitErr := tx.Commit(); commitErr != nil {
		return updated, catalogStoreError("commit catalog page", commitErr)
	}
	return updated, nil
}

func (s *sqliteCatalogStore) ListLibrarySyncMissingIDs(
	ctx context.Context, sync core.LibrarySync, after string, limit int,
) ([]string, error) {
	if err := validateMissingCatalogIDs(sync, after, limit); err != nil {
		return nil, err
	}
	if err := sqliteFenceCatalog(ctx, s.q, sync); err != nil {
		return nil, err
	}
	ids, err := s.q.ListMissingLibraryItemIDs(ctx, sqlite.ListMissingLibraryItemIDsParams{
		MediaServerID: sync.MediaServerID, StartedAt: formatSQLiteTime(*sync.StartedAt),
		AfterItemID: after, RowLimit: int64(limit),
	})
	return ids, catalogStoreError("list missing catalog items", err)
}

func (s *sqliteCatalogStore) CommitLibrarySyncArchives(
	ctx context.Context, sync core.LibrarySync, itemIDs []string, now time.Time,
) (updated core.LibrarySync, result error) {
	if err := validateCatalogArchive(sync, itemIDs, now); err != nil {
		return updated, err
	}
	tx, err := s.pool.BeginTx(ctx, nil)
	if err != nil {
		return updated, catalogStoreError("begin catalog archival", err)
	}
	defer rollbackCatalog(tx, &result)
	q := s.q.WithTx(tx)
	if err = sqliteFenceCatalog(ctx, q, sync); err != nil {
		return updated, err
	}
	archived, err := archiveSQLiteCatalogIDs(ctx, q, sync, itemIDs, now)
	if err != nil {
		return updated, err
	}
	return s.checkpointSQLiteArchives(ctx, tx, q, sync, archived)
}

func archiveSQLiteCatalogIDs(
	ctx context.Context, q *sqlite.Queries, sync core.LibrarySync, itemIDs []string, now time.Time,
) (int64, error) {
	var archived int64
	for _, itemID := range itemIDs {
		rows, err := q.ArchiveLibraryItem(ctx, sqlite.ArchiveLibraryItemParams{
			Now: formatSQLiteTime(now), MediaServerID: sync.MediaServerID,
			ItemID: itemID, StartedAt: formatSQLiteTime(*sync.StartedAt),
		})
		if err != nil {
			return 0, catalogStoreError("archive catalog item", err)
		}
		archived += rows
	}
	return archived, nil
}

func (s *sqliteCatalogStore) checkpointSQLiteArchives(
	ctx context.Context, tx *sql.Tx, q *sqlite.Queries, sync core.LibrarySync, archived int64,
) (core.LibrarySync, error) {
	rows, err := q.CheckpointLibrarySyncArchives(ctx, sqlite.CheckpointLibrarySyncArchivesParams{
		ArchivedDelta: archived, ExpiresAt: sqliteNullableTime(sync.LeaseExpiresAt),
		MediaServerID: sync.MediaServerID, Token: sync.LeaseToken,
	})
	if rows != 1 || err != nil {
		return core.LibrarySync{}, catalogStoreError("checkpoint catalog archival", errors.Join(err, core.ErrLibrarySyncLeaseLost))
	}
	updated, err := s.getSync(ctx, q, sync.MediaServerID)
	if err == nil {
		err = tx.Commit()
	}
	return updated, catalogStoreError("commit catalog archival", err)
}

func (s *sqliteCatalogStore) FinishLibrarySync(
	ctx context.Context, sync core.LibrarySync, now time.Time,
) (updated core.LibrarySync, result error) {
	if !sync.Valid() || sync.StartedAt == nil || now.IsZero() {
		return updated, core.ErrInvalidArgument
	}
	tx, err := s.pool.BeginTx(ctx, nil)
	if err != nil {
		return updated, catalogStoreError("begin catalog completion", err)
	}
	defer rollbackCatalog(tx, &result)
	q := s.q.WithTx(tx)
	if fenceErr := sqliteFenceCatalog(ctx, q, sync); fenceErr != nil {
		return updated, fenceErr
	}
	if err = q.RebuildLibraryItemRollups(ctx, sync.MediaServerID); err != nil {
		return updated, catalogStoreError("rebuild catalog rollups", err)
	}
	if _, err = q.DeleteArchivedLibraryItemGenres(ctx, sync.MediaServerID); err != nil {
		return updated, catalogStoreError("delete archived catalog genres", err)
	}
	rows, err := q.CompleteLibrarySync(ctx, sqlite.CompleteLibrarySyncParams{
		Now: sqliteNullableTime(&now), MediaServerID: sync.MediaServerID, Token: sync.LeaseToken,
	})
	if rows != 1 || err != nil {
		return updated, catalogStoreError("complete library sync", errors.Join(err, core.ErrLibrarySyncLeaseLost))
	}
	updated, err = s.getSync(ctx, q, sync.MediaServerID)
	if err == nil {
		err = tx.Commit()
	}
	return updated, catalogStoreError("finish library sync", err)
}

func (s *sqliteCatalogStore) FailLibrarySync(ctx context.Context, sync core.LibrarySync, message string, now time.Time) error {
	if !sync.Valid() || sync.State != core.LibrarySyncRunning || now.IsZero() || len(message) > core.MaxCatalogErrorBytes {
		return core.ErrInvalidArgument
	}
	rows, err := s.q.FailLibrarySync(ctx, sqlite.FailLibrarySyncParams{
		LastError: message, Now: sqliteNullableTime(&now), MediaServerID: sync.MediaServerID, Token: sync.LeaseToken,
	})
	if rows != 1 || err != nil {
		return catalogStoreError("fail library sync", errors.Join(err, core.ErrLibrarySyncLeaseLost))
	}
	return nil
}

func sqliteFenceCatalog(ctx context.Context, q *sqlite.Queries, sync core.LibrarySync) error {
	_, err := q.FenceLibrarySync(ctx, sqlite.FenceLibrarySyncParams{
		MediaServerID: sync.MediaServerID, Token: sync.LeaseToken,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return core.ErrLibrarySyncLeaseLost
	}
	return catalogStoreError("fence library sync", err)
}

func sqliteUpsertCatalogItem(ctx context.Context, q *sqlite.Queries, item core.LibraryItem) error {
	genres, err := encodeCatalogGenres(item.Genres)
	if err != nil {
		return err
	}
	params := sqlite.UpsertLibraryItemParams{
		MediaServerID: item.MediaServerID, ItemID: item.ItemID, LibraryID: item.LibraryID,
		ParentID: item.ParentID, ItemType: item.ItemType, Name: item.Name,
		SeriesID: item.SeriesID, SeriesName: item.SeriesName, SeasonID: item.SeasonID,
		SeasonNumber: nullableCatalogInt32(item.SeasonNumber), IndexNumber: nullableCatalogInt32(item.IndexNumber),
		RuntimeMs: nullableDurationMilliseconds(item.Runtime), PremiereDate: sqliteNullableTime(item.PremiereDate),
		ProductionYear: nullableCatalogInt32(item.ProductionYear), CommunityRating: nullableCatalogFloat(item.CommunityRating),
		Genres: genres, PrimaryImageTag: item.PrimaryImageTag, DateCreated: sqliteNullableTime(item.DateCreated),
		Archived: boolToInt64(item.Archived), FirstSeenAt: formatSQLiteTime(item.FirstSeenAt),
		LastSeenAt: formatSQLiteTime(item.LastSeenAt), UpdatedAt: formatSQLiteTime(item.UpdatedAt),
	}
	if _, upsertErr := q.UpsertLibraryItem(ctx, params); upsertErr != nil {
		return catalogStoreError("upsert catalog item", upsertErr)
	}
	if genresErr := replaceSQLiteCatalogGenres(ctx, q, item); genresErr != nil {
		return genresErr
	}
	_, err = q.BackfillWatchCatalogItem(ctx, sqlite.BackfillWatchCatalogItemParams{
		ItemName: item.Name, SeriesID: optionalStreamString(item.SeriesID), SeriesName: item.SeriesName,
		LibraryID: item.LibraryID, MediaServerID: item.MediaServerID, ItemID: item.ItemID,
	})
	return catalogStoreError("backfill watch catalog fields", err)
}

func replaceSQLiteCatalogGenres(ctx context.Context, q *sqlite.Queries, item core.LibraryItem) error {
	params := sqlite.DeleteLibraryItemGenresParams{MediaServerID: item.MediaServerID, ItemID: item.ItemID}
	if err := q.DeleteLibraryItemGenres(ctx, params); err != nil {
		return catalogStoreError("delete catalog item genres", err)
	}
	for _, genre := range item.Genres {
		if err := q.InsertLibraryItemGenre(ctx, sqlite.InsertLibraryItemGenreParams{
			MediaServerID: item.MediaServerID, ItemID: item.ItemID, Genre: genre,
		}); err != nil {
			return catalogStoreError("insert catalog item genre", err)
		}
	}
	return nil
}

func (s *sqliteCatalogStore) getSync(ctx context.Context, q *sqlite.Queries, serverID string) (core.LibrarySync, error) {
	row, err := q.GetLibrarySync(ctx, serverID)
	if err != nil {
		return core.LibrarySync{}, catalogStoreError("get library sync", importNotFound(err))
	}
	return sqliteCatalogSync(row)
}

func sqliteCatalogSync(row sqlite.LibrarySync) (core.LibrarySync, error) {
	lease, err := sqliteOptionalTime(row.LeaseExpiresAt)
	started, startedErr := sqliteOptionalTime(row.StartedAt)
	finished, finishedErr := sqliteOptionalTime(row.FinishedAt)
	sync := core.LibrarySync{
		MediaServerID: row.MediaServerID, State: core.LibrarySyncState(row.State), Cursor: row.Cursor,
		Seen: row.SeenCount, Upserted: row.UpsertedCount, Archived: row.ArchivedCount,
		LastError: row.LastError, LeaseToken: row.LeaseToken, LeaseExpiresAt: lease,
		StartedAt: started, FinishedAt: finished,
	}
	if err != nil || startedErr != nil || finishedErr != nil || !sync.Valid() {
		return core.LibrarySync{}, catalogStoreError("map library sync", errors.Join(err, startedErr, finishedErr))
	}
	return sync, nil
}

var _ core.LibraryCatalogStore = (*sqliteCatalogStore)(nil)
