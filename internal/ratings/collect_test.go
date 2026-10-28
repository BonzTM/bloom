package ratings

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/BonzTM/bloom/internal/core"
)

type ownedSource struct {
	mu      sync.Mutex
	ratings core.TitleRatings
	err     error
	calls   int
}

func (s *ownedSource) OwnedTitleRatings(context.Context, core.RatedTitle) (core.TitleRatings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	return cloneRatings(s.ratings), s.err
}

func (s *ownedSource) asked() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

func rating(score float64, source core.RatingSource) *core.TitleRating {
	return &core.TitleRating{Score: score, Source: source}
}

func storeKey(t *testing.T, h harness) {
	t.Helper()
	if err := h.service.SetKey(t.Context(), testKey); err != nil {
		t.Fatalf("SetKey: %v", err)
	}
}

func TestOwnedScoresComeFirstAndOMDbFillsTheRest(t *testing.T) {
	radarr := &ownedSource{ratings: core.TitleRatings{IMDb: rating(8.8, core.RatingSourceRadarr)}}
	jellyfin := &ownedSource{ratings: core.TitleRatings{
		IMDb: rating(1, core.RatingSourceJellyfin), RottenTomatoes: rating(90, core.RatingSourceJellyfin),
	}}
	h := newHarness(t, radarr, jellyfin)
	h.factory.client.ratings = core.TitleRatings{
		IMDb: rating(8.7, core.RatingSourceOMDb), RottenTomatoes: rating(83, core.RatingSourceOMDb),
		Metacritic: rating(73, core.RatingSourceOMDb),
	}
	storeKey(t, h)
	ratings, err := h.service.Ratings(t.Context(), testTitle)
	if err != nil {
		t.Fatalf("Ratings: %v", err)
	}
	if *ratings.IMDb != *rating(8.8, core.RatingSourceRadarr) ||
		*ratings.RottenTomatoes != *rating(90, core.RatingSourceJellyfin) ||
		*ratings.Metacritic != *rating(73, core.RatingSourceOMDb) {
		t.Fatalf("ratings = %+v %+v %+v", ratings.IMDb, ratings.RottenTomatoes, ratings.Metacritic)
	}
}

func TestOMDbIsNotAskedWhenOwnedSourcesKnowEveryScore(t *testing.T) {
	radarr := &ownedSource{ratings: core.TitleRatings{
		IMDb: rating(8.8, core.RatingSourceRadarr), RottenTomatoes: rating(88, core.RatingSourceRadarr),
		Metacritic: rating(70, core.RatingSourceRadarr),
	}}
	h := newHarness(t, radarr)
	storeKey(t, h)
	if _, err := h.service.Ratings(t.Context(), testTitle); err != nil {
		t.Fatalf("Ratings: %v", err)
	}
	if calls := h.factory.client.ratingCalls(); calls != 0 {
		t.Fatalf("OMDb asked %d times", calls)
	}
}

func TestOwnedScoresWorkWithoutAnOMDbKeyOrIMDbID(t *testing.T) {
	jellyfin := &ownedSource{ratings: core.TitleRatings{RottenTomatoes: rating(90, core.RatingSourceJellyfin)}}
	h := newHarness(t, jellyfin)
	ratings, err := h.service.Ratings(t.Context(), testTitle)
	if err != nil || ratings.RottenTomatoes == nil || ratings.IMDb != nil {
		t.Fatalf("Ratings without a key = %+v, %v", ratings, err)
	}
	storeKey(t, h)
	untracked := testTitle
	untracked.TMDBID, untracked.IMDbID = "604", ""
	if _, err := h.service.Ratings(t.Context(), untracked); err != nil || h.factory.client.ratingCalls() != 0 {
		t.Fatalf("Ratings without an IMDb ID: OMDb calls %d, %v", h.factory.client.ratingCalls(), err)
	}
}

func TestAFailingOwnedSourceIsSkippedAndNotCached(t *testing.T) {
	radarr := &ownedSource{err: errors.New("radarr down")}
	h := newHarness(t, radarr)
	storeKey(t, h)
	for range 2 {
		ratings, err := h.service.Ratings(t.Context(), testTitle)
		if err != nil || ratings.IMDb == nil || ratings.IMDb.Source != core.RatingSourceOMDb {
			t.Fatalf("Ratings = %+v, %v", ratings, err)
		}
	}
	if radarr.asked() != 2 {
		t.Fatalf("a result with a failed source was cached: radarr asked %d times", radarr.asked())
	}
}

func TestAFailingOMDbKeepsTheOwnedScores(t *testing.T) {
	jellyfin := &ownedSource{ratings: core.TitleRatings{RottenTomatoes: rating(90, core.RatingSourceJellyfin)}}
	h := newHarness(t, jellyfin)
	storeKey(t, h)
	h.factory.client.ratingsErr = core.ErrMetadataUnavailable
	ratings, err := h.service.Ratings(t.Context(), testTitle)
	if err != nil || ratings.RottenTomatoes == nil {
		t.Fatalf("Ratings = %+v, %v", ratings, err)
	}
	if _, err := h.service.Ratings(t.Context(), testTitle); err != nil || jellyfin.asked() != 2 {
		t.Fatalf("a result with a failed OMDb was cached: asked %d, %v", jellyfin.asked(), err)
	}
}

func TestNewServiceRejectsANilOwnedSource(t *testing.T) {
	h := newHarness(t)
	_, err := NewService(h.store, h.store, h.service.cipher, h.factory, []core.OwnedRatingsSource{nil}, h.clock, h.service.logger)
	if err == nil {
		t.Fatal("NewService accepted a nil owned source")
	}
}
