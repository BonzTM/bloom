package omdb

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/testutil"
)

const testKey = "a1b2c3d4"

// matrixAnswer is shaped like OMDb's documented title answer.
const matrixAnswer = `{"Title":"The Matrix","Year":"1999","Response":"True","imdbID":"tt0133093",
"imdbRating":"8.7","imdbVotes":"2,123,456","Metascore":"73",
"Ratings":[{"Source":"Internet Movie Database","Value":"8.7/10"},
{"Source":"Rotten Tomatoes","Value":"83%"},{"Source":"Metacritic","Value":"73/100"}]}`

func newTestClient(t *testing.T, handler http.HandlerFunc) (*Client, *recordingMetrics) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	metrics := &recordingMetrics{}
	client, err := New(testKey, Dependencies{
		HTTPClient: server.Client(), BaseURL: server.URL + "/", Metrics: metrics,
		Clock: testutil.NewFakeClock(time.Date(2026, 10, 28, 0, 0, 0, 0, time.UTC)),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return client, metrics
}

func TestTitleRatingsMapsAllThreeScores(t *testing.T) {
	client, metrics := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		if query.Get("apikey") != testKey || query.Get("i") != "tt0133093" || query.Get("r") != "json" {
			t.Errorf("query = %q", r.URL.RawQuery)
		}
		writeBody(t, w, matrixAnswer)
	})
	ratings, err := client.TitleRatings(t.Context(), "tt0133093")
	if err != nil {
		t.Fatalf("TitleRatings: %v", err)
	}
	want := core.TitleRatings{
		IMDb:           &core.TitleRating{Score: 8.7, Votes: 2_123_456, Source: core.RatingSourceOMDb},
		RottenTomatoes: &core.TitleRating{Score: 83, Source: core.RatingSourceOMDb},
		Metacritic:     &core.TitleRating{Score: 73, Source: core.RatingSourceOMDb},
	}
	assertRatings(t, ratings, want)
	if got := metrics.calls(); len(got) != 1 || got[0] != "omdb ratings success" {
		t.Fatalf("metrics = %v", got)
	}
}

func TestTitleRatingsDropsMissingAndOutOfRangeScores(t *testing.T) {
	client, _ := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		writeBody(t, w, `{"Response":"True","imdbRating":"N/A","imdbVotes":"N/A","Metascore":"140",
"Ratings":[{"Source":"Rotten Tomatoes","Value":"91%"}]}`)
	})
	ratings, err := client.TitleRatings(t.Context(), "tt0133093")
	if err != nil {
		t.Fatalf("TitleRatings: %v", err)
	}
	assertRatings(t, ratings, core.TitleRatings{
		RottenTomatoes: &core.TitleRating{Score: 91, Source: core.RatingSourceOMDb},
	})
}

func TestTitleRatingsUnknownTitleIsEmpty(t *testing.T) {
	client, _ := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		writeBody(t, w, `{"Response":"False","Error":"Incorrect IMDb ID."}`)
	})
	ratings, err := client.TitleRatings(t.Context(), "tt9999999")
	if err != nil {
		t.Fatalf("TitleRatings: %v", err)
	}
	assertRatings(t, ratings, core.TitleRatings{})
}

func TestTitleRatingsClassifiesFailures(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		want   error
	}{
		{"bad key", http.StatusUnauthorized, `{"Response":"False","Error":"Invalid API key!"}`, core.ErrMetadataUnauthorized},
		{"spent quota", http.StatusUnauthorized, `{"Response":"False","Error":"Request limit reached!"}`, core.ErrMetadataUnavailable},
		{"server error", http.StatusBadGateway, `{}`, core.ErrMetadataUnavailable},
		{"not json", http.StatusOK, `<html>`, core.ErrMetadataMalformed},
		{"redirect", http.StatusFound, `{}`, core.ErrMetadataMalformed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client, _ := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Location", "https://elsewhere.example/")
				w.WriteHeader(tc.status)
				writeBody(t, w, tc.body)
			})
			_, err := client.TitleRatings(t.Context(), "tt0133093")
			if !errors.Is(err, tc.want) {
				t.Fatalf("error = %v, want %v", err, tc.want)
			}
			if strings.Contains(err.Error(), testKey) {
				t.Fatalf("error exposes the key: %v", err)
			}
		})
	}
}

func TestTransportErrorNeverCarriesTheKey(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	server.Close()
	client, err := New(testKey, Dependencies{
		BaseURL: server.URL + "/", Clock: testutil.NewFakeClock(time.Unix(0, 0).UTC()),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = client.TitleRatings(t.Context(), "tt0133093")
	if !errors.Is(err, core.ErrMetadataUnreachable) || strings.Contains(err.Error(), testKey) {
		t.Fatalf("error = %v, want unreachable without the key", err)
	}
}

func TestProbeRejectsAMissingProbeTitle(t *testing.T) {
	client, _ := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		writeBody(t, w, `{"Response":"False","Error":"Incorrect IMDb ID."}`)
	})
	if err := client.Probe(t.Context()); !errors.Is(err, core.ErrMetadataMalformed) {
		t.Fatalf("Probe error = %v, want %v", err, core.ErrMetadataMalformed)
	}
}

func TestNewAndTitleRatingsRejectInvalidInput(t *testing.T) {
	clock := testutil.NewFakeClock(time.Unix(0, 0).UTC())
	for _, key := range []string{"", "short", "has space1", strings.Repeat("a", 65), "key&i=tt1"} {
		if _, err := New(key, Dependencies{Clock: clock}); !errors.Is(err, core.ErrInvalidArgument) {
			t.Errorf("New(%q) error = %v", key, err)
		}
	}
	client, err := New(testKey, Dependencies{Clock: clock})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	for _, id := range []string{"", "0133093", "tt123", "tt0133093&apikey=x", "nm0000206"} {
		if _, err := client.TitleRatings(context.Background(), id); !errors.Is(err, core.ErrInvalidArgument) {
			t.Errorf("TitleRatings(%q) error = %v", id, err)
		}
	}
}

func writeBody(t *testing.T, w http.ResponseWriter, body string) {
	t.Helper()
	if _, err := w.Write([]byte(body)); err != nil {
		t.Errorf("write test body: %v", err)
	}
}

func assertRatings(t *testing.T, got, want core.TitleRatings) {
	t.Helper()
	pairs := []struct {
		name      string
		got, want *core.TitleRating
	}{{"IMDb", got.IMDb, want.IMDb}, {"RottenTomatoes", got.RottenTomatoes, want.RottenTomatoes}, {"Metacritic", got.Metacritic, want.Metacritic}}
	for _, pair := range pairs {
		if (pair.got == nil) != (pair.want == nil) || (pair.got != nil && *pair.got != *pair.want) {
			t.Errorf("%s = %+v, want %+v", pair.name, pair.got, pair.want)
		}
	}
}

type recordingMetrics struct {
	mu       sync.Mutex
	observed []string
}

func (m *recordingMetrics) ObserveMetadataRequest(provider, operation, outcome string, _ float64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.observed = append(m.observed, provider+" "+operation+" "+outcome)
}

func (m *recordingMetrics) calls() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.observed...)
}
