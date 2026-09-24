package db

import (
	"fmt"
	"time"

	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/db/sqlite"
)

func mapSQLiteMovieTitles(rows []sqlite.StatsMovieTitlesRow, queryErr error) ([]core.StatsTitle, error) {
	if queryErr != nil {
		return nil, statsStoreError("query statistics movie titles", queryErr)
	}
	result := make([]core.StatsTitle, 0, len(rows))
	for _, row := range rows {
		mapped, err := mapStatsTitle(core.StatsTitleMovie, row.MediaServerID, row.TitleKey,
			row.TitleName, row.Plays, row.WatchSeconds, row.LastWatchedAt)
		if err != nil {
			return nil, statsStoreError("map statistics movie titles", err)
		}
		result = append(result, mapped)
	}
	return result, nil
}

func mapSQLiteSeriesTitles(rows []sqlite.StatsSeriesTitlesRow, queryErr error) ([]core.StatsTitle, error) {
	if queryErr != nil {
		return nil, statsStoreError("query statistics series titles", queryErr)
	}
	result := make([]core.StatsTitle, 0, len(rows))
	for _, row := range rows {
		mapped, err := mapStatsTitle(core.StatsTitleSeries, row.MediaServerID, row.TitleKey,
			row.TitleName, row.Plays, row.WatchSeconds, row.LastWatchedAt)
		if err != nil {
			return nil, statsStoreError("map statistics series titles", err)
		}
		result = append(result, mapped)
	}
	return result, nil
}

func mapSQLiteOtherTitles(rows []sqlite.StatsOtherTitlesRow, queryErr error) ([]core.StatsTitle, error) {
	if queryErr != nil {
		return nil, statsStoreError("query statistics other titles", queryErr)
	}
	result := make([]core.StatsTitle, 0, len(rows))
	for _, row := range rows {
		mapped, err := mapStatsTitle(core.StatsTitleOther, row.MediaServerID, row.TitleKey,
			row.TitleName, row.Plays, row.WatchSeconds, row.LastWatchedAt)
		if err != nil {
			return nil, statsStoreError("map statistics other titles", err)
		}
		result = append(result, mapped)
	}
	return result, nil
}

func mapStatsTitle(
	kind core.StatsTitleKind, serverID, key string, name any,
	plays int64, watchSeconds, lastWatched any,
) (core.StatsTitle, error) {
	mappedName, err := statsValueString(name)
	if err != nil {
		return core.StatsTitle{}, err
	}
	seconds, err := statsValueInt64(watchSeconds)
	if err != nil {
		return core.StatsTitle{}, err
	}
	watched, err := statsValueTime(lastWatched)
	if err != nil {
		return core.StatsTitle{}, err
	}
	return core.StatsTitle{
		Kind: kind, MediaServerID: serverID, Key: key, Name: mappedName,
		Plays: plays, WatchSeconds: seconds, LastWatchedAt: watched,
	}, nil
}

func mapSQLiteBreakdowns(rows []sqlite.StatsClientsRow) ([]core.StatsBreakdown, error) {
	result := make([]core.StatsBreakdown, 0, len(rows))
	for _, row := range rows {
		mapped, err := mapStatsBreakdown(row.Name, row.Plays, row.WatchSeconds)
		if err != nil {
			return nil, statsStoreError("map statistics clients", err)
		}
		result = append(result, mapped)
	}
	return result, nil
}

func mapSQLiteDeviceBreakdowns(rows []sqlite.StatsDevicesRow) ([]core.StatsBreakdown, error) {
	result := make([]core.StatsBreakdown, 0, len(rows))
	for _, row := range rows {
		mapped, err := mapStatsBreakdown(row.Name, row.Plays, row.WatchSeconds)
		if err != nil {
			return nil, statsStoreError("map statistics devices", err)
		}
		result = append(result, mapped)
	}
	return result, nil
}

func mapSQLiteMethodBreakdowns(rows []sqlite.StatsPlayMethodsRow) ([]core.StatsBreakdown, error) {
	result := make([]core.StatsBreakdown, 0, len(rows))
	for _, row := range rows {
		mapped, err := mapStatsBreakdown(row.Name, row.Plays, row.WatchSeconds)
		if err != nil {
			return nil, statsStoreError("map statistics play methods", err)
		}
		result = append(result, mapped)
	}
	return result, nil
}

func mapStatsBreakdown(name string, plays int64, watchSeconds any) (core.StatsBreakdown, error) {
	seconds, err := statsValueInt64(watchSeconds)
	if err != nil {
		return core.StatsBreakdown{}, err
	}
	return core.StatsBreakdown{Name: name, Plays: plays, WatchSeconds: seconds}, nil
}

func sqliteStatsWatch(row sqlite.StatsUserRecentWatchesRow) (core.PlaybackWatch, error) {
	started, err := parseSQLiteTime(row.StartedAt)
	if err != nil {
		return core.PlaybackWatch{}, fmt.Errorf("started at: %w", err)
	}
	lastSeen, err := parseSQLiteTime(row.LastSeenAt)
	if err != nil {
		return core.PlaybackWatch{}, fmt.Errorf("last seen at: %w", err)
	}
	created, err := parseSQLiteTime(row.CreatedAt)
	if err != nil {
		return core.PlaybackWatch{}, fmt.Errorf("created at: %w", err)
	}
	updated, err := parseSQLiteTime(row.UpdatedAt)
	if err != nil {
		return core.PlaybackWatch{}, fmt.Errorf("updated at: %w", err)
	}
	season, err := sqliteInt32(row.SeasonNumber)
	if err != nil {
		return core.PlaybackWatch{}, fmt.Errorf("season number: %w", err)
	}
	episode, err := sqliteInt32(row.EpisodeNumber)
	if err != nil {
		return core.PlaybackWatch{}, fmt.Errorf("episode number: %w", err)
	}
	ended, err := sqliteOptionalTime(row.EndedAt)
	if err != nil {
		return core.PlaybackWatch{}, fmt.Errorf("ended at: %w", err)
	}
	return core.PlaybackWatch{
		ID: row.ID, MediaServerID: row.MediaServerID, MediaServerName: row.MediaServerName,
		MediaUserID: row.MediaUserID, Username: row.Username, DeviceID: row.DeviceID,
		DeviceName: row.DeviceName, Client: row.Client, ServerSessionID: row.ServerSessionID,
		ItemID: row.ItemID, ItemName: row.ItemName, ItemType: row.ItemType, SeriesName: row.SeriesName,
		SeasonNumber: season, EpisodeNumber: episode,
		PlayMethod: core.PlayMethod(row.PlayMethod), State: core.WatchState(row.State),
		StartedAt: started, LastSeenAt: lastSeen, EndedAt: ended,
		ActiveTime:   time.Duration(row.ActiveSeconds) * time.Second,
		LastPosition: time.Duration(row.LastPositionMs) * time.Millisecond,
		Source:       core.WatchSource(row.Source), CreatedAt: created, UpdatedAt: updated,
	}, nil
}
