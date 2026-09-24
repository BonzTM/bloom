package playback

import (
	"context"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/BonzTM/bloom/internal/core"
)

const (
	libraryResolutionQueueCapacity = 256
	libraryResolutionBatchTimeout  = 10 * time.Second
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
		queue: newLibraryResolutionQueue(libraryResolutionQueueCapacity),
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
	for attempts, processed := 0, 0; attempts < core.MaxPlaybackLibraryBackfillItems &&
		processed < libraryResolutionQueueCapacity && batchCtx.Err() == nil; processed++ {
		itemID, ok := r.queue.pop()
		if !ok {
			break
		}
		if r.resolveOne(batchCtx, itemID) {
			attempts++
		}
		r.queue.finish(itemID)
	}
	if r.queue.hasItems() || r.backfillRequested.Load() {
		r.signal()
	}
}

func (r *libraryResolverWorker) enqueueForeground(mutations []core.PlaybackMutation) {
	for _, mutation := range slices.Backward(mutations) {
		watch := mutation.Watch
		if watch.LibraryID == "" {
			r.enqueue(watch.ItemID, true)
		}
	}
}

func (r *libraryResolverWorker) requestBackfill() {
	r.backfillRequested.Store(true)
	r.signal()
}

func (r *libraryResolverWorker) enqueue(itemID string, foreground bool) bool {
	accepted, dropped := r.queue.enqueue(itemID, foreground)
	if dropped {
		r.observe("dropped")
	}
	if accepted {
		r.signal()
	}
	return accepted || !dropped
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
	completed := true
	for _, itemID := range items {
		if _, found, cached := r.cache.get(itemID); cached && !found {
			r.cursor = itemID
			continue
		}
		if !r.enqueue(itemID, false) {
			completed = false
			break
		}
		r.cursor = itemID
	}
	if completed && len(items) < core.MaxPlaybackLibraryBackfillItems {
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
		r.observe("failed")
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
		r.deps.Observer.IncLibraryResolution(r.server.ID, outcome)
	}
}

func (r *libraryResolverWorker) signal() {
	select {
	case r.wake <- struct{}{}:
	default:
	}
}

type libraryResolutionQueue struct {
	mu       sync.Mutex
	items    []string
	present  map[string]struct{}
	capacity int
}

func newLibraryResolutionQueue(capacity int) *libraryResolutionQueue {
	return &libraryResolutionQueue{
		items: make([]string, 0, capacity), present: make(map[string]struct{}, capacity),
		capacity: capacity,
	}
}

func (q *libraryResolutionQueue) enqueue(itemID string, foreground bool) (bool, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if _, exists := q.present[itemID]; exists {
		q.promote(itemID, foreground)
		return false, false
	}
	dropped := false
	if len(q.present) == q.capacity {
		if !foreground || len(q.items) == 0 {
			return false, true
		}
		last := len(q.items) - 1
		delete(q.present, q.items[last])
		q.items = q.items[:last]
		dropped = true
	}
	q.present[itemID] = struct{}{}
	if foreground {
		q.items = append(q.items, "")
		copy(q.items[1:], q.items[:len(q.items)-1])
		q.items[0] = itemID
	} else {
		q.items = append(q.items, itemID)
	}
	return true, dropped
}

func (q *libraryResolutionQueue) promote(itemID string, foreground bool) bool {
	for index, queuedID := range q.items {
		if queuedID != itemID {
			continue
		}
		if !foreground || index == 0 {
			return true
		}
		copy(q.items[1:index+1], q.items[:index])
		q.items[0] = itemID
		return true
	}
	return false
}

func (q *libraryResolutionQueue) pop() (string, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.items) == 0 {
		return "", false
	}
	itemID := q.items[0]
	q.items = q.items[1:]
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
	return len(q.items) > 0
}
