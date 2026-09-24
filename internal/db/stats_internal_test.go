package db

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/BonzTM/bloom/internal/core"
)

func TestEnforceStatsBucketRowLimit(t *testing.T) {
	t.Parallel()
	if err := enforceStatsBucketRowLimit(core.MaxStatsBucketRows); err != nil {
		t.Fatalf("maximum row count rejected: %v", err)
	}
	if err := enforceStatsBucketRowLimit(core.MaxStatsBucketRows + 1); !errors.Is(err, core.ErrStatsRowLimit) {
		t.Fatalf("overflow error = %v, want ErrStatsRowLimit", err)
	}
}

func TestDailyBucketsEnumerateCivilDatesAcrossDST(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, zone string
		end        time.Time
		rows       []core.StatsBucketRow
		want       []core.StatsDailyBucket
	}{
		{
			name: "Santiago missing midnight", zone: "America/Santiago",
			end: time.Date(2025, 9, 8, 4, 0, 0, 0, time.UTC),
			rows: statsBucketRows(
				time.Date(2025, 9, 7, 3, 30, 0, 0, time.UTC),
				time.Date(2025, 9, 7, 4, 30, 0, 0, time.UTC),
				time.Date(2025, 9, 8, 3, 30, 0, 0, time.UTC),
			),
			want: statsDailyBuckets("2025-09-06", "2025-09-07", "2025-09-08"),
		},
		{
			name: "Los Angeles spring forward", zone: "America/Los_Angeles",
			end: time.Date(2025, 3, 10, 12, 0, 0, 0, time.UTC),
			rows: statsBucketRows(
				time.Date(2025, 3, 8, 20, 0, 0, 0, time.UTC),
				time.Date(2025, 3, 9, 10, 30, 0, 0, time.UTC),
				time.Date(2025, 3, 10, 8, 0, 0, 0, time.UTC),
			),
			want: statsDailyBuckets("2025-03-08", "2025-03-09", "2025-03-10"),
		},
		{
			name: "Los Angeles fall back", zone: "America/Los_Angeles",
			end: time.Date(2025, 11, 3, 12, 0, 0, 0, time.UTC),
			rows: statsBucketRows(
				time.Date(2025, 11, 1, 20, 0, 0, 0, time.UTC),
				time.Date(2025, 11, 2, 8, 30, 0, 0, time.UTC),
				time.Date(2025, 11, 3, 8, 0, 0, 0, time.UTC),
			),
			want: statsDailyBuckets("2025-11-01", "2025-11-02", "2025-11-03"),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			window, err := core.NewStatsWindow(2, "", test.zone, test.end)
			if err != nil {
				t.Fatal(err)
			}
			if got := dailyBuckets(window, test.rows); !reflect.DeepEqual(got, test.want) {
				t.Fatalf("daily buckets = %+v, want %+v", got, test.want)
			}
		})
	}
}

func statsBucketRows(instants ...time.Time) []core.StatsBucketRow {
	rows := make([]core.StatsBucketRow, 0, len(instants))
	for _, instant := range instants {
		rows = append(rows, core.StatsBucketRow{StartedAt: instant, WatchSeconds: 10})
	}
	return rows
}

func statsDailyBuckets(dates ...string) []core.StatsDailyBucket {
	buckets := make([]core.StatsDailyBucket, 0, len(dates))
	for _, date := range dates {
		buckets = append(buckets, core.StatsDailyBucket{Date: date, Plays: 1, WatchSeconds: 10})
	}
	return buckets
}
