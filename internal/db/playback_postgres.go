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
		watch, mapErr := postgresOpenWatch(row)
		if mapErr != nil {
			return nil, playbackStoreError("map open playback watch", mapErr)
		}
		watches = append(watches, watch)
	}
	return watches, nil
}

func (s *postgresPlaybackStore) ListUnresolvedWatchItemIDs(
	ctx context.Context, mediaServerID, afterItemID string, limit int,
) ([]string, error) {
	if !core.ValidID(mediaServerID) || limit < 1 || limit > core.MaxPlaybackLibraryBackfillItems {
		return nil, fmt.Errorf("list unresolved watch items: %w", core.ErrInvalidArgument)
	}
	items, err := s.q.ListUnresolvedWatchItemIDs(ctx, postgres.ListUnresolvedWatchItemIDsParams{
		MediaServerID: mediaServerID, AfterItemID: afterItemID,
		RowLimit: int32(limit),
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
	params, err := postgresWatchParams(mutation.Watch)
	if err != nil {
		return playbackStoreError("encode playback watch", err)
	}
	if err := queries.UpsertPlaybackWatch(ctx, params); err != nil {
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
	stream, err := encodeStreamDetails(position.Stream)
	if err != nil {
		return playbackStoreError("encode playback position", err)
	}
	params := postgres.UpsertWatchPositionParams{
		WatchID: position.WatchID, ObservedAt: core.NormalizeTime(position.ObservedAt),
		PositionMs: durationMilliseconds(position.Position), Paused: position.Paused,
		PlayMethod: string(position.PlayMethod), Source: string(position.Source),
		StreamContainer: stream.container, StreamVideoCodec: stream.videoCodec,
		StreamAudioCodec: stream.audioCodec, StreamBitrate: stream.bitrate,
		StreamWidth: postgresNullInt32(stream.width), StreamHeight: postgresNullInt32(stream.height),
		StreamFramerateHundredths: postgresNullInt32(stream.framerate),
		StreamAudioChannels:       postgresNullInt32(stream.audioChannels),
		StreamIsVideoDirect:       stream.videoDirect, StreamIsAudioDirect: stream.audioDirect,
		StreamTranscodeReasons: stream.reasons,
	}
	if err := queries.UpsertWatchPosition(ctx, params); err != nil {
		return playbackStoreError("upsert playback position", err)
	}
	if err := queries.TrimWatchPositions(ctx, position.WatchID); err != nil {
		return playbackStoreError("trim playback positions", err)
	}
	return nil
}

func (s *postgresPlaybackStore) ListWatchPositions(
	ctx context.Context, watchID string,
) ([]core.PlaybackPosition, error) {
	if !core.ValidID(watchID) {
		return nil, fmt.Errorf("list watch positions: %w", core.ErrInvalidArgument)
	}
	if _, err := s.q.GetPlaybackWatchID(ctx, watchID); errors.Is(err, sql.ErrNoRows) {
		return nil, core.ErrNotFound
	} else if err != nil {
		return nil, playbackStoreError("find playback watch", err)
	}
	rows, err := s.q.ListWatchPositions(ctx, watchID)
	if err != nil {
		return nil, playbackStoreError("list watch positions", err)
	}
	positions := make([]core.PlaybackPosition, 0, len(rows))
	for _, row := range rows {
		position, mapErr := postgresPosition(row)
		if mapErr != nil {
			return nil, playbackStoreError("map watch position", mapErr)
		}
		positions = append(positions, position)
	}
	return positions, nil
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
		watch, mapErr := postgresRecentServerWatch(row)
		if mapErr != nil {
			return nil, playbackStoreError("map recent playback watch", mapErr)
		}
		watches = append(watches, watch)
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
		watch, mapErr := postgresNowWatch(row)
		if mapErr != nil {
			return nil, playbackStoreError("map now playing", mapErr)
		}
		watches = append(watches, watch)
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
		watch, mapErr := postgresHistoryWatch(row)
		if mapErr != nil {
			return nil, playbackStoreError("map playback history", mapErr)
		}
		watches = append(watches, watch)
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
	watch, err := postgresRecentWatch(row)
	if err != nil {
		return nil, playbackStoreError("map recent playback watch", err)
	}
	return []core.PlaybackWatch{watch}, nil
}

func postgresPlaybackCursor(query core.PlaybackQuery) (time.Time, string) {
	if query.BeforeStartedAt.IsZero() {
		return time.Date(9999, 12, 31, 23, 59, 59, 0, time.UTC), "\uffff"
	}
	return core.NormalizeTime(query.BeforeStartedAt), query.BeforeID
}

func postgresWatchParams(w core.PlaybackWatch) (postgres.UpsertPlaybackWatchParams, error) {
	stream, err := encodeStreamDetails(w.Stream)
	if err != nil {
		return postgres.UpsertPlaybackWatchParams{}, err
	}
	return postgres.UpsertPlaybackWatchParams{
		ID: w.ID, MediaServerID: w.MediaServerID, MediaUserID: w.MediaUserID,
		Username: w.Username, DeviceID: w.DeviceID, DeviceName: w.DeviceName, Client: w.Client,
		ServerSessionID: w.ServerSessionID, ItemID: w.ItemID, ItemName: w.ItemName,
		ItemType: w.ItemType, SeriesName: w.SeriesName,
		LibraryID: w.LibraryID, LibraryName: w.LibraryName,
		SeasonNumber: nullableInt32(w.SeasonNumber), EpisodeNumber: nullableInt32(w.EpisodeNumber),
		PlayMethod: string(w.PlayMethod), State: string(w.State),
		StreamContainer: stream.container, StreamVideoCodec: stream.videoCodec,
		StreamAudioCodec: stream.audioCodec, StreamBitrate: stream.bitrate,
		StreamWidth: postgresNullInt32(stream.width), StreamHeight: postgresNullInt32(stream.height),
		StreamFramerateHundredths: postgresNullInt32(stream.framerate),
		StreamAudioChannels:       postgresNullInt32(stream.audioChannels),
		StreamIsVideoDirect:       stream.videoDirect, StreamIsAudioDirect: stream.audioDirect,
		StreamTranscodeReasons: stream.reasons,
		StartedAt:              core.NormalizeTime(w.StartedAt), LastSeenAt: core.NormalizeTime(w.LastSeenAt),
		EndedAt: nullableTime(w.EndedAt), ActiveSeconds: durationSeconds(w.ActiveTime),
		LastPositionMs: durationMilliseconds(w.LastPosition), Source: string(w.Source),
		CreatedAt: core.NormalizeTime(w.CreatedAt), UpdatedAt: core.NormalizeTime(w.UpdatedAt),
	}, nil
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
	stream storedStreamDetails,
) (core.PlaybackWatch, error) {
	details, err := stream.domain()
	if err != nil {
		return core.PlaybackWatch{}, err
	}
	return storedPlaybackWatch{
		id: id, mediaServerID: serverID, mediaServerName: serverName, mediaUserID: userID,
		username: username, deviceID: deviceID, deviceName: deviceName, client: client,
		serverSessionID: sessionID, itemID: itemID, itemName: itemName, itemType: itemType,
		seriesName: seriesName, libraryID: libraryID, libraryName: libraryName,
		seasonNumber: int32FromNull(season), episodeNumber: int32FromNull(episode),
		playMethod: core.PlayMethod(method), state: core.WatchState(state),
		stream:    details,
		startedAt: core.NormalizeTime(started), lastSeenAt: core.NormalizeTime(lastSeen),
		endedAt: timeFromNull(ended), activeSeconds: activeSeconds, lastPositionMS: positionMS,
		source: core.WatchSource(source), createdAt: core.NormalizeTime(created), updatedAt: core.NormalizeTime(updated),
	}.domain(), nil
}

func postgresOpenWatch(row postgres.ListOpenPlaybackWatchesRow) (core.PlaybackWatch, error) {
	return postgresStoredWatch(
		row.ID, row.MediaServerID, row.MediaServerName, row.MediaUserID, row.Username,
		row.DeviceID, row.DeviceName, row.Client, row.ServerSessionID,
		row.ItemID, row.ItemName, row.ItemType, row.SeriesName, row.LibraryID, row.LibraryName,
		row.SeasonNumber, row.EpisodeNumber,
		row.PlayMethod, row.State, row.StartedAt, row.LastSeenAt, row.EndedAt,
		row.ActiveSeconds, row.LastPositionMs, row.Source, row.CreatedAt, row.UpdatedAt,
		postgresStream(row.StreamContainer, row.StreamVideoCodec, row.StreamAudioCodec,
			row.StreamBitrate, row.StreamWidth, row.StreamHeight, row.StreamFramerateHundredths,
			row.StreamAudioChannels, row.StreamIsVideoDirect, row.StreamIsAudioDirect, row.StreamTranscodeReasons),
	)
}

func postgresNowWatch(row postgres.ListNowPlayingRow) (core.PlaybackWatch, error) {
	return postgresStoredWatch(
		row.ID, row.MediaServerID, row.MediaServerName, row.MediaUserID, row.Username,
		row.DeviceID, row.DeviceName, row.Client, row.ServerSessionID,
		row.ItemID, row.ItemName, row.ItemType, row.SeriesName, row.LibraryID, row.LibraryName,
		row.SeasonNumber, row.EpisodeNumber,
		row.PlayMethod, row.State, row.StartedAt, row.LastSeenAt, row.EndedAt,
		row.ActiveSeconds, row.LastPositionMs, row.Source, row.CreatedAt, row.UpdatedAt,
		postgresStream(row.StreamContainer, row.StreamVideoCodec, row.StreamAudioCodec,
			row.StreamBitrate, row.StreamWidth, row.StreamHeight, row.StreamFramerateHundredths,
			row.StreamAudioChannels, row.StreamIsVideoDirect, row.StreamIsAudioDirect, row.StreamTranscodeReasons),
	)
}

func postgresHistoryWatch(row postgres.ListPlaybackHistoryRow) (core.PlaybackWatch, error) {
	return postgresStoredWatch(
		row.ID, row.MediaServerID, row.MediaServerName, row.MediaUserID, row.Username,
		row.DeviceID, row.DeviceName, row.Client, row.ServerSessionID,
		row.ItemID, row.ItemName, row.ItemType, row.SeriesName, row.LibraryID, row.LibraryName,
		row.SeasonNumber, row.EpisodeNumber,
		row.PlayMethod, row.State, row.StartedAt, row.LastSeenAt, row.EndedAt,
		row.ActiveSeconds, row.LastPositionMs, row.Source, row.CreatedAt, row.UpdatedAt,
		postgresStream(row.StreamContainer, row.StreamVideoCodec, row.StreamAudioCodec,
			row.StreamBitrate, row.StreamWidth, row.StreamHeight, row.StreamFramerateHundredths,
			row.StreamAudioChannels, row.StreamIsVideoDirect, row.StreamIsAudioDirect, row.StreamTranscodeReasons),
	)
}

func postgresRecentWatch(row postgres.FindRecentPlaybackWatchRow) (core.PlaybackWatch, error) {
	return postgresStoredWatch(
		row.ID, row.MediaServerID, row.MediaServerName, row.MediaUserID, row.Username,
		row.DeviceID, row.DeviceName, row.Client, row.ServerSessionID,
		row.ItemID, row.ItemName, row.ItemType, row.SeriesName, row.LibraryID, row.LibraryName,
		row.SeasonNumber, row.EpisodeNumber,
		row.PlayMethod, row.State, row.StartedAt, row.LastSeenAt, row.EndedAt,
		row.ActiveSeconds, row.LastPositionMs, row.Source, row.CreatedAt, row.UpdatedAt,
		postgresStream(row.StreamContainer, row.StreamVideoCodec, row.StreamAudioCodec,
			row.StreamBitrate, row.StreamWidth, row.StreamHeight, row.StreamFramerateHundredths,
			row.StreamAudioChannels, row.StreamIsVideoDirect, row.StreamIsAudioDirect, row.StreamTranscodeReasons),
	)
}

func postgresRecentServerWatch(row postgres.ListRecentPlaybackWatchesRow) (core.PlaybackWatch, error) {
	return postgresStoredWatch(
		row.ID, row.MediaServerID, row.MediaServerName, row.MediaUserID, row.Username,
		row.DeviceID, row.DeviceName, row.Client, row.ServerSessionID,
		row.ItemID, row.ItemName, row.ItemType, row.SeriesName, row.LibraryID, row.LibraryName,
		row.SeasonNumber, row.EpisodeNumber,
		row.PlayMethod, row.State, row.StartedAt, row.LastSeenAt, row.EndedAt,
		row.ActiveSeconds, row.LastPositionMs, row.Source, row.CreatedAt, row.UpdatedAt,
		postgresStream(row.StreamContainer, row.StreamVideoCodec, row.StreamAudioCodec,
			row.StreamBitrate, row.StreamWidth, row.StreamHeight, row.StreamFramerateHundredths,
			row.StreamAudioChannels, row.StreamIsVideoDirect, row.StreamIsAudioDirect, row.StreamTranscodeReasons),
	)
}

func postgresStream(
	container, videoCodec, audioCodec sql.NullString,
	bitrate sql.NullInt64,
	width, height, framerate, channels sql.NullInt32,
	videoDirect, audioDirect sql.NullBool,
	reasons sql.NullString,
) storedStreamDetails {
	return storedStreamDetails{
		container: container, videoCodec: videoCodec, audioCodec: audioCodec, bitrate: bitrate,
		width: int64FromNullInt32(width), height: int64FromNullInt32(height),
		framerate: int64FromNullInt32(framerate), audioChannels: int64FromNullInt32(channels),
		videoDirect: videoDirect, audioDirect: audioDirect, reasons: reasons,
	}
}

func postgresPosition(row postgres.WatchPosition) (core.PlaybackPosition, error) {
	stream, err := postgresStream(
		row.StreamContainer, row.StreamVideoCodec, row.StreamAudioCodec, row.StreamBitrate,
		row.StreamWidth, row.StreamHeight, row.StreamFramerateHundredths, row.StreamAudioChannels,
		row.StreamIsVideoDirect, row.StreamIsAudioDirect, row.StreamTranscodeReasons,
	).domain()
	if err != nil {
		return core.PlaybackPosition{}, err
	}
	return core.PlaybackPosition{
		WatchID: row.WatchID, ObservedAt: core.NormalizeTime(row.ObservedAt),
		Position: time.Duration(row.PositionMs) * time.Millisecond, Paused: row.Paused,
		PlayMethod: core.PlayMethod(row.PlayMethod), Stream: stream, Source: core.WatchSource(row.Source),
	}, nil
}

func postgresNullInt32(value sql.NullInt64) sql.NullInt32 {
	return sql.NullInt32{
		Int32: int32(value.Int64), //nolint:gosec // Encoded core stream values already passed bounded validation.
		Valid: value.Valid,
	}
}

func int64FromNullInt32(value sql.NullInt32) sql.NullInt64 {
	return sql.NullInt64{Int64: int64(value.Int32), Valid: value.Valid}
}
