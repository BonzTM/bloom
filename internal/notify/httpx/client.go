// Package httpx owns the bounded outbound HTTP policy for notification adapters.
package httpx

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/BonzTM/bloom/internal/core"
)

const (
	defaultTimeout       = 10 * time.Second
	maxResponseBytes     = 64 * 1024
	maxResolvedAddresses = 64
)

// Config defines one bounded HTTP notification destination.
type Config struct {
	URL           string
	AllowInsecure bool
	AllowPrivate  bool
	Timeout       time.Duration
	HTTPClient    *http.Client
}

// Client posts bounded JSON messages under the shared destination policy.
type Client struct {
	url    string
	client *http.Client
}

// New validates a destination and constructs a bounded client.
func New(config Config) (*Client, error) {
	target, err := ValidateURL(config.URL, config.AllowInsecure, config.AllowPrivate)
	if err != nil {
		return nil, err
	}
	if config.Timeout <= 0 {
		config.Timeout = defaultTimeout
	}
	client := configuredClient(config.HTTPClient, config.Timeout, config.AllowPrivate)
	return &Client{url: target, client: client}, nil
}

func configuredClient(input *http.Client, timeout time.Duration, allowPrivate bool) *http.Client {
	if input == nil {
		input = &http.Client{Transport: safeTransport(allowPrivate)}
	}
	result := *input
	if result.Transport == nil {
		result.Transport = safeTransport(allowPrivate)
	}
	result.Timeout = timeout
	result.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &result
}

// ValidateURL enforces the notification scheme and literal-address policy.
func ValidateURL(raw string, allowInsecure, allowPrivate bool) (string, error) {
	if raw == "" || len(raw) > core.MaxNotificationTargetBytes || strings.TrimSpace(raw) != raw {
		return "", core.ErrInvalidArgument
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || parsed.Hostname() == "" || parsed.User != nil || parsed.Fragment != "" {
		return "", core.ErrInvalidArgument
	}
	if parsed.Scheme != "https" && (parsed.Scheme != "http" || !allowInsecure) {
		return "", core.ErrInvalidArgument
	}
	if parsed.Scheme == "https" && allowInsecure {
		return "", core.ErrInvalidArgument
	}
	if address, parseErr := netip.ParseAddr(parsed.Hostname()); parseErr == nil && !AddressAllowed(address, allowPrivate) {
		return "", core.ErrInvalidArgument
	}
	return parsed.String(), nil
}

// Post sends one JSON body and classifies the response.
func (c *Client) Post(ctx context.Context, body []byte, headers map[string]string) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(body))
	if err != nil {
		return failure(core.NotificationMalformed, false, err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	response, err := c.client.Do(request) //nolint:bodyclose // closed below on every response path.
	if err != nil {
		return failure(core.NotificationUnavailable, true, err)
	}
	defer drainAndClose(response.Body)
	data, readErr := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if readErr != nil {
		return failure(core.NotificationUnavailable, true, readErr)
	}
	if len(data) > maxResponseBytes {
		return failure(core.NotificationMalformed, false, errors.New("response exceeds size limit"))
	}
	return classify(response)
}

func classify(response *http.Response) error {
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		return nil
	}
	if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
		return failure(core.NotificationUnauthorized, false, nil)
	}
	if response.StatusCode == http.StatusRequestTimeout || response.StatusCode == http.StatusTooManyRequests ||
		response.StatusCode >= http.StatusInternalServerError {
		delay, err := strconv.Atoi(response.Header.Get("Retry-After"))
		if err != nil {
			delay = 0
		}
		return &core.NotificationError{
			Kind: core.NotificationUnavailable, Operation: "send", Retryable: true,
			RetryAfter: min(time.Duration(max(delay, 0))*time.Second, 30*time.Second),
		}
	}
	return failure(core.NotificationRejected, false, errors.New("unexpected HTTP status"))
}

func failure(kind core.NotificationFailureKind, retryable bool, err error) error {
	return &core.NotificationError{Kind: kind, Operation: "send", Retryable: retryable, Err: err}
}

func safeTransport(allowPrivate bool) *http.Transport {
	dialer := &net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}
	return &http.Transport{
		Proxy: nil, DialContext: SafeDialContext(net.DefaultResolver, dialer, allowPrivate),
		MaxIdleConns: 20, MaxIdleConnsPerHost: 5, IdleConnTimeout: 90 * time.Second,
		TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: 5 * time.Second,
		ExpectContinueTimeout: time.Second, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12},
	}
}

// DialContext applies the notification destination policy before opening a TCP connection.
func DialContext(ctx context.Context, network, address string, allowPrivate bool) (net.Conn, error) {
	dialer := &net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}
	return SafeDialContext(net.DefaultResolver, dialer, allowPrivate)(ctx, network, address)
}

// Resolver performs bounded destination address resolution.
type Resolver interface {
	LookupNetIP(context.Context, string, string) ([]netip.Addr, error)
}

// Dialer opens one validated destination address.
type Dialer interface {
	DialContext(context.Context, string, string) (net.Conn, error)
}

// SafeDialContext validates every resolved address immediately before dialing.
func SafeDialContext(r Resolver, d Dialer, allowPrivate bool) func(context.Context, string, string) (net.Conn, error) {
	if r == nil {
		r = net.DefaultResolver
	}
	if d == nil {
		d = &net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}
	}
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, fmt.Errorf("split notification destination: %w", err)
		}
		addresses, err := r.LookupNetIP(ctx, "ip", host)
		if err != nil || len(addresses) == 0 || len(addresses) > maxResolvedAddresses {
			return nil, errors.New("resolve notification destination")
		}
		for _, candidate := range addresses {
			if !AddressAllowed(candidate, allowPrivate) {
				return nil, errors.New("resolve notification destination: denied address")
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
		return nil, fmt.Errorf("dial notification destination: %w", last)
	}
}

// AddressAllowed applies the notification destination policy to one IP address.
func AddressAllowed(address netip.Addr, allowPrivate bool) bool {
	if !core.MediaServerAddressAllowed(address) {
		return false
	}
	address = address.Unmap()
	return allowPrivate || !address.IsPrivate()
}

func drainAndClose(body io.ReadCloser) {
	if _, err := io.Copy(io.Discard, io.LimitReader(body, maxResponseBytes+1)); err != nil {
		_ = body.Close()
		return
	}
	_ = body.Close()
}

// CloseIdleConnections releases pooled HTTP connections.
func (c *Client) CloseIdleConnections() { c.client.CloseIdleConnections() }
