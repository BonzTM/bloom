package db

import (
	"time"

	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/db/postgres"
)

func mapPostgresMovieTitles(rows []postgres.StatsMovieTitlesRow, queryErr error) ([]core.StatsTitle, error) {
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

func mapPostgresSeriesTitles(rows []postgres.StatsSeriesTitlesRow, queryErr error) ([]core.StatsTitle, error) {
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

func mapPostgresOtherTitles(rows []postgres.StatsOtherTitlesRow, queryErr error) ([]core.StatsTitle, error) {
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

func mapPostgresBreakdowns(rows []postgres.StatsClientsRow) ([]core.StatsBreakdown, error) {
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

func mapPostgresDeviceBreakdowns(rows []postgres.StatsDevicesRow) ([]core.StatsBreakdown, error) {
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

func mapPostgresMethodBreakdowns(rows []postgres.StatsPlayMethodsRow) ([]core.StatsBreakdown, error) {
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

func postgresStatsWatch(row postgres.StatsUserRecentWatchesRow) (core.PlaybackWatch, error) {
	stream, err := postgresStream(
		row.StreamContainer, row.StreamVideoCodec, row.StreamAudioCodec, row.StreamBitrate,
		row.StreamWidth, row.StreamHeight, row.StreamFramerateHundredths, row.StreamAudioChannels,
		row.StreamIsVideoDirect, row.StreamIsAudioDirect, row.StreamTranscodeReasons,
	).domain()
	if err != nil {
		return core.PlaybackWatch{}, err
	}
	return core.PlaybackWatch{
		ID: row.ID, MediaServerID: row.MediaServerID, MediaServerName: row.MediaServerName,
		MediaUserID: row.MediaUserID, Username: row.Username, DeviceID: row.DeviceID,
		DeviceName: row.DeviceName, Client: row.Client, ServerSessionID: row.ServerSessionID,
		ItemID: row.ItemID, ItemName: row.ItemName, ItemType: row.ItemType, SeriesName: row.SeriesName,
		LibraryID: row.LibraryID, LibraryName: row.LibraryName,
		SeasonNumber: int32FromNull(row.SeasonNumber), EpisodeNumber: int32FromNull(row.EpisodeNumber),
		PlayMethod: core.PlayMethod(row.PlayMethod), State: core.WatchState(row.State),
		Stream:    stream,
		StartedAt: core.NormalizeTime(row.StartedAt), LastSeenAt: core.NormalizeTime(row.LastSeenAt),
		EndedAt: timeFromNull(row.EndedAt), ActiveTime: time.Duration(row.ActiveSeconds) * time.Second,
		LastPosition: time.Duration(row.LastPositionMs) * time.Millisecond,
		Source:       core.WatchSource(row.Source), CreatedAt: core.NormalizeTime(row.CreatedAt),
		UpdatedAt: core.NormalizeTime(row.UpdatedAt),
	}, nil
}
