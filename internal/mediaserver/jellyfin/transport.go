package jellyfin

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"time"

	"github.com/BonzTM/bloom/internal/core"
)

const maxResolvedAddresses = 64

type ipResolver interface {
	LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error)
}

type contextDialer interface {
	DialContext(ctx context.Context, network, address string) (net.Conn, error)
}

func newHTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{Transport: newSafeTransport(), Timeout: timeout}
}

func newSafeTransport() *http.Transport {
	dialer := &net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}
	return newSafeTransportWith(net.DefaultResolver, dialer)
}

func newSafeTransportWith(resolver ipResolver, dialer contextDialer) *http.Transport {
	return &http.Transport{
		Proxy: nil, DialContext: safeDialContext(resolver, dialer),
		MaxIdleConns: 20, MaxIdleConnsPerHost: 5, IdleConnTimeout: 90 * time.Second,
		TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: 5 * time.Second,
		ExpectContinueTimeout: time.Second,
	}
}

func safeDialContext(resolver ipResolver, dialer contextDialer) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, fmt.Errorf("split media server address: %w", err)
		}
		addresses, err := resolveAllowed(ctx, resolver, host)
		if err != nil {
			return nil, err
		}
		return dialAllowed(ctx, dialer, network, port, addresses)
	}
}

func resolveAllowed(ctx context.Context, resolver ipResolver, host string) ([]netip.Addr, error) {
	addresses, err := resolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return nil, fmt.Errorf("resolve media server destination: %w", err)
	}
	if len(addresses) == 0 || len(addresses) > maxResolvedAddresses {
		return nil, errors.New("resolve media server destination: invalid address count")
	}
	allowed := make([]netip.Addr, 0, len(addresses))
	for _, address := range addresses {
		if !core.MediaServerAddressAllowed(address) {
			return nil, errors.New("resolve media server destination: denied address")
		}
		allowed = append(allowed, address.Unmap())
	}
	return allowed, nil
}

func dialAllowed(
	ctx context.Context,
	dialer contextDialer,
	network, port string,
	addresses []netip.Addr,
) (net.Conn, error) {
	var last error
	for _, address := range addresses {
		connection, err := dialer.DialContext(ctx, network, net.JoinHostPort(address.String(), port))
		if err == nil {
			return connection, nil
		}
		last = err
	}
	return nil, fmt.Errorf("dial media server destination: %w", last)
}

func (c *Client) requestAttempt(
	ctx context.Context,
	operation, method, path string,
	body []byte,
	started time.Time,
) ([]byte, bool, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return nil, false, mediaError(operation, core.MediaServerMalformed, err)
	}
	req.Header.Set("Authorization", c.authorization)
	req.Header.Set("Accept", "application/json")
	if len(body) > 0 {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return c.classifyTransportError(ctx, operation, started, err)
	}
	defer func() {
		_, drainErr := io.Copy(io.Discard, io.LimitReader(resp.Body, maxResponseBytes+1))
		_ = drainErr
		closeErr := resp.Body.Close()
		_ = closeErr
	}()
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if readErr != nil {
		return c.classifyTransportError(ctx, operation, started, readErr)
	}
	if len(body) > maxResponseBytes {
		c.observe(operation, "malformed", started)
		return nil, false, mediaError(operation, core.MediaServerMalformed, errors.New("response exceeds size limit"))
	}
	return c.classifyResponse(operation, resp, body, started)
}

func (c *Client) classifyTransportError(
	ctx context.Context,
	operation string,
	started time.Time,
	err error,
) ([]byte, bool, error) {
	retry, classified := retryableNetworkError(ctx, operation, err)
	outcome := "terminal_network"
	if retry {
		outcome = "unavailable"
	}
	c.observe(operation, outcome, started)
	return nil, retry, classified
}

func (c *Client) classifyResponse(
	operation string,
	resp *http.Response,
	body []byte,
	started time.Time,
) ([]byte, bool, error) {
	status := resp.StatusCode
	switch {
	case status >= 200 && status < 300:
		return body, false, nil
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		c.observe(operation, "unauthorized", started)
		return nil, false, mediaError(operation, core.MediaServerUnauthorized, nil)
	case status == http.StatusNotFound:
		c.observe(operation, "not_found", started)
		return nil, false, mediaError(operation, core.MediaServerNotFound, nil)
	case operation == "create_user" && status == http.StatusBadRequest:
		c.observe(operation, "username_rejected", started)
		return nil, false, &core.MediaUserNameError{}
	case retryableStatus(status):
		c.observe(operation, "unavailable", started)
		delay, valid := parseRetryAfter(resp.Header.Get("Retry-After"), c.now())
		return nil, true, retryableResponseError(operation, delay, valid)
	default:
		c.observe(operation, "malformed", started)
		return nil, false, mediaError(operation, core.MediaServerMalformed, errors.New("unexpected HTTP status "+strconv.Itoa(status)))
	}
}
