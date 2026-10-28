package mediaserver

import (
	"context"
	"errors"
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
