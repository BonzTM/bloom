package db

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/BonzTM/bloom/internal/config"
	"github.com/BonzTM/bloom/internal/core"
)

const maxPlaybackPageSize = 1025

// NewPlaybackStore returns the playback persistence seam for the configured engine.
func NewPlaybackStore(pool *sql.DB, driver config.Driver) (core.PlaybackPersistence, error) {
	switch driver {
	case config.DriverSQLite:
		return newSQLitePlaybackStore(pool), nil
	case config.DriverPostgres:
		return newPostgresPlaybackStore(pool), nil
	default:
		return nil, fmt.Errorf("unsupported database driver %q", driver)
	}
}

type storedPlaybackWatch struct {
	id, mediaServerID, mediaServerName, mediaUserID, username string
	deviceID, deviceName, client, serverSessionID             string
	itemID, itemName, itemType, seriesName                    string
	libraryID, libraryName                                    string
	seasonNumber, episodeNumber                               *int32
	playMethod                                                core.PlayMethod
	state                                                     core.WatchState
	startedAt, lastSeenAt                                     time.Time
	endedAt                                                   *time.Time
	activeSeconds, lastPositionMS                             int64
	source                                                    core.WatchSource
	createdAt, updatedAt                                      time.Time
}

func (row storedPlaybackWatch) domain() core.PlaybackWatch {
	return core.PlaybackWatch{
		ID: row.id, MediaServerID: row.mediaServerID, MediaServerName: row.mediaServerName,
		MediaUserID: row.mediaUserID, Username: row.username,
		DeviceID: row.deviceID, DeviceName: row.deviceName, Client: row.client,
		ServerSessionID: row.serverSessionID, ItemID: row.itemID, ItemName: row.itemName,
		ItemType: row.itemType, SeriesName: row.seriesName,
		LibraryID: row.libraryID, LibraryName: row.libraryName,
		SeasonNumber: row.seasonNumber, EpisodeNumber: row.episodeNumber,
		PlayMethod: row.playMethod, State: row.state,
		StartedAt: row.startedAt, LastSeenAt: row.lastSeenAt, EndedAt: row.endedAt,
		ActiveTime:   time.Duration(row.activeSeconds) * time.Second,
		LastPosition: time.Duration(row.lastPositionMS) * time.Millisecond,
		Source:       row.source, CreatedAt: row.createdAt, UpdatedAt: row.updatedAt,
	}
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

func playbackStoreError(operation string, err error) error {
	return fmt.Errorf("%s: %w", operation, errors.Join(core.ErrPlaybackStore, err))
}

func durationSeconds(value time.Duration) int64      { return int64(value / time.Second) }
func durationMilliseconds(value time.Duration) int64 { return int64(value / time.Millisecond) }

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
