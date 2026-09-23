package metadata

import (
	"sync"
	"time"

	"github.com/BonzTM/bloom/internal/core"
)

type cacheKey struct {
	provider core.MetadataProviderKind
	id       string
	kind     core.MediaKind
}

type cacheValue struct {
	title  core.MetadataTitle
	series core.MetadataSeries
}

type cacheEntry struct {
	key       cacheKey
	value     cacheValue
	expiresAt time.Time
	previous  *cacheEntry
	next      *cacheEntry
}

type detailCache struct {
	mu       sync.Mutex
	clock    core.Clock
	capacity int
	ttl      time.Duration
	entries  map[cacheKey]*cacheEntry
	newest   *cacheEntry
	oldest   *cacheEntry
}

func newDetailCache(clock core.Clock, capacity int, ttl time.Duration) *detailCache {
	return &detailCache{clock: clock, capacity: capacity, ttl: ttl, entries: make(map[cacheKey]*cacheEntry, capacity)}
}

func (c *detailCache) get(key cacheKey) (cacheValue, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry := c.entries[key]
	if entry == nil {
		return cacheValue{}, false
	}
	if !c.clock.Now().Before(entry.expiresAt) {
		c.remove(entry)
		return cacheValue{}, false
	}
	c.touch(entry)
	return cloneCacheValue(entry.value), true
}

func (c *detailCache) put(key cacheKey, value cacheValue) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if existing := c.entries[key]; existing != nil {
		existing.value, existing.expiresAt = cloneCacheValue(value), c.clock.Now().Add(c.ttl)
		c.touch(existing)
		return
	}
	entry := &cacheEntry{key: key, value: cloneCacheValue(value), expiresAt: c.clock.Now().Add(c.ttl)}
	c.entries[key] = entry
	c.linkNewest(entry)
	if len(c.entries) > c.capacity {
		c.remove(c.oldest)
	}
}

func (c *detailCache) clear() {
	c.mu.Lock()
	clear(c.entries)
	c.newest, c.oldest = nil, nil
	c.mu.Unlock()
}

func (c *detailCache) touch(entry *cacheEntry) {
	if entry == c.newest {
		return
	}
	c.detach(entry)
	c.linkNewest(entry)
}

func (c *detailCache) linkNewest(entry *cacheEntry) {
	entry.next = c.newest
	if c.newest != nil {
		c.newest.previous = entry
	} else {
		c.oldest = entry
	}
	c.newest = entry
}

func (c *detailCache) detach(entry *cacheEntry) {
	if entry.previous != nil {
		entry.previous.next = entry.next
	} else {
		c.newest = entry.next
	}
	if entry.next != nil {
		entry.next.previous = entry.previous
	} else {
		c.oldest = entry.previous
	}
}

func (c *detailCache) remove(entry *cacheEntry) {
	delete(c.entries, entry.key)
	c.detach(entry)
}

func cloneCacheValue(value cacheValue) cacheValue {
	value.series.Seasons = append([]core.MetadataSeason(nil), value.series.Seasons...)
	return value
}
