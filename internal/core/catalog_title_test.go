package core

import "testing"

func TestCatalogTitleQueryValid(t *testing.T) {
	valid := CatalogTitleQuery{
		MediaServerID: "8a000000-0000-4000-8000-000000000001", Kind: MediaKindMovie, TMDBID: 603, Limit: 1,
	}
	if !valid.Valid() || valid.ItemType() != "Movie" {
		t.Fatalf("valid query = %t, %q", valid.Valid(), valid.ItemType())
	}
	series := valid
	series.Kind, series.Limit = MediaKindSeries, MaxCatalogTitleItems
	if !series.Valid() || series.ItemType() != "Series" {
		t.Fatalf("series query = %t, %q", series.Valid(), series.ItemType())
	}
	invalid := []func(*CatalogTitleQuery){
		func(q *CatalogTitleQuery) { q.MediaServerID = "not-an-id" },
		func(q *CatalogTitleQuery) { q.Kind = "music" },
		func(q *CatalogTitleQuery) { q.TMDBID = 0 },
		func(q *CatalogTitleQuery) { q.TMDBID = MaxCatalogTMDBID + 1 },
		func(q *CatalogTitleQuery) { q.Limit = 0 },
		func(q *CatalogTitleQuery) { q.Limit = MaxCatalogTitleItems + 1 },
	}
	for index, mutate := range invalid {
		query := valid
		mutate(&query)
		if query.Valid() {
			t.Errorf("case %d: %+v is valid", index, query)
		}
	}
}
