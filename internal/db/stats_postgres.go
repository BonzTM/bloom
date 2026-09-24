package db

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/db/postgres"
)

type postgresStatsBackend struct{ q *postgres.Queries }

func newPostgresStatsBackend(pool *sql.DB) *postgresStatsBackend {
	return &postgresStatsBackend{q: postgres.New(pool)}
}

func (s *postgresStatsBackend) totals(ctx context.Context, query core.StatsQuery) (core.StatsTotals, error) {
	filter := postgresStatsFilter(query)
	row, err := s.q.StatsTotals(ctx, postgres.StatsTotalsParams{
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

func (s *postgresStatsBackend) titles(
	ctx context.Context, query core.StatsQuery, kind core.StatsTitleKind, limit int32,
) ([]core.StatsTitle, error) {
	filter := postgresStatsFilter(query)
	switch kind {
	case core.StatsTitleMovie:
		rows, err := s.q.StatsMovieTitles(ctx, postgres.StatsMovieTitlesParams{
			WindowStart: filter.start, WindowEnd: filter.end, MediaServerFilter: filter.server,
			LibraryFilter: filter.library, UserServerFilter: filter.userServer,
			MediaUserFilter: filter.user, RowLimit: limit,
		})
		return mapPostgresMovieTitles(rows, err)
	case core.StatsTitleSeries:
		rows, err := s.q.StatsSeriesTitles(ctx, postgres.StatsSeriesTitlesParams{
			WindowStart: filter.start, WindowEnd: filter.end, MediaServerFilter: filter.server,
			LibraryFilter: filter.library, UserServerFilter: filter.userServer,
			MediaUserFilter: filter.user, RowLimit: limit,
		})
		return mapPostgresSeriesTitles(rows, err)
	case core.StatsTitleOther:
		rows, err := s.q.StatsOtherTitles(ctx, postgres.StatsOtherTitlesParams{
			WindowStart: filter.start, WindowEnd: filter.end, MediaServerFilter: filter.server,
			LibraryFilter: filter.library, UserServerFilter: filter.userServer,
			MediaUserFilter: filter.user, RowLimit: limit,
		})
		return mapPostgresOtherTitles(rows, err)
	default:
		return nil, core.ErrInvalidArgument
	}
}

func (s *postgresStatsBackend) users(ctx context.Context, query core.StatsQuery, limit int32) ([]core.StatsUser, error) {
	filter := postgresStatsFilter(query)
	rows, err := s.q.StatsUsers(ctx, postgres.StatsUsersParams{
		WindowStart: filter.start, WindowEnd: filter.end,
		MediaServerFilter: filter.server, LibraryFilter: filter.library, RowLimit: limit,
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

func (s *postgresStatsBackend) libraries(
	ctx context.Context, query core.StatsQuery, limit int32,
) ([]core.StatsLibrary, error) {
	filter := postgresStatsFilter(query)
	rows, err := s.q.StatsLibraries(ctx, postgres.StatsLibrariesParams{
		WindowStart: filter.start, WindowEnd: filter.end, MediaServerFilter: filter.server,
		LibraryFilter: filter.library, RowLimit: limit,
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

func (s *postgresStatsBackend) breakdowns(
	ctx context.Context, query core.StatsQuery,
) ([]core.StatsBreakdown, []core.StatsBreakdown, []core.StatsBreakdown, error) {
	filter := postgresStatsFilter(query)
	clients, err := s.q.StatsClients(ctx, filter.params())
	if err != nil {
		return nil, nil, nil, statsStoreError("query statistics clients", err)
	}
	devices, err := s.q.StatsDevices(ctx, postgres.StatsDevicesParams(filter.params()))
	if err != nil {
		return nil, nil, nil, statsStoreError("query statistics devices", err)
	}
	methods, err := s.q.StatsPlayMethods(ctx, postgres.StatsPlayMethodsParams(filter.params()))
	if err != nil {
		return nil, nil, nil, statsStoreError("query statistics play methods", err)
	}
	clientRows, err := mapPostgresBreakdowns(clients)
	if err != nil {
		return nil, nil, nil, err
	}
	deviceRows, err := mapPostgresDeviceBreakdowns(devices)
	if err != nil {
		return nil, nil, nil, err
	}
	methodRows, err := mapPostgresMethodBreakdowns(methods)
	return clientRows, deviceRows, methodRows, err
}

func (s *postgresStatsBackend) bucketRows(ctx context.Context, query core.StatsQuery) ([]core.StatsBucketRow, error) {
	filter := postgresStatsFilter(query)
	rows, err := s.q.StatsBucketRows(ctx, postgres.StatsBucketRowsParams{
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
		result = append(result, core.StatsBucketRow{
			StartedAt: core.NormalizeTime(row.StartedAt), WatchSeconds: row.ActiveSeconds,
		})
	}
	return result, nil
}

func (s *postgresStatsBackend) recentWatches(ctx context.Context, query core.StatsQuery) ([]core.PlaybackWatch, error) {
	rows, err := s.q.StatsUserRecentWatches(ctx, postgres.StatsUserRecentWatchesParams{
		WindowStart: query.Window.Start, WindowEnd: query.Window.End,
		UserServerID: query.UserServerID, MediaUserID: query.MediaUserID, LibraryFilter: query.LibraryID,
	})
	if err != nil {
		return nil, statsStoreError("query statistics recent watches", err)
	}
	result := make([]core.PlaybackWatch, 0, len(rows))
	for _, row := range rows {
		watch, mapErr := postgresStatsWatch(row)
		if mapErr != nil {
			return nil, statsStoreError("map statistics recent watches", mapErr)
		}
		result = append(result, watch)
	}
	return result, nil
}

type postgresStatsFilterValues struct {
	start, end       time.Time
	server, library  string
	userServer, user string
}

func postgresStatsFilter(query core.StatsQuery) postgresStatsFilterValues {
	return postgresStatsFilterValues{
		start: query.Window.Start, end: query.Window.End,
		server: query.Window.MediaServerID, library: query.LibraryID,
		userServer: query.UserServerID, user: query.MediaUserID,
	}
}

func (f postgresStatsFilterValues) params() postgres.StatsClientsParams {
	return postgres.StatsClientsParams{
		WindowStart: f.start, WindowEnd: f.end, MediaServerFilter: f.server, LibraryFilter: f.library,
		UserServerFilter: f.userServer, MediaUserFilter: f.user,
	}
}
