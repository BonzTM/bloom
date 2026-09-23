package tmdb

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/testutil"
)

func TestClientSearchMovieAndSeries(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("api_key") != "secret" {
			t.Errorf("api_key = %q", r.URL.Query().Get("api_key"))
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/3/search/multi":
			writeTestResponse(t, w, `{"results":[{"id":11,"media_type":"movie","title":"Film","release_date":"2024-01-02"},{"id":12,"media_type":"tv","name":"Show","first_air_date":"2023-04-05"},{"id":13,"media_type":"person","name":"Ignored"}]}`)
		case "/3/movie/11":
			writeTestResponse(t, w, `{"id":11,"title":"Film","release_date":"2024-01-02","overview":"Plot","poster_path":"/film.jpg"}`)
		case "/3/tv/12":
			writeTestResponse(t, w, `{"id":12,"name":"Show","first_air_date":"2023-04-05","seasons":[{"season_number":0,"name":"Specials","episode_count":2},{"season_number":1,"name":"Season 1","episode_count":8,"air_date":"2023-04-05"}]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client := newTestClient(t, server.URL, nil)
	results, err := client.Search(t.Context(), core.MetadataSearch{Query: "title"})
	if err != nil || len(results) != 2 || results[0].Title != "Film" || results[1].Title != "Show" {
		t.Fatalf("Search = %+v, %v", results, err)
	}
	movie, err := client.Movie(t.Context(), "11")
	if err != nil || movie.Year != 2024 || movie.Overview != "Plot" {
		t.Fatalf("Movie = %+v, %v", movie, err)
	}
	series, err := client.Series(t.Context(), "12", false)
	if err != nil || len(series.Seasons) != 1 || series.Seasons[0].Number != 1 {
		t.Fatalf("Series = %+v, %v", series, err)
	}
}

func writeTestResponse(t *testing.T, w http.ResponseWriter, body string) {
	t.Helper()
	if _, err := w.Write([]byte(body)); err != nil {
		t.Errorf("write fake TMDB response: %v", err)
	}
}

func TestClientClassifiesProviderFailures(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
		want   error
	}{
		{name: "bad key", status: http.StatusUnauthorized, body: `{}`, want: core.ErrMetadataUnauthorized},
		{name: "missing", status: http.StatusNotFound, body: `{}`, want: core.ErrNotFound},
		{name: "server", status: http.StatusInternalServerError, body: `{}`, want: core.ErrMetadataUnavailable},
		{name: "malformed", status: http.StatusOK, body: `{`, want: core.ErrMetadataMalformed},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(testCase.status)
				writeTestResponse(t, w, testCase.body)
			}))
			defer server.Close()
			_, err := newTestClient(t, server.URL, nil).Movie(t.Context(), "11")
			if !errors.Is(err, testCase.want) {
				t.Fatalf("Movie error = %v, want %v", err, testCase.want)
			}
		})
	}
}

func TestClientRetries429UsingRetryAfter(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if calls.Add(1) == 1 {
			w.Header().Set("Retry-After", "2")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		writeTestResponse(t, w, `{"id":11,"title":"Film"}`)
	}))
	defer server.Close()
	var waited time.Duration
	client := newTestClient(t, server.URL, func(_ context.Context, delay time.Duration) error {
		waited += delay
		return nil
	})
	if _, err := client.Movie(t.Context(), "11"); err != nil {
		t.Fatalf("Movie: %v", err)
	}
	if calls.Load() != 2 || waited != 2*time.Second {
		t.Fatalf("retry calls = %d, waited = %s", calls.Load(), waited)
	}
}

func newTestClient(t *testing.T, baseURL string, wait func(context.Context, time.Duration) error) *Client {
	t.Helper()
	clock := testutil.NewFakeClock(time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC))
	client, err := New("secret", Dependencies{BaseURL: baseURL, Clock: clock, Wait: wait, RandomInt64N: func(int64) int64 { return 0 }})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(client.CloseIdleConnections)
	return client
}

func TestClientCapsResponseBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		writeTestResponse(t, w, `{"id":11,"title":"`+strings.Repeat("a", int(maxResponseBytes))+`"}`)
	}))
	defer server.Close()
	_, err := newTestClient(t, server.URL, nil).Movie(t.Context(), "11")
	if !errors.Is(err, core.ErrMetadataMalformed) {
		t.Fatalf("oversize error = %v", err)
	}
}

func TestClientSpansNeverContainAPIKey(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	previous := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)
	t.Cleanup(func() {
		otel.SetTracerProvider(previous)
		if err := provider.Shutdown(context.Background()); err != nil {
			t.Errorf("shutdown tracer provider: %v", err)
		}
	})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("api_key") != "secret" {
			t.Errorf("upstream API key = %q", r.URL.Query().Get("api_key"))
		}
		w.Header().Set("Content-Type", "application/json")
		writeTestResponse(t, w, `{"id":11,"title":"Film"}`)
	}))
	defer server.Close()
	if _, err := newTestClient(t, server.URL, nil).Movie(t.Context(), "11"); err != nil {
		t.Fatalf("Movie: %v", err)
	}
	if rendered := fmt.Sprintf("%+v", exporter.GetSpans()); strings.Contains(rendered, "secret") {
		t.Fatalf("exported span data contains TMDB key: %s", rendered)
	}
}
