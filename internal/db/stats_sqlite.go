package db

import (
	"context"
	"database/sql"
	"errors"

	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/db/sqlite"
)

type sqliteStatsBackend struct{ q *sqlite.Queries }

func newSQLiteStatsBackend(pool *sql.DB) *sqliteStatsBackend {
	return &sqliteStatsBackend{q: sqlite.New(pool)}
}

func (s *sqliteStatsBackend) totals(ctx context.Context, query core.StatsQuery) (core.StatsTotals, error) {
	filter := sqliteStatsFilter(query)
	row, err := s.q.StatsTotals(ctx, sqlite.StatsTotalsParams{
		WindowStart: filter.start, WindowEnd: filter.end, MediaServerFilter: filter.server,
		LibraryFilter: filter.library, UserServerFilter: filter.userServer, MediaUserFilter: filter.user,
	})
	if err != nil {
		return core.StatsTotals{}, statsStoreError("query statistics totals", err)
	}
	seconds, err := statsValueInt64(row.WatchSeconds)
	if err != nil {
		return core.StatsTotals{}, statsStoreError("map statistics totals", err)
	}
	return core.StatsTotals{
		Plays: row.Plays, WatchSeconds: seconds,
		UniqueUsers: row.UniqueUsers, UniqueTitles: row.UniqueTitles,
	}, nil
}

func (s *sqliteStatsBackend) titles(
	ctx context.Context, query core.StatsQuery, kind core.StatsTitleKind, limit int32,
) ([]core.StatsTitle, error) {
	filter := sqliteStatsFilter(query)
	switch kind {
	case core.StatsTitleMovie:
		rows, err := s.q.StatsMovieTitles(ctx, sqlite.StatsMovieTitlesParams{
			WindowStart: filter.start, WindowEnd: filter.end, MediaServerFilter: filter.server,
			LibraryFilter: filter.library, UserServerFilter: filter.userServer,
			MediaUserFilter: filter.user, RowLimit: int64(limit),
		})
		return mapSQLiteMovieTitles(rows, err)
	case core.StatsTitleSeries:
		rows, err := s.q.StatsSeriesTitles(ctx, sqlite.StatsSeriesTitlesParams{
			WindowStart: filter.start, WindowEnd: filter.end, MediaServerFilter: filter.server,
			LibraryFilter: filter.library, UserServerFilter: filter.userServer,
			MediaUserFilter: filter.user, RowLimit: int64(limit),
		})
		return mapSQLiteSeriesTitles(rows, err)
	case core.StatsTitleOther:
		rows, err := s.q.StatsOtherTitles(ctx, sqlite.StatsOtherTitlesParams{
			WindowStart: filter.start, WindowEnd: filter.end, MediaServerFilter: filter.server,
			LibraryFilter: filter.library, UserServerFilter: filter.userServer,
			MediaUserFilter: filter.user, RowLimit: int64(limit),
		})
		return mapSQLiteOtherTitles(rows, err)
	default:
		return nil, core.ErrInvalidArgument
	}
}

func (s *sqliteStatsBackend) users(ctx context.Context, query core.StatsQuery, limit int32) ([]core.StatsUser, error) {
	filter := sqliteStatsFilter(query)
	rows, err := s.q.StatsUsers(ctx, sqlite.StatsUsersParams{
		WindowStart: filter.start, WindowEnd: filter.end,
		MediaServerFilter: filter.server, LibraryFilter: filter.library, RowLimit: int64(limit),
	})
	if err != nil {
		return nil, statsStoreError("query statistics users", err)
	}
	result := make([]core.StatsUser, 0, len(rows))
	for _, row := range rows {
		username, mapErr := statsValueString(row.Username)
		seconds, secondsErr := statsValueInt64(row.WatchSeconds)
		watched, timeErr := statsValueTime(row.LastWatchedAt)
		if mapErr != nil || secondsErr != nil || timeErr != nil {
			return nil, statsStoreError("map statistics users", errors.Join(mapErr, secondsErr, timeErr))
		}
		result = append(result, core.StatsUser{
			MediaServerID: row.MediaServerID, MediaUserID: row.MediaUserID, Username: username,
			Plays: row.Plays, WatchSeconds: seconds, LastWatchedAt: watched,
		})
	}
	return result, nil
}

func (s *sqliteStatsBackend) libraries(
	ctx context.Context, query core.StatsQuery, limit int32,
) ([]core.StatsLibrary, error) {
	filter := sqliteStatsFilter(query)
	rows, err := s.q.StatsLibraries(ctx, sqlite.StatsLibrariesParams{
		WindowStart: filter.start, WindowEnd: filter.end, MediaServerFilter: filter.server,
		LibraryFilter: filter.library, RowLimit: int64(limit),
	})
	if err != nil {
		return nil, statsStoreError("query statistics libraries", err)
	}
	result := make([]core.StatsLibrary, 0, len(rows))
	for _, row := range rows {
		mapped, mapErr := mapStatsLibrary(row.MediaServerID, row.LibraryID, row.LibraryName,
			row.Plays, row.WatchSeconds, row.UniqueUsers, row.UniqueTitles, row.LastWatchedAt)
		if mapErr != nil {
			return nil, statsStoreError("map statistics libraries", mapErr)
		}
		result = append(result, mapped)
	}
	return result, nil
}

func (s *sqliteStatsBackend) breakdowns(
	ctx context.Context, query core.StatsQuery,
) ([]core.StatsBreakdown, []core.StatsBreakdown, []core.StatsBreakdown, error) {
	filter := sqliteStatsFilter(query)
	clients, err := s.q.StatsClients(ctx, filter.params())
	if err != nil {
		return nil, nil, nil, statsStoreError("query statistics clients", err)
	}
	devices, err := s.q.StatsDevices(ctx, sqlite.StatsDevicesParams(filter.params()))
	if err != nil {
		return nil, nil, nil, statsStoreError("query statistics devices", err)
	}
	methods, err := s.q.StatsPlayMethods(ctx, sqlite.StatsPlayMethodsParams(filter.params()))
	if err != nil {
		return nil, nil, nil, statsStoreError("query statistics play methods", err)
	}
	clientRows, err := mapSQLiteBreakdowns(clients)
	if err != nil {
		return nil, nil, nil, err
	}
	deviceRows, err := mapSQLiteDeviceBreakdowns(devices)
	if err != nil {
		return nil, nil, nil, err
	}
	methodRows, err := mapSQLiteMethodBreakdowns(methods)
	return clientRows, deviceRows, methodRows, err
}

func (s *sqliteStatsBackend) bucketRows(ctx context.Context, query core.StatsQuery) ([]core.StatsBucketRow, error) {
	filter := sqliteStatsFilter(query)
	rows, err := s.q.StatsBucketRows(ctx, sqlite.StatsBucketRowsParams{
		WindowStart: filter.start, WindowEnd: filter.end, MediaServerFilter: filter.server,
		LibraryFilter: filter.library, UserServerFilter: filter.userServer, MediaUserFilter: filter.user,
		RowLimit: core.MaxStatsBucketRows + 1,
	})
	if err != nil {
		return nil, statsStoreError("query statistics buckets", err)
	}
	if err := enforceStatsBucketRowLimit(len(rows)); err != nil {
		return nil, err
	}
	result := make([]core.StatsBucketRow, 0, len(rows))
	for _, row := range rows {
		started, parseErr := parseSQLiteTime(row.StartedAt)
		if parseErr != nil {
			return nil, statsStoreError("map statistics buckets", parseErr)
		}
		result = append(result, core.StatsBucketRow{StartedAt: started, WatchSeconds: row.ActiveSeconds})
	}
	return result, nil
}

func (s *sqliteStatsBackend) recentWatches(ctx context.Context, query core.StatsQuery) ([]core.PlaybackWatch, error) {
	rows, err := s.q.StatsUserRecentWatches(ctx, sqlite.StatsUserRecentWatchesParams{
		WindowStart: formatSQLiteTime(query.Window.Start), WindowEnd: formatSQLiteTime(query.Window.End),
		UserServerID: query.UserServerID, MediaUserID: query.MediaUserID, LibraryFilter: query.LibraryID,
	})
	if err != nil {
		return nil, statsStoreError("query statistics recent watches", err)
	}
	result := make([]core.PlaybackWatch, 0, len(rows))
	for _, row := range rows {
		watch, mapErr := sqliteStatsWatch(row)
		if mapErr != nil {
			return nil, statsStoreError("map statistics recent watches", mapErr)
		}
		result = append(result, watch)
	}
	return result, nil
}

type sqliteStatsFilterValues struct{ start, end, server, library, userServer, user string }

func sqliteStatsFilter(query core.StatsQuery) sqliteStatsFilterValues {
	return sqliteStatsFilterValues{
		start: formatSQLiteTime(query.Window.Start), end: formatSQLiteTime(query.Window.End),
		server: query.Window.MediaServerID, library: query.LibraryID,
		userServer: query.UserServerID, user: query.MediaUserID,
	}
}

func (f sqliteStatsFilterValues) params() sqlite.StatsClientsParams {
	return sqlite.StatsClientsParams{
		WindowStart: f.start, WindowEnd: f.end, MediaServerFilter: f.server, LibraryFilter: f.library,
		UserServerFilter: f.userServer, MediaUserFilter: f.user,
	}
}
