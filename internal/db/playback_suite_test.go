package db_test

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/BonzTM/bloom/internal/config"
	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/db"
)

func runPlaybackEngineTests(t *testing.T, pool *sql.DB, driver config.Driver) {
	t.Helper()
	t.Run("playback persistence and cascade", func(t *testing.T) {
		reader, writer, err := db.NewMediaServerStores(pool, driver)
		if err != nil {
			t.Fatalf("NewMediaServerStores: %v", err)
		}
		_ = reader
		now := core.NormalizeTime(time.Date(2026, 9, 23, 15, 0, 0, 0, time.UTC))
		server := mediaServerRecord(t, "Playback "+mustID(t), "https://playback.example.test", now)
		if err := writer.CreateMediaServer(t.Context(), server); err != nil {
			t.Fatalf("CreateMediaServer: %v", err)
		}
		store := newPlaybackTestStore(t, pool, driver)
		watch := playbackStoreWatch(t, server.ID, now)
		position := playbackPosition(watch.ID, now, time.Minute)
		if err := store.SaveWatches(t.Context(), []core.PlaybackMutation{{
			Watch: watch, SegmentStart: new(now), SegmentSource: core.WatchSourceWebsocket,
			Position: &position,
		}}); err != nil {
			t.Fatalf("SaveWatches(start): %v", err)
		}
		assertPlaybackRowSources(t, pool, watch.ID)
		assertPlaybackRestart(t, pool, driver, server.ID, watch.ID)
		writePlaybackPositions(t, store, watch, now)
		assertRowCount(t, pool, "SELECT COUNT(*) FROM watch_positions WHERE watch_id = $1", watch.ID, 512)
		closePlaybackWatch(t, store, watch, now)
		assertPlaybackReads(t, store, server.ID, watch.ID)
		if err := writer.DeleteMediaServer(t.Context(), server.ID); err != nil {
			t.Fatalf("DeleteMediaServer: %v", err)
		}
		assertRowCount(t, pool, "SELECT COUNT(*) FROM watches WHERE id = $1", watch.ID, 0)
		assertRowCount(t, pool, "SELECT COUNT(*) FROM watch_segments WHERE watch_id = $1", watch.ID, 0)
		assertRowCount(t, pool, "SELECT COUNT(*) FROM watch_positions WHERE watch_id = $1", watch.ID, 0)
	})
	t.Run("restart restores large open and exact recent sets", func(t *testing.T) {
		testLargePlaybackRestart(t, pool, driver)
	})
	t.Run("now playing paginates across large multi-server set", func(t *testing.T) {
		testNowPlayingPagination(t, pool, driver)
	})
}

func testNowPlayingPagination(t *testing.T, pool *sql.DB, driver config.Driver) {
	t.Helper()
	_, writer, err := db.NewMediaServerStores(pool, driver)
	if err != nil {
		t.Fatalf("NewMediaServerStores: %v", err)
	}
	now := core.NormalizeTime(time.Date(2026, 9, 23, 18, 0, 0, 0, time.UTC))
	servers := []core.MediaServerRecord{
		mediaServerRecord(t, "Now A "+mustID(t), "https://now-a.example.test", now),
		mediaServerRecord(t, "Now B "+mustID(t), "https://now-b.example.test", now),
	}
	for _, server := range servers {
		if err := writer.CreateMediaServer(t.Context(), server); err != nil {
			t.Fatalf("CreateMediaServer: %v", err)
		}
		serverID := server.ID
		t.Cleanup(func() {
			if err := writer.DeleteMediaServer(context.Background(), serverID); err != nil {
				t.Errorf("DeleteMediaServer cleanup: %v", err)
			}
		})
	}
	store := newPlaybackTestStore(t, pool, driver)
	mutations := nowPlayingMutations(t, servers, now, core.MaxPlaybackSessions+2)
	if err := store.SaveWatches(t.Context(), mutations); err != nil {
		t.Fatalf("SaveWatches: %v", err)
	}
	assertNowPlayingPages(t, store, len(mutations))
}

func nowPlayingMutations(
	t *testing.T,
	servers []core.MediaServerRecord,
	now time.Time,
	count int,
) []core.PlaybackMutation {
	t.Helper()
	mutations := make([]core.PlaybackMutation, 0, count)
	for index := range count {
		startedAt := now.Add(-time.Duration(index/2) * time.Microsecond)
		watch := playbackStoreWatch(t, servers[index%len(servers)].ID, startedAt)
		watch.MediaUserID = fmt.Sprintf("now-user-%04d", index)
		watch.DeviceID = fmt.Sprintf("now-device-%04d", index)
		watch.ItemID = fmt.Sprintf("now-item-%04d", index)
		mutations = append(mutations, core.PlaybackMutation{Watch: watch})
	}
	return mutations
}

func assertNowPlayingPages(t *testing.T, store core.PlaybackStore, want int) {
	t.Helper()
	query := core.PlaybackQuery{Mode: core.PlaybackQueryNow, PageSize: 101}
	seen := make(map[string]bool, want)
	for range 12 {
		watches, err := store.ListWatches(t.Context(), query)
		if err != nil {
			t.Fatalf("ListWatches(now): %v", err)
		}
		more := len(watches) > 100
		if more {
			watches = watches[:100]
		}
		for _, watch := range watches {
			if seen[watch.ID] {
				t.Fatalf("duplicate now-playing watch %s", watch.ID)
			}
			seen[watch.ID] = true
		}
		if !more {
			if len(seen) != want {
				t.Fatalf("now-playing watches = %d, want %d", len(seen), want)
			}
			return
		}
		last := watches[len(watches)-1]
		query.BeforeStartedAt, query.BeforeID = last.StartedAt, last.ID
	}
	t.Fatalf("now-playing pagination did not finish; collected %d of %d", len(seen), want)
}

func testLargePlaybackRestart(t *testing.T, pool *sql.DB, driver config.Driver) {
	t.Helper()
	reader, writer, storeErr := db.NewMediaServerStores(pool, driver)
	if storeErr != nil {
		t.Fatalf("NewMediaServerStores: %v", storeErr)
	}
	_ = reader
	now := core.NormalizeTime(time.Date(2026, 9, 23, 17, 0, 0, 0, time.UTC))
	server := mediaServerRecord(t, "Large "+mustID(t), "https://large.example.test", now)
	if createErr := writer.CreateMediaServer(t.Context(), server); createErr != nil {
		t.Fatalf("CreateMediaServer: %v", createErr)
	}
	t.Cleanup(func() {
		if deleteErr := writer.DeleteMediaServer(context.Background(), server.ID); deleteErr != nil {
			t.Errorf("DeleteMediaServer cleanup: %v", deleteErr)
		}
	})
	store := newPlaybackTestStore(t, pool, driver)
	open, recent := largePlaybackMutations(t, server.ID, now, 1025)
	if saveErr := store.SaveWatches(t.Context(), open); saveErr != nil {
		t.Fatalf("SaveWatches(open): %v", saveErr)
	}
	if saveErr := store.SaveWatches(t.Context(), recent); saveErr != nil {
		t.Fatalf("SaveWatches(recent): %v", saveErr)
	}
	restarted := newPlaybackTestStore(t, pool, driver)
	loaded, err := restarted.LoadOpenWatches(t.Context(), server.ID)
	if err != nil || len(loaded) != len(open) {
		t.Fatalf("LoadOpenWatches = %d, %v; want %d", len(loaded), err, len(open))
	}
	oldest := recent[0].Watch
	found, err := restarted.ListWatches(t.Context(), core.PlaybackQuery{
		Mode: core.PlaybackQueryRecent, Key: oldest.Key(), EndedAfter: now.Add(-time.Hour), PageSize: 1,
	})
	if err != nil || len(found) != 1 || found[0].ID != oldest.ID {
		t.Fatalf("exact recent lookup = %+v, %v; want %s", found, err, oldest.ID)
	}
}

func largePlaybackMutations(
	t *testing.T,
	serverID string,
	now time.Time,
	count int,
) ([]core.PlaybackMutation, []core.PlaybackMutation) {
	t.Helper()
	open := make([]core.PlaybackMutation, 0, count)
	recent := make([]core.PlaybackMutation, 0, count)
	for index := range count {
		watch := playbackStoreWatch(t, serverID, now.Add(time.Duration(index)*time.Microsecond))
		watch.MediaUserID = fmt.Sprintf("open-user-%04d", index)
		watch.DeviceID = fmt.Sprintf("open-device-%04d", index)
		watch.ItemID = fmt.Sprintf("open-item-%04d", index)
		open = append(open, core.PlaybackMutation{Watch: watch})
		closed := playbackStoreWatch(t, serverID, now.Add(-time.Minute+time.Duration(index)*time.Microsecond))
		closed.MediaUserID = fmt.Sprintf("recent-user-%04d", index)
		closed.DeviceID = fmt.Sprintf("recent-device-%04d", index)
		closed.ItemID = fmt.Sprintf("recent-item-%04d", index)
		closed.State = core.WatchStopped
		closed.EndedAt = new(closed.LastSeenAt)
		recent = append(recent, core.PlaybackMutation{Watch: closed})
	}
	return open, recent
}

func newPlaybackTestStore(t *testing.T, pool *sql.DB, driver config.Driver) core.PlaybackStore {
	t.Helper()
	store, err := db.NewPlaybackStore(pool, driver)
	if err != nil {
		t.Fatalf("NewPlaybackStore: %v", err)
	}
	return store
}

func playbackStoreWatch(t *testing.T, serverID string, now time.Time) core.PlaybackWatch {
	t.Helper()
	return core.PlaybackWatch{
		ID: mustID(t), MediaServerID: serverID, MediaUserID: "user-1", Username: "alice",
		DeviceID: "device-1", DeviceName: "Living Room", Client: "Jellyfin Web",
		ServerSessionID: "session-1", ItemID: "item-1", ItemName: "Pilot",
		ItemType: "Episode", SeriesName: "Series", PlayMethod: core.PlayMethodDirectPlay,
		State: core.WatchPlaying, StartedAt: now, LastSeenAt: now,
		LastPosition: time.Minute, Source: core.WatchSourcePoll, CreatedAt: now, UpdatedAt: now,
	}
}

func assertPlaybackRestart(
	t *testing.T,
	pool *sql.DB,
	driver config.Driver,
	serverID, watchID string,
) {
	t.Helper()
	restarted := newPlaybackTestStore(t, pool, driver)
	watches, err := restarted.LoadOpenWatches(t.Context(), serverID)
	if err != nil || len(watches) != 1 || watches[0].ID != watchID {
		t.Fatalf("LoadOpenWatches after restart = %+v, %v", watches, err)
	}
	if watches[0].MediaServerName == "" || watches[0].LastPosition != time.Minute {
		t.Fatalf("restored watch = %+v", watches[0])
	}
}

func writePlaybackPositions(
	t *testing.T,
	store core.PlaybackStore,
	watch core.PlaybackWatch,
	now time.Time,
) {
	t.Helper()
	mutations := make([]core.PlaybackMutation, 0, core.MaxWatchPositions)
	for index := 1; index <= core.MaxWatchPositions; index++ {
		observedAt := now.Add(time.Duration(index) * time.Second)
		watch.LastSeenAt = observedAt
		watch.UpdatedAt = observedAt
		watch.ActiveTime = time.Duration(index) * time.Second
		watch.LastPosition = time.Minute + time.Duration(index)*time.Second
		position := playbackPosition(watch.ID, observedAt, watch.LastPosition)
		mutations = append(mutations, core.PlaybackMutation{Watch: watch, Position: &position})
	}
	if err := store.SaveWatches(t.Context(), mutations); err != nil {
		t.Fatalf("SaveWatches(position batch): %v", err)
	}
}

func closePlaybackWatch(
	t *testing.T,
	store core.PlaybackStore,
	watch core.PlaybackWatch,
	now time.Time,
) {
	t.Helper()
	endedAt := now.Add(core.MaxWatchPositions * time.Second)
	watch.State = core.WatchStopped
	watch.LastSeenAt = endedAt
	watch.EndedAt = new(endedAt)
	watch.ActiveTime = core.MaxWatchPositions * time.Second
	watch.LastPosition = time.Minute + core.MaxWatchPositions*time.Second
	watch.UpdatedAt = endedAt
	if err := store.SaveWatches(t.Context(), []core.PlaybackMutation{{
		Watch: watch, SegmentEnd: new(endedAt), CloseReason: "timeout",
	}}); err != nil {
		t.Fatalf("SaveWatches(stop): %v", err)
	}
}

func assertPlaybackReads(t *testing.T, store core.PlaybackStore, serverID, watchID string) {
	t.Helper()
	nowWatches, err := store.ListWatches(t.Context(), core.PlaybackQuery{
		Mode: core.PlaybackQueryNow, PageSize: 2,
	})
	if err != nil || len(nowWatches) != 0 {
		t.Fatalf("ListWatches(now) = %+v, %v", nowWatches, err)
	}
	history, err := store.ListWatches(t.Context(), core.PlaybackQuery{
		Mode: core.PlaybackQueryHistory, MediaServerID: serverID, PageSize: 2,
	})
	if err != nil || len(history) != 1 || history[0].ID != watchID || history[0].EndedAt == nil {
		t.Fatalf("ListWatches(history) = %+v, %v", history, err)
	}
}

func playbackPosition(watchID string, observedAt time.Time, position time.Duration) core.PlaybackPosition {
	return core.PlaybackPosition{
		WatchID: watchID, ObservedAt: observedAt, Position: position,
		PlayMethod: core.PlayMethodDirectPlay, Source: core.WatchSourceWebhook,
	}
}

func assertPlaybackRowSources(t *testing.T, pool *sql.DB, watchID string) {
	t.Helper()
	for table, want := range map[string]string{
		"watch_segments":  string(core.WatchSourceWebsocket),
		"watch_positions": string(core.WatchSourceWebhook),
	} {
		var got string
		if err := pool.QueryRowContext(t.Context(),
			"SELECT source FROM "+table+" WHERE watch_id = $1", watchID).Scan(&got); err != nil {
			t.Fatalf("read %s source: %v", table, err)
		}
		if got != want {
			t.Fatalf("%s source = %q, want %q", table, got, want)
		}
	}
}
