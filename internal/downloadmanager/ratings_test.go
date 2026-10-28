package downloadmanager

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/testutil"
)

type ratingAdapter struct {
	serviceAdapter
	ratings core.TitleRatings
	found   bool
	err     error
	calls   int
}

func (a *ratingAdapter) MovieRatings(context.Context, string) (core.TitleRatings, bool, error) {
	a.calls++
	return a.ratings, a.found, a.err
}

func ratingsService(t *testing.T, kind core.DownloadManagerKind, adapter core.DownloadManagerAdapter) *Service {
	t.Helper()
	store := &serviceStore{record: core.DownloadManagerRecord{DownloadManager: core.DownloadManager{
		ID: "11111111-1111-4111-8111-111111111111", Kind: kind, Name: "Main", BaseURL: "https://arr.example",
	}}}
	service, err := NewService(
		store, store, &serviceCipher{}, &serviceFactory{adapter: adapter},
		testutil.NewFakeClock(time.Date(2026, 10, 28, 0, 0, 0, 0, time.UTC)),
	)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return service
}

var ratedMovie = core.RatedTitle{Kind: core.MediaKindMovie, TMDBID: "603", IMDbID: "tt0133093", Title: "The Matrix"}

func TestOwnedTitleRatingsAsksRadarrForAMovie(t *testing.T) {
	rating := &core.TitleRating{Score: 8.7, Votes: 10, Source: core.RatingSourceRadarr}
	adapter := &ratingAdapter{ratings: core.TitleRatings{IMDb: rating}, found: true}
	ratings, err := ratingsService(t, core.DownloadManagerKindRadarr, adapter).OwnedTitleRatings(t.Context(), ratedMovie)
	if err != nil || ratings.IMDb == nil || *ratings.IMDb != *rating {
		t.Fatalf("OwnedTitleRatings = %+v, %v", ratings, err)
	}
}

func TestOwnedTitleRatingsSkipsSeriesAndSonarr(t *testing.T) {
	adapter := &ratingAdapter{found: true}
	series := ratedMovie
	series.Kind = core.MediaKindSeries
	if _, err := ratingsService(t, core.DownloadManagerKindRadarr, adapter).OwnedTitleRatings(t.Context(), series); err != nil {
		t.Fatalf("series: %v", err)
	}
	if _, err := ratingsService(t, core.DownloadManagerKindSonarr, adapter).OwnedTitleRatings(t.Context(), ratedMovie); err != nil {
		t.Fatalf("sonarr: %v", err)
	}
	if adapter.calls != 0 {
		t.Fatalf("adapter asked %d times", adapter.calls)
	}
}

func TestOwnedTitleRatingsReturnsAFailureOnlyWhenNoManagerAnswered(t *testing.T) {
	failure := &core.DownloadManagerError{Kind: core.DownloadManagerUnavailable, Operation: "ratings"}
	adapter := &ratingAdapter{err: failure}
	ratings, err := ratingsService(t, core.DownloadManagerKindRadarr, adapter).OwnedTitleRatings(t.Context(), ratedMovie)
	if !errors.Is(err, failure) || !ratings.Empty() {
		t.Fatalf("OwnedTitleRatings = %+v, %v", ratings, err)
	}
	invalid := ratedMovie
	invalid.TMDBID = "x"
	if _, err := ratingsService(t, core.DownloadManagerKindRadarr, adapter).OwnedTitleRatings(t.Context(), invalid); !errors.Is(err, core.ErrInvalidArgument) {
		t.Fatalf("invalid title error = %v", err)
	}
}
