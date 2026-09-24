package playback

import (
	"fmt"
	"testing"
	"time"

	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/testutil"
)

func TestLibraryCacheEvictsOldestAtCapacity(t *testing.T) {
	clock := testutil.NewFakeClock(time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC))
	cache := newLibraryCache(clock)
	for index := range libraryCacheCapacity + 1 {
		cache.put(fmt.Sprintf("item-%04d", index), core.Library{ID: "library", Name: "Movies"}, true)
	}
	if len(cache.entries) != libraryCacheCapacity {
		t.Fatalf("cache entries = %d, want %d", len(cache.entries), libraryCacheCapacity)
	}
	if _, _, cached := cache.get("item-0000"); cached {
		t.Fatal("oldest cache entry was not evicted")
	}
	if _, found, cached := cache.get("item-4096"); !cached || !found {
		t.Fatalf("newest cache entry = cached %t, found %t", cached, found)
	}
}

func TestLibraryCacheExpiresPositiveAndNegativeEntries(t *testing.T) {
	clock := testutil.NewFakeClock(time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC))
	cache := newLibraryCache(clock)
	cache.put("found", core.Library{ID: "library", Name: "Movies"}, true)
	cache.put("missing", core.Library{}, false)
	clock.Advance(libraryCacheTTL)
	for _, itemID := range []string{"found", "missing"} {
		if _, _, cached := cache.get(itemID); cached {
			t.Fatalf("expired item %q remained cached", itemID)
		}
	}
}
