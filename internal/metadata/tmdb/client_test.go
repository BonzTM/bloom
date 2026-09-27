package tmdb

import (
	"context"
	"errors"
	"fmt"
	"io"
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

const testReadAccessToken = "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJhdWQiOiJ0bWRiIiwic3ViIjoiYmxvb20tdGVzdCIsImlhdCI6MTcwMDAwMDAwMH0.c2lnbmF0dXJlLXNpZ25hdHVyZS1zaWduYXR1cmUtc2lnbmF0dXJl"

func TestClientSearchMovieAndSeries(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+testReadAccessToken {
			t.Errorf("Authorization = %q", r.Header.Get("Authorization"))
		}
		if strings.Contains(r.URL.RawQuery, testReadAccessToken) || r.URL.Query().Has("api_key") {
			t.Errorf("credential leaked in query %q", r.URL.RawQuery)
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

func TestClientDiscoverLists(t *testing.T) {
	client := newDiscoveryTestClient(t)
	lists := []core.MetadataDiscoverList{
		core.MetadataTrending, core.MetadataMoviesPopular, core.MetadataSeriesPopular,
		core.MetadataMoviesUpcoming, core.MetadataSeriesUpcoming,
	}
	for _, list := range lists {
		page, err := client.Discover(t.Context(), core.MetadataDiscover{List: list, Page: 2})
		if err != nil || page.Page != 2 || page.TotalPages != core.MaxMetadataPage || len(page.Items) == 0 {
			t.Fatalf("Discover(%s) = %+v, %v", list, page, err)
		}
		if page.Items[0].BackdropPath != "/backdrop.jpg" {
			t.Fatalf("Discover(%s) backdrop = %q", list, page.Items[0].BackdropPath)
		}
	}
}

func TestClientGenres(t *testing.T) {
	client := newDiscoveryTestClient(t)
	for _, testCase := range []struct {
		kind core.MediaKind
		name string
	}{{kind: core.MediaKindMovie, name: "Action"}, {kind: core.MediaKindSeries, name: "Drama"}} {
		genres, err := client.Genres(t.Context(), testCase.kind)
		if err != nil || len(genres) != 1 || genres[0].Name != testCase.name {
			t.Fatalf("Genres(%s) = %+v, %v", testCase.kind, genres, err)
		}
	}
}

func newDiscoveryTestClient(t *testing.T) *Client {
	t.Helper()
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("Authorization") != "Bearer "+testReadAccessToken {
			t.Errorf("Authorization = %q", r.Header.Get("Authorization"))
		}
		if r.URL.Query().Has("api_key") || strings.Contains(r.URL.RawQuery, testReadAccessToken) {
			t.Errorf("credential leaked in query %q", r.URL.RawQuery)
		}
		var body string
		switch r.URL.Path {
		case "/3/trending/all/week":
			if r.URL.Query().Get("page") != "2" {
				t.Errorf("trending page = %q, want 2", r.URL.Query().Get("page"))
			}
			body = discoverMixedFixture
		case "/3/movie/popular", "/3/movie/upcoming":
			assertDiscoverPage(t, r)
			body = discoverMovieFixture
		case "/3/tv/popular", "/3/tv/on_the_air":
			assertDiscoverPage(t, r)
			body = discoverSeriesFixture
		case "/3/genre/movie/list":
			body = `{"genres":[{"id":28,"name":"Action"}]}`
		case "/3/genre/tv/list":
			body = `{"genres":[{"id":18,"name":"Drama"}]}`
		default:
			return &http.Response{StatusCode: http.StatusNotFound, Header: make(http.Header), Body: http.NoBody, Request: r}, nil
		}
		return &http.Response{
			StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}},
			Body: io.NopCloser(strings.NewReader(body)), Request: r,
		}, nil
	})
	clock := testutil.NewFakeClock(time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC))
	client, err := New(testReadAccessToken, Dependencies{
		BaseURL: "https://tmdb.test", Clock: clock, HTTPClient: &http.Client{Transport: transport},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return client
}

func assertDiscoverPage(t *testing.T, request *http.Request) {
	t.Helper()
	if request.URL.Query().Get("page") != "2" {
		t.Errorf("%s page = %q, want 2", request.URL.Path, request.URL.Query().Get("page"))
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

const (
	discoverMixedFixture = `{"page":2,"total_pages":50,"results":[` +
		`{"id":11,"media_type":"movie","title":"Film","release_date":"2024-01-02","backdrop_path":"/backdrop.jpg"},` +
		`{"id":12,"media_type":"tv","name":"Show","first_air_date":"2023-04-05","backdrop_path":"/show.jpg"},` +
		`{"id":13,"media_type":"person","name":"Ignored"}]}`
	discoverMovieFixture  = `{"page":2,"total_pages":50,"results":[{"id":11,"title":"Film","release_date":"2024-01-02","backdrop_path":"/backdrop.jpg"}]}`
	discoverSeriesFixture = `{"page":2,"total_pages":50,"results":[{"id":12,"name":"Show","first_air_date":"2023-04-05","backdrop_path":"/backdrop.jpg"}]}`
)

func TestClientProbeClassifiesUnauthorized(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/3/authentication" {
			t.Errorf("path = %q", r.URL.Path)
		}
		w.WriteHeader(http.StatusUnauthorized)
		writeTestResponse(t, w, `{}`)
	}))
	defer server.Close()
	if err := newTestClient(t, server.URL, nil).Probe(t.Context()); !errors.Is(err, core.ErrMetadataUnauthorized) {
		t.Fatalf("Probe error = %v, want %v", err, core.ErrMetadataUnauthorized)
	}
}

func TestClientProbeAcceptsValidatedToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+testReadAccessToken {
			t.Errorf("Authorization = %q", r.Header.Get("Authorization"))
		}
		if r.URL.Query().Has("api_key") || strings.Contains(r.URL.RawQuery, testReadAccessToken) {
			t.Errorf("credential leaked in query %q", r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "application/json")
		writeTestResponse(t, w, `{"success":true,"status_code":1}`)
	}))
	defer server.Close()
	if err := newTestClient(t, server.URL, nil).Probe(t.Context()); err != nil {
		t.Fatalf("Probe: %v", err)
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

func TestClientRejectsCrossOriginRedirectWithoutLeakingKey(t *testing.T) {
	var destinationCalls atomic.Int32
	destination := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		destinationCalls.Add(1)
		if r.Header.Get("Authorization") != "" {
			t.Errorf("cross-origin Authorization = %q", r.Header.Get("Authorization"))
		}
	}))
	defer destination.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+testReadAccessToken {
			t.Errorf("source Authorization = %q", r.Header.Get("Authorization"))
		}
		http.Redirect(w, r, destination.URL, http.StatusFound)
	}))
	defer source.Close()
	clock := testutil.NewFakeClock(time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC))
	client, err := New(testReadAccessToken, Dependencies{BaseURL: source.URL, Clock: clock})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(client.CloseIdleConnections)
	_, err = client.Movie(t.Context(), "11")
	if err == nil || strings.Contains(err.Error(), testReadAccessToken) {
		t.Fatalf("redirect error = %v", err)
	}
	if destinationCalls.Load() != 0 {
		t.Fatalf("redirect destination calls = %d, want 0", destinationCalls.Load())
	}
}

func TestBearerTransportDropsAuthorizationFromCrossOriginRequest(t *testing.T) {
	wantErr := errors.New("transport stopped")
	capture := &captureTransport{err: wantErr}
	base := httptest.NewRequest(http.MethodGet, "https://api.example.test/3/movie/11", nil).URL
	request := httptest.NewRequest(http.MethodGet, "https://redirect.example.test/target", nil)
	request.Header.Set("Authorization", "Bearer "+testReadAccessToken)
	transport := &bearerTransport{next: capture, token: testReadAccessToken, baseURL: base}
	_, err := transport.RoundTrip(request)
	if !errors.Is(err, wantErr) {
		t.Fatalf("RoundTrip error = %v", err)
	}
	if capture.request == nil {
		t.Fatal("cross-origin request did not reach transport")
	}
	if capture.request.Header.Get("Authorization") != "" {
		t.Fatalf("cross-origin Authorization = %q", capture.request.Header.Get("Authorization"))
	}
	if strings.Contains(capture.request.URL.RawQuery, testReadAccessToken) {
		t.Fatalf("cross-origin request query leaked credential: %q", capture.request.URL.RawQuery)
	}
}

type captureTransport struct {
	request *http.Request
	err     error
}

func (t *captureTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	t.request = request
	return nil, t.err
}

func TestClientClassifiesInvalidProviderTitleAsMalformed(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasPrefix(request.URL.Path, "/3/tv/") {
			writeTestResponse(t, w, `{"id":12,"name":" ","seasons":[{"season_number":1,"name":"Season 1","episode_count":8}]}`)
			return
		}
		writeTestResponse(t, w, `{"id":11,"title":" "}`)
	}))
	defer server.Close()
	client := newTestClient(t, server.URL, nil)
	_, movieErr := client.Movie(t.Context(), "11")
	if !errors.Is(movieErr, core.ErrMetadataMalformed) || !errors.Is(movieErr, core.ErrInvalidArgument) {
		t.Fatalf("invalid movie title error = %v", movieErr)
	}
	_, seriesErr := client.Series(t.Context(), "12", false)
	if !errors.Is(seriesErr, core.ErrMetadataMalformed) || !errors.Is(seriesErr, core.ErrInvalidArgument) {
		t.Fatalf("invalid series title error = %v", seriesErr)
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
	client, err := New(testReadAccessToken, Dependencies{BaseURL: baseURL, Clock: clock, Wait: wait, RandomInt64N: func(int64) int64 { return 0 }})
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
		if r.Header.Get("Authorization") != "Bearer "+testReadAccessToken {
			t.Errorf("upstream Authorization = %q", r.Header.Get("Authorization"))
		}
		if strings.Contains(r.URL.RawQuery, testReadAccessToken) || r.URL.Query().Has("api_key") {
			t.Errorf("credential leaked in query %q", r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "application/json")
		writeTestResponse(t, w, `{"id":11,"title":"Film"}`)
	}))
	defer server.Close()
	if _, err := newTestClient(t, server.URL, nil).Movie(t.Context(), "11"); err != nil {
		t.Fatalf("Movie: %v", err)
	}
	if rendered := fmt.Sprintf("%+v", exporter.GetSpans()); strings.Contains(rendered, testReadAccessToken) {
		t.Fatalf("exported span data contains TMDB token: %s", rendered)
	}
}
