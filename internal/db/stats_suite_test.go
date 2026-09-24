package db_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/BonzTM/bloom/internal/config"
	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/db"
)

func runStatsEngineTests(t *testing.T, pool *sql.DB, driver config.Driver) {
	t.Helper()
	t.Run("statistics query parity", func(t *testing.T) {
		fixture := seedStatsFixture(t, pool, driver)
		reader, err := db.NewStatsReader(pool, driver)
		if err != nil {
			t.Fatalf("NewStatsReader: %v", err)
		}
		assertStatsOverview(t, reader, fixture)
		assertStatsTitleQueries(t, reader, fixture)
		assertStatsUsers(t, reader, fixture)
		assertStatsZoneBuckets(t, reader, fixture)
		assertStatsUserDetail(t, reader, fixture)
		assertStatsBinaryRanking(t, pool, driver, reader, fixture)
		assertStatsRejectsUnsafeUserIDs(t, reader, fixture)
	})
}

type statsFixture struct {
	end, start       time.Time
	serverA, serverB string
	userA            string
}

func seedStatsFixture(t *testing.T, pool *sql.DB, driver config.Driver) statsFixture {
	t.Helper()
	_, writer, err := db.NewMediaServerStores(pool, driver)
	if err != nil {
		t.Fatalf("NewMediaServerStores: %v", err)
	}
	end := core.NormalizeTime(time.Date(2026, 9, 24, 8, 0, 0, 0, time.UTC))
	fixture := statsFixture{end: end, start: end.Add(-48 * time.Hour), userA: "stats-user-a"}
	servers := []core.MediaServerRecord{
		mediaServerRecord(t, "Stats A "+mustID(t), "https://stats-a.example.test", end),
		mediaServerRecord(t, "Stats B "+mustID(t), "https://stats-b.example.test", end),
	}
	fixture.serverA, fixture.serverB = servers[0].ID, servers[1].ID
	for _, server := range servers {
		if err := writer.CreateMediaServer(t.Context(), server); err != nil {
			t.Fatalf("CreateMediaServer: %v", err)
		}
		serverID := server.ID
		t.Cleanup(func() {
			if err := writer.DeleteMediaServer(context.Background(), serverID); err != nil {
				t.Errorf("DeleteMediaServer: %v", err)
			}
		})
	}
	store := newPlaybackTestStore(t, pool, driver)
	watches := []core.PlaybackWatch{
		statsWatch(t, fixture.serverA, fixture.userA, "alice", fixture.start.Add(-time.Microsecond), "old", "Old", "Movie", "", "Web", "TV", core.PlayMethodDirectPlay, 999),
		statsWatch(t, fixture.serverA, fixture.userA, "alice", fixture.start, "movie-1", "Film", "Movie", "", "Web", "TV", core.PlayMethodDirectPlay, 100),
		statsWatch(t, fixture.serverA, fixture.userA, "alice", time.Date(2026, 9, 23, 0, 30, 0, 0, time.UTC), "ep-1", "Pilot", "Episode", "Show", "Android", "Phone", core.PlayMethodTranscode, 200),
		statsWatch(t, fixture.serverA, "stats-user-b", "bob", time.Date(2026, 9, 23, 18, 0, 0, 0, time.UTC), "ep-2", "Second", "Episode", "Show", "Web", "Laptop", core.PlayMethodDirectStream, 300),
		statsWatch(t, fixture.serverB, "stats-user-c", "carol", time.Date(2026, 9, 24, 7, 0, 0, 0, time.UTC), "track-1", "Song", "Audio", "", "Mobile", "Phone", core.PlayMethodDirectPlay, 400),
		statsWatch(t, fixture.serverB, "stats-user-c", "carol", fixture.end, "future", "Future", "Movie", "", "Web", "TV", core.PlayMethodDirectPlay, 888),
	}
	mutations := make([]core.PlaybackMutation, 0, len(watches))
	for _, watch := range watches {
		mutations = append(mutations, core.PlaybackMutation{Watch: watch})
	}
	if err := store.SaveWatches(t.Context(), mutations); err != nil {
		t.Fatalf("SaveWatches: %v", err)
	}
	return fixture
}

func statsWatch(
	t *testing.T, serverID, userID, username string, started time.Time,
	itemID, itemName, itemType, series, client, device string,
	method core.PlayMethod, seconds int,
) core.PlaybackWatch {
	t.Helper()
	watch := playbackStoreWatch(t, serverID, core.NormalizeTime(started))
	watch.MediaUserID, watch.Username = userID, username
	watch.DeviceID, watch.DeviceName = "device-"+itemID, device
	watch.Client, watch.ItemID, watch.ItemName = client, itemID, itemName
	watch.ItemType, watch.SeriesName, watch.PlayMethod = itemType, series, method
	watch.ServerSessionID = "session-" + itemID
	watch.ActiveTime = time.Duration(seconds) * time.Second
	return watch
}

func statsQuery(t *testing.T, fixture statsFixture, report core.StatsReport, zone string) core.StatsQuery {
	t.Helper()
	window, err := core.NewStatsWindow(2, "", zone, fixture.end)
	if err != nil {
		t.Fatal(err)
	}
	return core.StatsQuery{Window: window, Report: report}
}

func assertStatsOverview(t *testing.T, reader core.StatsReader, fixture statsFixture) {
	t.Helper()
	result, err := reader.ReadStats(t.Context(), statsQuery(t, fixture, core.StatsReportOverview, "UTC"))
	if err != nil {
		t.Fatalf("overview: %v", err)
	}
	if result.Totals != (core.StatsTotals{Plays: 4, WatchSeconds: 1000, UniqueUsers: 3, UniqueTitles: 3}) {
		t.Fatalf("overview totals = %+v", result.Totals)
	}
	if len(result.Titles) != 3 || len(result.Users) != 3 || len(result.Clients) != 3 ||
		len(result.Devices) != 3 || len(result.PlayMethods) != 3 {
		t.Fatalf("overview rankings = titles %d users %d clients %d devices %d methods %d",
			len(result.Titles), len(result.Users), len(result.Clients), len(result.Devices), len(result.PlayMethods))
	}
	query := statsQuery(t, fixture, core.StatsReportOverview, "UTC")
	query.Window.MediaServerID = fixture.serverA
	filtered, err := reader.ReadStats(t.Context(), query)
	if err != nil || filtered.Totals.Plays != 3 || filtered.Totals.WatchSeconds != 600 {
		t.Fatalf("filtered overview = %+v, %v", filtered.Totals, err)
	}
}

func assertStatsTitleQueries(t *testing.T, reader core.StatsReader, fixture statsFixture) {
	t.Helper()
	for _, test := range []struct {
		kind core.StatsTitleKind
		key  string
	}{
		{core.StatsTitleMovie, "movie-1"},
		{core.StatsTitleSeries, "Show"},
		{core.StatsTitleOther, "Audio"},
	} {
		query := statsQuery(t, fixture, core.StatsReportTitles, "UTC")
		query.TitleKind = test.kind
		result, err := reader.ReadStats(t.Context(), query)
		if err != nil || len(result.Titles) != 1 || result.Titles[0].Key != test.key {
			t.Fatalf("titles %s = %+v, %v", test.kind, result.Titles, err)
		}
	}
}

func assertStatsUsers(t *testing.T, reader core.StatsReader, fixture statsFixture) {
	t.Helper()
	result, err := reader.ReadStats(t.Context(), statsQuery(t, fixture, core.StatsReportUsers, "UTC"))
	if err != nil || len(result.Users) != 3 || result.Users[0].Username != "alice" {
		t.Fatalf("users = %+v, %v", result.Users, err)
	}
}

func assertStatsZoneBuckets(t *testing.T, reader core.StatsReader, fixture statsFixture) {
	t.Helper()
	utc, err := reader.ReadStats(t.Context(), statsQuery(t, fixture, core.StatsReportDaily, "UTC"))
	if err != nil {
		t.Fatalf("UTC daily: %v", err)
	}
	la, err := reader.ReadStats(t.Context(), statsQuery(t, fixture, core.StatsReportDaily, "America/Los_Angeles"))
	if err != nil {
		t.Fatalf("Los Angeles daily: %v", err)
	}
	if dailyPlays(utc.Daily, "2026-09-23") != 2 || dailyPlays(la.Daily, "2026-09-22") != 2 {
		t.Fatalf("zone daily buckets = UTC %+v LA %+v", utc.Daily, la.Daily)
	}
	patterns, err := reader.ReadStats(t.Context(), statsQuery(t, fixture, core.StatsReportPatterns, "America/Los_Angeles"))
	if err != nil || len(patterns.Weekdays) != 7 || len(patterns.Hours) != 24 || patterns.Hours[17].Plays != 1 {
		t.Fatalf("patterns = %+v %+v, %v", patterns.Weekdays, patterns.Hours, err)
	}
}

func dailyPlays(buckets []core.StatsDailyBucket, date string) int64 {
	for _, bucket := range buckets {
		if bucket.Date == date {
			return bucket.Plays
		}
	}
	return 0
}

func assertStatsUserDetail(t *testing.T, reader core.StatsReader, fixture statsFixture) {
	t.Helper()
	query := statsQuery(t, fixture, core.StatsReportUser, "UTC")
	query.UserServerID, query.MediaUserID = fixture.serverA, fixture.userA
	result, err := reader.ReadStats(t.Context(), query)
	if err != nil || result.Totals.Plays != 2 || result.Totals.WatchSeconds != 300 ||
		len(result.Titles) != 2 || len(result.Watches) != 2 || len(result.Daily) == 0 {
		t.Fatalf("user detail = %+v, %v", result, err)
	}
}

func assertStatsBinaryRanking(
	t *testing.T, pool *sql.DB, driver config.Driver, reader core.StatsReader, fixture statsFixture,
) {
	t.Helper()
	serverID := seedStatsRankingFixture(t, pool, driver, fixture.end)
	query := statsQuery(t, fixture, core.StatsReportTitles, "UTC")
	query.Window.MediaServerID = serverID
	query.TitleKind = core.StatsTitleMovie
	titles, err := reader.ReadStats(t.Context(), query)
	if err != nil {
		t.Fatalf("binary title ranking: %v", err)
	}
	assertStatsRankingKeys(t, titleKeys(titles.Titles), rankingKeys()[:50])
	if titles.Titles[0].Name != "éclair" {
		t.Fatalf("binary MAX(item_name) = %q, want éclair", titles.Titles[0].Name)
	}
	query.Report, query.TitleKind = core.StatsReportUsers, ""
	users, err := reader.ReadStats(t.Context(), query)
	if err != nil {
		t.Fatalf("binary user ranking: %v", err)
	}
	assertStatsRankingKeys(t, userKeys(users.Users), rankingKeys()[:50])
	if users.Users[0].Username != "éclair" {
		t.Fatalf("binary MAX(username) = %q, want éclair", users.Users[0].Username)
	}
}

func seedStatsRankingFixture(
	t *testing.T, pool *sql.DB, driver config.Driver, now time.Time,
) string {
	t.Helper()
	_, writer, err := db.NewMediaServerStores(pool, driver)
	if err != nil {
		t.Fatalf("NewMediaServerStores: %v", err)
	}
	server := mediaServerRecord(t, "Stats ranking "+mustID(t), "https://stats-ranking.example.test", now)
	if err := writer.CreateMediaServer(t.Context(), server); err != nil {
		t.Fatalf("CreateMediaServer: %v", err)
	}
	t.Cleanup(func() {
		if err := writer.DeleteMediaServer(context.Background(), server.ID); err != nil {
			t.Errorf("DeleteMediaServer: %v", err)
		}
	})
	store := newPlaybackTestStore(t, pool, driver)
	mutations := rankingMutations(t, server.ID, now)
	if err := store.SaveWatches(t.Context(), mutations); err != nil {
		t.Fatalf("SaveWatches ranking fixture: %v", err)
	}
	return server.ID
}

func rankingMutations(t *testing.T, serverID string, end time.Time) []core.PlaybackMutation {
	t.Helper()
	keys := rankingKeys()
	mutations := make([]core.PlaybackMutation, 0, len(keys)+1)
	for index, key := range keys {
		watch := statsWatch(t, serverID, key, "Zulu", end.Add(-time.Hour-time.Duration(index)*time.Microsecond),
			key, "Zulu", "Movie", "", key, key, core.PlayMethodDirectPlay, 1)
		mutations = append(mutations, core.PlaybackMutation{Watch: watch})
	}
	extra := statsWatch(t, serverID, keys[0], "éclair", end.Add(-30*time.Minute),
		keys[0], "éclair", "Movie", "", keys[0], keys[0], core.PlayMethodDirectPlay, 1)
	return append(mutations, core.PlaybackMutation{Watch: extra})
}

func rankingKeys() []string {
	keys := make([]string, 0, 52)
	keys = append(keys, "A-top")
	for index := range 48 {
		keys = append(keys, fmt.Sprintf("B-%02d", index))
	}
	return append(keys, "Z-edge-in", "a-edge-out", "é-last")
}

func titleKeys(titles []core.StatsTitle) []string {
	keys := make([]string, 0, len(titles))
	for _, title := range titles {
		keys = append(keys, title.Key)
	}
	return keys
}

func userKeys(users []core.StatsUser) []string {
	keys := make([]string, 0, len(users))
	for _, user := range users {
		keys = append(keys, user.MediaUserID)
	}
	return keys
}

func assertStatsRankingKeys(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("ranking length = %d, want %d", len(got), len(want))
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("ranking[%d] = %q, want %q", index, got[index], want[index])
		}
	}
}

func assertStatsRejectsUnsafeUserIDs(t *testing.T, reader core.StatsReader, fixture statsFixture) {
	t.Helper()
	for _, userID := range []string{"nul\x00user", string([]byte{0xff})} {
		query := statsQuery(t, fixture, core.StatsReportUser, "UTC")
		query.UserServerID, query.MediaUserID = fixture.serverA, userID
		if _, err := reader.ReadStats(t.Context(), query); !errors.Is(err, core.ErrInvalidArgument) {
			t.Fatalf("unsafe media user ID %q error = %v, want ErrInvalidArgument", userID, err)
		}
	}
}
