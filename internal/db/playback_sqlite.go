package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/db/sqlite"
)

type sqlitePlaybackStore struct {
	pool *sql.DB
	q    *sqlite.Queries
}

var (
	_ core.PlaybackStore        = (*sqlitePlaybackStore)(nil)
	_ core.PlaybackLibraryStore = (*sqlitePlaybackStore)(nil)
)

func newSQLitePlaybackStore(pool *sql.DB) *sqlitePlaybackStore {
	return &sqlitePlaybackStore{pool: pool, q: sqlite.New(pool)}
}

func (s *sqlitePlaybackStore) LoadOpenWatches(
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
		watch, mapErr := sqliteOpenWatch(row)
		if mapErr != nil {
			return nil, playbackStoreError("map open playback watch", mapErr)
		}
		watches = append(watches, watch)
	}
	return watches, nil
}

func (s *sqlitePlaybackStore) ListUnresolvedWatchItemIDs(
	ctx context.Context, mediaServerID, afterItemID string, limit int,
) ([]string, error) {
	if !core.ValidID(mediaServerID) || limit < 1 || limit > core.MaxPlaybackLibraryBackfillItems {
		return nil, fmt.Errorf("list unresolved watch items: %w", core.ErrInvalidArgument)
	}
	items, err := s.q.ListUnresolvedWatchItemIDs(ctx, sqlite.ListUnresolvedWatchItemIDsParams{
		MediaServerID: mediaServerID, AfterItemID: afterItemID, RowLimit: int64(limit),
	})
	if err != nil {
		return nil, playbackStoreError("list unresolved watch items", err)
	}
	return items, nil
}

func (s *sqlitePlaybackStore) BackfillWatchLibrary(
	ctx context.Context, mediaServerID, itemID string, library core.Library,
) error {
	if !core.ValidID(mediaServerID) || !core.ValidLibraryID(itemID) || !library.Valid() {
		return fmt.Errorf("backfill watch library: %w", core.ErrInvalidArgument)
	}
	_, err := s.q.BackfillWatchLibrary(ctx, sqlite.BackfillWatchLibraryParams{
		MediaServerID: mediaServerID, ItemID: itemID, LibraryID: library.ID, LibraryName: library.Name,
	})
	if err != nil {
		return playbackStoreError("backfill watch library", err)
	}
	return nil
}

func (s *sqlitePlaybackStore) SaveWatches(
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
		if err := saveSQLiteMutation(ctx, queries, mutation); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return playbackStoreError("commit playback transaction", err)
	}
	return nil
}

func saveSQLiteMutation(
	ctx context.Context,
	queries *sqlite.Queries,
	mutation core.PlaybackMutation,
) error {
	params, err := sqliteWatchParams(mutation.Watch)
	if err != nil {
		return playbackStoreError("encode playback watch", err)
	}
	if err := queries.UpsertPlaybackWatch(ctx, params); err != nil {
		return playbackStoreError("upsert playback watch", err)
	}
	if mutation.SegmentEnd != nil {
		params := sqlite.CloseOpenWatchSegmentParams{
			WatchID: mutation.Watch.ID, EndedAt: sqliteNullableTime(mutation.SegmentEnd),
		}
		if _, err := queries.CloseOpenWatchSegment(ctx, params); err != nil {
			return playbackStoreError("close playback segment", err)
		}
	}
	if mutation.SegmentStart != nil {
		params := sqlite.CreateWatchSegmentParams{
			WatchID: mutation.Watch.ID, StartedAt: formatSQLiteTime(*mutation.SegmentStart),
			Source: string(mutation.SegmentSource),
		}
		if err := queries.CreateWatchSegment(ctx, params); err != nil {
			return playbackStoreError("create playback segment", err)
		}
	}
	return saveSQLitePosition(ctx, queries, mutation.Position)
}

func saveSQLitePosition(
	ctx context.Context,
	queries *sqlite.Queries,
	position *core.PlaybackPosition,
) error {
	if position == nil {
		return nil
	}
	stream, err := encodeStreamDetails(position.Stream)
	if err != nil {
		return playbackStoreError("encode playback position", err)
	}
	params := sqlite.UpsertWatchPositionParams{
		WatchID: position.WatchID, ObservedAt: formatSQLiteTime(position.ObservedAt),
		PositionMs: durationMilliseconds(position.Position), Paused: boolToInt64(position.Paused),
		PlayMethod: string(position.PlayMethod), Source: string(position.Source),
		StreamContainer: stream.container, StreamVideoCodec: stream.videoCodec,
		StreamAudioCodec: stream.audioCodec, StreamBitrate: stream.bitrate,
		StreamWidth: stream.width, StreamHeight: stream.height,
		StreamFramerateHundredths: stream.framerate, StreamAudioChannels: stream.audioChannels,
		StreamIsVideoDirect: sqliteNullBool(stream.videoDirect),
		StreamIsAudioDirect: sqliteNullBool(stream.audioDirect), StreamTranscodeReasons: stream.reasons,
		IsTransition: boolToInt64(position.IsTransition),
	}
	if err := queries.UpsertWatchPosition(ctx, params); err != nil {
		return playbackStoreError("upsert playback position", err)
	}
	if err := queries.TrimWatchPositions(ctx, position.WatchID); err != nil {
		return playbackStoreError("trim playback positions", err)
	}
	return nil
}

func (s *sqlitePlaybackStore) ListWatchPositions(
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
		position, mapErr := sqlitePosition(row)
		if mapErr != nil {
			return nil, playbackStoreError("map watch position", mapErr)
		}
		positions = append(positions, position)
	}
	return positions, nil
}

func (s *sqlitePlaybackStore) ListWatches(
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

func (s *sqlitePlaybackStore) listRecent(
	ctx context.Context,
	query core.PlaybackQuery,
) ([]core.PlaybackWatch, error) {
	rows, err := s.q.ListRecentPlaybackWatches(ctx, sqlite.ListRecentPlaybackWatchesParams{
		MediaServerID: query.MediaServerID, EndedAfter: sqliteNullableTime(&query.EndedAfter),
		PageSize: int64(query.PageSize),
	})
	if err != nil {
		return nil, playbackStoreError("list recent playback watches", err)
	}
	watches := make([]core.PlaybackWatch, 0, len(rows))
	for _, row := range rows {
		watch, mapErr := sqliteRecentServerWatch(row)
		if mapErr != nil {
			return nil, playbackStoreError("map recent playback watch", mapErr)
		}
		watches = append(watches, watch)
	}
	return watches, nil
}

func (s *sqlitePlaybackStore) listNow(
	ctx context.Context,
	query core.PlaybackQuery,
) ([]core.PlaybackWatch, error) {
	beforeTime, beforeID := sqlitePlaybackCursor(query)
	rows, err := s.q.ListNowPlaying(ctx, sqlite.ListNowPlayingParams{
		BeforeStartedAt: beforeTime, BeforeID: beforeID, PageSize: int64(query.PageSize),
	})
	if err != nil {
		return nil, playbackStoreError("list now playing", err)
	}
	watches := make([]core.PlaybackWatch, 0, len(rows))
	for _, row := range rows {
		watch, mapErr := sqliteNowWatch(row)
		if mapErr != nil {
			return nil, playbackStoreError("map now playing", mapErr)
		}
		watches = append(watches, watch)
	}
	return watches, nil
}

func (s *sqlitePlaybackStore) listHistory(
	ctx context.Context,
	query core.PlaybackQuery,
) ([]core.PlaybackWatch, error) {
	beforeTime, beforeID := sqlitePlaybackCursor(query)
	rows, err := s.q.ListPlaybackHistory(ctx, sqlite.ListPlaybackHistoryParams{
		MediaServerFilter: query.MediaServerID, BeforeStartedAt: beforeTime,
		BeforeID: beforeID, PageSize: int64(query.PageSize),
	})
	if err != nil {
		return nil, playbackStoreError("list playback history", err)
	}
	watches := make([]core.PlaybackWatch, 0, len(rows))
	for _, row := range rows {
		watch, mapErr := sqliteHistoryWatch(row)
		if mapErr != nil {
			return nil, playbackStoreError("map playback history", mapErr)
		}
		watches = append(watches, watch)
	}
	return watches, nil
}

func (s *sqlitePlaybackStore) findRecent(
	ctx context.Context,
	query core.PlaybackQuery,
) ([]core.PlaybackWatch, error) {
	key := query.Key
	row, err := s.q.FindRecentPlaybackWatch(ctx, sqlite.FindRecentPlaybackWatchParams{
		MediaServerID: key.MediaServerID, MediaUserID: key.MediaUserID,
		DeviceID: key.DeviceID, ItemID: key.ItemID, ServerSessionID: key.ServerSessionID,
		EndedAfter: sqliteNullableTime(&query.EndedAfter),
	})
	if errors.Is(err, sql.ErrNoRows) {
		return []core.PlaybackWatch{}, nil
	}
	if err != nil {
		return nil, playbackStoreError("find recent playback watch", err)
	}
	watch, err := sqliteRecentWatch(row)
	if err != nil {
		return nil, playbackStoreError("map recent playback watch", err)
	}
	return []core.PlaybackWatch{watch}, nil
}

func sqlitePlaybackCursor(query core.PlaybackQuery) (string, string) {
	if query.BeforeStartedAt.IsZero() {
		return formatSQLiteTime(time.Date(9999, 12, 31, 23, 59, 59, 0, time.UTC)), "\uffff"
	}
	return formatSQLiteTime(query.BeforeStartedAt), query.BeforeID
}

func sqliteWatchParams(w core.PlaybackWatch) (sqlite.UpsertPlaybackWatchParams, error) {
	stream, err := encodeStreamDetails(w.Stream)
	if err != nil {
		return sqlite.UpsertPlaybackWatchParams{}, err
	}
	return sqlite.UpsertPlaybackWatchParams{
		ID: w.ID, MediaServerID: w.MediaServerID, MediaUserID: w.MediaUserID,
		Username: w.Username, DeviceID: w.DeviceID, DeviceName: w.DeviceName, Client: w.Client,
		ServerSessionID: w.ServerSessionID, ItemID: w.ItemID, ItemName: w.ItemName,
		ItemType: w.ItemType, SeriesName: w.SeriesName,
		LibraryID: w.LibraryID, LibraryName: w.LibraryName,
		SeasonNumber: sqliteNullableInt32(w.SeasonNumber), EpisodeNumber: sqliteNullableInt32(w.EpisodeNumber),
		PlayMethod: string(w.PlayMethod), State: string(w.State),
		StreamContainer: stream.container, StreamVideoCodec: stream.videoCodec,
		StreamAudioCodec: stream.audioCodec, StreamBitrate: stream.bitrate,
		StreamWidth: stream.width, StreamHeight: stream.height,
		StreamFramerateHundredths: stream.framerate, StreamAudioChannels: stream.audioChannels,
		StreamIsVideoDirect: sqliteNullBool(stream.videoDirect),
		StreamIsAudioDirect: sqliteNullBool(stream.audioDirect), StreamTranscodeReasons: stream.reasons,
		StartedAt: formatSQLiteTime(w.StartedAt), LastSeenAt: formatSQLiteTime(w.LastSeenAt),
		EndedAt: sqliteNullableTime(w.EndedAt), ActiveSeconds: durationSeconds(w.ActiveTime),
		LastPositionMs: durationMilliseconds(w.LastPosition), Source: string(w.Source),
		CreatedAt: formatSQLiteTime(w.CreatedAt), UpdatedAt: formatSQLiteTime(w.UpdatedAt),
	}, nil
}

func sqliteNullableInt32(value *int32) sql.NullInt64 {
	if value == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: int64(*value), Valid: true}
}

func sqliteInt32(value sql.NullInt64) (*int32, error) {
	if !value.Valid {
		return nil, nil
	}
	if value.Int64 < math.MinInt32 || value.Int64 > math.MaxInt32 {
		return nil, errors.New("playback episode number exceeds int32")
	}
	converted := int32(value.Int64)
	return &converted, nil
}

func sqliteOptionalTime(value sql.NullString) (*time.Time, error) {
	if !value.Valid {
		return nil, nil
	}
	parsed, err := parseSQLiteTime(value.String)
	if err != nil {
		return nil, err
	}
	return &parsed, nil
}

func sqliteStoredWatch(
	id, serverID, serverName, userID, username, deviceID, deviceName, client, sessionID string,
	itemID, itemName, itemType, seriesName, libraryID, libraryName string,
	season, episode sql.NullInt64,
	method, state, started, lastSeen string,
	ended sql.NullString,
	activeSeconds, positionMS int64,
	source, created, updated string,
	stream storedStreamDetails,
) (core.PlaybackWatch, error) {
	seasonNumber, err := sqliteInt32(season)
	if err != nil {
		return core.PlaybackWatch{}, err
	}
	episodeNumber, err := sqliteInt32(episode)
	if err != nil {
		return core.PlaybackWatch{}, err
	}
	startedAt, err := parseSQLiteTime(started)
	if err != nil {
		return core.PlaybackWatch{}, err
	}
	lastSeenAt, err := parseSQLiteTime(lastSeen)
	if err != nil {
		return core.PlaybackWatch{}, err
	}
	endedAt, err := sqliteOptionalTime(ended)
	if err != nil {
		return core.PlaybackWatch{}, err
	}
	createdAt, err := parseSQLiteTime(created)
	if err != nil {
		return core.PlaybackWatch{}, err
	}
	updatedAt, err := parseSQLiteTime(updated)
	if err != nil {
		return core.PlaybackWatch{}, err
	}
	details, err := stream.domain()
	if err != nil {
		return core.PlaybackWatch{}, err
	}
	return storedPlaybackWatch{
		id: id, mediaServerID: serverID, mediaServerName: serverName, mediaUserID: userID,
		username: username, deviceID: deviceID, deviceName: deviceName, client: client,
		serverSessionID: sessionID, itemID: itemID, itemName: itemName, itemType: itemType,
		seriesName: seriesName, libraryID: libraryID, libraryName: libraryName,
		seasonNumber: seasonNumber, episodeNumber: episodeNumber,
		playMethod: core.PlayMethod(method), state: core.WatchState(state),
		stream:    details,
		startedAt: startedAt, lastSeenAt: lastSeenAt, endedAt: endedAt,
		activeSeconds: activeSeconds, lastPositionMS: positionMS, source: core.WatchSource(source),
		createdAt: createdAt, updatedAt: updatedAt,
	}.domain(), nil
}

func sqliteOpenWatch(row sqlite.ListOpenPlaybackWatchesRow) (core.PlaybackWatch, error) {
	return sqliteStoredWatch(
		row.ID, row.MediaServerID, row.MediaServerName, row.MediaUserID, row.Username,
		row.DeviceID, row.DeviceName, row.Client, row.ServerSessionID,
		row.ItemID, row.ItemName, row.ItemType, row.SeriesName, row.LibraryID, row.LibraryName,
		row.SeasonNumber, row.EpisodeNumber,
		row.PlayMethod, row.State, row.StartedAt, row.LastSeenAt, row.EndedAt,
		row.ActiveSeconds, row.LastPositionMs, row.Source, row.CreatedAt, row.UpdatedAt,
		sqliteStream(row.StreamContainer, row.StreamVideoCodec, row.StreamAudioCodec,
			row.StreamBitrate, row.StreamWidth, row.StreamHeight, row.StreamFramerateHundredths,
			row.StreamAudioChannels, row.StreamIsVideoDirect, row.StreamIsAudioDirect, row.StreamTranscodeReasons),
	)
}

func sqliteNowWatch(row sqlite.ListNowPlayingRow) (core.PlaybackWatch, error) {
	return sqliteStoredWatch(
		row.ID, row.MediaServerID, row.MediaServerName, row.MediaUserID, row.Username,
		row.DeviceID, row.DeviceName, row.Client, row.ServerSessionID,
		row.ItemID, row.ItemName, row.ItemType, row.SeriesName, row.LibraryID, row.LibraryName,
		row.SeasonNumber, row.EpisodeNumber,
		row.PlayMethod, row.State, row.StartedAt, row.LastSeenAt, row.EndedAt,
		row.ActiveSeconds, row.LastPositionMs, row.Source, row.CreatedAt, row.UpdatedAt,
		sqliteStream(row.StreamContainer, row.StreamVideoCodec, row.StreamAudioCodec,
			row.StreamBitrate, row.StreamWidth, row.StreamHeight, row.StreamFramerateHundredths,
			row.StreamAudioChannels, row.StreamIsVideoDirect, row.StreamIsAudioDirect, row.StreamTranscodeReasons),
	)
}

func sqliteHistoryWatch(row sqlite.ListPlaybackHistoryRow) (core.PlaybackWatch, error) {
	return sqliteStoredWatch(
		row.ID, row.MediaServerID, row.MediaServerName, row.MediaUserID, row.Username,
		row.DeviceID, row.DeviceName, row.Client, row.ServerSessionID,
		row.ItemID, row.ItemName, row.ItemType, row.SeriesName, row.LibraryID, row.LibraryName,
		row.SeasonNumber, row.EpisodeNumber,
		row.PlayMethod, row.State, row.StartedAt, row.LastSeenAt, row.EndedAt,
		row.ActiveSeconds, row.LastPositionMs, row.Source, row.CreatedAt, row.UpdatedAt,
		sqliteStream(row.StreamContainer, row.StreamVideoCodec, row.StreamAudioCodec,
			row.StreamBitrate, row.StreamWidth, row.StreamHeight, row.StreamFramerateHundredths,
			row.StreamAudioChannels, row.StreamIsVideoDirect, row.StreamIsAudioDirect, row.StreamTranscodeReasons),
	)
}

func sqliteRecentWatch(row sqlite.FindRecentPlaybackWatchRow) (core.PlaybackWatch, error) {
	return sqliteStoredWatch(
		row.ID, row.MediaServerID, row.MediaServerName, row.MediaUserID, row.Username,
		row.DeviceID, row.DeviceName, row.Client, row.ServerSessionID,
		row.ItemID, row.ItemName, row.ItemType, row.SeriesName, row.LibraryID, row.LibraryName,
		row.SeasonNumber, row.EpisodeNumber,
		row.PlayMethod, row.State, row.StartedAt, row.LastSeenAt, row.EndedAt,
		row.ActiveSeconds, row.LastPositionMs, row.Source, row.CreatedAt, row.UpdatedAt,
		sqliteStream(row.StreamContainer, row.StreamVideoCodec, row.StreamAudioCodec,
			row.StreamBitrate, row.StreamWidth, row.StreamHeight, row.StreamFramerateHundredths,
			row.StreamAudioChannels, row.StreamIsVideoDirect, row.StreamIsAudioDirect, row.StreamTranscodeReasons),
	)
}

func sqliteRecentServerWatch(row sqlite.ListRecentPlaybackWatchesRow) (core.PlaybackWatch, error) {
	return sqliteStoredWatch(
		row.ID, row.MediaServerID, row.MediaServerName, row.MediaUserID, row.Username,
		row.DeviceID, row.DeviceName, row.Client, row.ServerSessionID,
		row.ItemID, row.ItemName, row.ItemType, row.SeriesName, row.LibraryID, row.LibraryName,
		row.SeasonNumber, row.EpisodeNumber,
		row.PlayMethod, row.State, row.StartedAt, row.LastSeenAt, row.EndedAt,
		row.ActiveSeconds, row.LastPositionMs, row.Source, row.CreatedAt, row.UpdatedAt,
		sqliteStream(row.StreamContainer, row.StreamVideoCodec, row.StreamAudioCodec,
			row.StreamBitrate, row.StreamWidth, row.StreamHeight, row.StreamFramerateHundredths,
			row.StreamAudioChannels, row.StreamIsVideoDirect, row.StreamIsAudioDirect, row.StreamTranscodeReasons),
	)
}

func sqliteStream(
	container, videoCodec, audioCodec sql.NullString,
	bitrate, width, height, framerate, channels sql.NullInt64,
	videoDirect, audioDirect sql.NullInt64,
	reasons sql.NullString,
) storedStreamDetails {
	return storedStreamDetails{
		container: container, videoCodec: videoCodec, audioCodec: audioCodec, bitrate: bitrate,
		width: width, height: height, framerate: framerate, audioChannels: channels,
		videoDirect: sqliteBoolFromNull(videoDirect), audioDirect: sqliteBoolFromNull(audioDirect),
		reasons: reasons,
	}
}

func sqlitePosition(row sqlite.WatchPosition) (core.PlaybackPosition, error) {
	observedAt, err := parseSQLiteTime(row.ObservedAt)
	if err != nil {
		return core.PlaybackPosition{}, err
	}
	stream, err := sqliteStream(
		row.StreamContainer, row.StreamVideoCodec, row.StreamAudioCodec, row.StreamBitrate,
		row.StreamWidth, row.StreamHeight, row.StreamFramerateHundredths, row.StreamAudioChannels,
		row.StreamIsVideoDirect, row.StreamIsAudioDirect, row.StreamTranscodeReasons,
	).domain()
	if err != nil {
		return core.PlaybackPosition{}, err
	}
	return core.PlaybackPosition{
		WatchID: row.WatchID, ObservedAt: observedAt,
		Position: time.Duration(row.PositionMs) * time.Millisecond, Paused: row.Paused != 0,
		PlayMethod: core.PlayMethod(row.PlayMethod), Stream: stream, Source: core.WatchSource(row.Source),
		IsTransition: row.IsTransition != 0,
	}, nil
}

func sqliteNullBool(value sql.NullBool) sql.NullInt64 {
	if !value.Valid {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: boolToInt64(value.Bool), Valid: true}
}

func sqliteBoolFromNull(value sql.NullInt64) sql.NullBool {
	return sql.NullBool{Bool: value.Int64 != 0, Valid: value.Valid}
}
