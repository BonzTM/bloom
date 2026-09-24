package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/db/postgres"
)

type postgresPlaybackStore struct {
	pool *sql.DB
	q    *postgres.Queries
}

var (
	_ core.PlaybackStore        = (*postgresPlaybackStore)(nil)
	_ core.PlaybackLibraryStore = (*postgresPlaybackStore)(nil)
)

func newPostgresPlaybackStore(pool *sql.DB) *postgresPlaybackStore {
	return &postgresPlaybackStore{pool: pool, q: postgres.New(pool)}
}

func (s *postgresPlaybackStore) LoadOpenWatches(
	ctx context.Context,
	mediaServerID string,
) ([]core.PlaybackWatch, error) {
	if !core.ValidID(mediaServerID) {
		return nil, fmt.Errorf("load open playback watches: %w", core.ErrInvalidArgument)
	}
	rows, err := s.q.ListOpenPlaybackWatches(ctx, mediaServerID)
	if err != nil {
		return nil, playbackStoreError("load open playback watches", err)
	}
	watches := make([]core.PlaybackWatch, 0, len(rows))
	for _, row := range rows {
		watches = append(watches, postgresOpenWatch(row))
	}
	return watches, nil
}

func (s *postgresPlaybackStore) ListUnresolvedWatchItemIDs(
	ctx context.Context, mediaServerID string, limit int,
) ([]string, error) {
	if !core.ValidID(mediaServerID) || limit < 1 || limit > core.MaxPlaybackLibraryBackfillItems {
		return nil, fmt.Errorf("list unresolved watch items: %w", core.ErrInvalidArgument)
	}
	items, err := s.q.ListUnresolvedWatchItemIDs(ctx, postgres.ListUnresolvedWatchItemIDsParams{
		MediaServerID: mediaServerID, RowLimit: int32(limit),
	})
	if err != nil {
		return nil, playbackStoreError("list unresolved watch items", err)
	}
	return items, nil
}

func (s *postgresPlaybackStore) BackfillWatchLibrary(
	ctx context.Context, mediaServerID, itemID string, library core.Library,
) error {
	if !core.ValidID(mediaServerID) || !core.ValidLibraryID(itemID) || !library.Valid() {
		return fmt.Errorf("backfill watch library: %w", core.ErrInvalidArgument)
	}
	_, err := s.q.BackfillWatchLibrary(ctx, postgres.BackfillWatchLibraryParams{
		MediaServerID: mediaServerID, ItemID: itemID, LibraryID: library.ID, LibraryName: library.Name,
	})
	if err != nil {
		return playbackStoreError("backfill watch library", err)
	}
	return nil
}

func (s *postgresPlaybackStore) SaveWatches(
	ctx context.Context,
	mutations []core.PlaybackMutation,
) (result error) {
	if err := validatePlaybackMutations(mutations); err != nil {
		return fmt.Errorf("save playback watches: %w", err)
	}
	tx, err := s.pool.BeginTx(ctx, nil)
	if err != nil {
		return playbackStoreError("begin playback transaction", err)
	}
	defer rollbackPlayback(tx, &result)
	queries := s.q.WithTx(tx)
	for _, mutation := range mutations {
		if err := savePostgresMutation(ctx, queries, mutation); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return playbackStoreError("commit playback transaction", err)
	}
	return nil
}

func savePostgresMutation(
	ctx context.Context,
	queries *postgres.Queries,
	mutation core.PlaybackMutation,
) error {
	if err := queries.UpsertPlaybackWatch(ctx, postgresWatchParams(mutation.Watch)); err != nil {
		return playbackStoreError("upsert playback watch", err)
	}
	if mutation.SegmentEnd != nil {
		params := postgres.CloseOpenWatchSegmentParams{
			WatchID: mutation.Watch.ID, EndedAt: nullableTime(mutation.SegmentEnd),
		}
		if _, err := queries.CloseOpenWatchSegment(ctx, params); err != nil {
			return playbackStoreError("close playback segment", err)
		}
	}
	if mutation.SegmentStart != nil {
		params := postgres.CreateWatchSegmentParams{
			WatchID: mutation.Watch.ID, StartedAt: core.NormalizeTime(*mutation.SegmentStart),
			Source: string(mutation.SegmentSource),
		}
		if err := queries.CreateWatchSegment(ctx, params); err != nil {
			return playbackStoreError("create playback segment", err)
		}
	}
	return savePostgresPosition(ctx, queries, mutation.Position)
}

func savePostgresPosition(
	ctx context.Context,
	queries *postgres.Queries,
	position *core.PlaybackPosition,
) error {
	if position == nil {
		return nil
	}
	params := postgres.UpsertWatchPositionParams{
		WatchID: position.WatchID, ObservedAt: core.NormalizeTime(position.ObservedAt),
		PositionMs: durationMilliseconds(position.Position), Paused: position.Paused,
		PlayMethod: string(position.PlayMethod), Source: string(position.Source),
	}
	if err := queries.UpsertWatchPosition(ctx, params); err != nil {
		return playbackStoreError("upsert playback position", err)
	}
	if err := queries.TrimWatchPositions(ctx, position.WatchID); err != nil {
		return playbackStoreError("trim playback positions", err)
	}
	return nil
}

func (s *postgresPlaybackStore) ListWatches(
	ctx context.Context,
	query core.PlaybackQuery,
) ([]core.PlaybackWatch, error) {
	if err := validatePlaybackQuery(query); err != nil {
		return nil, fmt.Errorf("list playback watches: %w", err)
	}
	switch query.Mode {
	case core.PlaybackQueryNow:
		return s.listNow(ctx, query)
	case core.PlaybackQueryHistory:
		return s.listHistory(ctx, query)
	case core.PlaybackQueryRecent:
		return s.findRecent(ctx, query)
	case core.PlaybackQueryRecentServer:
		return s.listRecent(ctx, query)
	default:
		return nil, fmt.Errorf("list playback watches: %w", core.ErrInvalidArgument)
	}
}

func (s *postgresPlaybackStore) listRecent(
	ctx context.Context,
	query core.PlaybackQuery,
) ([]core.PlaybackWatch, error) {
	rows, err := s.q.ListRecentPlaybackWatches(ctx, postgres.ListRecentPlaybackWatchesParams{
		MediaServerID: query.MediaServerID, EndedAfter: nullableTime(&query.EndedAfter),
		PageSize: int32(query.PageSize), //nolint:gosec // validated above.
	})
	if err != nil {
		return nil, playbackStoreError("list recent playback watches", err)
	}
	watches := make([]core.PlaybackWatch, 0, len(rows))
	for _, row := range rows {
		watches = append(watches, postgresRecentServerWatch(row))
	}
	return watches, nil
}

func (s *postgresPlaybackStore) listNow(
	ctx context.Context,
	query core.PlaybackQuery,
) ([]core.PlaybackWatch, error) {
	beforeTime, beforeID := postgresPlaybackCursor(query)
	rows, err := s.q.ListNowPlaying(ctx, postgres.ListNowPlayingParams{
		BeforeStartedAt: beforeTime, BeforeID: beforeID,
		PageSize: int32(query.PageSize), //nolint:gosec // validated above.
	})
	if err != nil {
		return nil, playbackStoreError("list now playing", err)
	}
	watches := make([]core.PlaybackWatch, 0, len(rows))
	for _, row := range rows {
		watches = append(watches, postgresNowWatch(row))
	}
	return watches, nil
}

func (s *postgresPlaybackStore) listHistory(
	ctx context.Context,
	query core.PlaybackQuery,
) ([]core.PlaybackWatch, error) {
	beforeTime, beforeID := postgresPlaybackCursor(query)
	rows, err := s.q.ListPlaybackHistory(ctx, postgres.ListPlaybackHistoryParams{
		MediaServerFilter: query.MediaServerID, BeforeStartedAt: beforeTime,
		BeforeID: beforeID, PageSize: int32(query.PageSize), //nolint:gosec // validated above.
	})
	if err != nil {
		return nil, playbackStoreError("list playback history", err)
	}
	watches := make([]core.PlaybackWatch, 0, len(rows))
	for _, row := range rows {
		watches = append(watches, postgresHistoryWatch(row))
	}
	return watches, nil
}

func (s *postgresPlaybackStore) findRecent(
	ctx context.Context,
	query core.PlaybackQuery,
) ([]core.PlaybackWatch, error) {
	key := query.Key
	row, err := s.q.FindRecentPlaybackWatch(ctx, postgres.FindRecentPlaybackWatchParams{
		MediaServerID: key.MediaServerID, MediaUserID: key.MediaUserID,
		DeviceID: key.DeviceID, ItemID: key.ItemID, ServerSessionID: key.ServerSessionID,
		EndedAfter: nullableTime(&query.EndedAfter),
	})
	if errors.Is(err, sql.ErrNoRows) {
		return []core.PlaybackWatch{}, nil
	}
	if err != nil {
		return nil, playbackStoreError("find recent playback watch", err)
	}
	return []core.PlaybackWatch{postgresRecentWatch(row)}, nil
}

func postgresPlaybackCursor(query core.PlaybackQuery) (time.Time, string) {
	if query.BeforeStartedAt.IsZero() {
		return time.Date(9999, 12, 31, 23, 59, 59, 0, time.UTC), "\uffff"
	}
	return core.NormalizeTime(query.BeforeStartedAt), query.BeforeID
}

func postgresWatchParams(w core.PlaybackWatch) postgres.UpsertPlaybackWatchParams {
	return postgres.UpsertPlaybackWatchParams{
		ID: w.ID, MediaServerID: w.MediaServerID, MediaUserID: w.MediaUserID,
		Username: w.Username, DeviceID: w.DeviceID, DeviceName: w.DeviceName, Client: w.Client,
		ServerSessionID: w.ServerSessionID, ItemID: w.ItemID, ItemName: w.ItemName,
		ItemType: w.ItemType, SeriesName: w.SeriesName,
		LibraryID: w.LibraryID, LibraryName: w.LibraryName,
		SeasonNumber: nullableInt32(w.SeasonNumber), EpisodeNumber: nullableInt32(w.EpisodeNumber),
		PlayMethod: string(w.PlayMethod), State: string(w.State),
		StartedAt: core.NormalizeTime(w.StartedAt), LastSeenAt: core.NormalizeTime(w.LastSeenAt),
		EndedAt: nullableTime(w.EndedAt), ActiveSeconds: durationSeconds(w.ActiveTime),
		LastPositionMs: durationMilliseconds(w.LastPosition), Source: string(w.Source),
		CreatedAt: core.NormalizeTime(w.CreatedAt), UpdatedAt: core.NormalizeTime(w.UpdatedAt),
	}
}

func postgresStoredWatch(
	id, serverID, serverName, userID, username, deviceID, deviceName, client, sessionID string,
	itemID, itemName, itemType, seriesName, libraryID, libraryName string,
	season, episode sql.NullInt32,
	method, state string,
	started, lastSeen time.Time,
	ended sql.NullTime,
	activeSeconds, positionMS int64,
	source string,
	created, updated time.Time,
) core.PlaybackWatch {
	return storedPlaybackWatch{
		id: id, mediaServerID: serverID, mediaServerName: serverName, mediaUserID: userID,
		username: username, deviceID: deviceID, deviceName: deviceName, client: client,
		serverSessionID: sessionID, itemID: itemID, itemName: itemName, itemType: itemType,
		seriesName: seriesName, libraryID: libraryID, libraryName: libraryName,
		seasonNumber: int32FromNull(season), episodeNumber: int32FromNull(episode),
		playMethod: core.PlayMethod(method), state: core.WatchState(state),
		startedAt: core.NormalizeTime(started), lastSeenAt: core.NormalizeTime(lastSeen),
		endedAt: timeFromNull(ended), activeSeconds: activeSeconds, lastPositionMS: positionMS,
		source: core.WatchSource(source), createdAt: core.NormalizeTime(created), updatedAt: core.NormalizeTime(updated),
	}.domain()
}

func postgresOpenWatch(row postgres.ListOpenPlaybackWatchesRow) core.PlaybackWatch {
	return postgresStoredWatch(
		row.ID, row.MediaServerID, row.MediaServerName, row.MediaUserID, row.Username,
		row.DeviceID, row.DeviceName, row.Client, row.ServerSessionID,
		row.ItemID, row.ItemName, row.ItemType, row.SeriesName, row.LibraryID, row.LibraryName,
		row.SeasonNumber, row.EpisodeNumber,
		row.PlayMethod, row.State, row.StartedAt, row.LastSeenAt, row.EndedAt,
		row.ActiveSeconds, row.LastPositionMs, row.Source, row.CreatedAt, row.UpdatedAt,
	)
}

func postgresNowWatch(row postgres.ListNowPlayingRow) core.PlaybackWatch {
	return postgresStoredWatch(
		row.ID, row.MediaServerID, row.MediaServerName, row.MediaUserID, row.Username,
		row.DeviceID, row.DeviceName, row.Client, row.ServerSessionID,
		row.ItemID, row.ItemName, row.ItemType, row.SeriesName, row.LibraryID, row.LibraryName,
		row.SeasonNumber, row.EpisodeNumber,
		row.PlayMethod, row.State, row.StartedAt, row.LastSeenAt, row.EndedAt,
		row.ActiveSeconds, row.LastPositionMs, row.Source, row.CreatedAt, row.UpdatedAt,
	)
}

func postgresHistoryWatch(row postgres.ListPlaybackHistoryRow) core.PlaybackWatch {
	return postgresStoredWatch(
		row.ID, row.MediaServerID, row.MediaServerName, row.MediaUserID, row.Username,
		row.DeviceID, row.DeviceName, row.Client, row.ServerSessionID,
		row.ItemID, row.ItemName, row.ItemType, row.SeriesName, row.LibraryID, row.LibraryName,
		row.SeasonNumber, row.EpisodeNumber,
		row.PlayMethod, row.State, row.StartedAt, row.LastSeenAt, row.EndedAt,
		row.ActiveSeconds, row.LastPositionMs, row.Source, row.CreatedAt, row.UpdatedAt,
	)
}

func postgresRecentWatch(row postgres.FindRecentPlaybackWatchRow) core.PlaybackWatch {
	return postgresStoredWatch(
		row.ID, row.MediaServerID, row.MediaServerName, row.MediaUserID, row.Username,
		row.DeviceID, row.DeviceName, row.Client, row.ServerSessionID,
		row.ItemID, row.ItemName, row.ItemType, row.SeriesName, row.LibraryID, row.LibraryName,
		row.SeasonNumber, row.EpisodeNumber,
		row.PlayMethod, row.State, row.StartedAt, row.LastSeenAt, row.EndedAt,
		row.ActiveSeconds, row.LastPositionMs, row.Source, row.CreatedAt, row.UpdatedAt,
	)
}

func postgresRecentServerWatch(row postgres.ListRecentPlaybackWatchesRow) core.PlaybackWatch {
	return postgresStoredWatch(
		row.ID, row.MediaServerID, row.MediaServerName, row.MediaUserID, row.Username,
		row.DeviceID, row.DeviceName, row.Client, row.ServerSessionID,
		row.ItemID, row.ItemName, row.ItemType, row.SeriesName, row.LibraryID, row.LibraryName,
		row.SeasonNumber, row.EpisodeNumber,
		row.PlayMethod, row.State, row.StartedAt, row.LastSeenAt, row.EndedAt,
		row.ActiveSeconds, row.LastPositionMs, row.Source, row.CreatedAt, row.UpdatedAt,
	)
}
