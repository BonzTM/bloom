package stats

import (
	"fmt"
	"sync"
	"time"

	"github.com/BonzTM/bloom/internal/core"
)

type cacheKey struct {
	startUnixNano int64
	endUnixNano   int64
	server        string
	library       string
	zone          string
	kind          string
}

type cacheEntry struct {
	key       cacheKey
	value     core.StatsResult
	expiresAt time.Time
	previous  *cacheEntry
	next      *cacheEntry
}

type resultCache struct {
	mu       sync.Mutex
	clock    core.Clock
	capacity int
	ttl      time.Duration
	entries  map[cacheKey]*cacheEntry
	newest   *cacheEntry
	oldest   *cacheEntry
}

func newResultCache(clock core.Clock, capacity int, ttl time.Duration) *resultCache {
	return &resultCache{
		clock: clock, capacity: capacity, ttl: ttl,
		entries: make(map[cacheKey]*cacheEntry, capacity),
	}
}

func resultCacheKey(query core.StatsQuery) cacheKey {
	kind := string(query.Report)
	if query.Report == core.StatsReportTitles {
		kind += ":" + string(query.TitleKind)
	}
	if query.Report == core.StatsReportUser {
		kind += fmt.Sprintf(":%s:%s", query.UserServerID, query.MediaUserID)
	}
	return cacheKey{
		startUnixNano: query.Window.Start.UnixNano(), endUnixNano: query.Window.End.UnixNano(),
		server:  query.Window.MediaServerID,
		library: query.LibraryID,
		zone:    query.Window.Zone, kind: kind,
	}
}

func (c *resultCache) get(key cacheKey) (core.StatsResult, bool) {
	if c.ttl == 0 {
		return core.StatsResult{}, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	entry := c.entries[key]
	if entry == nil {
		return core.StatsResult{}, false
	}
	if !c.clock.Now().Before(entry.expiresAt) {
		c.remove(entry)
		return core.StatsResult{}, false
	}
	c.touch(entry)
	return cloneResult(entry.value), true
}

func (c *resultCache) put(key cacheKey, value core.StatsResult) {
	if c.ttl == 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if existing := c.entries[key]; existing != nil {
		existing.value, existing.expiresAt = cloneResult(value), c.clock.Now().Add(c.ttl)
		c.touch(existing)
		return
	}
	entry := &cacheEntry{key: key, value: cloneResult(value), expiresAt: c.clock.Now().Add(c.ttl)}
	c.entries[key] = entry
	c.linkNewest(entry)
	if len(c.entries) > c.capacity {
		c.remove(c.oldest)
	}
}

func (c *resultCache) touch(entry *cacheEntry) {
	if entry == c.newest {
		return
	}
	c.detach(entry)
	c.linkNewest(entry)
}

func (c *resultCache) linkNewest(entry *cacheEntry) {
	entry.next = c.newest
	if c.newest != nil {
		c.newest.previous = entry
	} else {
		c.oldest = entry
	}
	c.newest = entry
}

func (c *resultCache) detach(entry *cacheEntry) {
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

func (c *resultCache) remove(entry *cacheEntry) {
	delete(c.entries, entry.key)
	c.detach(entry)
}

func cloneResult(value core.StatsResult) core.StatsResult {
	value.Titles = append([]core.StatsTitle(nil), value.Titles...)
	value.Users = append([]core.StatsUser(nil), value.Users...)
	value.Libraries = append([]core.StatsLibrary(nil), value.Libraries...)
	value.Clients = append([]core.StatsBreakdown(nil), value.Clients...)
	value.Devices = append([]core.StatsBreakdown(nil), value.Devices...)
	value.PlayMethods = append([]core.StatsBreakdown(nil), value.PlayMethods...)
	value.Daily = append([]core.StatsDailyBucket(nil), value.Daily...)
	value.Weekdays = append([]core.StatsWeekdayBucket(nil), value.Weekdays...)
	value.Hours = append([]core.StatsHourBucket(nil), value.Hours...)
	value.Watches = append([]core.PlaybackWatch(nil), value.Watches...)
	return value
}
