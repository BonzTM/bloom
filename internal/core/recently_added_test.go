package core

import (
	"testing"
	"time"
)

func TestRecentlyAddedFromCatalogMapsMoviesAndSeriesOnly(t *testing.T) {
	added := time.Date(2026, 10, 20, 8, 0, 0, 0, time.UTC)
	year := int32(2024)
	base := LibraryItem{
		MediaServerID: "11111111-1111-4111-8111-111111111111", ItemID: "item", Name: "Title",
		ProductionYear: &year, DateCreated: &added, TMDBID: 949,
	}
	tests := []struct {
		itemType string
		dated    bool
		kind     MediaKind
		ok       bool
	}{
		{itemType: "Movie", dated: true, kind: MediaKindMovie, ok: true},
		{itemType: "Series", dated: true, kind: MediaKindSeries, ok: true},
		{itemType: "Episode", dated: true},
		{itemType: "movie", dated: true},
		{itemType: "Movie"},
	}
	for _, testCase := range tests {
		item := base
		item.ItemType = testCase.itemType
		if !testCase.dated {
			item.DateCreated = nil
		}
		title, ok := RecentlyAddedFromCatalog(item)
		if ok != testCase.ok {
			t.Fatalf("%s dated=%v ok = %v, want %v", testCase.itemType, testCase.dated, ok, testCase.ok)
		}
		want := RecentlyAddedTitle{
			MediaServerID: base.MediaServerID, ItemID: "item", Kind: testCase.kind, Title: "Title", Year: 2024, TMDBID: 949, AddedAt: added,
		}
		if ok && title != want {
			t.Fatalf("%s title = %+v, want %+v", testCase.itemType, title, want)
		}
	}
}

func TestRecentlyAddedFromCatalogLeavesUnknownYearZero(t *testing.T) {
	added := time.Date(2026, 10, 20, 8, 0, 0, 0, time.UTC)
	title, ok := RecentlyAddedFromCatalog(LibraryItem{ItemType: "Movie", Name: "Undated", DateCreated: &added})
	if !ok || title.Year != 0 {
		t.Fatalf("title = %+v, %v", title, ok)
	}
}

func TestValidRecentlyAddedLimit(t *testing.T) {
	for limit, want := range map[int]bool{0: false, 1: true, MaxRecentlyAddedLimit: true, MaxRecentlyAddedLimit + 1: false, -1: false} {
		if got := ValidRecentlyAddedLimit(limit); got != want {
			t.Fatalf("ValidRecentlyAddedLimit(%d) = %v, want %v", limit, got, want)
		}
	}
}
