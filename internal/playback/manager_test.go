package playback

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/testutil"
)

type managerServerLister struct {
	mu      sync.Mutex
	servers []core.MediaServerConnection
	err     error
}

func (l *managerServerLister) List(
	context.Context,
	string,
	int,
) ([]core.MediaServerConnection, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]core.MediaServerConnection(nil), l.servers...), l.err
}

func (l *managerServerLister) set(servers ...core.MediaServer) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.servers = make([]core.MediaServerConnection, 0, len(servers))
	for _, server := range servers {
		l.servers = append(l.servers, core.MediaServerConnection{Server: server})
	}
}

type managerBlockingSource struct {
	entered chan struct{}
	exited  chan struct{}
	once    sync.Once
}

func (s *managerBlockingSource) ListSessions(ctx context.Context) ([]core.PlaybackSession, error) {
	s.entered <- struct{}{}
	<-ctx.Done()
	s.once.Do(func() { close(s.exited) })
	return nil, ctx.Err()
}

func TestManagerRefreshAddsAndRemovesCollectors(t *testing.T) {
	lister := &managerServerLister{}
	refresh := make(chan time.Time, 2)
	source := &managerBlockingSource{entered: make(chan struct{}, 1), exited: make(chan struct{})}
	manager := newTestManager(t, lister, &memoryPlaybackStore{}, refresh, func(core.MediaServer) Source {
		return source
	})
	if err := manager.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	server := managerTestServer()
	lister.set(server)
	refresh <- time.Time{}
	receiveSignal(t, source.entered)
	lister.set()
	refresh <- time.Time{}
	receiveSignal(t, source.exited)
	assertManagerStops(t, manager)
}

func TestManagerRefreshFailureStopsRunLoop(t *testing.T) {
	lister := &managerServerLister{}
	refresh := make(chan time.Time, 1)
	manager := newTestManager(t, lister, &memoryPlaybackStore{}, refresh, func(core.MediaServer) Source {
		return &sequenceSource{}
	})
	if err := manager.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	wantErr := errors.New("list failed")
	lister.mu.Lock()
	lister.err = wantErr
	lister.mu.Unlock()
	refresh <- time.Time{}
	select {
	case err := <-manager.Errors():
		if !errors.Is(err, wantErr) {
			t.Fatalf("manager error = %v, want %v", err, wantErr)
		}
	case <-time.After(time.Second):
		t.Fatal("manager did not report refresh failure")
	}
	assertManagerStops(t, manager)
}

func TestManagerRestoresLargeOpenSetBeforeCollectorStarts(t *testing.T) {
	now := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)
	store := &memoryPlaybackStore{open: managerOpenWatches(1025, now)}
	lister := &managerServerLister{}
	lister.set(managerTestServer())
	source := &managerBlockingSource{entered: make(chan struct{}, 1), exited: make(chan struct{})}
	manager := newTestManager(t, lister, store, make(chan time.Time), func(core.MediaServer) Source {
		return source
	})
	if err := manager.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	receiveSignal(t, source.entered)
	assertManagerStops(t, manager)
	receiveSignal(t, source.exited)
}

func TestManagerStartsWhileCollectorRetriesLoadFailure(t *testing.T) {
	store := &memoryPlaybackStore{loadErrs: []error{errors.New("database unavailable")}}
	source := &managerBlockingSource{entered: make(chan struct{}, 1), exited: make(chan struct{})}
	manager := managerWithServer(t, store, source)
	if err := manager.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	receiveSignal(t, source.entered)
	assertManagerStops(t, manager)
	receiveSignal(t, source.exited)
}

func TestManagerStartsWhileCollectorRetriesRestoreSaveFailure(t *testing.T) {
	now := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)
	stale := managerOpenWatches(1, now.Add(-time.Hour))
	store := &memoryPlaybackStore{
		open: stale, saveErrs: []error{errors.New("database unavailable")},
	}
	source := &managerBlockingSource{entered: make(chan struct{}, 1), exited: make(chan struct{})}
	manager := managerWithServer(t, store, source)
	if err := manager.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	receiveSignal(t, source.entered)
	assertManagerStops(t, manager)
	receiveSignal(t, source.exited)
}

type blockingSaveStore struct {
	memoryPlaybackStore
	started chan struct{}
	release chan struct{}
}

func (s *blockingSaveStore) SaveWatches(ctx context.Context, _ []core.PlaybackMutation) error {
	s.started <- struct{}{}
	<-s.release
	if ctx.Err() == nil {
		return errors.New("expected collector cancellation")
	}
	return errors.New("media server foreign key removed")
}

func TestManagerStopServerWaitsForInFlightPersistenceRace(t *testing.T) {
	store := &blockingSaveStore{
		started: make(chan struct{}, 1), release: make(chan struct{}),
	}
	lister := &managerServerLister{}
	server := managerTestServer()
	lister.set(server)
	manager := newTestManager(t, lister, store, make(chan time.Time), func(core.MediaServer) Source {
		return &sequenceSource{responses: [][]core.PlaybackSession{{collectorSession()}}}
	})
	if err := manager.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	receiveSignal(t, store.started)
	stopped := make(chan error, 1)
	go func() { stopped <- manager.StopServer(context.Background(), server.ID) }()
	select {
	case err := <-stopped:
		t.Fatalf("StopServer returned before persistence finished: %v", err)
	default:
	}
	close(store.release)
	if err := receiveError(t, stopped); err != nil {
		t.Fatalf("StopServer: %v", err)
	}
	manager.FinishServerDelete(server.ID, true)
	select {
	case err := <-manager.Errors():
		t.Fatalf("deletion race reached manager error channel: %v", err)
	default:
	}
	assertManagerStops(t, manager)
}

func newTestManager(
	t *testing.T,
	lister serverLister,
	store core.PlaybackStore,
	refresh <-chan time.Time,
	factory SourceFactory,
) *Manager {
	t.Helper()
	manager, err := NewManager(lister, store, Config{
		ActiveInterval: 5 * time.Second, IdleInterval: 30 * time.Second,
		MissedPolls: 3, ResumeWindow: 5 * time.Minute, StoreTimeout: time.Second,
	}, factory, testutil.NewFakeClock(time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)),
		slog.New(slog.DiscardHandler), nil, ManagerOptions{
			Refresh: refresh,
			Wait: func(ctx context.Context, _ time.Duration) error {
				if err := ctx.Err(); err != nil {
					return err
				}
				return nil
			},
			RandomInt64: func(upper int64) int64 {
				return (upper - 1) / 2
			},
		})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	return manager
}

func managerWithServer(t *testing.T, store core.PlaybackStore, source Source) *Manager {
	t.Helper()
	lister := &managerServerLister{}
	lister.set(managerTestServer())
	return newTestManager(t, lister, store, make(chan time.Time), func(core.MediaServer) Source {
		return source
	})
}

func managerTestServer() core.MediaServer {
	return core.MediaServer{ID: collectorServerID, Kind: core.MediaServerKindJellyfin, Name: "Home"}
}

func managerOpenWatches(count int, now time.Time) []core.PlaybackWatch {
	watches := make([]core.PlaybackWatch, 0, count)
	for index := range count {
		watch := core.PlaybackWatch{
			ID:            fmt.Sprintf("00000000-0000-4000-8000-%012d", index+1),
			MediaServerID: collectorServerID, MediaUserID: fmt.Sprintf("user-%04d", index),
			DeviceID: "device", ItemID: "item", PlayMethod: core.PlayMethodDirectPlay,
			State: core.WatchPlaying, StartedAt: now.Add(-time.Minute), LastSeenAt: now,
			Source: core.WatchSourcePoll, CreatedAt: now.Add(-time.Minute), UpdatedAt: now,
		}
		watches = append(watches, watch)
	}
	return watches
}

func assertManagerStops(t *testing.T, manager *Manager) {
	t.Helper()
	result := make(chan error, 1)
	go func() { result <- manager.Stop(context.Background()) }()
	if err := receiveError(t, result); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}

func receiveError(t *testing.T, values <-chan error) error {
	t.Helper()
	select {
	case err := <-values:
		return err
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for error result")
		return nil
	}
}
