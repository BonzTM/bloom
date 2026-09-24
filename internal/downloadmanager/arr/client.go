// Package arr contains the shared bounded HTTP policy for Radarr and Sonarr.
package arr

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"syscall"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"

	"github.com/BonzTM/bloom/internal/core"
)

const (
	maxResponseBytes     = 1 << 20
	maxResolvedAddresses = 64
	maxAttempts          = 3
	defaultTimeout       = 10 * time.Second
	maxRetryAfter        = 30 * time.Second
)

// Observer records bounded request and retry metrics without exposing credentials.
type Observer interface {
	ObserveDownloadManagerRequest(kind, operation, outcome string, seconds float64)
	ObserveDownloadManagerRetry(kind, operation, outcome string)
}

// Config contains one Arr client's connection policy.
type Config struct {
	Kind       core.DownloadManagerKind
	BaseURL    string
	APIKey     string
	Timeout    time.Duration
	HTTPClient *http.Client
	Observer   Observer
}

// Client executes bounded authenticated JSON requests for Arr adapters.
type Client struct {
	kind       core.DownloadManagerKind
	baseURL    string
	apiKey     string
	httpClient *http.Client
	timeout    time.Duration
	observer   Observer
	now        func() time.Time
	sleep      func(context.Context, time.Duration) error
	random     func(int64) int64
}

// New validates cfg and constructs a destination-restricted client.
func New(cfg Config) (*Client, error) {
	if !cfg.Kind.Valid() || cfg.APIKey == "" {
		return nil, core.ErrInvalidArgument
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = defaultTimeout
	}
	client := configuredHTTPClient(cfg.HTTPClient, cfg.Timeout)
	return &Client{
		kind: cfg.Kind, baseURL: cfg.BaseURL, apiKey: cfg.APIKey, httpClient: client,
		timeout: cfg.Timeout, observer: cfg.Observer, now: time.Now,
		sleep: sleepContext, random: rand.Int64N,
	}, nil
}

func configuredHTTPClient(input *http.Client, timeout time.Duration) *http.Client {
	if input == nil {
		input = &http.Client{Transport: newSafeTransport(), Timeout: timeout}
	}
	client := *input
	transport := client.Transport
	if transport == nil {
		transport = newSafeTransport()
	}
	client.Transport = otelhttp.NewTransport(transport)
	client.Timeout = timeout
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &client
}

// GetJSON performs a retryable GET and decodes its bounded JSON response.
func (c *Client) GetJSON(ctx context.Context, operation, path string, target any) error {
	body, err := c.do(ctx, operation, http.MethodGet, path, nil, true)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(body, target); err != nil {
		return c.failure(operation, core.DownloadManagerMalformed, false, err)
	}
	return nil
}

// PostJSON performs a non-retryable POST and decodes its bounded JSON response.
func (c *Client) PostJSON(ctx context.Context, operation, path string, input, target any) error {
	body, err := json.Marshal(input)
	if err != nil {
		return c.failure(operation, core.DownloadManagerMalformed, false, err)
	}
	response, err := c.do(ctx, operation, http.MethodPost, path, body, false)
	clear(body)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(response, target); err != nil {
		return c.failure(operation, core.DownloadManagerMalformed, false, err)
	}
	return nil
}

// PutJSON performs a retryable idempotent PUT and decodes its bounded JSON response.
func (c *Client) PutJSON(ctx context.Context, operation, path string, input, target any) error {
	body, err := json.Marshal(input)
	if err != nil {
		return c.failure(operation, core.DownloadManagerMalformed, false, err)
	}
	response, err := c.do(ctx, operation, http.MethodPut, path, body, true)
	clear(body)
	if err != nil {
		return err
	}
	if target != nil && len(response) != 0 {
		if err := json.Unmarshal(response, target); err != nil {
			return c.failure(operation, core.DownloadManagerMalformed, false, err)
		}
	}
	return nil
}

func (c *Client) do(
	ctx context.Context, operation, method, path string, body []byte, retryable bool,
) ([]byte, error) {
	callCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	var last error
	for attempt := range maxAttempts {
		response, retry, err := c.attempt(callCtx, operation, method, path, body)
		if err == nil {
			return response, nil
		}
		last = err
		if !retryable || !retry || attempt == maxAttempts-1 {
			return nil, last
		}
		delay := retryDelay(last, attempt, c.random)
		c.observeRetry(operation, "scheduled")
		if err := c.sleep(callCtx, delay); err != nil {
			return nil, c.failure(operation, core.DownloadManagerUnavailable, false, err)
		}
	}
	return nil, last
}

func (c *Client) attempt(
	ctx context.Context, operation, method, path string, body []byte,
) ([]byte, bool, error) {
	started := c.now()
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return nil, false, c.failure(operation, core.DownloadManagerMalformed, false, err)
	}
	req.Header.Set("X-Api-Key", c.apiKey)
	req.Header.Set("Accept", "application/json")
	if len(body) > 0 {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.httpClient.Do(req) //nolint:bodyclose // drainAndClose closes every successful response.
	if err != nil {
		return c.transportFailure(ctx, operation, started, err)
	}
	defer drainAndClose(resp.Body)
	response, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return c.transportFailure(ctx, operation, started, err)
	}
	return c.classify(operation, resp, response, started)
}

func (c *Client) classify(
	operation string, resp *http.Response, body []byte, started time.Time,
) ([]byte, bool, error) {
	if len(body) > maxResponseBytes {
		return nil, false, c.observedFailure(operation, "malformed", core.DownloadManagerMalformed, false, started, errors.New("response exceeds size limit"))
	}
	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		c.observe(operation, "success", started)
		return body, false, nil
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return nil, false, c.observedFailure(operation, "unauthorized", core.DownloadManagerUnauthorized, false, started, nil)
	case resp.StatusCode == http.StatusNotFound:
		return nil, false, c.observedFailure(operation, "not_found", core.DownloadManagerNotFound, false, started, nil)
	case retryableStatus(resp.StatusCode):
		delay, _ := parseRetryAfter(resp.Header.Get("Retry-After"), c.now())
		err := &core.DownloadManagerError{Kind: core.DownloadManagerUnavailable, Operation: operation, Retryable: true, RetryAfter: delay}
		c.observe(operation, "unavailable", started)
		return nil, true, err
	default:
		return nil, false, c.observedFailure(operation, "malformed", core.DownloadManagerMalformed, false, started, errors.New("unexpected HTTP status"))
	}
}

func (c *Client) transportFailure(
	ctx context.Context, operation string, started time.Time, err error,
) ([]byte, bool, error) {
	retry := ctx.Err() == nil && transientNetworkError(err)
	kind := core.DownloadManagerUnavailable
	classified := c.observedFailure(operation, "unavailable", kind, retry, started, err)
	return nil, retry, classified
}

func (c *Client) observedFailure(
	operation, outcome string, kind core.DownloadManagerErrorKind, retry bool, started time.Time, err error,
) error {
	c.observe(operation, outcome, started)
	return c.failure(operation, kind, retry, err)
}

func (c *Client) failure(operation string, kind core.DownloadManagerErrorKind, retry bool, err error) error {
	return &core.DownloadManagerError{Kind: kind, Operation: operation, Retryable: retry, Err: err}
}

func (c *Client) observe(operation, outcome string, started time.Time) {
	if c.observer != nil {
		c.observer.ObserveDownloadManagerRequest(string(c.kind), operation, outcome, c.now().Sub(started).Seconds())
	}
}

func (c *Client) observeRetry(operation, outcome string) {
	if c.observer != nil {
		c.observer.ObserveDownloadManagerRetry(string(c.kind), operation, outcome)
	}
}

// CloseIdleConnections releases pooled connections.
func (c *Client) CloseIdleConnections() { c.httpClient.CloseIdleConnections() }

func retryableStatus(status int) bool {
	return status == http.StatusTooManyRequests || status == http.StatusBadGateway ||
		status == http.StatusServiceUnavailable || status == http.StatusGatewayTimeout
}

func retryDelay(err error, attempt int, random func(int64) int64) time.Duration {
	var classified *core.DownloadManagerError
	if errors.As(err, &classified) && classified.RetryAfter > 0 {
		return min(classified.RetryAfter, maxRetryAfter)
	}
	maximum := min(50*time.Millisecond*time.Duration(1<<attempt), 500*time.Millisecond)
	return time.Duration(random(int64(maximum) + 1))
}

func parseRetryAfter(value string, now time.Time) (time.Duration, bool) {
	seconds, err := strconv.ParseUint(value, 10, 64)
	if err == nil {
		if seconds >= uint64(maxRetryAfter/time.Second) {
			return maxRetryAfter, true
		}
		return min(time.Duration(seconds)*time.Second, maxRetryAfter), true
	}
	when, err := http.ParseTime(value)
	if err != nil {
		return 0, false
	}
	return min(max(when.Sub(now), 0), maxRetryAfter), true
}

func transientNetworkError(err error) bool {
	if terminalTLSError(err) {
		return false
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.EPIPE) {
		return true
	}
	var networkError net.Error
	return errors.As(err, &networkError) && networkError.Timeout()
}

func terminalTLSError(err error) bool {
	var verification *tls.CertificateVerificationError
	var record tls.RecordHeaderError
	var authority x509.UnknownAuthorityError
	return errors.As(err, &verification) || errors.As(err, &record) || errors.As(err, &authority)
}

func drainAndClose(body io.ReadCloser) {
	_, copyErr := io.Copy(io.Discard, io.LimitReader(body, maxResponseBytes+1))
	closeErr := body.Close()
	if copyErr != nil || closeErr != nil {
		return
	}
}

func sleepContext(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func newSafeTransport() *http.Transport {
	dialer := &net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}
	return &http.Transport{
		Proxy: nil, DialContext: safeDialContext(net.DefaultResolver, dialer),
		MaxIdleConns: 20, MaxIdleConnsPerHost: 5, IdleConnTimeout: 90 * time.Second,
		TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: 5 * time.Second,
		ExpectContinueTimeout: time.Second,
	}
}

type resolver interface {
	LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error)
}

type dialer interface {
	DialContext(ctx context.Context, network, address string) (net.Conn, error)
}

func safeDialContext(r resolver, d dialer) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, fmt.Errorf("split download manager address: %w", err)
		}
		addresses, err := r.LookupNetIP(ctx, "ip", host)
		if err != nil || len(addresses) == 0 || len(addresses) > maxResolvedAddresses {
			return nil, errors.New("resolve download manager destination")
		}
		for _, candidate := range addresses {
			if !core.MediaServerAddressAllowed(candidate) {
				return nil, errors.New("resolve download manager destination: denied address")
			}
		}
		var last error
		for _, candidate := range addresses {
			connection, dialErr := d.DialContext(ctx, network, net.JoinHostPort(candidate.Unmap().String(), port))
			if dialErr == nil {
				return connection, nil
			}
			last = dialErr
		}
		return nil, fmt.Errorf("dial download manager destination: %w", last)
	}
}
