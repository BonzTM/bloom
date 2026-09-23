package metadata

import (
	"testing"
	"time"

	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/testutil"
)

func TestDetailCacheHitExpiryAndBound(t *testing.T) {
	clock := testutil.NewFakeClock(time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC))
	cache := newDetailCache(clock, 2, time.Minute)
	first := cacheKey{provider: core.MetadataProviderTMDB, kind: core.MediaKindMovie, id: "1"}
	second := cacheKey{provider: core.MetadataProviderTMDB, kind: core.MediaKindMovie, id: "2"}
	third := cacheKey{provider: core.MetadataProviderTMDB, kind: core.MediaKindMovie, id: "3"}
	cache.put(first, cacheValue{title: core.MetadataTitle{Title: "one"}})
	if value, ok := cache.get(first); !ok || value.title.Title != "one" {
		t.Fatalf("cache hit = %+v, %v; want one, true", value, ok)
	}
	cache.put(second, cacheValue{title: core.MetadataTitle{Title: "two"}})
	if _, ok := cache.get(first); !ok {
		t.Fatal("first entry was not available before eviction")
	}
	cache.put(third, cacheValue{title: core.MetadataTitle{Title: "three"}})
	if _, ok := cache.get(second); ok {
		t.Fatal("least-recently-used entry survived capacity bound")
	}
	if len(cache.entries) != 2 {
		t.Fatalf("cache entries = %d, want 2", len(cache.entries))
	}
	clock.Advance(time.Minute)
	if _, ok := cache.get(first); ok {
		t.Fatal("expired cache entry returned as a hit")
	}
}

func TestDetailCacheClonesSeriesSeasons(t *testing.T) {
	clock := testutil.NewFakeClock(time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC))
	cache := newDetailCache(clock, 1, time.Minute)
	key := cacheKey{provider: core.MetadataProviderTMDB, kind: core.MediaKindSeries, id: "1"}
	input := cacheValue{series: core.MetadataSeries{Seasons: []core.MetadataSeason{{Number: 1}}}}
	cache.put(key, input)
	input.series.Seasons[0].Number = 99
	got, ok := cache.get(key)
	if !ok || got.series.Seasons[0].Number != 1 {
		t.Fatalf("cached clone = %+v, %v", got, ok)
	}
}
