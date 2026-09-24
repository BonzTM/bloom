package playback

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/BonzTM/bloom/internal/core"
)

const (
	libraryResolutionQueueCapacity     = 256
	libraryBackfillQueueCapacity       = 64
	libraryResolutionBatchProcessLimit = libraryResolutionQueueCapacity + libraryBackfillQueueCapacity
	libraryResolutionBatchTimeout      = 10 * time.Second
)

type libraryResolverWorker struct {
	server            core.MediaServer
	config            Config
	deps              Dependencies
	resolver          core.LibraryResolver
	cache             *libraryCache
	queue             *libraryResolutionQueue
	wake              chan struct{}
	backfillRequested atomic.Bool
	cursor            string
}

func newLibraryResolverWorker(
	server core.MediaServer, config Config, deps Dependencies, resolver core.LibraryResolver,
) *libraryResolverWorker {
	return &libraryResolverWorker{
		server: server, config: config, deps: deps, resolver: resolver,
		cache: newLibraryCache(deps.Clock),
		queue: newLibraryResolutionQueue(libraryResolutionQueueCapacity, libraryBackfillQueueCapacity),
		wake:  make(chan struct{}, 1),
	}
}

func (r *libraryResolverWorker) run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-r.wake:
			r.runBatch(ctx)
		}
	}
}

func (r *libraryResolverWorker) runBatch(ctx context.Context) {
	batchCtx, cancel := context.WithTimeout(ctx, libraryResolutionBatchTimeout)
	defer cancel()
	if r.backfillRequested.Swap(false) {
		r.enqueueBackfill(batchCtx)
	}
	attempts, processed := r.reserveBackfillAttempt(batchCtx)
	r.drainQueue(batchCtx, attempts, processed)
	if r.queue.hasItems() || r.backfillRequested.Load() {
		r.signal()
	}
}

func (r *libraryResolverWorker) reserveBackfillAttempt(ctx context.Context) (int, int) {
	if !r.queue.hasForeground() {
		return 0, 0
	}
	for processed := 0; processed < libraryBackfillQueueCapacity && ctx.Err() == nil; processed++ {
		itemID, ok := r.queue.popBackfill()
		if !ok {
			return 0, processed
		}
		attempted := r.resolveOne(ctx, itemID)
		r.queue.finish(itemID)
		if attempted {
			return 1, processed + 1
		}
	}
	return 0, libraryBackfillQueueCapacity
}

func (r *libraryResolverWorker) drainQueue(ctx context.Context, attempts, processed int) {
	for attempts < core.MaxPlaybackLibraryBackfillItems &&
		processed < libraryResolutionBatchProcessLimit && ctx.Err() == nil {
		itemID, ok := r.queue.popForeground()
		if !ok {
			itemID, ok = r.queue.popBackfill()
		}
		if !ok {
			return
		}
		if r.resolveOne(ctx, itemID) {
			attempts++
		}
		r.queue.finish(itemID)
		processed++
	}
}

func (r *libraryResolverWorker) enqueueForeground(mutations []core.PlaybackMutation) {
	for _, mutation := range mutations {
		watch := mutation.Watch
		if watch.LibraryID == "" {
			r.enqueueForegroundID(watch.ItemID)
		}
	}
}

func (r *libraryResolverWorker) requestBackfill() {
	r.backfillRequested.Store(true)
	r.signal()
}

func (r *libraryResolverWorker) enqueueForegroundID(itemID string) {
	admitted, inserted := r.queue.enqueueForeground(itemID)
	if !admitted {
		r.observe("dropped")
	}
	if inserted {
		r.signal()
	}
}

func (r *libraryResolverWorker) enqueueBackfill(ctx context.Context) {
	storeCtx, cancel := context.WithTimeout(ctx, r.config.StoreTimeout)
	items, err := r.deps.Store.ListUnresolvedWatchItemIDs(
		storeCtx, r.server.ID, r.cursor, core.MaxPlaybackLibraryBackfillItems,
	)
	cancel()
	if err != nil {
		r.deps.Logger.DebugContext(ctx, "playback library backfill list failed", "media_server_id", r.server.ID)
		return
	}
	r.advanceBackfill(items)
}

func (r *libraryResolverWorker) advanceBackfill(items []string) {
	for _, itemID := range items {
		if _, found, cached := r.cache.get(itemID); cached && !found {
			r.cursor = itemID
			continue
		}
		admitted, inserted := r.queue.enqueueBackfill(itemID)
		if !admitted {
			r.backfillRequested.Store(true)
			return
		}
		if inserted {
			r.signal()
		}
		r.cursor = itemID
	}
	if len(items) < core.MaxPlaybackLibraryBackfillItems {
		r.cursor = ""
	}
}

func (r *libraryResolverWorker) resolveOne(ctx context.Context, itemID string) bool {
	if library, found, cached := r.cache.get(itemID); cached {
		if found {
			r.backfill(ctx, itemID, library)
		}
		return false
	}
	library, found, err := r.resolver.ResolveLibrary(ctx, itemID)
	if err != nil {
		r.observe("failed")
		r.deps.Logger.DebugContext(ctx, "playback library resolution failed", "item_id", itemID)
		return true
	}
	if !found {
		r.cache.put(itemID, core.Library{}, false)
		r.observe("missing")
		return true
	}
	if !library.Valid() {
		r.observe("failed")
		r.deps.Logger.DebugContext(ctx, "playback library resolution returned invalid data", "item_id", itemID)
		return true
	}
	r.observe("resolved")
	if r.backfill(ctx, itemID, library) {
		r.cache.put(itemID, library, true)
	}
	return true
}

func (r *libraryResolverWorker) backfill(ctx context.Context, itemID string, library core.Library) bool {
	storeCtx, cancel := context.WithTimeout(ctx, r.config.StoreTimeout)
	defer cancel()
	if err := r.deps.Store.BackfillWatchLibrary(storeCtx, r.server.ID, itemID, library); err != nil {
		r.deps.Logger.DebugContext(ctx, "playback library backfill update failed", "item_id", itemID)
		return false
	}
	return true
}

func (r *libraryResolverWorker) observe(outcome string) {
	if r.deps.Observer != nil {
		r.deps.Observer.IncLibraryResolution(outcome)
	}
}

func (r *libraryResolverWorker) signal() {
	select {
	case r.wake <- struct{}{}:
	default:
	}
}

type libraryResolutionQueue struct {
	mu                 sync.Mutex
	foreground         []string
	backfill           []string
	present            map[string]struct{}
	foregroundCapacity int
	backfillCapacity   int
}

func newLibraryResolutionQueue(foregroundCapacity, backfillCapacity int) *libraryResolutionQueue {
	return &libraryResolutionQueue{
		foreground:         make([]string, 0, foregroundCapacity),
		backfill:           make([]string, 0, backfillCapacity),
		present:            make(map[string]struct{}, foregroundCapacity+backfillCapacity),
		foregroundCapacity: foregroundCapacity,
		backfillCapacity:   backfillCapacity,
	}
}

func (q *libraryResolutionQueue) enqueueForeground(itemID string) (bool, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if _, exists := q.present[itemID]; exists {
		return true, false
	}
	if len(q.foreground) == q.foregroundCapacity {
		return false, false
	}
	q.present[itemID] = struct{}{}
	q.foreground = append(q.foreground, itemID)
	return true, true
}

func (q *libraryResolutionQueue) enqueueBackfill(itemID string) (bool, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if _, exists := q.present[itemID]; exists {
		return true, false
	}
	if len(q.backfill) == q.backfillCapacity {
		return false, false
	}
	q.present[itemID] = struct{}{}
	q.backfill = append(q.backfill, itemID)
	return true, true
}

func (q *libraryResolutionQueue) popForeground() (string, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	return popLibraryResolutionItem(&q.foreground)
}

func (q *libraryResolutionQueue) popBackfill() (string, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	return popLibraryResolutionItem(&q.backfill)
}

func popLibraryResolutionItem(items *[]string) (string, bool) {
	if len(*items) == 0 {
		return "", false
	}
	itemID := (*items)[0]
	*items = (*items)[1:]
	return itemID, true
}

func (q *libraryResolutionQueue) finish(itemID string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	delete(q.present, itemID)
}

func (q *libraryResolutionQueue) hasItems() bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.foreground) > 0 || len(q.backfill) > 0
}

func (q *libraryResolutionQueue) hasForeground() bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.foreground) > 0
}
