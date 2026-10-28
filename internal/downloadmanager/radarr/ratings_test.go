package radarr

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/BonzTM/bloom/internal/core"
)

func ratingsServer(t *testing.T, movies any) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v3/movie" || r.URL.Query().Get("tmdbId") != "603" {
			t.Errorf("request = %s", r.URL.String())
		}
		writeTestJSON(t, w, movies)
	}))
	t.Cleanup(server.Close)
	return server
}

func TestMovieRatingsReadsTheLibraryEntry(t *testing.T) {
	server := ratingsServer(t, []map[string]any{{
		"id": 9, "tmdbId": 603, "ratings": map[string]any{
			"imdb":           map[string]any{"votes": 2123456, "value": 8.7, "type": "user"},
			"rottenTomatoes": map[string]any{"votes": 0, "value": 83, "type": "user"},
			"metacritic":     map[string]any{"votes": 0, "value": 73, "type": "user"},
			"tmdb":           map[string]any{"votes": 26000, "value": 8.2, "type": "user"},
		},
	}})
	ratings, found, err := newTestClient(t, server).MovieRatings(t.Context(), "603")
	if err != nil || !found {
		t.Fatalf("MovieRatings = %t, %v", found, err)
	}
	want := core.TitleRatings{
		IMDb:           &core.TitleRating{Score: 8.7, Votes: 2123456, Source: core.RatingSourceRadarr},
		RottenTomatoes: &core.TitleRating{Score: 83, Source: core.RatingSourceRadarr},
		Metacritic:     &core.TitleRating{Score: 73, Source: core.RatingSourceRadarr},
	}
	if *ratings.IMDb != *want.IMDb || *ratings.RottenTomatoes != *want.RottenTomatoes || *ratings.Metacritic != *want.Metacritic {
		t.Fatalf("ratings = %+v %+v %+v", ratings.IMDb, ratings.RottenTomatoes, ratings.Metacritic)
	}
}

func TestMovieRatingsTreatsZeroAndOutOfRangeAsMissing(t *testing.T) {
	server := ratingsServer(t, []map[string]any{{
		"id": 9, "tmdbId": 603, "ratings": map[string]any{
			"imdb":           map[string]any{"votes": 0, "value": 0},
			"rottenTomatoes": map[string]any{"value": 140},
		},
	}})
	ratings, found, err := newTestClient(t, server).MovieRatings(t.Context(), "603")
	if err != nil || !found || !ratings.Empty() {
		t.Fatalf("MovieRatings = %+v, %t, %v", ratings, found, err)
	}
}

func TestMovieRatingsNotInTheLibrary(t *testing.T) {
	server := ratingsServer(t, []map[string]any{{"id": 4, "tmdbId": 11}})
	ratings, found, err := newTestClient(t, server).MovieRatings(t.Context(), "603")
	if err != nil || found || !ratings.Empty() {
		t.Fatalf("MovieRatings = %+v, %t, %v", ratings, found, err)
	}
}

func TestMovieRatingsRejectsMalformedAnswersAndIDs(t *testing.T) {
	server := ratingsServer(t, []map[string]any{{"id": 9, "tmdbId": 603, "ratings": "high"}})
	client := newTestClient(t, server)
	_, _, err := client.MovieRatings(t.Context(), "603")
	var managerErr *core.DownloadManagerError
	if !errors.As(err, &managerErr) || managerErr.Kind != core.DownloadManagerMalformed {
		t.Fatalf("error = %v, want malformed", err)
	}
	for _, id := range []string{"", "0", "-3", "abc", "99999999999"} {
		if _, _, err := client.MovieRatings(t.Context(), id); !errors.Is(err, core.ErrInvalidArgument) {
			t.Errorf("MovieRatings(%q) error = %v", id, err)
		}
	}
}
