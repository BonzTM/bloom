package playback

import (
	"time"

	"github.com/BonzTM/bloom/internal/core"
)

const (
	libraryCacheCapacity = 4096
	libraryCacheTTL      = 24 * time.Hour
)

type libraryCacheEntry struct {
	library   core.Library
	found     bool
	expiresAt time.Time
	sequence  uint64
}

type libraryCache struct {
	clock    core.Clock
	entries  map[string]libraryCacheEntry
	next     uint64
	capacity int
	ttl      time.Duration
}

func newLibraryCache(clock core.Clock) *libraryCache {
	return &libraryCache{
		clock: clock, entries: make(map[string]libraryCacheEntry, libraryCacheCapacity),
		capacity: libraryCacheCapacity, ttl: libraryCacheTTL,
	}
}

func (c *libraryCache) get(itemID string) (core.Library, bool, bool) {
	entry, ok := c.entries[itemID]
	if !ok {
		return core.Library{}, false, false
	}
	if !c.clock.Now().Before(entry.expiresAt) {
		delete(c.entries, itemID)
		return core.Library{}, false, false
	}
	return entry.library, entry.found, true
}

func (c *libraryCache) put(itemID string, library core.Library, found bool) {
	if _, exists := c.entries[itemID]; !exists && len(c.entries) >= c.capacity {
		c.evictOldest()
	}
	c.next++
	c.entries[itemID] = libraryCacheEntry{
		library: library, found: found, expiresAt: c.clock.Now().Add(c.ttl), sequence: c.next,
	}
}

func (c *libraryCache) evictOldest() {
	var oldestID string
	var oldestSequence uint64
	for itemID, entry := range c.entries {
		if oldestID == "" || entry.sequence < oldestSequence {
			oldestID, oldestSequence = itemID, entry.sequence
		}
	}
	if oldestID != "" {
		delete(c.entries, oldestID)
	}
}
