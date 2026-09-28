package core

import (
	"testing"
	"time"
)

func TestGroupTimelineGroupsOnlyConsecutiveNearbyItems(t *testing.T) {
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	ended := now.Add(time.Hour)
	watches := []PlaybackWatch{
		{ID: "00000000-0000-4000-8000-000000000004", ItemID: "movie", StartedAt: now, EndedAt: &ended, ActiveTime: time.Minute},
		{ID: "00000000-0000-4000-8000-000000000003", ItemID: "movie", StartedAt: now.Add(-time.Hour), ActiveTime: 2 * time.Minute},
		{ID: "00000000-0000-4000-8000-000000000002", ItemID: "other", StartedAt: now.Add(-2 * time.Hour), ActiveTime: 3 * time.Minute},
		{ID: "00000000-0000-4000-8000-000000000001", ItemID: "movie", StartedAt: now.Add(-3 * time.Hour), ActiveTime: 4 * time.Minute},
	}
	page := GroupTimeline(watches, 10, DefaultTimelineGap)
	if len(page.Items) != 3 || page.Items[0].PlayCount != 2 || page.Items[0].ActiveTime != 3*time.Minute ||
		!page.Items[0].FirstStartedAt.Equal(now.Add(-time.Hour)) || page.Items[0].LastEndedAt != &ended {
		t.Fatalf("timeline = %+v", page)
	}
}

func TestGroupTimelineBoundsWork(t *testing.T) {
	watch := PlaybackWatch{ID: "00000000-0000-4000-8000-000000000001", ItemID: "item", StartedAt: time.Now()}
	for _, testCase := range []struct {
		name  string
		count int
		limit int
		gap   time.Duration
	}{
		{name: "zero page", count: 1, limit: 0, gap: time.Hour},
		{name: "large page", count: 1, limit: MaxActivityPageSize + 1, gap: time.Hour},
		{name: "zero gap", count: 1, limit: 1},
		{name: "large gap", count: 1, limit: 1, gap: MaxTimelineGap + time.Second},
		{name: "large fetch", count: MaxTimelineWatchFetch + 1, limit: 1, gap: time.Hour},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			watches := make([]PlaybackWatch, testCase.count)
			for index := range watches {
				watches[index] = watch
			}
			if page := GroupTimeline(watches, testCase.limit, testCase.gap); len(page.Items) != 0 {
				t.Fatalf("items = %d, want zero", len(page.Items))
			}
		})
	}
}
