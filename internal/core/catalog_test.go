package core

import "testing"

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
