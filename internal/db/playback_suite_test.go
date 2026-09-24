package db_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"testing"
	"time"

	"github.com/BonzTM/bloom/internal/config"
	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/db"
	"github.com/BonzTM/bloom/internal/playback"
	"github.com/BonzTM/bloom/internal/testutil"
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
		assertPlaybackPositions(t, store, watch.ID)
		closePlaybackWatch(t, store, watch, now)
		assertPlaybackReads(t, store, server.ID, watch.ID)
		if err := writer.DeleteMediaServer(t.Context(), server.ID); err != nil {
			t.Fatalf("DeleteMediaServer: %v", err)
		}
		assertRowCount(t, pool, "SELECT COUNT(*) FROM watches WHERE id = $1", watch.ID, 0)
		assertRowCount(t, pool, "SELECT COUNT(*) FROM watch_segments WHERE watch_id = $1", watch.ID, 0)
		assertRowCount(t, pool, "SELECT COUNT(*) FROM watch_positions WHERE watch_id = $1", watch.ID, 0)
		if _, err := store.ListWatchPositions(t.Context(), watch.ID); !errors.Is(err, core.ErrNotFound) {
			t.Fatalf("ListWatchPositions deleted watch = %v, want not found", err)
		}
	})
	t.Run("transitions outlive position-only samples", func(t *testing.T) {
		testPlaybackTransitionRetention(t, pool, driver)
	})
	t.Run("framerate hundredths round trip", func(t *testing.T) {
		testPlaybackFramerateRoundTrip(t, pool, driver)
	})
	t.Run("restart restores large open and exact recent sets", func(t *testing.T) {
		testLargePlaybackRestart(t, pool, driver)
	})
	t.Run("now playing paginates across large multi-server set", func(t *testing.T) {
		testNowPlayingPagination(t, pool, driver)
	})
	t.Run("library backfill is bounded and fill only", func(t *testing.T) {
		testPlaybackLibraryBackfill(t, pool, driver)
	})
	t.Run("library backfill keyset reaches later resolvable items", func(t *testing.T) {
		testPlaybackLibraryBackfillKeyset(t, pool, driver)
	})
}

func testPlaybackFramerateRoundTrip(t *testing.T, pool *sql.DB, driver config.Driver) {
	t.Helper()
	server := createPlaybackTestServer(t, pool, driver, "Framerate")
	store := newPlaybackTestStore(t, pool, driver)
	now := core.NormalizeTime(time.Date(2026, 9, 23, 22, 0, 0, 0, time.UTC))
	want := []float64{20.06, 29.97, 23.98}
	for index, framerate := range want {
		watch := playbackStoreWatch(t, server.ID, now.Add(time.Duration(index)*time.Second))
		watch.MediaUserID = fmt.Sprintf("framerate-user-%d", index)
		watch.Stream.Framerate = framerate
		if err := store.SaveWatches(t.Context(), []core.PlaybackMutation{{Watch: watch}}); err != nil {
			t.Fatalf("SaveWatches(%v): %v", framerate, err)
		}
	}
	watches, err := store.LoadOpenWatches(t.Context(), server.ID)
	if err != nil || len(watches) != len(want) {
		t.Fatalf("LoadOpenWatches = %d, %v", len(watches), err)
	}
	got := make(map[float64]bool, len(watches))
	for _, watch := range watches {
		got[watch.Stream.Framerate] = true
	}
	for _, framerate := range want {
		if !got[framerate] {
			t.Errorf("round trip omitted framerate %v: %v", framerate, got)
		}
	}
}

func testPlaybackTransitionRetention(t *testing.T, pool *sql.DB, driver config.Driver) {
	t.Helper()
	server := createPlaybackTestServer(t, pool, driver, "Transitions")
	store := newPlaybackTestStore(t, pool, driver)
	now := core.NormalizeTime(time.Date(2026, 9, 23, 21, 0, 0, 0, time.UTC))
	watch := playbackStoreWatch(t, server.ID, now)
	start := playbackPosition(watch.ID, now, watch.LastPosition)
	if err := store.SaveWatches(t.Context(), []core.PlaybackMutation{{Watch: watch, Position: &start}}); err != nil {
		t.Fatalf("SaveWatches(start): %v", err)
	}
	transitionAt := now.Add(time.Second)
	transition := playbackPosition(watch.ID, transitionAt, watch.LastPosition+time.Second)
	transition.IsTransition = true
	watch.LastSeenAt, watch.UpdatedAt = transitionAt, transitionAt
	watch.LastPosition = transition.Position
	if err := store.SaveWatches(t.Context(), []core.PlaybackMutation{{Watch: watch, Position: &transition}}); err != nil {
		t.Fatalf("SaveWatches(transition): %v", err)
	}
	writeLaterPlaybackPositions(t, store, watch, transitionAt)
	positions, err := store.ListWatchPositions(t.Context(), watch.ID)
	if err != nil || len(positions) != core.MaxWatchPositions {
		t.Fatalf("retained positions = %d, %v", len(positions), err)
	}
	found := false
	for _, position := range positions {
		if position.ObservedAt.Equal(transitionAt) {
			found = position.IsTransition
		}
	}
	if !found {
		t.Fatal("older transition sample was trimmed before position-only samples")
	}
	assertRowCount(t, pool, "SELECT COUNT(*) FROM watch_positions WHERE watch_id = $1", watch.ID, core.MaxWatchPositions)
}

func writeLaterPlaybackPositions(
	t *testing.T,
	store core.PlaybackStore,
	watch core.PlaybackWatch,
	start time.Time,
) {
	t.Helper()
	mutations := make([]core.PlaybackMutation, 0, core.MaxWatchPositions)
	for index := 1; index <= core.MaxWatchPositions; index++ {
		observedAt := start.Add(time.Duration(index) * time.Second)
		watch.LastSeenAt, watch.UpdatedAt = observedAt, observedAt
		watch.LastPosition += time.Second
		position := playbackPosition(watch.ID, observedAt, watch.LastPosition)
		mutations = append(mutations, core.PlaybackMutation{Watch: watch, Position: &position})
	}
	if err := store.SaveWatches(t.Context(), mutations); err != nil {
		t.Fatalf("SaveWatches(later positions): %v", err)
	}
}

type keysetResolverSource struct {
	called chan string
}

func (*keysetResolverSource) ListSessions(context.Context) ([]core.PlaybackSession, error) {
	return nil, nil
}

func (s *keysetResolverSource) ResolveLibrary(
	_ context.Context, itemID string,
) (core.Library, bool, error) {
	s.called <- itemID
	if itemID == "item-25" {
		return core.Library{ID: "library-keyset", Name: "Reached"}, true, nil
	}
	return core.Library{}, false, nil
}

type signalingPlaybackStore struct {
	core.PlaybackPersistence
	backfilled chan string
}

func (s signalingPlaybackStore) BackfillWatchLibrary(
	ctx context.Context, serverID, itemID string, library core.Library,
) error {
	if err := s.PlaybackPersistence.BackfillWatchLibrary(ctx, serverID, itemID, library); err != nil {
		return err
	}
	s.backfilled <- itemID
	return nil
}

func testPlaybackLibraryBackfillKeyset(t *testing.T, pool *sql.DB, driver config.Driver) {
	t.Helper()
	server := createPlaybackTestServer(t, pool, driver, "Keyset")
	store := newPlaybackTestStore(t, pool, driver)
	seedUnresolvedItems(t, store, server.ID, 26)
	source := &keysetResolverSource{called: make(chan string, 26)}
	signalingStore := signalingPlaybackStore{PlaybackPersistence: store, backfilled: make(chan string, 1)}
	polls, release := make(chan time.Duration, 2), make(chan struct{}, 1)
	collector, err := playback.NewCollector(server.MediaServer, playback.Config{
		ActiveInterval: 5 * time.Second, IdleInterval: 30 * time.Second,
		MissedPolls: 3, ResumeWindow: 5 * time.Minute, StoreTimeout: time.Second,
	}, playback.Dependencies{
		Store: signalingStore, Source: source,
		Clock:  testutil.NewFakeClock(time.Date(2026, 9, 23, 20, 0, 0, 0, time.UTC)),
		Logger: slog.New(slog.DiscardHandler),
		Wait: func(ctx context.Context, delay time.Duration) error {
			polls <- delay
			select {
			case <-release:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- collector.Run(ctx) }()
	receiveDBDuration(t, polls)
	for index := range 25 {
		want := fmt.Sprintf("item-%02d", index)
		if got := receiveDBString(t, source.called); got != want {
			t.Fatalf("first page item %d = %q, want %q", index, got, want)
		}
	}
	release <- struct{}{}
	receiveDBDuration(t, polls)
	if got := receiveDBString(t, source.called); got != "item-25" {
		t.Fatalf("second page item = %q, want item-25", got)
	}
	if got := receiveDBString(t, signalingStore.backfilled); got != "item-25" {
		t.Fatalf("backfilled item = %q, want item-25", got)
	}
	cancel()
	assertDBCollectorStopped(t, done)
	assertKeysetLibrary(t, pool, server.ID)
}

func createPlaybackTestServer(
	t *testing.T, pool *sql.DB, driver config.Driver, prefix string,
) core.MediaServerRecord {
	t.Helper()
	_, writer, err := db.NewMediaServerStores(pool, driver)
	if err != nil {
		t.Fatal(err)
	}
	now := core.NormalizeTime(time.Date(2026, 9, 23, 20, 0, 0, 0, time.UTC))
	server := mediaServerRecord(t, prefix+" "+mustID(t), "https://keyset.example.test", now)
	if err := writer.CreateMediaServer(t.Context(), server); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := writer.DeleteMediaServer(context.Background(), server.ID); err != nil {
			t.Errorf("DeleteMediaServer cleanup: %v", err)
		}
	})
	return server
}

func seedUnresolvedItems(
	t *testing.T, store core.PlaybackPersistence, serverID string, count int,
) {
	t.Helper()
	now := core.NormalizeTime(time.Date(2026, 9, 23, 20, 0, 0, 0, time.UTC))
	mutations := make([]core.PlaybackMutation, 0, count)
	for index := range count {
		watch := playbackStoreWatch(t, serverID, now.Add(time.Duration(index)*time.Second))
		watch.ItemID = fmt.Sprintf("item-%02d", index)
		mutations = append(mutations, core.PlaybackMutation{Watch: watch})
	}
	if err := store.SaveWatches(t.Context(), mutations); err != nil {
		t.Fatal(err)
	}
}

func receiveDBDuration(t *testing.T, values <-chan time.Duration) time.Duration {
	t.Helper()
	select {
	case value := <-values:
		return value
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for playback poll")
		return 0
	}
}

func receiveDBString(t *testing.T, values <-chan string) string {
	t.Helper()
	select {
	case value := <-values:
		return value
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for playback resolution")
		return ""
	}
}

func assertDBCollectorStopped(t *testing.T, done <-chan error) {
	t.Helper()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("playback collector did not stop")
	}
}

func assertKeysetLibrary(t *testing.T, pool *sql.DB, serverID string) {
	t.Helper()
	var libraryID string
	err := pool.QueryRowContext(t.Context(),
		"SELECT library_id FROM watches WHERE media_server_id = $1 AND item_id = $2",
		serverID, "item-25").Scan(&libraryID)
	if err != nil || libraryID != "library-keyset" {
		t.Fatalf("keyset library = %q, %v", libraryID, err)
	}
}

func testPlaybackLibraryBackfill(t *testing.T, pool *sql.DB, driver config.Driver) {
	t.Helper()
	_, writer, err := db.NewMediaServerStores(pool, driver)
	if err != nil {
		t.Fatal(err)
	}
	now := core.NormalizeTime(time.Date(2026, 9, 23, 19, 0, 0, 0, time.UTC))
	server := mediaServerRecord(t, "Backfill "+mustID(t), "https://backfill.example.test", now)
	if createErr := writer.CreateMediaServer(t.Context(), server); createErr != nil {
		t.Fatal(createErr)
	}
	t.Cleanup(func() {
		if deleteErr := writer.DeleteMediaServer(context.Background(), server.ID); deleteErr != nil {
			t.Errorf("DeleteMediaServer cleanup: %v", deleteErr)
		}
	})
	store := newPlaybackTestStore(t, pool, driver)
	first := playbackStoreWatch(t, server.ID, now.Add(-2*time.Hour))
	second := playbackStoreWatch(t, server.ID, now.Add(-time.Hour))
	second.ItemID = "item-2"
	third := playbackStoreWatch(t, server.ID, now)
	third.ItemID = first.ItemID
	if saveErr := store.SaveWatches(t.Context(), playbackMutations(first, second, third)); saveErr != nil {
		t.Fatal(saveErr)
	}
	items, err := store.ListUnresolvedWatchItemIDs(t.Context(), server.ID, "", 1)
	if err != nil || len(items) != 1 || items[0] != first.ItemID {
		t.Fatalf("unresolved items = %v, %v", items, err)
	}
	library := core.Library{ID: "library-1", Name: "Movies"}
	if err := store.BackfillWatchLibrary(t.Context(), server.ID, first.ItemID, library); err != nil {
		t.Fatal(err)
	}
	if err := store.BackfillWatchLibrary(t.Context(), server.ID, first.ItemID,
		core.Library{ID: "library-2", Name: "Other"}); err != nil {
		t.Fatal(err)
	}
	assertBackfilledLibrary(t, pool, server.ID, first.ItemID, library)
}

func playbackMutations(watches ...core.PlaybackWatch) []core.PlaybackMutation {
	mutations := make([]core.PlaybackMutation, 0, len(watches))
	for _, watch := range watches {
		mutations = append(mutations, core.PlaybackMutation{Watch: watch})
	}
	return mutations
}

func assertBackfilledLibrary(
	t *testing.T, pool *sql.DB, serverID, itemID string, want core.Library,
) {
	t.Helper()
	var libraryID, libraryName string
	var count int
	err := pool.QueryRowContext(t.Context(), `SELECT MIN(library_id), MIN(library_name), COUNT(*)
		FROM watches WHERE media_server_id = $1 AND item_id = $2`, serverID, itemID).
		Scan(&libraryID, &libraryName, &count)
	if err != nil || count != 2 || libraryID != want.ID || libraryName != want.Name {
		t.Fatalf("backfilled library = %q, %q, %d, %v", libraryID, libraryName, count, err)
	}
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

func newPlaybackTestStore(t *testing.T, pool *sql.DB, driver config.Driver) core.PlaybackPersistence {
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
		Stream: playbackStoreStream(),
		State:  core.WatchPlaying, StartedAt: now, LastSeenAt: now,
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
	if watches[0].MediaServerName == "" || watches[0].LastPosition != time.Minute ||
		watches[0].Stream == nil || watches[0].Stream.VideoCodec != "h264" {
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
		PlayMethod: core.PlayMethodDirectPlay, Stream: playbackStoreStream(), Source: core.WatchSourceWebhook,
	}
}

func playbackStoreStream() *core.StreamDetails {
	videoDirect, audioDirect := false, true
	return &core.StreamDetails{
		Container: "ts", VideoCodec: "h264", AudioCodec: "aac", Bitrate: 8_000_000,
		Width: 1920, Height: 1080, Framerate: 23.98, AudioChannels: 6,
		IsVideoDirect: &videoDirect, IsAudioDirect: &audioDirect,
		TranscodeReasons: []string{"VideoCodecNotSupported", "FutureReason"},
	}
}

func assertPlaybackPositions(t *testing.T, store core.PlaybackStore, watchID string) {
	t.Helper()
	positions, err := store.ListWatchPositions(t.Context(), watchID)
	if err != nil || len(positions) != core.MaxWatchPositions {
		t.Fatalf("ListWatchPositions = %d, %v", len(positions), err)
	}
	first, last := positions[0], positions[len(positions)-1]
	if !first.ObservedAt.After(last.ObservedAt) || first.Stream == nil ||
		first.Stream.Framerate != 23.98 || len(first.Stream.TranscodeReasons) != 2 {
		t.Fatalf("positions = first %+v, last %+v", first, last)
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
