package mediaserver

import (
	"crypto/sha256"
	"testing"
	"time"

	"github.com/BonzTM/bloom/internal/testutil"
)

func TestAdapterCacheEvictsLeastRecentlyUsedAtCapacity(t *testing.T) {
	clock := testutil.NewFakeClock(time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC))
	cache := newAdapterCache(clock, 2, time.Hour)
	first := newAdapterEntry(&fakeAdapter{}, [sha256.Size]byte{1})
	secondAdapter := &fakeAdapter{}
	second := newAdapterEntry(secondAdapter, [sha256.Size]byte{2})
	third := newAdapterEntry(&fakeAdapter{}, [sha256.Size]byte{3})
	cache.put("first", first)
	cache.put("second", second)
	requireCachedCall(t, cache, "first", first.config).release()
	cache.put("third", third)
	requireCacheMiss(t, cache, "second", second.config)
	if got := secondAdapter.closeCalls.Load(); got != 1 {
		t.Fatalf("evicted adapter closes = %d, want 1", got)
	}
	requireCachedCall(t, cache, "first", first.config).release()
	requireCachedCall(t, cache, "third", third.config).release()
}

func TestAdapterCacheExpiresAndClosesEntry(t *testing.T) {
	clock := testutil.NewFakeClock(time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC))
	cache := newAdapterCache(clock, 2, time.Minute)
	adapter := &fakeAdapter{}
	entry := newAdapterEntry(adapter, [sha256.Size]byte{1})
	cache.put("server", entry)
	clock.Advance(time.Minute)
	requireCacheMiss(t, cache, "server", entry.config)
	if got := adapter.closeCalls.Load(); got != 1 {
		t.Fatalf("expired adapter closes = %d, want 1", got)
	}
}

func TestAdapterEntryReleaseClosesConnectionAfterShutdown(t *testing.T) {
	clock := testutil.NewFakeClock(time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC))
	cache := newAdapterCache(clock, 1, time.Hour)
	adapter := &fakeAdapter{}
	entry := newAdapterEntry(adapter, [sha256.Size]byte{1})
	cache.put("server", entry)
	call := requireCachedCall(t, cache, "server", entry.config)
	cache.closeIdleConnections()
	if got := adapter.closeCalls.Load(); got != 0 {
		t.Fatalf("shutdown closes before release = %d, want 0", got)
	}
	requireCacheMiss(t, cache, "server", entry.config)
	call.release()
	if got := adapter.closeCalls.Load(); got != 1 {
		t.Fatalf("shutdown closes after release = %d, want 1", got)
	}
}

func TestAdapterLimiterDeletionWaitsForActiveCall(t *testing.T) {
	clock := testutil.NewFakeClock(time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC))
	cache := newAdapterCache(clock, 1, time.Hour)
	adapter := &fakeAdapter{}
	entry := newAdapterEntry(adapter, [sha256.Size]byte{1})
	cache.put("server", entry)
	call := requireCachedCall(t, cache, "server", entry.config)
	cache.remove("server")
	assertLimiterState(t, cache, "server", true, 1, true)
	if got := adapter.closeCalls.Load(); got != 0 {
		t.Fatalf("delete closes before release = %d, want 0", got)
	}
	call.release()
	assertLimiterState(t, cache, "server", false, 0, false)
	if got := adapter.closeCalls.Load(); got != 1 {
		t.Fatalf("delete closes after release = %d, want 1", got)
	}
}

func requireCachedCall(
	t *testing.T, cache *adapterCache, id string, config [sha256.Size]byte,
) *adapterCall {
	t.Helper()
	build := cache.begin(id, "test")
	call, found, err := cache.get(build, config)
	if err != nil || !found || call == nil {
		t.Fatalf("cache get %q = (%v, %t, %v), want admitted hit", id, call, found, err)
	}
	return call
}

func requireCacheMiss(t *testing.T, cache *adapterCache, id string, config [sha256.Size]byte) {
	t.Helper()
	build := cache.begin(id, "test")
	call, found, err := cache.get(build, config)
	if err != nil || found || call != nil {
		t.Fatalf("cache get %q = (%v, %t, %v), want miss", id, call, found, err)
	}
	cache.cancel(build)
}

func assertLimiterState(
	t *testing.T, cache *adapterCache, id string, wantExists bool, wantReferences uint64, wantDeleted bool,
) {
	t.Helper()
	cache.mu.Lock()
	limiter := cache.limiters[id]
	var references uint64
	var deleted bool
	if limiter != nil {
		references, deleted = limiter.references, limiter.deleted
	}
	cache.mu.Unlock()
	if (limiter != nil) != wantExists {
		t.Fatalf("limiter exists = %t, want %t", limiter != nil, wantExists)
	}
	if limiter != nil && (references != wantReferences || deleted != wantDeleted) {
		t.Fatalf("limiter = {references:%d deleted:%t}, want {%d %t}", references, deleted, wantReferences, wantDeleted)
	}
}
