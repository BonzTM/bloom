package db_test

import (
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/BonzTM/bloom/internal/config"
	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/db"
)

const activityServerID = "87000000-0000-4000-8000-000000000004"

func runActivityEngineTests(t *testing.T, pool *sql.DB, driver config.Driver) {
	t.Helper()
	t.Run("activity filters exclusions and uses literal search", func(t *testing.T) {
		testActivityVisibility(t, pool, driver)
	})
	t.Run("activity and timeline plans use keyset indexes", func(t *testing.T) {
		testActivityPlans(t, pool, driver)
	})
	t.Run("activity search has ASCII parity", func(t *testing.T) {
		testActivitySearchParity(t, pool, driver)
	})
	t.Run("activity cursor is stable across equal start times", func(t *testing.T) {
		testActivityCursorBoundary(t, pool, driver)
	})
}

func testActivityVisibility(t *testing.T, pool *sql.DB, driver config.Driver) {
	t.Helper()
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	createCatalogServer(t, pool, driver, activityServerID, "Activity Suite", now)
	playback := newPlaybackTestStore(t, pool, driver)
	visible := activityWatch(t, activityServerID, "visible", "100%_Literal", now)
	excludedUser := activityWatch(t, activityServerID, "excluded", "Hidden User", now.Add(-time.Minute))
	excludedUser.MediaUserID = "excluded-user"
	excludedLibrary := activityWatch(t, activityServerID, "library", "Hidden Library", now.Add(-2*time.Minute))
	excludedLibrary.LibraryID = "excluded-library"
	if err := playback.SaveWatches(t.Context(), playbackMutations(visible, excludedUser, excludedLibrary)); err != nil {
		t.Fatalf("SaveWatches: %v", err)
	}
	exclusions, err := db.NewExclusionStore(pool, driver)
	if err != nil {
		t.Fatalf("NewExclusionStore: %v", err)
	}
	settings := core.MediaServerExclusions{
		MediaServerID: activityServerID,
		MediaUserIDs:  []string{"excluded-user"}, LibraryIDs: []string{"excluded-library"},
	}
	if err = exclusions.ReplaceExclusions(t.Context(), settings); err != nil {
		t.Fatalf("ReplaceExclusions: %v", err)
	}
	activity, ok := playback.(core.ActivityStore)
	if !ok {
		t.Fatal("playback store does not implement ActivityStore")
	}
	watches, err := activity.ListActivity(t.Context(), core.ActivityQuery{Search: "%_", Limit: 10})
	if err != nil || len(watches) != 1 || watches[0].ID != visible.ID {
		t.Fatalf("ListActivity = %+v, %v", watches, err)
	}
	timeline, err := activity.ListTimelineWatches(t.Context(), core.TimelineWatchQuery{
		MediaServerID: activityServerID, MediaUserID: "excluded-user", Limit: 10,
	})
	if err != nil || len(timeline) != 0 {
		t.Fatalf("excluded timeline = %+v, %v", timeline, err)
	}
	nowPlaying, err := playback.ListWatches(t.Context(), core.PlaybackQuery{Mode: core.PlaybackQueryNow, PageSize: 10})
	if err != nil || len(nowPlaying) != 1 || nowPlaying[0].ID != visible.ID {
		t.Fatalf("visible playback = %+v, %v", nowPlaying, err)
	}
	if _, err = playback.ListWatchPositions(t.Context(), excludedUser.ID); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("excluded watch positions = %v, want not found", err)
	}
}

func activityWatch(t *testing.T, serverID, suffix, name string, started time.Time) core.PlaybackWatch {
	t.Helper()
	watch := playbackStoreWatch(t, serverID, started)
	watch.ID = "88000000-0000-4000-8000-" + activityIDDigits(suffix)
	watch.ServerSessionID = "activity-" + suffix
	watch.ItemID = "item-" + suffix
	watch.ItemName = name
	watch.LibraryID, watch.LibraryName = "library", "Library"
	return watch
}

func activityIDDigits(value string) string {
	var sum int
	for _, char := range value {
		sum += int(char)
	}
	return strings.Repeat("0", 9) + string(rune('0'+sum%10)) + "01"
}

func testActivityPlans(t *testing.T, pool *sql.DB, driver config.Driver) {
	t.Helper()
	seedCatalogPlanFixture(t, pool, driver)
	analyzeCatalogPlanTables(t, pool, driver)
	statement, err := db.ActivityStatement(driver)
	if err != nil {
		t.Fatalf("ActivityStatement: %v", err)
	}
	selectiveIndex := "watches_started_idx"
	if driver == config.DriverPostgres {
		selectiveIndex = "watches_server_user_started_idx"
	}
	tests := []struct {
		name, index string
		filters     activityPlanFilters
	}{
		{name: "empty filters", index: "watches_started_idx"},
		{name: "selective user", index: selectiveIndex, filters: activityPlanFilters{
			serverID: activityServerID, userID: "visible",
		}},
		{name: "title search", index: "watches_started_idx", filters: activityPlanFilters{search: "literal"}},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			plan := explainCatalogQuery(t, pool, driver, statement, activityPlanArgs(testCase.filters)...)
			assertActivityPlan(t, plan, testCase.index, testCase.name)
		})
	}
}

func assertActivityPlan(t *testing.T, plan, index, shape string) {
	t.Helper()
	if !strings.Contains(plan, index) || strings.Contains(plan, "TEMP B-TREE") ||
		strings.Contains(plan, "Sort") || strings.Contains(plan, "Seq Scan") ||
		(strings.Contains(plan, "SCAN w") && !strings.Contains(plan, "SCAN w USING INDEX")) {
		t.Fatalf("%s activity plan = %q; want %s without sort or sequential scan", shape, plan, index)
	}
}

type activityPlanFilters struct{ serverID, userID, search string }

func activityPlanArgs(filters activityPlanFilters) []any {
	minimum := time.Date(1, 1, 1, 0, 0, 0, 0, time.UTC)
	maximum := time.Date(9999, 12, 31, 23, 59, 59, 999999000, time.UTC)
	return []any{
		filters.serverID, filters.userID, "", "", "", "", "", "", "",
		0, minimum, 0, maximum, filters.search, maximum, "z", 50,
	}
}

func testActivitySearchParity(t *testing.T, pool *sql.DB, driver config.Driver) {
	t.Helper()
	const serverID = "87000000-0000-4000-8000-000000000002"
	now := time.Date(2026, 9, 28, 13, 0, 0, 0, time.UTC)
	createCatalogServer(t, pool, driver, serverID, "Activity Search", now)
	playback := newPlaybackTestStore(t, pool, driver)
	ascii := activityWatch(t, serverID, "ascii", "ASCII Mixed", now)
	unicode := activityWatch(t, serverID, "unicode", "Éclair", now.Add(-time.Minute))
	if err := playback.SaveWatches(t.Context(), playbackMutations(ascii, unicode)); err != nil {
		t.Fatalf("SaveWatches: %v", err)
	}
	activity, ok := playback.(core.ActivityStore)
	if !ok {
		t.Fatal("playback store does not implement ActivityStore")
	}
	assertActivitySearchIDs(t, activity, "ascii mixed", ascii.ID)
	assertActivitySearchIDs(t, activity, "éclair")
	assertActivitySearchIDs(t, activity, "Éclair", unicode.ID)
}

func assertActivitySearchIDs(t *testing.T, activity core.ActivityStore, search string, want ...string) {
	t.Helper()
	watches, err := activity.ListActivity(t.Context(), core.ActivityQuery{Search: search, Limit: 10})
	if err != nil {
		t.Fatalf("ListActivity search %q: %v", search, err)
	}
	got := make([]string, 0, len(watches))
	for _, watch := range watches {
		got = append(got, watch.ID)
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("ListActivity search %q IDs = %v, want %v", search, got, want)
	}
}

func testActivityCursorBoundary(t *testing.T, pool *sql.DB, driver config.Driver) {
	t.Helper()
	const serverID = "87000000-0000-4000-8000-000000000003"
	now := time.Date(2026, 9, 28, 14, 0, 0, 0, time.UTC)
	createCatalogServer(t, pool, driver, serverID, "Activity Cursor", now)
	playback := newPlaybackTestStore(t, pool, driver)
	watches := make([]core.PlaybackWatch, 3)
	for index := range watches {
		watches[index] = activityWatch(t, serverID, "cursor-"+string(rune('a'+index)), "Cursor", now)
		watches[index].ID = "89000000-0000-4000-8000-00000000000" + string(rune('1'+index))
	}
	if err := playback.SaveWatches(t.Context(), playbackMutations(watches...)); err != nil {
		t.Fatalf("SaveWatches: %v", err)
	}
	activity, ok := playback.(core.ActivityStore)
	if !ok {
		t.Fatal("playback store does not implement ActivityStore")
	}
	first, err := activity.ListActivity(t.Context(), core.ActivityQuery{MediaServerID: serverID, Limit: 2})
	if err != nil || len(first) != 2 || first[0].ID != watches[2].ID || first[1].ID != watches[1].ID {
		t.Fatalf("first page = %+v, %v", first, err)
	}
	second, err := activity.ListActivity(t.Context(), core.ActivityQuery{
		MediaServerID: serverID, Limit: 2,
		Before: &core.ActivityCursor{StartedAt: first[1].StartedAt, ID: first[1].ID},
	})
	if err != nil || len(second) != 1 || second[0].ID != watches[0].ID {
		t.Fatalf("second page = %+v, %v", second, err)
	}
}
