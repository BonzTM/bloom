package ratings

import (
	"sync"
	"time"

	"github.com/BonzTM/bloom/internal/core"
)

type cacheEntry struct {
	ratings   core.TitleRatings
	expiresAt time.Time
}

// ratingsCache is a bounded TTL map. When it is full, a put first drops
// expired entries and then, if still full, the entry that expires soonest.
// Each clear starts a new generation, and a put from an older one is dropped.
type ratingsCache struct {
	mu       sync.Mutex
	clock    core.Clock
	capacity int
	ttl      time.Duration
	entries  map[string]cacheEntry
	current  uint64
}

func newRatingsCache(clock core.Clock, capacity int, ttl time.Duration) *ratingsCache {
	return &ratingsCache{clock: clock, capacity: capacity, ttl: ttl, entries: make(map[string]cacheEntry, capacity)}
}

func (c *ratingsCache) get(key string) (core.TitleRatings, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[key]
	if !ok {
		return core.TitleRatings{}, false
	}
	if !c.clock.Now().Before(entry.expiresAt) {
		delete(c.entries, key)
		return core.TitleRatings{}, false
	}
	return cloneRatings(entry.ratings), true
}

func (c *ratingsCache) generation() uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.current
}

func (c *ratingsCache) put(key string, ratings core.TitleRatings, generation uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if generation != c.current {
		return
	}
	now := c.clock.Now()
	if _, ok := c.entries[key]; !ok && len(c.entries) >= c.capacity {
		c.evict(now)
	}
	c.entries[key] = cacheEntry{ratings: cloneRatings(ratings), expiresAt: now.Add(c.ttl)}
}

// evict runs at most once per put over at most capacity entries.
func (c *ratingsCache) evict(now time.Time) {
	soonestKey, soonest := "", time.Time{}
	for key, entry := range c.entries {
		if !now.Before(entry.expiresAt) {
			delete(c.entries, key)
			continue
		}
		if soonestKey == "" || entry.expiresAt.Before(soonest) {
			soonestKey, soonest = key, entry.expiresAt
		}
	}
	if len(c.entries) >= c.capacity {
		delete(c.entries, soonestKey)
	}
}

func (c *ratingsCache) clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	clear(c.entries)
	c.current++
}

func cloneRatings(ratings core.TitleRatings) core.TitleRatings {
	return core.TitleRatings{
		IMDb: cloneRating(ratings.IMDb), RottenTomatoes: cloneRating(ratings.RottenTomatoes),
		Metacritic: cloneRating(ratings.Metacritic),
	}
}

func cloneRating(rating *core.TitleRating) *core.TitleRating {
	if rating == nil {
		return nil
	}
	copied := *rating
	return &copied
}
