package core

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestLibraryItemTMDBIDBelongsToMoviesAndSeries(t *testing.T) {
	tests := []struct {
		itemType string
		tmdbID   int64
		want     bool
	}{
		{itemType: "Movie", tmdbID: 949, want: true},
		{itemType: "Series", tmdbID: MaxCatalogTMDBID, want: true},
		{itemType: "Episode", want: true},
		{itemType: "Episode", tmdbID: 62085},
		{itemType: "Movie", tmdbID: -1},
		{itemType: "Movie", tmdbID: MaxCatalogTMDBID + 1},
	}
	for _, testCase := range tests {
		item := LibraryItem{ItemID: "item", LibraryID: "library", ItemType: testCase.itemType, Name: "Title", TMDBID: testCase.tmdbID}
		if got := item.ValidUpstream(); got != testCase.want {
			t.Errorf("%s TMDBID %d valid = %v, want %v", testCase.itemType, testCase.tmdbID, got, testCase.want)
		}
	}
}

func TestLibraryItemUpstreamProblemNamesTheField(t *testing.T) {
	negative := -time.Second
	year := int32(10000)
	rating := 101.0
	valid := LibraryItem{ItemID: "item", LibraryID: "library", ItemType: "Episode", Name: "Pilot"}
	tests := []struct {
		edit func(*LibraryItem)
		want string
	}{
		{edit: func(*LibraryItem) {}, want: ""},
		{edit: func(i *LibraryItem) { i.ItemID = "" }, want: "item id is invalid"},
		{edit: func(i *LibraryItem) { i.Name = "" }, want: "name is empty"},
		{edit: func(i *LibraryItem) { i.Name = "Burtmizvah \u0096 The Musical" }, want: "name contains control character U+0096 at byte 11"},
		{edit: func(i *LibraryItem) { i.SeriesName = strings.Repeat("a", MaxCatalogTextBytes+1) }, want: "series name exceeds 500 bytes"},
		{edit: func(i *LibraryItem) { i.Name = "\xff" }, want: "name is not valid UTF-8"},
		{edit: func(i *LibraryItem) { i.Genres = []string{"Drama", "Sci\tFi"} }, want: "genre contains control character U+0009 at byte 3"},
		{edit: func(i *LibraryItem) { i.Runtime = &negative }, want: "runtime is negative"},
		{edit: func(i *LibraryItem) { i.ProductionYear = &year }, want: "production year is out of range"},
		{edit: func(i *LibraryItem) { i.CommunityRating = &rating }, want: "community rating is out of range"},
		{edit: func(i *LibraryItem) { i.TMDBID = 62085 }, want: "tmdb id is set on an item that is not a movie or series"},
	}
	for _, testCase := range tests {
		item := valid
		testCase.edit(&item)
		if got := item.UpstreamProblem(); got != testCase.want {
			t.Errorf("UpstreamProblem() = %q, want %q", got, testCase.want)
		}
		if item.ValidUpstream() != (testCase.want == "") {
			t.Errorf("ValidUpstream() disagrees with UpstreamProblem() %q", testCase.want)
		}
	}
}

func TestRejectLibraryItemBoundsWhatTheServerSent(t *testing.T) {
	names := []string{
		strings.Repeat("é", MaxCatalogRejectionTextBytes),
		strings.Repeat("a", MaxCatalogRejectionTextBytes-1) + "😀",
		strings.Repeat("\xff", 100) + "a",
	}
	for _, name := range names {
		rejection := RejectLibraryItem(LibraryItem{ItemID: "item", Name: name}, "name exceeds 500 bytes")
		if len(rejection.Name) > MaxCatalogRejectionTextBytes || !utf8.ValidString(rejection.Name) || rejection.Name == "" {
			t.Errorf("name = %q (%d bytes)", rejection.Name, len(rejection.Name))
		}
		if rejection.ItemID != "item" || rejection.Reason != "name exceeds 500 bytes" {
			t.Errorf("rejection = %+v", rejection)
		}
	}
}
