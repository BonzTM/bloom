package core

import (
	"context"
	"errors"
	"fmt"
	"time"
)

const (
	// MinStatsDays is the smallest accepted statistics window.
	MinStatsDays = 1
	// MaxStatsDays bounds every statistics query to one year.
	MaxStatsDays = 365
	// MaxStatsZoneBytes bounds untrusted IANA time-zone names.
	MaxStatsZoneBytes = 64
	// MaxStatsBucketRows bounds zone-dependent in-memory aggregation input.
	MaxStatsBucketRows = 100_000
)

// ErrStatsRowLimit means a zone-dependent report exceeded its safe row bound.
var ErrStatsRowLimit = errors.New("statistics row limit exceeded")

// StatsReport identifies one bounded dashboard projection.
type StatsReport string

const (
	// StatsReportOverview selects dashboard totals, rankings, and breakdowns.
	StatsReportOverview StatsReport = "overview"
	// StatsReportDaily selects local-calendar daily buckets.
	StatsReportDaily StatsReport = "daily"
	// StatsReportPatterns selects local weekday and hour buckets.
	StatsReportPatterns StatsReport = "patterns"
	// StatsReportTitles selects one ranked title kind.
	StatsReportTitles StatsReport = "titles"
	// StatsReportUsers selects ranked media-server users.
	StatsReportUsers StatsReport = "users"
	// StatsReportUser selects one media-server user's drill-down.
	StatsReportUser StatsReport = "user"
)

// StatsTitleKind selects Bloom's stable title grouping rule.
type StatsTitleKind string

const (
	// StatsTitleMovie groups movies by media-server item ID.
	StatsTitleMovie StatsTitleKind = "movie"
	// StatsTitleSeries groups episodes by series name.
	StatsTitleSeries StatsTitleKind = "series"
	// StatsTitleOther groups remaining watches by item type.
	StatsTitleOther StatsTitleKind = "other"
)

// Valid reports whether kind is a supported title grouping.
func (k StatsTitleKind) Valid() bool {
	return k == StatsTitleMovie || k == StatsTitleSeries || k == StatsTitleOther
}

// StatsWindow is a bounded half-open interval [Start, End) and bucket zone.
type StatsWindow struct {
	Days          int
	Start         time.Time
	End           time.Time
	MediaServerID string
	Zone          string
	Location      *time.Location
}

// NewStatsWindow validates a rolling whole-day window ending at now.
func NewStatsWindow(days int, mediaServerID, zone string, now time.Time) (StatsWindow, error) {
	if days < MinStatsDays || days > MaxStatsDays || now.IsZero() {
		return StatsWindow{}, fmt.Errorf("statistics window: %w", ErrInvalidArgument)
	}
	if mediaServerID != "" && !ValidID(mediaServerID) {
		return StatsWindow{}, fmt.Errorf("statistics media server: %w", ErrInvalidArgument)
	}
	if zone == "" {
		zone = "UTC"
	}
	if len(zone) > MaxStatsZoneBytes {
		return StatsWindow{}, fmt.Errorf("statistics time zone: %w", ErrInvalidArgument)
	}
	location, err := time.LoadLocation(zone)
	if err != nil {
		return StatsWindow{}, fmt.Errorf("statistics time zone: %w", ErrInvalidArgument)
	}
	end := NormalizeTime(now)
	return StatsWindow{
		Days: days, Start: end.Add(-time.Duration(days) * 24 * time.Hour), End: end,
		MediaServerID: mediaServerID, Zone: zone, Location: location,
	}, nil
}

// StatsQuery selects one report and optional title or user drill-down.
type StatsQuery struct {
	Window       StatsWindow
	Report       StatsReport
	TitleKind    StatsTitleKind
	MediaUserID  string
	UserServerID string
}

// Validate enforces report-specific parameters.
func (q StatsQuery) Validate() error {
	if q.Window.Days < MinStatsDays || q.Window.Days > MaxStatsDays ||
		q.Window.Start.IsZero() || q.Window.End.IsZero() || q.Window.Location == nil ||
		!q.Window.Start.Before(q.Window.End) ||
		q.Window.End.Sub(q.Window.Start) != time.Duration(q.Window.Days)*24*time.Hour ||
		q.Window.Zone == "" || len(q.Window.Zone) > MaxStatsZoneBytes ||
		q.Window.Location.String() != q.Window.Zone ||
		(q.Window.MediaServerID != "" && !ValidID(q.Window.MediaServerID)) {
		return ErrInvalidArgument
	}
	switch q.Report {
	case StatsReportOverview, StatsReportDaily, StatsReportPatterns, StatsReportUsers:
		return nil
	case StatsReportTitles:
		if !q.TitleKind.Valid() {
			return ErrInvalidArgument
		}
		return nil
	case StatsReportUser:
		if !ValidID(q.UserServerID) || q.MediaUserID == "" {
			return ErrInvalidArgument
		}
		return nil
	default:
		return ErrInvalidArgument
	}
}

// StatsTotals contains the dashboard headline counters.
type StatsTotals struct {
	Plays        int64
	WatchSeconds int64
	UniqueUsers  int64
	UniqueTitles int64
}

// StatsTitle is one ranked title grouping.
type StatsTitle struct {
	Kind          StatsTitleKind
	MediaServerID string
	Key           string
	Name          string
	Plays         int64
	WatchSeconds  int64
	LastWatchedAt time.Time
}

// StatsUser is one ranked media-server user.
type StatsUser struct {
	MediaServerID string
	MediaUserID   string
	Username      string
	Plays         int64
	WatchSeconds  int64
	LastWatchedAt time.Time
}

// StatsBreakdown is one client, device, or play-method grouping.
type StatsBreakdown struct {
	Name         string
	Plays        int64
	WatchSeconds int64
}

// StatsDailyBucket is one local calendar day.
type StatsDailyBucket struct {
	Date         string
	Plays        int64
	WatchSeconds int64
}

// StatsWeekdayBucket is one local weekday, Sunday zero through Saturday six.
type StatsWeekdayBucket struct {
	Weekday int
	Plays   int64
}

// StatsHourBucket is one local hour, zero through 23.
type StatsHourBucket struct {
	Hour  int
	Plays int64
}

// StatsBucketRow is bounded SQL input for zone-dependent aggregation.
type StatsBucketRow struct {
	StartedAt    time.Time
	WatchSeconds int64
}

// StatsResult contains the typed projection selected by StatsQuery.Report.
type StatsResult struct {
	Window      StatsWindow
	Totals      StatsTotals
	Titles      []StatsTitle
	Users       []StatsUser
	Clients     []StatsBreakdown
	Devices     []StatsBreakdown
	PlayMethods []StatsBreakdown
	Daily       []StatsDailyBucket
	Weekdays    []StatsWeekdayBucket
	Hours       []StatsHourBucket
	Watches     []PlaybackWatch
}

// StatsReader is the consumer-owned read seam for dashboard projections.
type StatsReader interface {
	ReadStats(ctx context.Context, query StatsQuery) (StatsResult, error)
}
