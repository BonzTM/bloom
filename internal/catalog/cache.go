package catalog

import (
	"sync"
	"time"

	"github.com/BonzTM/bloom/internal/core"
)

const catalogCacheCapacity = 256

const (
	cacheLibraries = "libraries"
	cacheGenres    = "genres"
)

type catalogCacheKey struct {
	kind, serverID, libraryID string
	window                    core.CatalogWindow
}

type cacheEntry struct {
	libraries []core.CatalogLibrarySummary
	genres    []core.CatalogGenreSummary
	expiresAt time.Time
}

type resultCache struct {
	mu      sync.Mutex
	clock   core.Clock
	ttl     time.Duration
	entries map[catalogCacheKey]cacheEntry
}

func newResultCache(clock core.Clock, ttl time.Duration) *resultCache {
	return &resultCache{clock: clock, ttl: ttl, entries: make(map[catalogCacheKey]cacheEntry, catalogCacheCapacity)}
}

func canonicalWindow(window core.CatalogWindow, ttl time.Duration) core.CatalogWindow {
	if !window.Enabled {
		return window
	}
	resolution := ttl
	if resolution == 0 {
		resolution = time.Second
	}
	duration := window.End.Sub(window.Start)
	window.End = window.End.Truncate(resolution)
	window.Start = window.End.Add(-duration)
	return window
}

func (c *resultCache) getLibraries(key catalogCacheKey) ([]core.CatalogLibrarySummary, bool) {
	entry, ok := c.get(key)
	return cloneLibraries(entry.libraries), ok
}

func (c *resultCache) putLibraries(key catalogCacheKey, value []core.CatalogLibrarySummary) {
	c.put(key, cacheEntry{libraries: cloneLibraries(value)})
}

func (c *resultCache) getGenres(key catalogCacheKey) ([]core.CatalogGenreSummary, bool) {
	entry, ok := c.get(key)
	return append([]core.CatalogGenreSummary(nil), entry.genres...), ok
}

func (c *resultCache) putGenres(key catalogCacheKey, value []core.CatalogGenreSummary) {
	c.put(key, cacheEntry{genres: append([]core.CatalogGenreSummary(nil), value...)})
}

func (c *resultCache) get(key catalogCacheKey) (cacheEntry, bool) {
	if c.ttl == 0 {
		return cacheEntry{}, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[key]
	if !ok || !c.clock.Now().Before(entry.expiresAt) {
		delete(c.entries, key)
		return cacheEntry{}, false
	}
	return entry, true
}

func (c *resultCache) put(key catalogCacheKey, entry cacheEntry) {
	if c.ttl == 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.entries) >= catalogCacheCapacity && c.entries[key].expiresAt.IsZero() {
		clear(c.entries)
	}
	entry.expiresAt = c.clock.Now().Add(c.ttl)
	c.entries[key] = entry
}

func (c *resultCache) clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	clear(c.entries)
}

func cloneLibraries(value []core.CatalogLibrarySummary) []core.CatalogLibrarySummary {
	result := append([]core.CatalogLibrarySummary(nil), value...)
	for index := range result {
		result[index].Types = append([]core.CatalogTypeCount(nil), result[index].Types...)
	}
	return result
}
