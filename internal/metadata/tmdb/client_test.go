package tmdb

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
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
	client := newTestClient(t, server.URL)
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

func TestClientRejectsInvalidDiscoverResponses(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "more than 20 results", body: `{"page":2,"total_pages":3,"results":[` +
			strings.TrimSuffix(strings.Repeat(`{"id":11,"title":"Film"},`, core.MetadataPageSize+1), ",") + `]}`},
		{name: "mismatched page", body: `{"page":1,"total_pages":3,"results":[]}`},
		{name: "missing total", body: `{"page":2,"results":[]}`},
		{name: "negative total", body: `{"page":2,"total_pages":-1,"results":[]}`},
	}
	input := core.MetadataDiscover{List: core.MetadataMoviesPopular, Page: 2}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := decodeDiscoverPage([]byte(testCase.body), input)
			if !errors.Is(err, core.ErrMetadataMalformed) {
				t.Fatalf("decodeDiscoverPage error = %v, want %v", err, core.ErrMetadataMalformed)
			}
		})
	}
}

func TestClientRejectsInvalidGenreResponses(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "oversized", body: `{"genres":[` +
			strings.TrimSuffix(strings.Repeat(`{"id":1,"name":"Action"},`, 101), ",") + `]}`},
		{name: "duplicate", body: `{"genres":[{"id":28,"name":"Action"},{"id":28,"name":"Adventure"}]}`},
		{name: "malformed JSON", body: `{"genres":[`},
		{name: "missing list", body: `{}`},
		{name: "malformed item", body: `{"genres":[{"id":28}]}`},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := decodeGenres([]byte(testCase.body), "genres_movie")
			if !errors.Is(err, core.ErrMetadataMalformed) {
				t.Fatalf("decodeGenres error = %v, want %v", err, core.ErrMetadataMalformed)
			}
		})
	}
}

func TestClientClassifiesListNotFoundAsUnavailable(t *testing.T) {
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return statusResponse(request, http.StatusNotFound), nil
	})
	client := newBaseTransportTestClient(t, transport, nil)

	_, discoverErr := client.Discover(t.Context(), core.MetadataDiscover{List: core.MetadataTrending, Page: 1})
	if !errors.Is(discoverErr, core.ErrMetadataUnavailable) || errors.Is(discoverErr, core.ErrNotFound) {
		t.Fatalf("Discover error = %v, want only %v", discoverErr, core.ErrMetadataUnavailable)
	}
	_, genresErr := client.Genres(t.Context(), core.MediaKindMovie)
	if !errors.Is(genresErr, core.ErrMetadataUnavailable) || errors.Is(genresErr, core.ErrNotFound) {
		t.Fatalf("Genres error = %v, want only %v", genresErr, core.ErrMetadataUnavailable)
	}
}

func TestClientClassifiesStalledListNotFoundAsUnavailable(t *testing.T) {
	tests := []struct {
		name string
		call func(*Client) error
	}{
		{
			name: "discovery",
			call: func(client *Client) error {
				_, err := client.Discover(t.Context(), core.MetadataDiscover{List: core.MetadataTrending, Page: 1})
				return err
			},
		},
		{
			name: "genres",
			call: func(client *Client) error {
				_, err := client.Genres(t.Context(), core.MediaKindMovie)
				return err
			},
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusNotFound,
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body:       &stalledResponseBody{ctx: request.Context()},
					Request:    request,
				}, nil
			})
			clock := testutil.NewFakeClock(time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC))
			client, err := New(testReadAccessToken, Dependencies{
				BaseURL: "https://tmdb.test", Clock: clock, HTTPClient: &http.Client{Transport: transport},
				AttemptTimeout: 20 * time.Millisecond, OperationTimeout: 40 * time.Millisecond,
			})
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			err = testCase.call(client)
			if !errors.Is(err, core.ErrMetadataUnavailable) || errors.Is(err, core.ErrMetadataMalformed) ||
				errors.Is(err, core.ErrNotFound) || errors.Is(err, core.ErrMetadataUnreachable) {
				t.Fatalf("%s error = %v, want only %v", testCase.name, err, core.ErrMetadataUnavailable)
			}
			assertResponseReceivedStatus(t, err, http.StatusNotFound)
		})
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
	if err := newTestClient(t, server.URL).Probe(t.Context()); !errors.Is(err, core.ErrMetadataUnauthorized) {
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
	if err := newTestClient(t, server.URL).Probe(t.Context()); err != nil {
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
		name      string
		status    int
		body      string
		want      error
		wantCalls int32
	}{
		{name: "bad key", status: http.StatusUnauthorized, body: `{}`, want: core.ErrMetadataUnauthorized, wantCalls: 1},
		{name: "missing", status: http.StatusNotFound, body: `{}`, want: core.ErrNotFound, wantCalls: 1},
		{name: "server", status: http.StatusInternalServerError, body: `{}`, want: core.ErrMetadataUnavailable, wantCalls: 1},
		{name: "unexpected client status", status: http.StatusForbidden, body: `{}`, want: core.ErrMetadataMalformed, wantCalls: 1},
		{name: "malformed", status: http.StatusOK, body: `{`, want: core.ErrMetadataMalformed, wantCalls: 1},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				w.WriteHeader(testCase.status)
				writeTestResponse(t, w, testCase.body)
			}))
			defer server.Close()
			_, err := newTestClient(t, server.URL).Movie(t.Context(), "11")
			if !errors.Is(err, testCase.want) {
				t.Fatalf("Movie error = %v, want %v", err, testCase.want)
			}
			if calls.Load() != testCase.wantCalls {
				t.Fatalf("provider calls = %d, want %d", calls.Load(), testCase.wantCalls)
			}
		})
	}
}

func TestClientDoesNotRetryTerminalServerFailures(t *testing.T) {
	for _, status := range []int{http.StatusNotImplemented, http.StatusHTTPVersionNotSupported} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var calls atomic.Int32
			transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
				calls.Add(1)
				return statusResponse(request, status), nil
			})
			_, err := newBaseTransportTestClient(t, transport, nil).Movie(t.Context(), "11")
			if !errors.Is(err, core.ErrMetadataUnavailable) {
				t.Fatalf("Movie error = %v, want %v", err, core.ErrMetadataUnavailable)
			}
			if calls.Load() != 1 {
				t.Fatalf("provider calls = %d, want 1", calls.Load())
			}
		})
	}
}

func statusResponse(request *http.Request, status int) *http.Response {
	return &http.Response{
		StatusCode: status, Header: http.Header{"Content-Type": []string{"application/json"}},
		Body: io.NopCloser(strings.NewReader(`{}`)), Request: request,
	}
}

func newBaseTransportTestClient(
	t *testing.T, transport http.RoundTripper, wait func(context.Context, time.Duration) error,
) *Client {
	t.Helper()
	clock := testutil.NewFakeClock(time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC))
	client, err := New(testReadAccessToken, Dependencies{
		BaseURL: "https://tmdb.test", BaseTransport: transport, Clock: clock,
		Wait: wait, RandomInt64N: func(int64) int64 { return 0 },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(client.CloseIdleConnections)
	return client
}

func TestClientClassifiesStalledResponseBodyByReceivedStatus(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		status int
		want   error
	}{
		{name: "success status", status: http.StatusOK, want: core.ErrMetadataMalformed},
		{name: "rate limited", status: http.StatusTooManyRequests, want: core.ErrMetadataUnavailable},
		{name: "server failure", status: http.StatusServiceUnavailable, want: core.ErrMetadataUnavailable},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: testCase.status,
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body:       &stalledResponseBody{ctx: request.Context()},
					Request:    request,
				}, nil
			})
			clock := testutil.NewFakeClock(time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC))
			client, err := New(testReadAccessToken, Dependencies{
				BaseURL: "https://tmdb.test", Clock: clock, HTTPClient: &http.Client{Transport: transport},
				AttemptTimeout: 20 * time.Millisecond, OperationTimeout: 40 * time.Millisecond,
			})
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			_, err = client.Movie(t.Context(), "11")
			if !errors.Is(err, testCase.want) || errors.Is(err, core.ErrMetadataUnreachable) {
				t.Fatalf("Movie error = %v, want only %v", err, testCase.want)
			}
			assertResponseReceivedStatus(t, err, testCase.status)
		})
	}
}

func TestClientBoundsUnreachableOperation(t *testing.T) {
	if metadataOperationTimeout != 6*time.Second {
		t.Fatalf("operation timeout = %s, want 6s", metadataOperationTimeout)
	}
	if attemptTimeout != 5*time.Second {
		t.Fatalf("attempt timeout = %s, want 5s", attemptTimeout)
	}
	defaultClient := newTestClient(t, "https://tmdb.test")
	if defaultClient.http.Timeout != metadataOperationTimeout {
		t.Fatalf("HTTP client timeout = %s, want %s", defaultClient.http.Timeout, metadataOperationTimeout)
	}
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		<-request.Context().Done()
		return nil, request.Context().Err()
	})
	clock := testutil.NewFakeClock(time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC))
	client, err := New(testReadAccessToken, Dependencies{
		BaseURL: "https://tmdb.test", Clock: clock, HTTPClient: &http.Client{Transport: transport},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	client.operationTimeout = 50 * time.Millisecond
	started := time.Now()
	_, err = client.Search(t.Context(), core.MetadataSearch{Query: "title"})
	if !errors.Is(err, core.ErrMetadataUnreachable) {
		t.Fatalf("Search error = %v, want %v", err, core.ErrMetadataUnreachable)
	}
	if elapsed := time.Since(started); elapsed >= 500*time.Millisecond {
		t.Fatalf("unreachable provider elapsed = %s, want under 500ms", elapsed)
	}
}

func TestRetryTransportStopsWhenAnotherAttemptCannotFit(t *testing.T) {
	var calls atomic.Int32
	next := roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return nil, &net.DNSError{Err: "blocked", Name: "tmdb.test"}
	})
	clock := testutil.NewFakeClock(time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC))
	transport := &retryTransport{
		next: next, metrics: nopMetrics{}, clock: clock, wait: waitContext,
		randomInt64N: func(int64) int64 { return 0 }, limiter: newTokenBucket(clock, ratePerSecond, rateBurst),
		attemptTimeout: attemptTimeout,
	}
	ctx, cancel := context.WithTimeout(t.Context(), attemptTimeout-time.Second)
	defer cancel()
	request := httptest.NewRequest(http.MethodGet, "https://tmdb.test/3/search/multi", nil).WithContext(ctx)
	_, err := transport.RoundTrip(request)
	dnsErr, ok := errors.AsType[*net.DNSError](err)
	if !ok || dnsErr == nil {
		t.Fatalf("RoundTrip error = %v, want DNS error", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("attempts = %d, want 1", calls.Load())
	}
	fullCtx, fullCancel := context.WithTimeout(t.Context(), metadataOperationTimeout)
	defer fullCancel()
	if !retryFits(fullCtx, 0, attemptTimeout) {
		t.Fatal("retry did not fit inside a fresh operation budget")
	}
}

func TestRetryTransportBoundsEachAttemptUntilBodyClose(t *testing.T) {
	var attemptDone <-chan struct{}
	next := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		deadline, ok := request.Context().Deadline()
		if !ok || time.Until(deadline) > attemptTimeout || time.Until(deadline) < attemptTimeout-time.Second {
			t.Errorf("attempt deadline = %v, want about %s", deadline, attemptTimeout)
		}
		attemptDone = request.Context().Done()
		return &http.Response{
			StatusCode: http.StatusOK, Header: make(http.Header), Body: http.NoBody, Request: request,
		}, nil
	})
	clock := testutil.NewFakeClock(time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC))
	transport := &retryTransport{
		next: next, metrics: nopMetrics{}, clock: clock, wait: waitContext,
		randomInt64N: func(int64) int64 { return 0 }, limiter: newTokenBucket(clock, ratePerSecond, rateBurst),
		attemptTimeout: attemptTimeout,
	}
	request := httptest.NewRequest(http.MethodGet, "https://tmdb.test/3/search/multi", nil)
	response, err := transport.RoundTrip(request)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	select {
	case <-attemptDone:
		t.Fatal("attempt context ended before response body close")
	default:
	}
	if err := response.Body.Close(); err != nil {
		t.Fatalf("close response body: %v", err)
	}
	select {
	case <-attemptDone:
	default:
		t.Fatal("attempt context remained active after response body close")
	}
}

func TestClientOperationBudgetClosesRetriedAndStalledAttempts(t *testing.T) {
	const (
		operationBudget = 200 * time.Millisecond
		attemptBudget   = 150 * time.Millisecond
		firstBodyDelay  = 80 * time.Millisecond
		elapsedCap      = 500 * time.Millisecond
	)
	base := &budgetTestTransport{firstBodyDelay: firstBodyDelay}
	clock := testutil.NewFakeClock(time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC))
	client, err := New(testReadAccessToken, Dependencies{
		BaseURL: "https://tmdb.test", BaseTransport: base, Clock: clock,
		Wait: func(context.Context, time.Duration) error { return nil }, RandomInt64N: func(int64) int64 { return 0 },
		AttemptTimeout: attemptBudget, OperationTimeout: operationBudget,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	started := time.Now()
	_, err = client.Movie(t.Context(), "11")
	if elapsed := time.Since(started); elapsed >= elapsedCap {
		t.Fatalf("Movie elapsed = %s, want under operation cap %s", elapsed, elapsedCap)
	}
	if !errors.Is(err, core.ErrMetadataMalformed) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Movie error = %v, want %v caused by operation deadline", err, core.ErrMetadataMalformed)
	}
	if got := base.calls.Load(); got != 2 {
		t.Fatalf("attempts = %d, want 2", got)
	}
	if got := base.activeBodies.Load(); got != 0 {
		t.Fatalf("active response bodies = %d, want 0", got)
	}
	if got := base.closedBodies.Load(); got != 2 {
		t.Fatalf("closed response bodies = %d, want 2", got)
	}
	if got := base.unauthenticated.Load(); got != 0 {
		t.Fatalf("unauthenticated attempts = %d, want 0", got)
	}
	for index, attemptContext := range base.attemptContexts() {
		if attemptContext.Err() == nil {
			t.Errorf("attempt %d context remains active", index+1)
		}
	}
	secondStart, secondDeadline := base.secondAttemptTiming(t)
	if remaining := secondDeadline.Sub(secondStart); remaining >= attemptBudget-firstBodyDelay/4 {
		t.Fatalf("second attempt budget = %s, want operation deadline shorter than %s", remaining, attemptBudget)
	}
}

type budgetTestTransport struct {
	mu              sync.Mutex
	contexts        []context.Context
	starts          []time.Time
	calls           atomic.Int32
	activeBodies    atomic.Int32
	closedBodies    atomic.Int32
	unauthenticated atomic.Int32
	firstBodyDelay  time.Duration
}

func (b *budgetTestTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	call := b.calls.Add(1)
	b.recordContext(request.Context())
	if request.Header.Get("Authorization") != "Bearer "+testReadAccessToken {
		b.unauthenticated.Add(1)
	}
	status := http.StatusServiceUnavailable
	body := io.NopCloser(strings.NewReader(`{}`))
	if call == 1 && b.firstBodyDelay > 0 {
		body = &delayedEOFBody{ctx: request.Context(), delay: b.firstBodyDelay}
	}
	if call == 2 {
		status = http.StatusOK
		body = &stalledResponseBody{ctx: request.Context()}
	}
	b.activeBodies.Add(1)
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body: &observedCloseBody{
			ReadCloser: body,
			onClose: func() {
				b.activeBodies.Add(-1)
				b.closedBodies.Add(1)
			},
		},
		Request: request,
	}, nil
}

func (b *budgetTestTransport) recordContext(ctx context.Context) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.contexts = append(b.contexts, ctx)
	b.starts = append(b.starts, time.Now())
}

func (b *budgetTestTransport) attemptContexts() []context.Context {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]context.Context(nil), b.contexts...)
}

func (b *budgetTestTransport) secondAttemptTiming(t *testing.T) (time.Time, time.Time) {
	t.Helper()
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.contexts) != 2 || len(b.starts) != 2 {
		t.Fatalf("attempt timings = %d contexts, %d starts", len(b.contexts), len(b.starts))
	}
	deadline, ok := b.contexts[1].Deadline()
	if !ok {
		t.Fatal("second attempt has no deadline")
	}
	return b.starts[1], deadline
}

type observedCloseBody struct {
	io.ReadCloser
	once    sync.Once
	onClose func()
}

func (b *observedCloseBody) Close() error {
	err := b.ReadCloser.Close()
	b.once.Do(b.onClose)
	return err
}

type stalledResponseBody struct {
	ctx context.Context
}

func (b *stalledResponseBody) Read([]byte) (int, error) {
	<-b.ctx.Done()
	return 0, b.ctx.Err()
}

func (*stalledResponseBody) Close() error {
	return nil
}

type delayedEOFBody struct {
	ctx   context.Context
	delay time.Duration
	done  bool
}

func (b *delayedEOFBody) Read([]byte) (int, error) {
	if b.done {
		return 0, io.EOF
	}
	timer := time.NewTimer(b.delay)
	defer timer.Stop()
	select {
	case <-b.ctx.Done():
		return 0, b.ctx.Err()
	case <-timer.C:
		b.done = true
		return 0, io.EOF
	}
}

func (*delayedEOFBody) Close() error {
	return nil
}

func TestClientRejectsCrossOriginRedirectWithoutLeakingKey(t *testing.T) {
	var destinationCalls atomic.Int32
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Host != "tmdb.test" {
			destinationCalls.Add(1)
			if request.Header.Get("Authorization") != "" {
				t.Errorf("cross-origin Authorization = %q", request.Header.Get("Authorization"))
			}
			return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: http.NoBody, Request: request}, nil
		}
		if request.Header.Get("Authorization") != "Bearer "+testReadAccessToken {
			t.Errorf("source Authorization = %q", request.Header.Get("Authorization"))
		}
		return &http.Response{
			StatusCode: http.StatusFound,
			Header:     http.Header{"Location": []string{"https://redirect.test/target"}},
			Body:       http.NoBody,
			Request:    request,
		}, nil
	})
	clock := testutil.NewFakeClock(time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC))
	client, err := New(testReadAccessToken, Dependencies{
		BaseURL: "https://tmdb.test", BaseTransport: transport, Clock: clock,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(client.CloseIdleConnections)
	_, err = client.Movie(t.Context(), "11")
	if !errors.Is(err, core.ErrMetadataMalformed) || errors.Is(err, core.ErrMetadataUnreachable) {
		t.Fatalf("redirect error = %v, want only %v", err, core.ErrMetadataMalformed)
	}
	assertResponseReceivedStatus(t, err, http.StatusFound)
	if strings.Contains(err.Error(), testReadAccessToken) {
		t.Fatalf("redirect error exposed credential: %v", err)
	}
	if destinationCalls.Load() != 0 {
		t.Fatalf("redirect destination calls = %d, want 0", destinationCalls.Load())
	}
}

func assertResponseReceivedStatus(t *testing.T, err error, want int) {
	t.Helper()
	responseErr, ok := errors.AsType[*responseReceivedError](err)
	if !ok || responseErr.status != want {
		t.Fatalf("response error = %v, want received status %d", err, want)
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
	client := newTestClient(t, server.URL)
	_, movieErr := client.Movie(t.Context(), "11")
	if !errors.Is(movieErr, core.ErrMetadataMalformed) || !errors.Is(movieErr, core.ErrInvalidArgument) {
		t.Fatalf("invalid movie title error = %v", movieErr)
	}
	_, seriesErr := client.Series(t.Context(), "12", false)
	if !errors.Is(seriesErr, core.ErrMetadataMalformed) || !errors.Is(seriesErr, core.ErrInvalidArgument) {
		t.Fatalf("invalid series title error = %v", seriesErr)
	}
}

func TestClientRetries429UsingLongRetryAfterWhenBudgetFits(t *testing.T) {
	const retryDelay = 31 * time.Second
	var calls atomic.Int32
	var waited time.Duration
	client := newBaseTransportTestClient(t, retryAfterTestTransport(&calls, true), func(_ context.Context, delay time.Duration) error {
		waited += delay
		return nil
	})
	client.operationTimeout = 40 * time.Second
	client.http.Timeout = 40 * time.Second
	if _, err := client.Movie(t.Context(), "11"); err != nil {
		t.Fatalf("Movie: %v", err)
	}
	if calls.Load() != 2 || waited != retryDelay {
		t.Fatalf("retry calls = %d, waited = %s", calls.Load(), waited)
	}
}

func TestClientReturns429WhenLongRetryAfterExceedsBudget(t *testing.T) {
	const retryDelay = 31 * time.Second
	var calls atomic.Int32
	started := time.Now()
	client := newBaseTransportTestClient(t, retryAfterTestTransport(&calls, false), nil)
	_, err := client.Movie(t.Context(), "11")
	elapsed := time.Since(started)
	if !errors.Is(err, core.ErrMetadataUnavailable) {
		t.Fatalf("Movie error = %v, want %v", err, core.ErrMetadataUnavailable)
	}
	if calls.Load() != 1 {
		t.Fatalf("provider calls = %d, want 1", calls.Load())
	}
	if elapsed >= retryDelay/10 {
		t.Fatalf("Movie elapsed = %s, want well under Retry-After %s", elapsed, retryDelay)
	}
}

func retryAfterTestTransport(calls *atomic.Int32, succeedAfterRetry bool) http.RoundTripper {
	return roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if calls.Add(1) > 1 && succeedAfterRetry {
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(`{"id":11,"title":"Film"}`)),
				Request:    request,
			}, nil
		}
		response := statusResponse(request, http.StatusTooManyRequests)
		response.Header.Set("Retry-After", "31")
		return response, nil
	})
}

func newTestClient(t *testing.T, baseURL string) *Client {
	t.Helper()
	clock := testutil.NewFakeClock(time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC))
	client, err := New(testReadAccessToken, Dependencies{BaseURL: baseURL, Clock: clock, RandomInt64N: func(int64) int64 { return 0 }})
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
	_, err := newTestClient(t, server.URL).Movie(t.Context(), "11")
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
	if _, err := newTestClient(t, server.URL).Movie(t.Context(), "11"); err != nil {
		t.Fatalf("Movie: %v", err)
	}
	if rendered := fmt.Sprintf("%+v", exporter.GetSpans()); strings.Contains(rendered, testReadAccessToken) {
		t.Fatalf("exported span data contains TMDB token: %s", rendered)
	}
}
