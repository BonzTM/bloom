package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/BonzTM/bloom/internal/config"
	"github.com/BonzTM/bloom/internal/core"
)

const (
	statsOverviewLimit = 5
	statsListLimit     = 50
)

type statsAggregateBackend interface {
	totals(context.Context, core.StatsQuery) (core.StatsTotals, error)
	titles(context.Context, core.StatsQuery, core.StatsTitleKind, int32) ([]core.StatsTitle, error)
	users(context.Context, core.StatsQuery, int32) ([]core.StatsUser, error)
}

type statsBreakdownBackend interface {
	breakdowns(context.Context, core.StatsQuery) ([]core.StatsBreakdown, []core.StatsBreakdown, []core.StatsBreakdown, error)
}

type statsBucketBackend interface {
	bucketRows(context.Context, core.StatsQuery) ([]core.StatsBucketRow, error)
}

type statsRecentBackend interface {
	recentWatches(context.Context, core.StatsQuery) ([]core.PlaybackWatch, error)
}

type statsStore struct {
	aggregates statsAggregateBackend
	breakdowns statsBreakdownBackend
	buckets    statsBucketBackend
	recent     statsRecentBackend
}

var _ core.StatsReader = (*statsStore)(nil)

// NewStatsReader returns the statistics reader for the configured engine.
func NewStatsReader(pool *sql.DB, driver config.Driver) (core.StatsReader, error) {
	if pool == nil {
		return nil, fmt.Errorf("build statistics reader: %w", core.ErrInvalidArgument)
	}
	switch driver {
	case config.DriverSQLite:
		backend := newSQLiteStatsBackend(pool)
		return newStatsStore(backend, backend, backend, backend), nil
	case config.DriverPostgres:
		backend := newPostgresStatsBackend(pool)
		return newStatsStore(backend, backend, backend, backend), nil
	default:
		return nil, fmt.Errorf("unsupported database driver %q", driver)
	}
}

func newStatsStore(
	aggregates statsAggregateBackend,
	breakdowns statsBreakdownBackend,
	buckets statsBucketBackend,
	recent statsRecentBackend,
) *statsStore {
	return &statsStore{
		aggregates: aggregates, breakdowns: breakdowns, buckets: buckets, recent: recent,
	}
}

func (s *statsStore) ReadStats(ctx context.Context, query core.StatsQuery) (core.StatsResult, error) {
	if err := query.Validate(); err != nil {
		return core.StatsResult{}, fmt.Errorf("read statistics: %w", err)
	}
	result := core.StatsResult{Window: query.Window}
	var err error
	switch query.Report {
	case core.StatsReportOverview:
		err = s.readOverview(ctx, query, &result)
	case core.StatsReportDaily:
		err = s.readDaily(ctx, query, &result)
	case core.StatsReportPatterns:
		err = s.readPatterns(ctx, query, &result)
	case core.StatsReportTitles:
		result.Titles, err = s.aggregates.titles(ctx, query, query.TitleKind, statsListLimit)
	case core.StatsReportUsers:
		result.Users, err = s.aggregates.users(ctx, query, statsListLimit)
	case core.StatsReportUser:
		err = s.readUser(ctx, query, &result)
	default:
		err = core.ErrInvalidArgument
	}
	if err != nil {
		return core.StatsResult{}, fmt.Errorf("read statistics %s: %w", query.Report, err)
	}
	return result, nil
}

func (s *statsStore) readOverview(ctx context.Context, query core.StatsQuery, result *core.StatsResult) error {
	var err error
	result.Totals, err = s.aggregates.totals(ctx, query)
	if err != nil {
		return err
	}
	for _, kind := range []core.StatsTitleKind{core.StatsTitleMovie, core.StatsTitleSeries, core.StatsTitleOther} {
		rows, titleErr := s.aggregates.titles(ctx, query, kind, statsOverviewLimit)
		if titleErr != nil {
			return titleErr
		}
		result.Titles = append(result.Titles, rows...)
	}
	result.Users, err = s.aggregates.users(ctx, query, statsOverviewLimit)
	if err != nil {
		return err
	}
	result.Clients, result.Devices, result.PlayMethods, err = s.breakdowns.breakdowns(ctx, query)
	return err
}

func (s *statsStore) readDaily(ctx context.Context, query core.StatsQuery, result *core.StatsResult) error {
	rows, err := s.buckets.bucketRows(ctx, query)
	if err != nil {
		return err
	}
	result.Daily = dailyBuckets(query.Window, rows)
	return nil
}

func (s *statsStore) readPatterns(ctx context.Context, query core.StatsQuery, result *core.StatsResult) error {
	rows, err := s.buckets.bucketRows(ctx, query)
	if err != nil {
		return err
	}
	result.Weekdays, result.Hours = patternBuckets(query.Window.Location, rows)
	return nil
}

func (s *statsStore) readUser(ctx context.Context, query core.StatsQuery, result *core.StatsResult) error {
	var err error
	result.Totals, err = s.aggregates.totals(ctx, query)
	if err != nil {
		return err
	}
	for _, kind := range []core.StatsTitleKind{core.StatsTitleMovie, core.StatsTitleSeries, core.StatsTitleOther} {
		rows, titleErr := s.aggregates.titles(ctx, query, kind, statsOverviewLimit)
		if titleErr != nil {
			return titleErr
		}
		result.Titles = append(result.Titles, rows...)
	}
	result.Clients, result.Devices, result.PlayMethods, err = s.breakdowns.breakdowns(ctx, query)
	if err != nil {
		return err
	}
	if err = s.readDaily(ctx, query, result); err != nil {
		return err
	}
	result.Watches, err = s.recent.recentWatches(ctx, query)
	return err
}

func dailyBuckets(window core.StatsWindow, rows []core.StatsBucketRow) []core.StatsDailyBucket {
	byDate := make(map[string]core.StatsDailyBucket, window.Days+1)
	for _, row := range rows {
		date := row.StartedAt.In(window.Location).Format(time.DateOnly)
		bucket := byDate[date]
		bucket.Date, bucket.Plays = date, bucket.Plays+1
		bucket.WatchSeconds += row.WatchSeconds
		byDate[date] = bucket
	}
	buckets := make([]core.StatsDailyBucket, 0, window.Days+1)
	day := civilDateCounter(window.Start, window.Location)
	lastDay := civilDateCounter(window.End.Add(-time.Nanosecond), window.Location)
	for range core.MaxStatsDays + 2 {
		if day.After(lastDay) {
			break
		}
		key := day.Format(time.DateOnly)
		bucket := byDate[key]
		bucket.Date = key
		buckets = append(buckets, bucket)
		day = day.AddDate(0, 0, 1)
	}
	return buckets
}

func civilDateCounter(value time.Time, location *time.Location) time.Time {
	year, month, day := value.In(location).Date()
	return time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
}

func patternBuckets(location *time.Location, rows []core.StatsBucketRow) ([]core.StatsWeekdayBucket, []core.StatsHourBucket) {
	weekdays := make([]core.StatsWeekdayBucket, 7)
	hours := make([]core.StatsHourBucket, 24)
	for index := range weekdays {
		weekdays[index].Weekday = index
	}
	for index := range hours {
		hours[index].Hour = index
	}
	for _, row := range rows {
		local := row.StartedAt.In(location)
		weekdays[int(local.Weekday())].Plays++
		hours[local.Hour()].Plays++
	}
	return weekdays, hours
}

func statsValueInt64(value any) (int64, error) {
	switch typed := value.(type) {
	case int64:
		return typed, nil
	case int32:
		return int64(typed), nil
	case []byte:
		return strconv.ParseInt(string(typed), 10, 64)
	case string:
		return strconv.ParseInt(typed, 10, 64)
	default:
		return 0, fmt.Errorf("unexpected statistics integer %T", value)
	}
}

func statsValueString(value any) (string, error) {
	switch typed := value.(type) {
	case string:
		return typed, nil
	case []byte:
		return string(typed), nil
	default:
		return "", fmt.Errorf("unexpected statistics string %T", value)
	}
}

func statsValueTime(value any) (time.Time, error) {
	switch typed := value.(type) {
	case time.Time:
		return core.NormalizeTime(typed), nil
	case string:
		return parseSQLiteTime(typed)
	case []byte:
		return parseSQLiteTime(string(typed))
	default:
		return time.Time{}, fmt.Errorf("unexpected statistics timestamp %T", value)
	}
}

func statsStoreError(operation string, err error) error {
	return fmt.Errorf("%s: %w", operation, errors.Join(core.ErrPlaybackStore, err))
}

func enforceStatsBucketRowLimit(count int) error {
	if count > core.MaxStatsBucketRows {
		return core.ErrStatsRowLimit
	}
	return nil
}
