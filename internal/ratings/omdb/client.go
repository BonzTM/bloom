// Package omdb implements Bloom's OMDb ratings adapter.
//
// OMDb takes its API key in the query string, so no error, log line, or span
// this package produces may carry a request URL. Transport errors are mapped
// to sentinel errors without wrapping, redirects are refused, and the client
// is not instrumented with otelhttp, which records the full URL.
package omdb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/BonzTM/bloom/internal/core"
)

const (
	defaultBaseURL         = "https://www.omdbapi.com/"
	requestTimeout         = 5 * time.Second
	maxResponseBytes int64 = 256 << 10
	// probeIMDbID is a long-lived title used to check a key: The Matrix.
	probeIMDbID = "tt0133093"
	provider    = "omdb"
)

// Metrics observes bounded outbound OMDb activity.
type Metrics interface {
	ObserveMetadataRequest(provider, operation, outcome string, seconds float64)
}

type nopMetrics struct{}

func (nopMetrics) ObserveMetadataRequest(string, string, string, float64) {}

// Dependencies configures the OMDb client and its test seams.
type Dependencies struct {
	HTTPClient *http.Client
	BaseURL    string
	Metrics    Metrics
	Clock      core.Clock
}

// Client reads title ratings from OMDb.
type Client struct {
	http    *http.Client
	base    *url.URL
	key     string
	metrics Metrics
	clock   core.Clock
}

var _ core.TitleRatingsProvider = (*Client)(nil)

// New constructs an OMDb client with a bounded, redirect-free HTTP client.
func New(key string, deps Dependencies) (*Client, error) {
	if core.ValidateOMDbKey(key) != nil || deps.Clock == nil {
		return nil, fmt.Errorf("omdb client: %w", core.ErrInvalidArgument)
	}
	baseURL := deps.BaseURL
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	base, err := url.Parse(baseURL)
	if err != nil || base.Scheme == "" || base.Host == "" {
		return nil, fmt.Errorf("parse OMDb base URL: %w", core.ErrInvalidArgument)
	}
	metrics := deps.Metrics
	if metrics == nil {
		metrics = nopMetrics{}
	}
	return &Client{http: newHTTPClient(deps.HTTPClient), base: base, key: key, metrics: metrics, clock: deps.Clock}, nil
}

func newHTTPClient(seed *http.Client) *http.Client {
	client := &http.Client{Transport: &http.Transport{
		Proxy: http.ProxyFromEnvironment, DialContext: (&net.Dialer{Timeout: 3 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		ForceAttemptHTTP2: true, MaxIdleConns: 10, MaxIdleConnsPerHost: 5,
		IdleConnTimeout: 90 * time.Second, TLSHandshakeTimeout: 3 * time.Second,
		ResponseHeaderTimeout: 4 * time.Second, ExpectContinueTimeout: time.Second,
	}}
	if seed != nil {
		copied := *seed
		client = &copied
	}
	client.Timeout = requestTimeout
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return client
}

// Probe verifies the key by reading one well-known title.
func (c *Client) Probe(ctx context.Context) error {
	_, found, err := c.fetch(ctx, "probe", probeIMDbID)
	if err == nil && !found {
		return fmt.Errorf("omdb probe title missing: %w", core.ErrMetadataMalformed)
	}
	return err
}

// TitleRatings returns the IMDb, Rotten Tomatoes, and Metacritic scores OMDb
// holds for imdbID. A title OMDb does not know has no ratings and no error.
func (c *Client) TitleRatings(ctx context.Context, imdbID string) (core.TitleRatings, error) {
	if !core.ValidIMDbID(imdbID) {
		return core.TitleRatings{}, core.ErrInvalidArgument
	}
	title, found, err := c.fetch(ctx, "ratings", imdbID)
	if err != nil || !found {
		return core.TitleRatings{}, err
	}
	return mapRatings(title), nil
}

// CloseIdleConnections releases pooled connections.
func (c *Client) CloseIdleConnections() { c.http.CloseIdleConnections() }

// fetch reads one title. It reports found false, with no error, when OMDb
// has no such title.
func (c *Client) fetch(ctx context.Context, operation, imdbID string) (rawTitle, bool, error) {
	started := c.clock.Now()
	title, found, err := c.fetchTitle(ctx, imdbID)
	c.metrics.ObserveMetadataRequest(provider, operation, outcome(err), c.clock.Now().Sub(started).Seconds())
	return title, found, err
}

func (c *Client) fetchTitle(ctx context.Context, imdbID string) (rawTitle, bool, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.titleURL(imdbID), http.NoBody)
	if err != nil {
		return rawTitle{}, false, fmt.Errorf("build OMDb request: %w", core.ErrInvalidArgument)
	}
	request.Header.Set("Accept", "application/json")
	response, err := c.http.Do(request)
	if err != nil {
		// The *url.Error holds the key in its URL, so it is never wrapped.
		return rawTitle{}, false, transportError(ctx)
	}
	defer func() { _ = response.Body.Close() }()
	var title rawTitle
	if decodeErr := json.NewDecoder(io.LimitReader(response.Body, maxResponseBytes)).Decode(&title); decodeErr != nil {
		return rawTitle{}, false, statusError(response.StatusCode, core.ErrMetadataMalformed)
	}
	found, err := answer(response.StatusCode, title)
	return title, found, err
}

func (c *Client) titleURL(imdbID string) string {
	target := *c.base
	target.RawQuery = url.Values{"apikey": {c.key}, "i": {imdbID}, "r": {"json"}}.Encode()
	return target.String()
}

func transportError(ctx context.Context) error {
	if errors.Is(ctx.Err(), context.Canceled) {
		return context.Canceled
	}
	return fmt.Errorf("omdb request: %w", core.ErrMetadataUnreachable)
}

func outcome(err error) string {
	switch {
	case err == nil:
		return "success"
	case errors.Is(err, core.ErrMetadataUnauthorized):
		return "unauthorized"
	case errors.Is(err, core.ErrMetadataMalformed):
		return "malformed"
	case errors.Is(err, core.ErrMetadataUnreachable):
		return "unreachable"
	default:
		return "unavailable"
	}
}
