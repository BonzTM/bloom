package mediaserver

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/BonzTM/bloom/internal/core"
)

type criticAdapter struct {
	*fakeAdapter
	score  float64
	found  bool
	err    error
	lookup bool
	asked  []string
}

func (a *criticAdapter) Capabilities() core.Capabilities {
	return core.Capabilities{ProviderIDLookup: a.lookup}
}

func (a *criticAdapter) CriticRating(_ context.Context, _ core.MediaKind, tmdbID, title string) (float64, bool, error) {
	a.asked = append(a.asked, tmdbID+" "+title)
	return a.score, a.found, a.err
}

func (a *criticAdapter) ItemCriticRating(
	_ context.Context, _ core.MediaKind, tmdbID string, itemIDs []string,
) (float64, bool, error) {
	a.asked = append(a.asked, tmdbID+" items "+strings.Join(itemIDs, ","))
	return a.score, a.found, a.err
}

type catalogTitles struct {
	itemIDs []string
	err     error
	asked   []core.CatalogTitleQuery
}

func (c *catalogTitles) ListCatalogTitleItemIDs(_ context.Context, query core.CatalogTitleQuery) ([]string, error) {
	c.asked = append(c.asked, query)
	return c.itemIDs, c.err
}

var ratedSeries = core.RatedTitle{Kind: core.MediaKindSeries, TMDBID: "1399", Title: "Game of Thrones"}

func criticService(t *testing.T, adapter *criticAdapter) *Service {
	t.Helper()
	store, service := newTestService(t, fixedAdapterFactory{adapter: adapter})
	seedEncryptedRecord(t, store, service)
	return service
}

func TestOwnedTitleRatingsReadsTheServerCriticScore(t *testing.T) {
	adapter := &criticAdapter{fakeAdapter: &fakeAdapter{}, score: 89, found: true, lookup: true}
	ratings, err := criticService(t, adapter).OwnedTitleRatings(t.Context(), ratedSeries)
	want := core.TitleRating{Score: 89, Source: core.RatingSourceJellyfin}
	if err != nil || ratings.RottenTomatoes == nil || *ratings.RottenTomatoes != want || ratings.IMDb != nil {
		t.Fatalf("OwnedTitleRatings = %+v, %v", ratings, err)
	}
	if len(adapter.asked) != 1 || adapter.asked[0] != "1399 Game of Thrones" {
		t.Fatalf("asked = %v", adapter.asked)
	}
}

func TestOwnedTitleRatingsSkipsServersWithoutLookupOrTitle(t *testing.T) {
	adapter := &criticAdapter{fakeAdapter: &fakeAdapter{}, score: 89, found: true}
	if ratings, err := criticService(t, adapter).OwnedTitleRatings(t.Context(), ratedSeries); err != nil || !ratings.Empty() {
		t.Fatalf("without lookup = %+v, %v", ratings, err)
	}
	adapter.lookup = true
	untitled := ratedSeries
	untitled.Title = ""
	if ratings, err := criticService(t, adapter).OwnedTitleRatings(t.Context(), untitled); err != nil || !ratings.Empty() {
		t.Fatalf("without title = %+v, %v", ratings, err)
	}
	if len(adapter.asked) != 0 {
		t.Fatalf("asked = %v", adapter.asked)
	}
}

func TestOwnedTitleRatingsReturnsTheServerFailure(t *testing.T) {
	failure := errors.New("jellyfin down")
	adapter := &criticAdapter{fakeAdapter: &fakeAdapter{}, err: failure, lookup: true}
	if _, err := criticService(t, adapter).OwnedTitleRatings(t.Context(), ratedSeries); !errors.Is(err, failure) {
		t.Fatalf("error = %v", err)
	}
}

func catalogCriticService(t *testing.T, adapter *criticAdapter, titles *catalogTitles) *Service {
	t.Helper()
	service := criticService(t, adapter)
	service.SetCatalogTitles(titles)
	return service
}

func TestOwnedTitleRatingsReadsTheCatalogItems(t *testing.T) {
	adapter := &criticAdapter{fakeAdapter: &fakeAdapter{}, score: 89, found: true, lookup: true}
	titles := &catalogTitles{itemIDs: []string{"item-a", "item-b"}}
	renamed := ratedSeries
	renamed.Title = "A Song of Ice and Fire"
	ratings, err := catalogCriticService(t, adapter, titles).OwnedTitleRatings(t.Context(), renamed)
	want := core.TitleRating{Score: 89, Source: core.RatingSourceJellyfin}
	if err != nil || ratings.RottenTomatoes == nil || *ratings.RottenTomatoes != want {
		t.Fatalf("OwnedTitleRatings = %+v, %v", ratings, err)
	}
	if len(adapter.asked) != 1 || adapter.asked[0] != "1399 items item-a,item-b" {
		t.Fatalf("asked = %v", adapter.asked)
	}
	if len(titles.asked) != 1 {
		t.Fatalf("catalog asked = %+v", titles.asked)
	}
	query := titles.asked[0]
	if !query.Valid() || query.Kind != core.MediaKindSeries || query.TMDBID != 1399 || query.Limit != core.MaxCatalogTitleItems {
		t.Fatalf("catalog query = %+v", query)
	}
}

func TestOwnedTitleRatingsCatalogItemWithoutScoreIsNotSearched(t *testing.T) {
	adapter := &criticAdapter{fakeAdapter: &fakeAdapter{}, lookup: true}
	titles := &catalogTitles{itemIDs: []string{"item-a"}}
	untitled := ratedSeries
	untitled.Title = ""
	ratings, err := catalogCriticService(t, adapter, titles).OwnedTitleRatings(t.Context(), untitled)
	if err != nil || !ratings.Empty() {
		t.Fatalf("OwnedTitleRatings = %+v, %v", ratings, err)
	}
	if len(adapter.asked) != 1 || adapter.asked[0] != "1399 items item-a" {
		t.Fatalf("asked = %v", adapter.asked)
	}
}

func TestOwnedTitleRatingsCatalogMissSearchesByName(t *testing.T) {
	adapter := &criticAdapter{fakeAdapter: &fakeAdapter{}, score: 89, found: true, lookup: true}
	titles := &catalogTitles{}
	ratings, err := catalogCriticService(t, adapter, titles).OwnedTitleRatings(t.Context(), ratedSeries)
	if err != nil || ratings.RottenTomatoes == nil || ratings.RottenTomatoes.Score != 89 {
		t.Fatalf("OwnedTitleRatings = %+v, %v", ratings, err)
	}
	if len(titles.asked) != 1 || len(adapter.asked) != 1 || adapter.asked[0] != "1399 Game of Thrones" {
		t.Fatalf("catalog asked = %v, adapter asked = %v", titles.asked, adapter.asked)
	}
}

func TestOwnedTitleRatingsReturnsTheCatalogFailure(t *testing.T) {
	failure := errors.New("database down")
	adapter := &criticAdapter{fakeAdapter: &fakeAdapter{}, score: 89, found: true, lookup: true}
	titles := &catalogTitles{err: failure}
	if _, err := catalogCriticService(t, adapter, titles).OwnedTitleRatings(t.Context(), ratedSeries); !errors.Is(err, failure) {
		t.Fatalf("error = %v", err)
	}
	if len(adapter.asked) != 0 {
		t.Fatalf("asked = %v", adapter.asked)
	}
}

func TestOwnedTitleRatingsSkipsTheCatalogForAnUnstorableTMDBID(t *testing.T) {
	adapter := &criticAdapter{fakeAdapter: &fakeAdapter{}, score: 89, found: true, lookup: true}
	titles := &catalogTitles{itemIDs: []string{"item-a"}}
	huge := ratedSeries
	huge.TMDBID = "1000000001"
	if _, err := catalogCriticService(t, adapter, titles).OwnedTitleRatings(t.Context(), huge); err != nil {
		t.Fatalf("error = %v", err)
	}
	if len(titles.asked) != 0 || len(adapter.asked) != 1 || adapter.asked[0] != "1000000001 Game of Thrones" {
		t.Fatalf("catalog asked = %v, adapter asked = %v", titles.asked, adapter.asked)
	}
}
