package stats

import (
	"testing"
	"time"

	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/testutil"
)

func TestResultCacheEvictsLeastRecentlyUsedAndClones(t *testing.T) {
	t.Parallel()
	clock := testutil.NewFakeClock(time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC))
	cache := newResultCache(clock, 2, time.Minute)
	first := cacheKey{days: 1, kind: "first"}
	second := cacheKey{days: 1, kind: "second"}
	third := cacheKey{days: 1, kind: "third"}
	cache.put(first, core.StatsResult{Titles: []core.StatsTitle{{Name: "first"}}})
	cache.put(second, core.StatsResult{Titles: []core.StatsTitle{{Name: "second"}}})
	value, ok := cache.get(first)
	if !ok {
		t.Fatal("first cache entry missed")
	}
	value.Titles[0].Name = "mutated"
	cache.put(third, core.StatsResult{})
	if _, found := cache.get(second); found {
		t.Fatal("least-recently-used entry was retained")
	}
	value, ok = cache.get(first)
	if !ok || value.Titles[0].Name != "first" {
		t.Fatalf("cached clone = %+v, %t", value, ok)
	}
	if len(cache.entries) != 2 {
		t.Fatalf("cache size = %d, want 2", len(cache.entries))
	}
}

func TestResultCacheKeySeparatesReportSubtypes(t *testing.T) {
	t.Parallel()
	window, err := core.NewStatsWindow(30, "", "UTC", time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	keys := make(map[cacheKey]bool)
	for _, query := range []core.StatsQuery{
		{Window: window, Report: core.StatsReportTitles, TitleKind: core.StatsTitleMovie},
		{Window: window, Report: core.StatsReportTitles, TitleKind: core.StatsTitleSeries},
		{Window: window, Report: core.StatsReportUser, UserServerID: "11111111-1111-4111-8111-111111111111", MediaUserID: "one"},
		{Window: window, Report: core.StatsReportUser, UserServerID: "11111111-1111-4111-8111-111111111111", MediaUserID: "two"},
	} {
		key := resultCacheKey(query)
		if keys[key] {
			t.Fatalf("duplicate cache key: %+v", key)
		}
		keys[key] = true
	}
}
