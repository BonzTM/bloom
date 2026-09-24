package stats_test

import (
	"context"
	"testing"
	"time"

	"github.com/BonzTM/bloom/internal/core"
	statsapp "github.com/BonzTM/bloom/internal/stats"
	"github.com/BonzTM/bloom/internal/testutil"
)

type countingReader struct{ calls int }

func (r *countingReader) ReadStats(_ context.Context, query core.StatsQuery) (core.StatsResult, error) {
	r.calls++
	return core.StatsResult{Window: query.Window, Totals: core.StatsTotals{Plays: int64(r.calls)}}, nil
}

type statsObserver struct{ calls int }

func (o *statsObserver) ObserveStatsQuery(string, string, float64) { o.calls++ }

type boundaryReader struct {
	calls int
	watch time.Time
}

func (r *boundaryReader) ReadStats(_ context.Context, query core.StatsQuery) (core.StatsResult, error) {
	r.calls++
	plays := int64(0)
	if !r.watch.Before(query.Window.Start) && r.watch.Before(query.Window.End) {
		plays = 1
	}
	return core.StatsResult{Window: query.Window, Totals: core.StatsTotals{Plays: plays}}, nil
}

func TestServiceCacheHitExpiryAndDisable(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		name string
		ttl  time.Duration
		want int
	}{
		{name: "expires", ttl: 30 * time.Second, want: 2},
		{name: "disabled", ttl: 0, want: 3},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			clock := testutil.NewFakeClock(now)
			reader, observer := &countingReader{}, &statsObserver{}
			service, err := statsapp.NewService(reader, clock, test.ttl, observer)
			if err != nil {
				t.Fatal(err)
			}
			window, err := core.NewStatsWindow(30, "", "UTC", now)
			if err != nil {
				t.Fatal(err)
			}
			query := core.StatsQuery{Window: window, Report: core.StatsReportOverview}
			for call := range 3 {
				result, readErr := service.ReadStats(t.Context(), query)
				if readErr != nil || result.Totals.Plays < 1 {
					t.Fatalf("call %d = %+v, %v", call, result, readErr)
				}
				if call == 1 {
					clock.Advance(test.ttl)
				}
			}
			if reader.calls != test.want || observer.calls != test.want {
				t.Fatalf("calls = reader %d observer %d, want %d", reader.calls, observer.calls, test.want)
			}
		})
	}
}

func TestServiceCanonicalWindowMovesAtCacheBoundary(t *testing.T) {
	t.Parallel()
	const ttl = 30 * time.Second
	now := time.Date(2026, 9, 23, 12, 0, 29, 0, time.UTC)
	clock := testutil.NewFakeClock(now)
	reader := &boundaryReader{watch: now.Add(-14 * time.Second)}
	service, err := statsapp.NewService(reader, clock, ttl, &statsObserver{})
	if err != nil {
		t.Fatal(err)
	}
	first := readOverviewAtClock(t, service, clock)
	clock.Advance(2 * time.Second)
	second := readOverviewAtClock(t, service, clock)
	if first.Totals.Plays != 0 || second.Totals.Plays != 1 || reader.calls != 2 {
		t.Fatalf("boundary results = %d then %d, reader calls %d", first.Totals.Plays, second.Totals.Plays, reader.calls)
	}
	wantFirstEnd := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)
	wantSecondEnd := wantFirstEnd.Add(ttl)
	if !first.Window.End.Equal(wantFirstEnd) || !second.Window.End.Equal(wantSecondEnd) {
		t.Fatalf("canonical ends = %s then %s", first.Window.End, second.Window.End)
	}
}

func readOverviewAtClock(
	t *testing.T, service *statsapp.Service, clock *testutil.FakeClock,
) core.StatsResult {
	t.Helper()
	window, err := core.NewStatsWindow(1, "", "UTC", clock.Now())
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.ReadStats(t.Context(), core.StatsQuery{Window: window, Report: core.StatsReportOverview})
	if err != nil {
		t.Fatal(err)
	}
	return result
}
