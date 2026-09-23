package mediaserver

import (
	"crypto/sha256"
	"strconv"
	"sync"
	"time"

	"github.com/BonzTM/bloom/internal/core"
)

const (
	maxConcurrentCallsPerServer = 4
	maxConcurrentRegistrations  = 4
	bulkheadRetryAfter          = time.Second
	adapterCacheCapacity        = 64
	adapterCacheTTL             = 15 * time.Minute
)

type adapterEntry struct {
	adapter core.MediaServerAdapter
	config  [sha256.Size]byte
	active  uint64
	retired bool
	closed  bool
}

func newAdapterEntry(adapter core.MediaServerAdapter, config [sha256.Size]byte) *adapterEntry {
	return &adapterEntry{adapter: adapter, config: config}
}

type serverLimiter struct {
	slots      chan struct{}
	references uint64
	committed  bool
	deleted    bool
}

type adapterCall struct {
	cache   *adapterCache
	entry   *adapterEntry
	limiter *serverLimiter
	id      string
}

func (c *adapterCall) release() { c.cache.release(c) }

type idleConnectionCloser interface{ CloseIdleConnections() }

func closeIdleConnections(adapter core.MediaServerAdapter) {
	if closer, ok := adapter.(idleConnectionCloser); ok {
		closer.CloseIdleConnections()
	}
}

type adapterCacheItem struct {
	id        string
	entry     *adapterEntry
	expiresAt time.Time
	previous  *adapterCacheItem
	next      *adapterCacheItem
}

type adapterBuild struct {
	id         string
	operation  string
	generation uint64
	limiter    *serverLimiter
}

type adapterBuildState struct {
	generation uint64
	active     uint64
}

type adapterCache struct {
	mu       sync.Mutex
	entries  map[string]*adapterCacheItem
	limiters map[string]*serverLimiter
	newest   *adapterCacheItem
	oldest   *adapterCacheItem
	clock    core.Clock
	capacity int
	ttl      time.Duration
	builds   map[string]adapterBuildState
}

func newAdapterCache(clock core.Clock, capacity int, ttl time.Duration) *adapterCache {
	return &adapterCache{
		entries:  make(map[string]*adapterCacheItem, capacity),
		limiters: make(map[string]*serverLimiter, capacity),
		clock:    clock, capacity: capacity, ttl: ttl,
		builds: make(map[string]adapterBuildState),
	}
}

func (c *adapterCache) begin(id, operation string) adapterBuild {
	c.mu.Lock()
	defer c.mu.Unlock()
	state := c.builds[id]
	state.active++
	c.builds[id] = state
	limiter := c.limiterLocked(id)
	limiter.references++
	return adapterBuild{id: id, operation: operation, generation: state.generation, limiter: limiter}
}

func (c *adapterCache) limiterLocked(id string) *serverLimiter {
	limiter := c.limiters[id]
	if limiter == nil {
		limiter = &serverLimiter{slots: make(chan struct{}, maxConcurrentCallsPerServer)}
		c.limiters[id] = limiter
	}
	return limiter
}

func (c *adapterCache) cancel(build adapterBuild) {
	c.mu.Lock()
	c.finishLocked(build)
	c.releaseLimiterLocked(build.id, build.limiter)
	c.mu.Unlock()
}

func (c *adapterCache) get(
	build adapterBuild, config [sha256.Size]byte,
) (*adapterCall, bool, error) {
	now := c.clock.Now()
	c.mu.Lock()
	if c.buildStaleLocked(build) {
		call, err := c.admitLocked(build, nil)
		c.mu.Unlock()
		return call, true, err
	}
	build.limiter.committed = true
	item := c.entries[build.id]
	if item == nil {
		c.mu.Unlock()
		return nil, false, nil
	}
	if item.entry.config == config && now.Before(item.expiresAt) {
		c.touchLocked(item)
		call, err := c.admitLocked(build, item.entry)
		c.mu.Unlock()
		return call, true, err
	}
	entry := c.removeItemLocked(item)
	toClose := c.retireLocked(entry)
	c.mu.Unlock()
	closeRetired(toClose)
	return nil, false, nil
}

func (c *adapterCache) put(id string, entry *adapterEntry) {
	c.mu.Lock()
	c.limiterLocked(id).committed = true
	var replaced, evicted core.MediaServerAdapter
	if existing := c.entries[id]; existing != nil {
		replaced = c.retireLocked(entry)
	} else {
		evicted = c.retireLocked(c.insertLocked(id, entry))
	}
	c.mu.Unlock()
	closeRetired(replaced)
	closeRetired(evicted)
}

func (c *adapterCache) publish(build adapterBuild, entry *adapterEntry) error {
	c.mu.Lock()
	stale := c.finishLocked(build)
	if stale || build.limiter.deleted {
		rejected := c.retireLocked(entry)
		c.releaseLimiterLocked(build.id, build.limiter)
		c.mu.Unlock()
		closeRetired(rejected)
		return core.ErrNotFound
	}
	build.limiter.committed = true
	var replaced core.MediaServerAdapter
	if existing := c.entries[build.id]; existing != nil {
		replaced = c.retireLocked(c.removeItemLocked(existing))
	}
	evicted := c.retireLocked(c.insertLocked(build.id, entry))
	c.releaseLimiterLocked(build.id, build.limiter)
	c.mu.Unlock()
	closeRetired(replaced)
	closeRetired(evicted)
	return nil
}

func (c *adapterCache) create(
	build adapterBuild,
	factory adapterFactory,
	record core.MediaServerRecord,
	credential string,
) (*adapterCall, error) {
	adapter, err := factory.New(record.Kind, record.BaseURL, credential, record.AllowInsecure)
	if err != nil {
		c.cancel(build)
		return nil, err
	}
	entry := newAdapterEntry(adapter, recordFingerprint(record))
	return c.install(build, entry)
}

func (c *adapterCache) install(build adapterBuild, entry *adapterEntry) (*adapterCall, error) {
	c.mu.Lock()
	var redundant, evicted core.MediaServerAdapter
	if c.buildStaleLocked(build) {
		redundant = c.retireLocked(entry)
		call, err := c.admitLocked(build, nil)
		c.mu.Unlock()
		closeRetired(redundant)
		return call, err
	}
	selected := entry
	if existing := c.entries[build.id]; existing != nil {
		selected = existing.entry
		c.touchLocked(existing)
		redundant = c.retireLocked(entry)
	} else {
		evicted = c.retireLocked(c.insertLocked(build.id, entry))
	}
	call, err := c.admitLocked(build, selected)
	c.mu.Unlock()
	closeRetired(redundant)
	closeRetired(evicted)
	return call, err
}

func (c *adapterCache) admitLocked(build adapterBuild, entry *adapterEntry) (*adapterCall, error) {
	stale := c.finishLocked(build)
	if stale || build.limiter.deleted || entry == nil || entry.retired {
		c.releaseLimiterLocked(build.id, build.limiter)
		return nil, core.ErrNotFound
	}
	build.limiter.committed = true
	select {
	case build.limiter.slots <- struct{}{}:
		entry.active++
		return &adapterCall{cache: c, entry: entry, limiter: build.limiter, id: build.id}, nil
	default:
		c.releaseLimiterLocked(build.id, build.limiter)
		return nil, saturationError(build.operation)
	}
}

func (c *adapterCache) release(call *adapterCall) {
	c.mu.Lock()
	<-call.limiter.slots
	call.entry.active--
	c.releaseLimiterLocked(call.id, call.limiter)
	toClose := c.closeRetiredLocked(call.entry)
	c.mu.Unlock()
	closeRetired(toClose)
}

func (c *adapterCache) finishLocked(build adapterBuild) bool {
	state := c.builds[build.id]
	stale := state.generation != build.generation
	state.active--
	if state.active == 0 {
		delete(c.builds, build.id)
	} else {
		c.builds[build.id] = state
	}
	return stale
}

func (c *adapterCache) buildStaleLocked(build adapterBuild) bool {
	state := c.builds[build.id]
	return state.generation != build.generation || build.limiter.deleted
}

func (c *adapterCache) releaseLimiterLocked(id string, limiter *serverLimiter) {
	limiter.references--
	if limiter.references == 0 && (limiter.deleted || !limiter.committed) && c.limiters[id] == limiter {
		delete(c.limiters, id)
	}
}

func (c *adapterCache) retireLocked(entry *adapterEntry) core.MediaServerAdapter {
	if entry == nil {
		return nil
	}
	entry.retired = true
	return c.closeRetiredLocked(entry)
}

func (c *adapterCache) closeRetiredLocked(entry *adapterEntry) core.MediaServerAdapter {
	if entry == nil || !entry.retired || entry.active != 0 || entry.closed {
		return nil
	}
	entry.closed = true
	return entry.adapter
}

func closeRetired(adapter core.MediaServerAdapter) {
	if adapter != nil {
		closeIdleConnections(adapter)
	}
}

func (c *adapterCache) insertLocked(id string, entry *adapterEntry) *adapterEntry {
	item := &adapterCacheItem{id: id, entry: entry, expiresAt: c.clock.Now().Add(c.ttl)}
	c.entries[id] = item
	c.linkNewestLocked(item)
	if len(c.entries) <= c.capacity {
		return nil
	}
	return c.removeItemLocked(c.oldest)
}

func (c *adapterCache) removeItemLocked(item *adapterCacheItem) *adapterEntry {
	delete(c.entries, item.id)
	c.detachLocked(item)
	return item.entry
}

func (c *adapterCache) touchLocked(item *adapterCacheItem) {
	if item == c.newest {
		return
	}
	c.detachLocked(item)
	c.linkNewestLocked(item)
}

func (c *adapterCache) linkNewestLocked(item *adapterCacheItem) {
	item.next = c.newest
	if c.newest == nil {
		c.oldest = item
	} else {
		c.newest.previous = item
	}
	c.newest = item
}

func (c *adapterCache) detachLocked(item *adapterCacheItem) {
	if item.previous == nil {
		c.newest = item.next
	} else {
		item.previous.next = item.next
	}
	if item.next == nil {
		c.oldest = item.previous
	} else {
		item.next.previous = item.previous
	}
	item.previous = nil
	item.next = nil
}

func recordFingerprint(record core.MediaServerRecord) [sha256.Size]byte {
	material := string(record.Kind) + "\x00" + record.BaseURL + "\x00" +
		strconv.FormatBool(record.AllowInsecure) + "\x00" + string(record.CredentialCiphertext)
	return sha256.Sum256([]byte(material))
}

func (c *adapterCache) remove(id string) {
	c.mu.Lock()
	c.invalidateLocked(id)
	var toClose core.MediaServerAdapter
	if item := c.entries[id]; item != nil {
		toClose = c.retireLocked(c.removeItemLocked(item))
	}
	if limiter := c.limiters[id]; limiter != nil {
		limiter.deleted = true
		if limiter.references == 0 {
			delete(c.limiters, id)
		}
	}
	c.mu.Unlock()
	closeRetired(toClose)
}

func (c *adapterCache) invalidateLocked(id string) {
	state, exists := c.builds[id]
	if !exists {
		return
	}
	state.generation++
	c.builds[id] = state
}

func (c *adapterCache) closeIdleConnections() {
	c.mu.Lock()
	entries := make([]core.MediaServerAdapter, 0, len(c.entries))
	for item := c.newest; item != nil; item = item.next {
		if adapter := c.retireLocked(item.entry); adapter != nil {
			entries = append(entries, adapter)
		}
	}
	clear(c.entries)
	c.newest = nil
	c.oldest = nil
	for id := range c.builds {
		c.invalidateLocked(id)
	}
	c.mu.Unlock()
	for _, adapter := range entries {
		closeRetired(adapter)
	}
}
