// Package tmdb implements Bloom's TMDB metadata-provider adapter.
package tmdb

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"

	"github.com/BonzTM/bloom/internal/core"
	tmdbapi "github.com/BonzTM/bloom/internal/metadata/tmdb/api"
)

const (
	defaultBaseURL                 = "https://api.themoviedb.org"
	attemptTimeout                 = 5 * time.Second
	metadataOperationTimeout       = 6 * time.Second
	maxResponseBytes         int64 = 2 << 20
	maxAttempts                    = 3
	ratePerSecond                  = 40
	rateBurst                      = 40
)

// Metrics observes bounded outbound TMDB requests and retries.
type Metrics interface {
	ObserveMetadataRequest(provider, operation, outcome string, seconds float64)
	ObserveMetadataRetry(provider, operation, outcome string)
}

type nopMetrics struct{}

func (nopMetrics) ObserveMetadataRequest(string, string, string, float64) {}
func (nopMetrics) ObserveMetadataRetry(string, string, string)            {}

// Dependencies configures the TMDB client and its test seams.
type Dependencies struct {
	HTTPClient       *http.Client
	BaseTransport    http.RoundTripper
	BaseURL          string
	Metrics          Metrics
	Clock            core.Clock
	Wait             func(context.Context, time.Duration) error
	RandomInt64N     func(int64) int64
	AttemptTimeout   time.Duration
	OperationTimeout time.Duration
}

// Client implements the metadata provider seam with TMDB's v3 API and v4 Read Access Token.
type Client struct {
	api              *tmdbapi.ClientWithResponses
	http             *http.Client
	metrics          Metrics
	clock            core.Clock
	operationTimeout time.Duration
}

// New constructs a TMDB client with bounded network behavior.
func New(token string, deps Dependencies) (*Client, error) {
	if core.ValidateMetadataCredential(core.MetadataProviderTMDB, token) != nil || deps.Clock == nil {
		return nil, fmt.Errorf("tmdb client: %w", core.ErrInvalidArgument)
	}
	if deps.HTTPClient != nil && deps.BaseTransport != nil {
		return nil, fmt.Errorf("tmdb client transport: %w", core.ErrInvalidArgument)
	}
	timeouts, err := resolveTimeouts(deps.AttemptTimeout, deps.OperationTimeout)
	if err != nil {
		return nil, err
	}
	baseURL := deps.BaseURL
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	base, err := url.Parse(baseURL)
	if err != nil || base.Scheme == "" || base.Host == "" {
		return nil, fmt.Errorf("parse TMDB base URL: %w", core.ErrInvalidArgument)
	}
	metrics := deps.Metrics
	if metrics == nil {
		metrics = nopMetrics{}
	}
	httpClient := deps.HTTPClient
	if httpClient == nil {
		httpClient = newHTTPClient(
			token, base, metrics, deps.Clock, deps.Wait, deps.RandomInt64N, deps.BaseTransport, timeouts,
		)
	} else {
		httpClient = withBearerToken(httpClient, token, base)
	}
	generated, err := tmdbapi.NewClientWithResponses(
		baseURL, tmdbapi.WithHTTPClient(&responseTrackingDoer{next: httpClient}),
	)
	if err != nil {
		return nil, fmt.Errorf("construct generated TMDB client: %w", err)
	}
	return &Client{
		api: generated, http: httpClient, metrics: metrics, clock: deps.Clock,
		operationTimeout: timeouts.operation,
	}, nil
}

type timeouts struct {
	attempt   time.Duration
	operation time.Duration
}

func resolveTimeouts(attempt, operation time.Duration) (timeouts, error) {
	if attempt < 0 || operation < 0 {
		return timeouts{}, fmt.Errorf("tmdb client timeouts: %w", core.ErrInvalidArgument)
	}
	if attempt == 0 {
		attempt = attemptTimeout
	}
	if operation == 0 {
		operation = metadataOperationTimeout
	}
	if attempt > operation {
		return timeouts{}, fmt.Errorf("tmdb client timeouts: %w", core.ErrInvalidArgument)
	}
	return timeouts{attempt: attempt, operation: operation}, nil
}

func newHTTPClient(
	token string,
	baseURL *url.URL,
	metrics Metrics,
	clock core.Clock,
	wait func(context.Context, time.Duration) error,
	randomInt64N func(int64) int64,
	baseTransport http.RoundTripper,
	timeouts timeouts,
) *http.Client {
	if baseTransport == nil {
		baseTransport = &http.Transport{
			Proxy: http.ProxyFromEnvironment, DialContext: (&net.Dialer{Timeout: 3 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
			ForceAttemptHTTP2: true, MaxIdleConns: 20, MaxIdleConnsPerHost: 10,
			IdleConnTimeout: 90 * time.Second, TLSHandshakeTimeout: 3 * time.Second,
			ResponseHeaderTimeout: 4 * time.Second, ExpectContinueTimeout: time.Second,
		}
	}
	if wait == nil {
		wait = waitContext
	}
	if randomInt64N == nil {
		randomInt64N = rand.Int64N
	}
	limiter := newTokenBucket(clock, ratePerSecond, rateBurst)
	retrying := &retryTransport{
		next: baseTransport, metrics: metrics, clock: clock, wait: wait, randomInt64N: randomInt64N,
		limiter: limiter, attemptTimeout: timeouts.attempt,
	}
	authenticated := &bearerTransport{next: retrying, token: token, baseURL: baseURL}
	client := &http.Client{Transport: otelhttp.NewTransport(authenticated), Timeout: timeouts.operation}
	client.CheckRedirect = redirectPolicy(baseURL, nil)
	return client
}

type responseTrackingDoer struct {
	next tmdbapi.HttpRequestDoer
}

func (d *responseTrackingDoer) Do(request *http.Request) (*http.Response, error) {
	response, err := d.next.Do(request)
	if response == nil {
		return nil, err
	}
	if err != nil {
		return response, newResponseReceivedError(response.StatusCode, err)
	}
	if response.Body == nil {
		response.Body = http.NoBody
	}
	response.Body = &responseTrackingBody{ReadCloser: response.Body, status: response.StatusCode}
	return response, nil
}

type responseTrackingBody struct {
	io.ReadCloser
	status int
}

func (b *responseTrackingBody) Read(buffer []byte) (int, error) {
	count, err := b.ReadCloser.Read(buffer)
	if err == nil || errors.Is(err, io.EOF) {
		return count, err
	}
	return count, newResponseReceivedError(b.status, err)
}

type responseReceivedError struct {
	status int
	err    error
}

func newResponseReceivedError(status int, err error) error {
	return &responseReceivedError{status: status, err: err}
}

func (e *responseReceivedError) Error() string {
	return fmt.Sprintf("TMDB response status %d: %v", e.status, e.err)
}

func (e *responseReceivedError) Unwrap() error {
	return e.err
}

type bearerTransport struct {
	next    http.RoundTripper
	token   string
	baseURL *url.URL
}

func (t *bearerTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	clone := request.Clone(request.Context())
	if !sameOrigin(request.URL, t.baseURL) {
		clone.Header.Del("Authorization")
		return t.next.RoundTrip(clone)
	}
	clone.Header.Set("Authorization", "Bearer "+t.token)
	return t.next.RoundTrip(clone)
}

func withBearerToken(client *http.Client, token string, baseURL *url.URL) *http.Client {
	clone := *client
	transport := clone.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	clone.Transport = &bearerTransport{next: transport, token: token, baseURL: baseURL}
	clone.CheckRedirect = redirectPolicy(baseURL, client.CheckRedirect)
	return &clone
}

// Probe verifies the configured Read Access Token with TMDB.
func (c *Client) Probe(ctx context.Context) error {
	ctx, cancel := c.operationContext(ctx)
	defer cancel()
	started := c.clock.Now()
	response, err := c.api.AuthenticationValidateKeyWithResponse(ctx)
	status := 0
	if response != nil {
		status = response.StatusCode()
	}
	c.observe("probe", started, status, err)
	if err != nil {
		return classifyCallError("probe", err)
	}
	if err := validateStatus("probe", response.StatusCode()); err != nil {
		return err
	}
	if response.JSON200 == nil || response.JSON200.Success == nil {
		return classifyError("probe", response.StatusCode(), core.ErrMetadataMalformed)
	}
	if !*response.JSON200.Success {
		return classifyError("probe", response.StatusCode(), core.ErrMetadataUnauthorized)
	}
	return nil
}

func redirectPolicy(baseURL *url.URL, previous func(*http.Request, []*http.Request) error) func(*http.Request, []*http.Request) error {
	return func(request *http.Request, via []*http.Request) error {
		if !sameOrigin(request.URL, baseURL) {
			return errors.New("TMDB redirect target origin rejected")
		}
		if previous != nil {
			return previous(request, via)
		}
		if len(via) >= 10 {
			return errors.New("stopped after 10 redirects")
		}
		return nil
	}
}

func sameOrigin(candidate, base *url.URL) bool {
	return candidate != nil && base != nil && strings.EqualFold(candidate.Scheme, base.Scheme) &&
		strings.EqualFold(candidate.Host, base.Host)
}

// Search searches TMDB movies and series.
func (c *Client) Search(ctx context.Context, input core.MetadataSearch) ([]core.MetadataTitle, error) {
	if err := core.ValidateMetadataSearch(input); err != nil {
		return nil, err
	}
	ctx, cancel := c.operationContext(ctx)
	defer cancel()
	started := c.clock.Now()
	response, err := c.api.SearchMultiWithResponse(ctx, &tmdbapi.SearchMultiParams{Query: input.Query})
	status := 0
	if response != nil {
		status = response.StatusCode()
	}
	c.observe("search", started, status, err)
	if err != nil {
		return nil, classifyCallError("search", err)
	}
	if err := validateStatus("search", response.StatusCode()); err != nil {
		return nil, err
	}
	if response.JSON200 == nil || response.JSON200.Results == nil {
		return nil, classifyError("search", response.StatusCode(), core.ErrMetadataMalformed)
	}
	return mapSearchResults(*response.JSON200.Results, input.Kind), nil
}

// Movie returns TMDB movie details.
func (c *Client) Movie(ctx context.Context, providerID string) (core.MetadataTitle, error) {
	id, err := parseProviderID(providerID)
	if err != nil {
		return core.MetadataTitle{}, err
	}
	ctx, cancel := c.operationContext(ctx)
	defer cancel()
	started := c.clock.Now()
	response, err := c.api.MovieDetailsWithResponse(ctx, id, nil)
	status := 0
	if response != nil {
		status = response.StatusCode()
	}
	c.observe("movie", started, status, err)
	if err != nil {
		return core.MetadataTitle{}, classifyCallError("movie", err)
	}
	if err := validateStatus("movie", response.StatusCode()); err != nil {
		return core.MetadataTitle{}, err
	}
	if response.JSON200 == nil || response.JSON200.Id == nil || response.JSON200.Title == nil {
		return core.MetadataTitle{}, classifyError("movie", response.StatusCode(), core.ErrMetadataMalformed)
	}
	title := core.MetadataTitle{
		Kind: core.MediaKindMovie, Provider: core.MetadataProviderTMDB,
		ProviderID: strconv.Itoa(*response.JSON200.Id), Title: *response.JSON200.Title,
		Year: yearFromDate(value(response.JSON200.ReleaseDate)), Overview: value(response.JSON200.Overview),
		PosterPath: value(response.JSON200.PosterPath), BackdropPath: value(response.JSON200.BackdropPath),
	}
	if err := core.ValidateMetadataTitle(title); err != nil {
		return core.MetadataTitle{}, classifyError("movie", response.StatusCode(), errors.Join(core.ErrMetadataMalformed, err))
	}
	return title, nil
}

// Series returns TMDB series details and seasons.
func (c *Client) Series(ctx context.Context, providerID string, includeSpecials bool) (core.MetadataSeries, error) {
	id, err := parseProviderID(providerID)
	if err != nil {
		return core.MetadataSeries{}, err
	}
	ctx, cancel := c.operationContext(ctx)
	defer cancel()
	started := c.clock.Now()
	response, err := c.api.TvSeriesDetailsWithResponse(ctx, id, nil)
	status := 0
	if response != nil {
		status = response.StatusCode()
	}
	c.observe("series", started, status, err)
	if err != nil {
		return core.MetadataSeries{}, classifyCallError("series", err)
	}
	if err := validateStatus("series", response.StatusCode()); err != nil {
		return core.MetadataSeries{}, err
	}
	return mapSeriesResponse(response, includeSpecials)
}

func (c *Client) observe(operation string, started time.Time, status int, err error) {
	outcome := "success"
	if err != nil || status < 200 || status >= 300 {
		outcome = "failure"
	}
	c.metrics.ObserveMetadataRequest("tmdb", operation, outcome, c.clock.Now().Sub(started).Seconds())
}

// CloseIdleConnections closes idle connections owned by the client transport.
func (c *Client) CloseIdleConnections() {
	c.http.CloseIdleConnections()
}

func (c *Client) operationContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, c.operationTimeout)
}

func parseProviderID(value string) (int32, error) {
	if err := core.ValidateProviderID(value); err != nil {
		return 0, err
	}
	id, err := strconv.ParseInt(value, 10, 32)
	if err != nil {
		return 0, core.ErrInvalidArgument
	}
	return int32(id), nil
}

func validateStatus(operation string, status int) error {
	switch status {
	case http.StatusOK:
		return nil
	case http.StatusUnauthorized:
		return classifyError(operation, status, core.ErrMetadataUnauthorized)
	case http.StatusNotFound:
		return core.ErrNotFound
	case http.StatusTooManyRequests:
		return classifyError(operation, status, core.ErrMetadataUnavailable)
	}
	if status >= http.StatusInternalServerError && status <= 599 {
		return classifyError(operation, status, core.ErrMetadataUnavailable)
	}
	return classifyError(operation, status, core.ErrMetadataMalformed)
}

func classifyError(operation string, status int, err error) error {
	return fmt.Errorf("tmdb %s status %d: %w", operation, status, err)
}

func classifyCallError(operation string, err error) error {
	if responseErr, ok := errors.AsType[*responseReceivedError](err); ok {
		classification := core.ErrMetadataMalformed
		if unavailableStatus(responseErr.status) {
			classification = core.ErrMetadataUnavailable
		}
		return classifyError(operation, responseErr.status, errors.Join(classification, err))
	}
	if retryableError(err) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return classifyError(operation, 0, errors.Join(core.ErrMetadataUnreachable, err))
	}
	return classifyError(operation, 0, errors.Join(core.ErrMetadataMalformed, err))
}

func value(pointer *string) string {
	if pointer == nil {
		return ""
	}
	return *pointer
}

func yearFromDate(date string) int {
	if len(date) < 4 {
		return 0
	}
	year, err := strconv.Atoi(date[:4])
	if err != nil || year < 0 || year > 9999 {
		return 0
	}
	return year
}

func waitContext(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func retryAfter(response *http.Response) time.Duration {
	if response == nil {
		return 0
	}
	seconds, err := strconv.Atoi(strings.TrimSpace(response.Header.Get("Retry-After")))
	if err != nil || seconds < 0 || seconds > 30 {
		return 0
	}
	return time.Duration(seconds) * time.Second
}

func retryableStatus(status int) bool {
	return status == http.StatusTooManyRequests || status == http.StatusBadGateway ||
		status == http.StatusServiceUnavailable || status == http.StatusGatewayTimeout
}

func unavailableStatus(status int) bool {
	return status == http.StatusTooManyRequests || status >= http.StatusInternalServerError && status <= 599
}

func retryableError(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var networkError net.Error
	return errors.As(err, &networkError)
}
