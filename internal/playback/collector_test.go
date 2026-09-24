package playback

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/testutil"
)

const collectorServerID = "11111111-1111-4111-8111-111111111111"

type memoryPlaybackStore struct {
	mu            sync.Mutex
	open          []core.PlaybackWatch
	recent        []core.PlaybackWatch
	mutations     []core.PlaybackMutation
	queries       []core.PlaybackQuery
	loadErrs      []error
	saveErrs      []error
	loads         int
	saves         int
	calls         []storeContextCall
	unresolved    []string
	backfills     []core.Library
	backfillItems []string
	backfilled    chan string
}

type storeContextCall struct {
	operation   string
	hasDeadline bool
}

func (s *memoryPlaybackStore) LoadOpenWatches(ctx context.Context, _ string) ([]core.PlaybackWatch, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.recordContext(ctx, "load")
	index := s.loads
	s.loads++
	if index < len(s.loadErrs) && s.loadErrs[index] != nil {
		return nil, s.loadErrs[index]
	}
	return append([]core.PlaybackWatch(nil), s.open...), nil
}

func (s *memoryPlaybackStore) SaveWatches(ctx context.Context, mutations []core.PlaybackMutation) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.recordContext(ctx, "save")
	index := s.saves
	s.saves++
	if index < len(s.saveErrs) && s.saveErrs[index] != nil {
		return s.saveErrs[index]
	}
	s.mutations = append(s.mutations, mutations...)
	return nil
}

func (s *memoryPlaybackStore) ListWatches(
	ctx context.Context,
	query core.PlaybackQuery,
) ([]core.PlaybackWatch, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.recordContext(ctx, "list")
	s.queries = append(s.queries, query)
	if query.Mode == core.PlaybackQueryRecent {
		for _, watch := range s.recent {
			key := watch.Key()
			if key.MediaServerID == query.Key.MediaServerID && key.MediaUserID == query.Key.MediaUserID &&
				key.DeviceID == query.Key.DeviceID && key.ItemID == query.Key.ItemID &&
				(key.ServerSessionID == query.Key.ServerSessionID || key.ServerSessionID == "" ||
					query.Key.ServerSessionID == "") && watch.EndedAt != nil &&
				!watch.EndedAt.Before(query.EndedAfter) {
				return []core.PlaybackWatch{watch}, nil
			}
		}
		return nil, nil
	}
	return append([]core.PlaybackWatch(nil), s.recent...), nil
}

func (s *memoryPlaybackStore) ListUnresolvedWatchItemIDs(
	ctx context.Context, _, afterItemID string, limit int,
) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.recordContext(ctx, "list unresolved")
	items := make([]string, 0, min(limit, len(s.unresolved)))
	for _, itemID := range s.unresolved {
		if itemID > afterItemID && len(items) < limit {
			items = append(items, itemID)
		}
	}
	return items, nil
}

func (s *memoryPlaybackStore) BackfillWatchLibrary(
	ctx context.Context, _, itemID string, library core.Library,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.recordContext(ctx, "backfill")
	s.backfillItems = append(s.backfillItems, itemID)
	s.backfills = append(s.backfills, library)
	for index := range s.mutations {
		if s.mutations[index].Watch.ItemID == itemID && s.mutations[index].Watch.LibraryID == "" {
			s.mutations[index].Watch.LibraryID = library.ID
			s.mutations[index].Watch.LibraryName = library.Name
		}
	}
	if s.backfilled != nil {
		select {
		case s.backfilled <- itemID:
		default:
		}
	}
	return nil
}

func (s *memoryPlaybackStore) recordContext(ctx context.Context, operation string) {
	_, hasDeadline := ctx.Deadline()
	s.calls = append(s.calls, storeContextCall{operation: operation, hasDeadline: hasDeadline})
}

type sequenceSource struct {
	mu        sync.Mutex
	responses [][]core.PlaybackSession
	errors    []error
	calls     int
}

type collectorObserver struct {
	mu        sync.Mutex
	closed    map[string]int
	libraries map[string]int
}

func (*collectorObserver) ObservePlaybackPoll(string, string, float64) {}
func (*collectorObserver) SetOpenWatches(string, string, int)          {}
func (o *collectorObserver) IncWatchesClosed(_, reason string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.closed[reason]++
}

func (o *collectorObserver) IncLibraryResolution(_, outcome string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.libraries[outcome]++
}

type resolvingSource struct {
	*sequenceSource
	mu        sync.Mutex
	responses map[string]core.Library
	found     map[string]bool
	errors    []error
	calls     []string
	called    chan string
}

type blockingLibrarySource struct {
	*sequenceSource
	library  core.Library
	found    bool
	entered  chan struct{}
	release  chan struct{}
	finished chan struct{}
}

func (s *blockingLibrarySource) ResolveLibrary(
	context.Context, string,
) (core.Library, bool, error) {
	s.entered <- struct{}{}
	<-s.release
	s.finished <- struct{}{}
	return s.library, s.found, nil
}

func (s *resolvingSource) ResolveLibrary(
	_ context.Context, itemID string,
) (core.Library, bool, error) {
	s.mu.Lock()
	s.calls = append(s.calls, itemID)
	index := len(s.calls) - 1
	library, found := s.responses[itemID], s.found[itemID]
	var err error
	if index < len(s.errors) && s.errors[index] != nil {
		err = s.errors[index]
	}
	s.mu.Unlock()
	if s.called != nil {
		s.called <- itemID
	}
	return library, found, err
}

func (s *sequenceSource) ListSessions(context.Context) ([]core.PlaybackSession, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	index := s.calls
	s.calls++
	if index < len(s.errors) && s.errors[index] != nil {
		return nil, s.errors[index]
	}
	if index >= len(s.responses) {
		return nil, nil
	}
	return append([]core.PlaybackSession(nil), s.responses[index]...), nil
}

func testCollector(
	t *testing.T,
	source Source,
	store core.PlaybackPersistence,
	wait func(context.Context, time.Duration) error,
) (context.CancelFunc, <-chan error) {
	t.Helper()
	clock := testutil.NewFakeClock(time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC))
	collector, err := NewCollector(core.MediaServer{
		ID: collectorServerID, Kind: core.MediaServerKindJellyfin,
	}, Config{
		ActiveInterval: 5 * time.Second, IdleInterval: 30 * time.Second,
		MissedPolls: 3, ResumeWindow: 5 * time.Minute, StoreTimeout: time.Second,
	}, Dependencies{
		Store: store, Source: source, Clock: clock,
		Logger: slog.New(slog.DiscardHandler), Wait: wait,
		RandomInt64: func(upper int64) int64 { return (upper - 1) / 2 },
		NewID: func() (string, error) {
			return "00000000-0000-4000-8000-000000000001", nil
		},
	})
	if err != nil {
		t.Fatalf("NewCollector: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- collector.Run(ctx) }()
	return cancel, done
}

func TestCollectorSwitchesFromIdleToActiveInterval(t *testing.T) {
	source := &sequenceSource{responses: [][]core.PlaybackSession{{}, {collectorSession()}}}
	store := &memoryPlaybackStore{}
	delays := make(chan time.Duration, 2)
	release := make(chan struct{}, 1)
	wait := func(ctx context.Context, delay time.Duration) error {
		delays <- delay
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	cancel, done := testCollector(t, source, store, wait)
	if delay := receiveDuration(t, delays); delay != 30*time.Second {
		t.Fatalf("idle delay = %s, want 30s", delay)
	}
	release <- struct{}{}
	if delay := receiveDuration(t, delays); delay != 5*time.Second {
		t.Fatalf("active delay = %s, want 5s", delay)
	}
	cancel()
	assertCollectorStopped(t, done)
}

type blockingSource struct {
	active, maximum atomic.Int32
	entered         chan struct{}
	release         chan struct{}
}

func (s *blockingSource) ListSessions(ctx context.Context) ([]core.PlaybackSession, error) {
	active := s.active.Add(1)
	defer s.active.Add(-1)
	for {
		maximum := s.maximum.Load()
		if active <= maximum || s.maximum.CompareAndSwap(maximum, active) {
			break
		}
	}
	s.entered <- struct{}{}
	select {
	case <-s.release:
		return nil, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func TestCollectorAllowsOnlyOnePollInFlight(t *testing.T) {
	source := &blockingSource{entered: make(chan struct{}, 2), release: make(chan struct{})}
	wait := func(ctx context.Context, _ time.Duration) error { <-ctx.Done(); return ctx.Err() }
	cancel, done := testCollector(t, source, &memoryPlaybackStore{}, wait)
	receiveSignal(t, source.entered)
	select {
	case <-source.entered:
		t.Fatal("a second poll started before the first completed")
	default:
	}
	if source.maximum.Load() != 1 {
		t.Fatalf("maximum in-flight polls = %d, want 1", source.maximum.Load())
	}
	cancel()
	assertCollectorStopped(t, done)
}

func TestCollectorBacksOffAfterConsecutiveFailures(t *testing.T) {
	wantErr := errors.New("upstream unavailable")
	source := &sequenceSource{errors: []error{wantErr, wantErr}}
	delays := make(chan time.Duration, 2)
	ctx, cancel := context.WithCancel(context.Background())
	clock := testutil.NewFakeClock(time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC))
	collector, err := NewCollector(core.MediaServer{ID: collectorServerID, Kind: core.MediaServerKindJellyfin}, Config{
		ActiveInterval: 5 * time.Second, IdleInterval: 30 * time.Second,
		MissedPolls: 3, ResumeWindow: 5 * time.Minute, StoreTimeout: time.Second,
	}, Dependencies{
		Store: &memoryPlaybackStore{}, Source: source, Clock: clock,
		Logger:      slog.New(slog.DiscardHandler),
		RandomInt64: func(upper int64) int64 { return upper - 1 },
		Wait: func(_ context.Context, delay time.Duration) error {
			delays <- delay
			if len(delays) == cap(delays) {
				cancel()
				return context.Canceled
			}
			return nil
		},
	})
	if err != nil {
		t.Fatalf("NewCollector: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- collector.Run(ctx) }()
	first, second := receiveDuration(t, delays), receiveDuration(t, delays)
	if first != time.Second || second != 2*time.Second {
		t.Fatalf("failure delays = [%s %s], want [1s 2s]", first, second)
	}
	assertCollectorStopped(t, done)
}

func TestCollectorFailureBackoffUsesFullJitterAndCap(t *testing.T) {
	collector := &Collector{deps: Dependencies{RandomInt64: func(int64) int64 { return 0 }}}
	if got := collector.nextDelay(1); got != 0 {
		t.Fatalf("minimum full jitter = %s, want 0", got)
	}
	collector.deps.RandomInt64 = func(upper int64) int64 { return upper - 1 }
	if got := collector.nextDelay(1); got != time.Second {
		t.Fatalf("maximum first backoff = %s, want 1s", got)
	}
	if got := collector.nextDelay(100); got != maxFailureBackoff {
		t.Fatalf("capped backoff = %s, want %s", got, maxFailureBackoff)
	}
}

func TestCollectorPersistenceFailureBacksOffAndRecovers(t *testing.T) {
	store := &memoryPlaybackStore{saveErrs: []error{errors.New("foreign key race")}}
	source := &sequenceSource{responses: [][]core.PlaybackSession{{collectorSession()}, {collectorSession()}}}
	delays := make(chan time.Duration, 2)
	release := make(chan struct{}, 1)
	cancel, done := testCollector(t, source, store, func(ctx context.Context, delay time.Duration) error {
		delays <- delay
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	if got := receiveDuration(t, delays); got != 500*time.Millisecond {
		t.Fatalf("persistence failure delay = %s, want 500ms", got)
	}
	release <- struct{}{}
	if got := receiveDuration(t, delays); got != 5*time.Second {
		t.Fatalf("recovered delay = %s, want 5s", got)
	}
	cancel()
	assertCollectorStopped(t, done)
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.saves != 2 || len(store.mutations) != 1 {
		t.Fatalf("persistence attempts = %d, mutations = %d", store.saves, len(store.mutations))
	}
}

func TestCollectorPersistsBeforeSlowLibraryResolutionAndFillsLater(t *testing.T) {
	now := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)
	source := &blockingLibrarySource{
		sequenceSource: &sequenceSource{responses: [][]core.PlaybackSession{{collectorSession()}}},
		library:        core.Library{ID: "library-one", Name: "Movies"}, found: true,
		entered: make(chan struct{}, 1), release: make(chan struct{}), finished: make(chan struct{}, 1),
	}
	store := &memoryPlaybackStore{backfilled: make(chan string, 1)}
	observer := &collectorObserver{closed: make(map[string]int), libraries: make(map[string]int)}
	collector := newLibraryCollector(t, now, source, store, observer, slog.New(slog.DiscardHandler))
	waited := make(chan time.Duration, 1)
	collector.deps.Wait = func(ctx context.Context, delay time.Duration) error {
		waited <- delay
		<-ctx.Done()
		return ctx.Err()
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- collector.Run(ctx) }()
	if delay := receiveDuration(t, waited); delay != 5*time.Second {
		t.Fatalf("poll delay = %s, want 5s", delay)
	}
	receiveSignal(t, source.entered)
	store.mu.Lock()
	if len(store.mutations) != 1 || store.mutations[0].Watch.LibraryID != "" {
		t.Fatalf("pre-resolution mutations = %+v", store.mutations)
	}
	store.mu.Unlock()
	close(source.release)
	if itemID := receiveString(t, store.backfilled); itemID != "item" {
		t.Fatalf("backfilled item = %q, want item", itemID)
	}
	cancel()
	assertCollectorStopped(t, done)
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.mutations[0].Watch.LibraryID != "library-one" {
		t.Fatalf("post-resolution mutation = %+v", store.mutations[0])
	}
}

func TestLibraryResolverCachesNotFoundAcrossBatches(t *testing.T) {
	now := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)
	source := &resolvingSource{
		sequenceSource: &sequenceSource{},
		responses:      make(map[string]core.Library), found: make(map[string]bool),
	}
	store := &memoryPlaybackStore{}
	observer := &collectorObserver{closed: make(map[string]int), libraries: make(map[string]int)}
	collector := newLibraryCollector(t, now, source, store, observer, slog.New(slog.DiscardHandler))
	for range 2 {
		collector.resolution.enqueue("item", true)
		collector.resolution.runBatch(t.Context())
	}
	source.mu.Lock()
	calls := len(source.calls)
	source.mu.Unlock()
	observer.mu.Lock()
	failed := observer.libraries["failed"]
	observer.mu.Unlock()
	if calls != 1 || failed != 1 {
		t.Fatalf("not-found resolution calls = %d, failed metric = %d", calls, failed)
	}
}

func TestLibraryResolverRetriesErrorsInNextBatch(t *testing.T) {
	now := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)
	source := &resolvingSource{
		sequenceSource: &sequenceSource{},
		responses:      map[string]core.Library{"item": {ID: "library-one", Name: "Movies"}},
		found:          map[string]bool{"item": true}, errors: []error{errors.New("sensitive upstream detail")},
	}
	store := &memoryPlaybackStore{}
	observer := &collectorObserver{closed: make(map[string]int), libraries: make(map[string]int)}
	var logs strings.Builder
	collector := newLibraryCollector(t, now, source, store, observer,
		slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	for range 2 {
		collector.resolution.enqueue("item", true)
		collector.resolution.runBatch(t.Context())
	}
	source.mu.Lock()
	calls := len(source.calls)
	source.mu.Unlock()
	if calls != 2 || strings.Contains(logs.String(), "sensitive upstream detail") ||
		!strings.Contains(logs.String(), "item_id=item") {
		t.Fatalf("resolution calls = %d, log = %q", calls, logs.String())
	}
}

func TestLibraryResolutionQueuePrioritizesForegroundAndCountsDrops(t *testing.T) {
	now := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)
	observer := &collectorObserver{closed: make(map[string]int), libraries: make(map[string]int)}
	source := &resolvingSource{
		sequenceSource: &sequenceSource{}, responses: make(map[string]core.Library),
		found: make(map[string]bool),
	}
	collector := newLibraryCollector(
		t, now, source, &memoryPlaybackStore{}, observer, slog.New(slog.DiscardHandler),
	)
	collector.resolution.queue = newLibraryResolutionQueue(2)
	collector.resolution.enqueue("background-a", false)
	collector.resolution.enqueue("background-b", false)
	collector.resolution.enqueue("background-b", true)
	collector.resolution.enqueue("foreground", true)
	first, firstOK := collector.resolution.queue.pop()
	second, secondOK := collector.resolution.queue.pop()
	_, thirdOK := collector.resolution.queue.pop()
	observer.mu.Lock()
	drops := observer.libraries["dropped"]
	observer.mu.Unlock()
	if !firstOK || !secondOK || thirdOK || first != "foreground" || second != "background-b" || drops != 1 {
		t.Fatalf("queue = %q, %q, drops %d", first, second, drops)
	}
}

func TestCollectorShutdownJoinsInFlightLibraryResolution(t *testing.T) {
	source := &blockingLibrarySource{
		sequenceSource: &sequenceSource{responses: [][]core.PlaybackSession{{collectorSession()}}},
		entered:        make(chan struct{}, 1), release: make(chan struct{}), finished: make(chan struct{}, 1),
	}
	cancel, done := testCollector(t, source, &memoryPlaybackStore{}, nil)
	receiveSignal(t, source.entered)
	cancel()
	select {
	case err := <-done:
		t.Fatalf("collector returned before resolver finished: %v", err)
	default:
	}
	close(source.release)
	receiveSignal(t, source.finished)
	assertCollectorStopped(t, done)
}

func newLibraryCollector(
	t *testing.T, now time.Time, source Source, store core.PlaybackPersistence,
	observer Observer, logger *slog.Logger,
) *Collector {
	t.Helper()
	collector, err := NewCollector(managerTestServer(), Config{
		ActiveInterval: 5 * time.Second, IdleInterval: 30 * time.Second,
		MissedPolls: 3, ResumeWindow: 5 * time.Minute, StoreTimeout: time.Second,
	}, Dependencies{
		Store: store, Source: source, Clock: testutil.NewFakeClock(now), Logger: logger,
		Observer: observer, RandomInt64: func(upper int64) int64 { return (upper - 1) / 2 },
		NewID: func() (string, error) {
			return "00000000-0000-4000-8000-000000000001", nil
		},
	})
	if err != nil {
		t.Fatalf("NewCollector: %v", err)
	}
	return collector
}

func TestCollectorObserveFailureDoesNotLoseEarlierStartTransition(t *testing.T) {
	first := collectorSession()
	first.MediaUserID = "user-a"
	second := collectorSession()
	second.MediaUserID = "user-b"
	sessions := []core.PlaybackSession{first, second}
	source := &sequenceSource{responses: [][]core.PlaybackSession{sessions, sessions}}
	store := &memoryPlaybackStore{}
	delays := make(chan time.Duration, 2)
	release := make(chan struct{}, 1)
	ids := 0
	collector, err := NewCollector(managerTestServer(), Config{
		ActiveInterval: 5 * time.Second, IdleInterval: 30 * time.Second,
		MissedPolls: 3, ResumeWindow: 5 * time.Minute, StoreTimeout: time.Second,
	}, Dependencies{
		Store: store, Source: source,
		Clock:  testutil.NewFakeClock(time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)),
		Logger: slog.New(slog.DiscardHandler), RandomInt64: func(upper int64) int64 { return (upper - 1) / 2 },
		Wait: func(ctx context.Context, delay time.Duration) error {
			delays <- delay
			select {
			case <-release:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		},
		NewID: func() (string, error) {
			ids++
			if ids == 2 {
				return "", errors.New("identifier source unavailable")
			}
			return fmt.Sprintf("00000000-0000-4000-8000-%012d", ids), nil
		},
	})
	if err != nil {
		t.Fatalf("NewCollector: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- collector.Run(ctx) }()
	receiveDuration(t, delays)
	release <- struct{}{}
	receiveDuration(t, delays)
	cancel()
	assertCollectorStopped(t, done)
	assertPersistedStart(t, store, first.MediaUserID)
}

func assertPersistedStart(t *testing.T, store *memoryPlaybackStore, mediaUserID string) {
	t.Helper()
	store.mu.Lock()
	defer store.mu.Unlock()
	for _, mutation := range store.mutations {
		if mutation.Watch.MediaUserID == mediaUserID {
			if mutation.SegmentStart == nil || mutation.Position == nil {
				t.Fatalf("first watch mutation lacks complete start transition: %+v", mutation)
			}
			return
		}
	}
	t.Fatalf("no persisted mutation for %s: %+v", mediaUserID, store.mutations)
}

func TestCollectorExitsCleanlyWhenServerIsGone(t *testing.T) {
	source := &sequenceSource{errors: []error{core.ErrNotFound}}
	cancel, done := testCollector(t, source, &memoryPlaybackStore{}, nil)
	defer cancel()
	assertCollectorStopped(t, done)
}

func TestCollectorCancelsInFlightPollOnShutdown(t *testing.T) {
	source := &blockingSource{entered: make(chan struct{}, 1), release: make(chan struct{})}
	cancel, done := testCollector(t, source, &memoryPlaybackStore{}, nil)
	receiveSignal(t, source.entered)
	cancel()
	assertCollectorStopped(t, done)
}

type deadlineBlockingStore struct {
	memoryPlaybackStore
	blockOperation string
	entered        chan struct{}
	exited         chan error
	blocked        bool
}

func (s *deadlineBlockingStore) LoadOpenWatches(ctx context.Context, id string) ([]core.PlaybackWatch, error) {
	if s.block(ctx, "load") {
		return nil, ctx.Err()
	}
	return s.memoryPlaybackStore.LoadOpenWatches(ctx, id)
}

func (s *deadlineBlockingStore) SaveWatches(
	ctx context.Context,
	mutations []core.PlaybackMutation,
) error {
	if s.block(ctx, "save") {
		return ctx.Err()
	}
	return s.memoryPlaybackStore.SaveWatches(ctx, mutations)
}

func (s *deadlineBlockingStore) block(ctx context.Context, operation string) bool {
	s.mu.Lock()
	shouldBlock := !s.blocked && s.blockOperation == operation
	if shouldBlock {
		s.blocked = true
	}
	s.mu.Unlock()
	if !shouldBlock {
		return false
	}
	s.entered <- struct{}{}
	<-ctx.Done()
	s.exited <- ctx.Err()
	return true
}

func TestCollectorStoreDeadlineRetriesAndPollsAfterRecovery(t *testing.T) {
	store := &deadlineBlockingStore{
		blockOperation: "load", entered: make(chan struct{}, 1), exited: make(chan error, 1),
	}
	source := &managerBlockingSource{entered: make(chan struct{}, 1), exited: make(chan struct{})}
	delays := make(chan time.Duration, 1)
	release := make(chan struct{}, 1)
	cancel, done := testCollectorWithTimeout(t, source, store, delays, release)
	defer cancel()
	receiveSignal(t, store.entered)
	if err := receiveError(t, store.exited); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("store error = %v, want deadline exceeded", err)
	}
	if delay := receiveDuration(t, delays); delay != 500*time.Millisecond {
		t.Fatalf("retry delay = %s, want 500ms", delay)
	}
	release <- struct{}{}
	receiveSignal(t, source.entered)
	cancel()
	assertCollectorStopped(t, done)
	receiveSignal(t, source.exited)
}

func TestCollectorShutdownJoinsBlockedStoreCall(t *testing.T) {
	store := &deadlineBlockingStore{
		blockOperation: "save", entered: make(chan struct{}, 1), exited: make(chan error, 1),
	}
	cancel, done := testCollector(t, &sequenceSource{responses: [][]core.PlaybackSession{{collectorSession()}}}, store, nil)
	receiveSignal(t, store.entered)
	cancel()
	assertCollectorStopped(t, done)
	if err := receiveError(t, store.exited); !errors.Is(err, context.Canceled) {
		t.Fatalf("store error = %v, want canceled", err)
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.calls) == 0 || !store.calls[0].hasDeadline {
		t.Fatalf("store calls lack deadline: %+v", store.calls)
	}
}

func testCollectorWithTimeout(
	t *testing.T,
	source Source,
	store core.PlaybackPersistence,
	delays chan<- time.Duration,
	release <-chan struct{},
) (context.CancelFunc, <-chan error) {
	t.Helper()
	collector, err := NewCollector(managerTestServer(), Config{
		ActiveInterval: 5 * time.Second, IdleInterval: 30 * time.Second,
		MissedPolls: 3, ResumeWindow: 5 * time.Minute, StoreTimeout: minStoreTimeout,
	}, Dependencies{
		Store: store, Source: source,
		Clock:       testutil.NewFakeClock(time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)),
		Logger:      slog.New(slog.DiscardHandler),
		RandomInt64: func(upper int64) int64 { return (upper - 1) / 2 },
		Wait: func(ctx context.Context, delay time.Duration) error {
			delays <- delay
			select {
			case <-release:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		},
	})
	if err != nil {
		t.Fatalf("NewCollector: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- collector.Run(ctx) }()
	return cancel, done
}

func TestCollectorAddsDeadlineToEachRestoreSaveBatch(t *testing.T) {
	store := &memoryPlaybackStore{}
	collector := &Collector{
		config: Config{StoreTimeout: time.Second},
		deps:   Dependencies{Store: store},
	}
	mutations := make([]core.PlaybackMutation, core.MaxPlaybackMutations+1)
	if err := collector.saveMutations(context.Background(), mutations); err != nil {
		t.Fatalf("saveMutations: %v", err)
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.calls) != 2 || !store.calls[0].hasDeadline || !store.calls[1].hasDeadline {
		t.Fatalf("restore batch contexts = %+v", store.calls)
	}
}

func TestCollectorResumesExactRecentWatchBeyondBulkLimit(t *testing.T) {
	now := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)
	recent := make([]core.PlaybackWatch, 0, 1025)
	for index := range 1025 {
		endedAt := now.Add(-time.Duration(index+1) * time.Millisecond)
		recent = append(recent, core.PlaybackWatch{
			ID:            fmt.Sprintf("00000000-0000-4000-8000-%012d", index+1),
			MediaServerID: collectorServerID, MediaUserID: fmt.Sprintf("user-%04d", index),
			DeviceID: "device", ItemID: "item", PlayMethod: core.PlayMethodDirectPlay,
			State: core.WatchStopped, StartedAt: now.Add(-time.Hour), LastSeenAt: endedAt,
			EndedAt: &endedAt, Source: core.WatchSourcePoll,
			CreatedAt: now.Add(-time.Hour), UpdatedAt: endedAt,
		})
	}
	target := recent[len(recent)-1]
	store := &memoryPlaybackStore{recent: recent}
	source := &sequenceSource{responses: [][]core.PlaybackSession{{
		{
			MediaUserID: target.MediaUserID, DeviceID: target.DeviceID, ItemID: target.ItemID,
			PlayMethod: core.PlayMethodDirectPlay,
		},
	}}}
	waited := make(chan struct{}, 1)
	cancel, done := testCollector(t, source, store, func(ctx context.Context, _ time.Duration) error {
		waited <- struct{}{}
		<-ctx.Done()
		return ctx.Err()
	})
	receiveSignal(t, waited)
	cancel()
	assertCollectorStopped(t, done)
	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.mutations) != 1 || store.mutations[0].Watch.ID != target.ID {
		t.Fatalf("resume mutations = %+v, want watch %s", store.mutations, target.ID)
	}
	if len(store.queries) != 1 || store.queries[0].Mode != core.PlaybackQueryRecent ||
		store.queries[0].Key != target.Key() {
		t.Fatalf("resume queries = %+v", store.queries)
	}
}

func TestCollectorClosesRestoreOverflowWithLogAndMetric(t *testing.T) {
	now := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)
	store := &memoryPlaybackStore{open: managerOpenWatches(1025, now)}
	source := &blockingSource{entered: make(chan struct{}, 1), release: make(chan struct{})}
	observer := &collectorObserver{closed: make(map[string]int)}
	var logs strings.Builder
	collector, err := NewCollector(managerTestServer(), Config{
		ActiveInterval: 5 * time.Second, IdleInterval: 30 * time.Second,
		MissedPolls: 1, ResumeWindow: 5 * time.Minute, StoreTimeout: time.Second,
	}, Dependencies{
		Store: store, Source: source, Clock: testutil.NewFakeClock(now),
		Logger: slog.New(slog.NewTextHandler(&logs, nil)), Observer: observer,
	})
	if err != nil {
		t.Fatalf("NewCollector: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- collector.Run(ctx) }()
	receiveSignal(t, source.entered)
	cancel()
	assertCollectorStopped(t, done)
	store.mu.Lock()
	mutations := append([]core.PlaybackMutation(nil), store.mutations...)
	store.mu.Unlock()
	observer.mu.Lock()
	overflowCount := observer.closed["overflow"]
	observer.mu.Unlock()
	if len(mutations) != 1 || mutations[0].CloseReason != "overflow" || overflowCount != 1 {
		t.Fatalf("overflow mutations = %+v, metric = %d", mutations, overflowCount)
	}
	if !strings.Contains(logs.String(), "closed playback restore overflow") ||
		!strings.Contains(logs.String(), "count=1") {
		t.Fatalf("overflow log = %q", logs.String())
	}
}

func collectorSession() core.PlaybackSession {
	return core.PlaybackSession{
		ServerSessionID: "session", MediaUserID: "user", DeviceID: "device", ItemID: "item",
		PlayMethod: core.PlayMethodDirectPlay,
	}
}

func receiveDuration(t *testing.T, values <-chan time.Duration) time.Duration {
	t.Helper()
	select {
	case value := <-values:
		return value
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for collector delay")
		return 0
	}
}

func receiveSignal(t *testing.T, values <-chan struct{}) {
	t.Helper()
	select {
	case <-values:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for collector signal")
	}
}

func receiveString(t *testing.T, values <-chan string) string {
	t.Helper()
	select {
	case value := <-values:
		return value
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for collector value")
		return ""
	}
}

func assertCollectorStopped(t *testing.T, done <-chan error) {
	t.Helper()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Collector.Run: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("collector did not stop")
	}
}
