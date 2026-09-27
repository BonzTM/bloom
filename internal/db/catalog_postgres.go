package db

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/db/postgres"
)

type postgresCatalogStore struct {
	pool *sql.DB
	q    *postgres.Queries
}

func newPostgresCatalogStore(pool *sql.DB) *postgresCatalogStore {
	return &postgresCatalogStore{pool: pool, q: postgres.New(pool)}
}

func (s *postgresCatalogStore) RequestLibrarySync(ctx context.Context, serverID string) (core.LibrarySync, error) {
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

func (s *postgresCatalogStore) ClaimLibrarySync(
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
	selected, err := q.SelectClaimableLibrarySync(ctx, postgres.SelectClaimableLibrarySyncParams{
		Now: nullableTime(&now), DueBefore: nullableTime(&dueBefore),
	})
	if err != nil {
		return sync, catalogStoreError("select library sync", importNotFound(err))
	}
	rows, err := q.ClaimLibrarySync(ctx, postgres.ClaimLibrarySyncParams{
		Token: lease.Token, ExpiresAt: nullableTime(&lease.ExpiresAt), Now: nullableTime(&now),
		MediaServerID: selected.MediaServerID, DueBefore: nullableTime(&dueBefore),
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

func (s *postgresCatalogStore) CommitLibrarySyncPage(
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
	if fenceErr := postgresFenceCatalog(ctx, q, sync); fenceErr != nil {
		return updated, fenceErr
	}
	for _, item := range items {
		if upsertErr := postgresUpsertCatalogItem(ctx, q, item); upsertErr != nil {
			return updated, upsertErr
		}
	}
	rows, err := q.CheckpointLibrarySync(ctx, postgres.CheckpointLibrarySyncParams{
		Cursor: cursor, SeenDelta: int64(len(items)), UpsertedDelta: int64(len(items)),
		ExpiresAt: nullableTime(sync.LeaseExpiresAt), MediaServerID: sync.MediaServerID, Token: sync.LeaseToken,
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

func (s *postgresCatalogStore) ListLibrarySyncMissingIDs(
	ctx context.Context, sync core.LibrarySync, after string, limit int,
) ([]string, error) {
	if err := validateMissingCatalogIDs(sync, after, limit); err != nil {
		return nil, err
	}
	if err := postgresFenceCatalog(ctx, s.q, sync); err != nil {
		return nil, err
	}
	ids, err := s.q.ListMissingLibraryItemIDs(ctx, postgres.ListMissingLibraryItemIDsParams{
		MediaServerID: sync.MediaServerID, StartedAt: core.NormalizeTime(*sync.StartedAt),
		AfterItemID: after, RowLimit: int64(limit),
	})
	return ids, catalogStoreError("list missing catalog items", err)
}

func (s *postgresCatalogStore) CommitLibrarySyncArchives(
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
	if err = postgresFenceCatalog(ctx, q, sync); err != nil {
		return updated, err
	}
	archived, err := archivePostgresCatalogIDs(ctx, q, sync, itemIDs, now)
	if err != nil {
		return updated, err
	}
	return s.checkpointPostgresArchives(ctx, tx, q, sync, archived)
}

func archivePostgresCatalogIDs(
	ctx context.Context, q *postgres.Queries, sync core.LibrarySync, itemIDs []string, now time.Time,
) (int64, error) {
	var archived int64
	for _, itemID := range itemIDs {
		rows, err := q.ArchiveLibraryItem(ctx, postgres.ArchiveLibraryItemParams{
			Now: core.NormalizeTime(now), MediaServerID: sync.MediaServerID,
			ItemID: itemID, StartedAt: core.NormalizeTime(*sync.StartedAt),
		})
		if err != nil {
			return 0, catalogStoreError("archive catalog item", err)
		}
		archived += rows
	}
	return archived, nil
}

func (s *postgresCatalogStore) checkpointPostgresArchives(
	ctx context.Context, tx *sql.Tx, q *postgres.Queries, sync core.LibrarySync, archived int64,
) (core.LibrarySync, error) {
	rows, err := q.CheckpointLibrarySyncArchives(ctx, postgres.CheckpointLibrarySyncArchivesParams{
		ArchivedDelta: archived, ExpiresAt: nullableTime(sync.LeaseExpiresAt),
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

func (s *postgresCatalogStore) FinishLibrarySync(
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
	if fenceErr := postgresFenceCatalog(ctx, q, sync); fenceErr != nil {
		return updated, fenceErr
	}
	if err = q.RebuildLibraryItemRollups(ctx, sync.MediaServerID); err != nil {
		return updated, catalogStoreError("rebuild catalog rollups", err)
	}
	if _, err = q.DeleteArchivedLibraryItemGenres(ctx, sync.MediaServerID); err != nil {
		return updated, catalogStoreError("delete archived catalog genres", err)
	}
	rows, err := q.CompleteLibrarySync(ctx, postgres.CompleteLibrarySyncParams{
		Now: nullableTime(&now), MediaServerID: sync.MediaServerID, Token: sync.LeaseToken,
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

func (s *postgresCatalogStore) FailLibrarySync(ctx context.Context, sync core.LibrarySync, message string, now time.Time) error {
	if !sync.Valid() || sync.State != core.LibrarySyncRunning || now.IsZero() || len(message) > core.MaxCatalogErrorBytes {
		return core.ErrInvalidArgument
	}
	rows, err := s.q.FailLibrarySync(ctx, postgres.FailLibrarySyncParams{
		LastError: message, Now: nullableTime(&now), MediaServerID: sync.MediaServerID, Token: sync.LeaseToken,
	})
	if rows != 1 || err != nil {
		return catalogStoreError("fail library sync", errors.Join(err, core.ErrLibrarySyncLeaseLost))
	}
	return nil
}

func postgresFenceCatalog(ctx context.Context, q *postgres.Queries, sync core.LibrarySync) error {
	_, err := q.FenceLibrarySync(ctx, postgres.FenceLibrarySyncParams{
		MediaServerID: sync.MediaServerID, Token: sync.LeaseToken,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return core.ErrLibrarySyncLeaseLost
	}
	return catalogStoreError("fence library sync", err)
}

func postgresUpsertCatalogItem(ctx context.Context, q *postgres.Queries, item core.LibraryItem) error {
	genres, err := encodeCatalogGenres(item.Genres)
	if err != nil {
		return err
	}
	params := postgres.UpsertLibraryItemParams{
		MediaServerID: item.MediaServerID, ItemID: item.ItemID, LibraryID: item.LibraryID,
		ParentID: item.ParentID, ItemType: item.ItemType, Name: item.Name,
		SeriesID: item.SeriesID, SeriesName: item.SeriesName, SeasonID: item.SeasonID,
		SeasonNumber: postgresNullableInt32(item.SeasonNumber), IndexNumber: postgresNullableInt32(item.IndexNumber),
		RuntimeMs: nullableDurationMilliseconds(item.Runtime), PremiereDate: nullableTime(item.PremiereDate),
		ProductionYear: postgresNullableInt32(item.ProductionYear), CommunityRating: nullableCatalogFloat(item.CommunityRating),
		Genres: genres, PrimaryImageTag: item.PrimaryImageTag, DateCreated: nullableTime(item.DateCreated),
		Archived: item.Archived, FirstSeenAt: core.NormalizeTime(item.FirstSeenAt),
		LastSeenAt: core.NormalizeTime(item.LastSeenAt), UpdatedAt: core.NormalizeTime(item.UpdatedAt),
	}
	if _, upsertErr := q.UpsertLibraryItem(ctx, params); upsertErr != nil {
		return catalogStoreError("upsert catalog item", upsertErr)
	}
	if genresErr := replacePostgresCatalogGenres(ctx, q, item); genresErr != nil {
		return genresErr
	}
	_, err = q.BackfillWatchCatalogItem(ctx, postgres.BackfillWatchCatalogItemParams{
		ItemName: item.Name, SeriesID: optionalStreamString(item.SeriesID), SeriesName: item.SeriesName,
		LibraryID: item.LibraryID, MediaServerID: item.MediaServerID, ItemID: item.ItemID,
	})
	return catalogStoreError("backfill watch catalog fields", err)
}

func replacePostgresCatalogGenres(ctx context.Context, q *postgres.Queries, item core.LibraryItem) error {
	params := postgres.DeleteLibraryItemGenresParams{MediaServerID: item.MediaServerID, ItemID: item.ItemID}
	if err := q.DeleteLibraryItemGenres(ctx, params); err != nil {
		return catalogStoreError("delete catalog item genres", err)
	}
	for _, genre := range item.Genres {
		if err := q.InsertLibraryItemGenre(ctx, postgres.InsertLibraryItemGenreParams{
			MediaServerID: item.MediaServerID, ItemID: item.ItemID, Genre: genre,
		}); err != nil {
			return catalogStoreError("insert catalog item genre", err)
		}
	}
	return nil
}

func (s *postgresCatalogStore) getSync(ctx context.Context, q *postgres.Queries, serverID string) (core.LibrarySync, error) {
	row, err := q.GetLibrarySync(ctx, serverID)
	if err != nil {
		return core.LibrarySync{}, catalogStoreError("get library sync", importNotFound(err))
	}
	sync := core.LibrarySync{
		MediaServerID: row.MediaServerID, State: core.LibrarySyncState(row.State), Cursor: row.Cursor,
		Seen: row.SeenCount, Upserted: row.UpsertedCount, Archived: row.ArchivedCount,
		LastError: row.LastError, LeaseToken: row.LeaseToken, LeaseExpiresAt: timeFromNull(row.LeaseExpiresAt),
		StartedAt: timeFromNull(row.StartedAt), FinishedAt: timeFromNull(row.FinishedAt),
	}
	if !sync.Valid() {
		return core.LibrarySync{}, errors.New("persisted library sync is invalid")
	}
	return sync, nil
}

func postgresNullableInt32(value *int32) sql.NullInt32 {
	if value == nil {
		return sql.NullInt32{}
	}
	return sql.NullInt32{Int32: *value, Valid: true}
}

var _ core.LibraryCatalogStore = (*postgresCatalogStore)(nil)
