package db

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/BonzTM/bloom/internal/config"
	"github.com/BonzTM/bloom/internal/core"
)

const maxPlaybackPageSize = 1025

// NewPlaybackStore returns the playback persistence seam for the configured engine.
func NewPlaybackStore(
	pool *sql.DB, driver config.Driver, resumeWindow time.Duration,
) (core.PlaybackPersistence, error) {
	if resumeWindow <= 0 {
		return nil, fmt.Errorf("playback resume window: %w", core.ErrInvalidArgument)
	}
	switch driver {
	case config.DriverSQLite:
		return newSQLitePlaybackStore(pool, resumeWindow), nil
	case config.DriverPostgres:
		return newPostgresPlaybackStore(pool, resumeWindow), nil
	default:
		return nil, fmt.Errorf("unsupported database driver %q", driver)
	}
}

type storedPlaybackWatch struct {
	id, mediaServerID, mediaServerName, mediaUserID, username string
	deviceID, deviceName, client, serverSessionID             string
	itemID, itemName, itemType, seriesID, seriesName          string
	libraryID, libraryName                                    string
	seasonNumber, episodeNumber                               *int32
	playMethod                                                core.PlayMethod
	stream                                                    *core.StreamDetails
	state                                                     core.WatchState
	startedAt, lastSeenAt                                     time.Time
	endedAt                                                   *time.Time
	activeSeconds, lastPositionMS                             int64
	runtime                                                   *time.Duration
	source                                                    core.WatchSource
	importSource                                              core.ImportSource
	importRecordID, importOriginRecordID                      string
	createdAt, updatedAt                                      time.Time
}

func (row storedPlaybackWatch) domain() core.PlaybackWatch {
	return core.PlaybackWatch{
		ID: row.id, MediaServerID: row.mediaServerID, MediaServerName: row.mediaServerName,
		MediaUserID: row.mediaUserID, Username: row.username,
		DeviceID: row.deviceID, DeviceName: row.deviceName, Client: row.client,
		ServerSessionID: row.serverSessionID, ItemID: row.itemID, ItemName: row.itemName,
		ItemType: row.itemType, SeriesID: row.seriesID, SeriesName: row.seriesName,
		LibraryID: row.libraryID, LibraryName: row.libraryName,
		SeasonNumber: row.seasonNumber, EpisodeNumber: row.episodeNumber,
		PlayMethod: row.playMethod, State: row.state,
		Stream:    row.stream,
		StartedAt: row.startedAt, LastSeenAt: row.lastSeenAt, EndedAt: row.endedAt,
		ActiveTime:   time.Duration(row.activeSeconds) * time.Second,
		LastPosition: time.Duration(row.lastPositionMS) * time.Millisecond,
		Runtime:      row.runtime,
		Source:       row.source, ImportSource: row.importSource, ImportRecordID: row.importRecordID,
		ImportOriginRecordID: row.importOriginRecordID,
		CreatedAt:            row.createdAt, UpdatedAt: row.updatedAt,
	}
}

type storedStreamDetails struct {
	container, videoCodec, audioCodec sql.NullString
	bitrate, width, height            sql.NullInt64
	framerate, audioChannels          sql.NullInt64
	videoDirect, audioDirect          sql.NullBool
	reasons                           sql.NullString
}

func encodeStreamDetails(stream *core.StreamDetails) (storedStreamDetails, error) {
	if stream == nil {
		return storedStreamDetails{}, nil
	}
	reasons, err := encodeStreamReasons(stream.TranscodeReasons)
	if err != nil {
		return storedStreamDetails{}, err
	}
	return storedStreamDetails{
		container: optionalStreamString(stream.Container), videoCodec: optionalStreamString(stream.VideoCodec),
		audioCodec: optionalStreamString(stream.AudioCodec),
		bitrate:    optionalPositiveInt64(stream.Bitrate), width: optionalPositiveInt64(int64(stream.Width)),
		height:        optionalPositiveInt64(int64(stream.Height)),
		framerate:     optionalPositiveInt64(int64(math.Round(stream.Framerate * 100))),
		audioChannels: optionalPositiveInt64(int64(stream.AudioChannels)),
		videoDirect:   nullableBool(stream.IsVideoDirect), audioDirect: nullableBool(stream.IsAudioDirect),
		reasons: reasons,
	}, nil
}

func encodeStreamReasons(reasons []string) (sql.NullString, error) {
	if len(reasons) == 0 {
		return sql.NullString{}, nil
	}
	encoded, err := json.Marshal(reasons)
	if err != nil {
		return sql.NullString{}, fmt.Errorf("encode stream transcode reasons: %w", err)
	}
	return sql.NullString{String: string(encoded), Valid: true}, nil
}

func (s storedStreamDetails) domain() (*core.StreamDetails, error) {
	if !s.present() {
		return nil, nil
	}
	if s.width.Int64 < 0 || s.width.Int64 > core.MaxStreamDimension ||
		s.height.Int64 < 0 || s.height.Int64 > core.MaxStreamDimension ||
		s.audioChannels.Int64 < 0 || s.audioChannels.Int64 > core.MaxStreamAudioChannels ||
		s.framerate.Int64 < 0 || s.framerate.Int64 > core.MaxStreamFramerate*100 {
		return nil, errors.New("stored stream numbers are out of range")
	}
	stream := &core.StreamDetails{
		Container: s.container.String, VideoCodec: s.videoCodec.String,
		AudioCodec: s.audioCodec.String, Bitrate: s.bitrate.Int64,
		Width: int32(s.width.Int64), Height: int32(s.height.Int64),
		Framerate:     float64(s.framerate.Int64) / 100,
		AudioChannels: int32(s.audioChannels.Int64),
		IsVideoDirect: boolFromNull(s.videoDirect), IsAudioDirect: boolFromNull(s.audioDirect),
	}
	if s.reasons.Valid && json.Unmarshal([]byte(s.reasons.String), &stream.TranscodeReasons) != nil {
		return nil, errors.New("decode stream transcode reasons")
	}
	if !stream.Valid() {
		return nil, errors.New("stored stream details are invalid")
	}
	return stream, nil
}

func (s storedStreamDetails) present() bool {
	return s.container.Valid || s.videoCodec.Valid || s.audioCodec.Valid || s.bitrate.Valid ||
		s.width.Valid || s.height.Valid || s.framerate.Valid || s.audioChannels.Valid ||
		s.videoDirect.Valid || s.audioDirect.Valid || s.reasons.Valid
}

func optionalStreamString(value string) sql.NullString {
	return sql.NullString{String: value, Valid: value != ""}
}

func optionalPositiveInt64(value int64) sql.NullInt64 {
	return sql.NullInt64{Int64: value, Valid: value > 0}
}

func nullableBool(value *bool) sql.NullBool {
	if value == nil {
		return sql.NullBool{}
	}
	return sql.NullBool{Bool: *value, Valid: true}
}

func boolFromNull(value sql.NullBool) *bool {
	if !value.Valid {
		return nil
	}
	return &value.Bool
}

func validatePlaybackQuery(query core.PlaybackQuery) error {
	if query.PageSize < 1 || query.PageSize > maxPlaybackPageSize {
		return core.ErrInvalidArgument
	}
	switch query.Mode {
	case core.PlaybackQueryNow:
		return validatePlaybackCursor(query)
	case core.PlaybackQueryHistory:
		if query.MediaServerID != "" && !core.ValidID(query.MediaServerID) {
			return core.ErrInvalidArgument
		}
		return validatePlaybackCursor(query)
	case core.PlaybackQueryRecent:
		if !core.ValidID(query.Key.MediaServerID) || query.Key.MediaUserID == "" ||
			query.Key.DeviceID == "" || query.Key.ItemID == "" || query.EndedAfter.IsZero() {
			return core.ErrInvalidArgument
		}
		return nil
	case core.PlaybackQueryRecentServer:
		if !core.ValidID(query.MediaServerID) || query.EndedAfter.IsZero() {
			return core.ErrInvalidArgument
		}
		return nil
	default:
		return core.ErrInvalidArgument
	}
}

func validatePlaybackCursor(query core.PlaybackQuery) error {
	if query.BeforeStartedAt.IsZero() && query.BeforeID == "" {
		return nil
	}
	if query.BeforeStartedAt.IsZero() || !core.ValidID(query.BeforeID) {
		return core.ErrInvalidArgument
	}
	return nil
}

func validateActivityQuery(query core.ActivityQuery) error {
	if query.Limit < 1 || query.Limit > core.MaxActivityPageSize+1 ||
		query.MediaServerID != "" && !core.ValidID(query.MediaServerID) ||
		!validActivityText(query.MediaUserID) || !validActivityText(query.LibraryID) ||
		!validActivityText(query.ItemType) || !validActivityText(query.Client) ||
		!validActivityText(query.DeviceID) || len(query.Search) > core.MaxActivitySearchBytes ||
		!utf8.ValidString(query.Search) || strings.IndexFunc(query.Search, unicode.IsControl) >= 0 ||
		query.PlayMethod != "" && !query.PlayMethod.Valid() ||
		query.Source != "" && !query.Source.Valid() ||
		query.ImportSource != "" && !query.ImportSource.Valid() {
		return core.ErrInvalidArgument
	}
	if query.StartedAfter != nil && query.StartedBefore != nil &&
		!query.StartedAfter.Before(*query.StartedBefore) {
		return core.ErrInvalidArgument
	}
	return validateActivityCursor(query.Before)
}

func validateTimelineWatchQuery(query core.TimelineWatchQuery) error {
	if !core.ValidID(query.MediaServerID) || !validActivityText(query.MediaUserID) ||
		query.MediaUserID == "" || query.Limit < 1 || query.Limit > core.MaxTimelineWatchFetch {
		return core.ErrInvalidArgument
	}
	return validateActivityCursor(query.Before)
}

func validateActivityCursor(cursor *core.ActivityCursor) error {
	if cursor == nil {
		return nil
	}
	if cursor.StartedAt.IsZero() || !core.ValidID(cursor.ID) {
		return core.ErrInvalidArgument
	}
	return nil
}

func validActivityText(value string) bool {
	return len(value) <= core.MaxExclusionIDBytes && utf8.ValidString(value) &&
		strings.IndexFunc(value, unicode.IsControl) < 0
}

func activityTime(value *time.Time, fallback time.Time) time.Time {
	if value == nil {
		return fallback
	}
	return core.NormalizeTime(*value)
}

func validatePlaybackMutations(mutations []core.PlaybackMutation) error {
	if len(mutations) < 1 || len(mutations) > core.MaxPlaybackMutations {
		return core.ErrInvalidArgument
	}
	for _, mutation := range mutations {
		if err := core.ValidatePlaybackMutation(mutation); err != nil {
			return err
		}
	}
	return nil
}

func playbackSessionStartedEvent(watch core.PlaybackWatch) (core.PlaybackSessionStartedEvent, string, error) {
	event := core.PlaybackSessionStartedEvent{
		WatchID: watch.ID, MediaServerID: watch.MediaServerID, MediaUserID: watch.MediaUserID,
		MediaUsername: watch.Username, ItemID: watch.ItemID, ItemName: watch.ItemName,
		ItemType: watch.ItemType, Client: watch.Client, DeviceID: watch.DeviceID,
		DeviceName: watch.DeviceName, At: core.NormalizeTime(watch.StartedAt),
	}
	encoded, err := json.Marshal(event)
	if err != nil || len(encoded) > core.MaxNotificationPayloadBytes {
		return core.PlaybackSessionStartedEvent{}, "", core.ErrInvalidArgument
	}
	return event, string(encoded), nil
}

func isNewCollectedWatch(watch core.PlaybackWatch) bool {
	return watch.Source != core.WatchSourceImport && watch.CreatedAt.Equal(watch.UpdatedAt)
}

func playbackStoreError(operation string, err error) error {
	return fmt.Errorf("%s: %w", operation, errors.Join(core.ErrPlaybackStore, err))
}

func durationSeconds(value time.Duration) int64      { return int64(value / time.Second) }
func durationMilliseconds(value time.Duration) int64 { return int64(value / time.Millisecond) }

func nullableDurationMilliseconds(value *time.Duration) sql.NullInt64 {
	if value == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: durationMilliseconds(*value), Valid: true}
}

func durationFromNullMilliseconds(value sql.NullInt64) (*time.Duration, error) {
	if !value.Valid {
		return nil, nil
	}
	if value.Int64 < 0 || value.Int64 > int64(math.MaxInt64/time.Millisecond) {
		return nil, errors.New("stored playback runtime is out of range")
	}
	runtime := time.Duration(value.Int64) * time.Millisecond
	return &runtime, nil
}

func nullableInt32(value *int32) sql.NullInt32 {
	if value == nil {
		return sql.NullInt32{}
	}
	return sql.NullInt32{Int32: *value, Valid: true}
}

func int32FromNull(value sql.NullInt32) *int32 {
	if !value.Valid {
		return nil
	}
	return &value.Int32
}

func nullableTime(value *time.Time) sql.NullTime {
	if value == nil {
		return sql.NullTime{}
	}
	return sql.NullTime{Time: core.NormalizeTime(*value), Valid: true}
}

func timeFromNull(value sql.NullTime) *time.Time {
	if !value.Valid {
		return nil
	}
	normalized := core.NormalizeTime(value.Time)
	return &normalized
}

func rollbackPlayback(tx *sql.Tx, result *error) {
	if rollbackErr := tx.Rollback(); rollbackErr != nil && !errors.Is(rollbackErr, sql.ErrTxDone) {
		*result = errors.Join(*result, playbackStoreError("rollback playback transaction", rollbackErr))
	}
}
