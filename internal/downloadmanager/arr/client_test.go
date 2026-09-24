package arr

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/BonzTM/bloom/internal/core"
)

func TestGetJSONClassifiesTerminalStatusesWithoutRetry(t *testing.T) {
	tests := []struct {
		status int
		kind   core.DownloadManagerErrorKind
	}{
		{status: http.StatusUnauthorized, kind: core.DownloadManagerUnauthorized},
		{status: http.StatusNotFound, kind: core.DownloadManagerNotFound},
	}
	for _, testCase := range tests {
		t.Run(http.StatusText(testCase.status), func(t *testing.T) {
			var calls atomic.Int32
			client, closeServer := testHTTPClient(t, func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				w.WriteHeader(testCase.status)
			})
			defer closeServer()
			err := client.GetJSON(t.Context(), "test", "/resource", &map[string]any{})
			assertManagerError(t, err, testCase.kind, false)
			if calls.Load() != 1 {
				t.Fatalf("calls = %d, want 1", calls.Load())
			}
		})
	}
}

func TestGetJSONRetries429AndRetryableServerFailures(t *testing.T) {
	tests := []int{http.StatusTooManyRequests, http.StatusServiceUnavailable}
	for _, status := range tests {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var calls atomic.Int32
			client, closeServer := testHTTPClient(t, func(w http.ResponseWriter, _ *http.Request) {
				if calls.Add(1) < 3 {
					w.Header().Set("Retry-After", "1")
					w.WriteHeader(status)
					return
				}
				if _, err := w.Write([]byte(`{"ok":true}`)); err != nil {
					t.Errorf("write response: %v", err)
				}
			})
			defer closeServer()
			var waits []time.Duration
			client.sleep = func(_ context.Context, delay time.Duration) error {
				waits = append(waits, delay)
				return nil
			}
			var value map[string]bool
			if err := client.GetJSON(t.Context(), "test", "/resource", &value); err != nil || !value["ok"] {
				t.Fatalf("GetJSON = %v, %v", value, err)
			}
			if calls.Load() != 3 || len(waits) != 2 {
				t.Fatalf("calls = %d waits = %v", calls.Load(), waits)
			}
		})
	}
}

func TestPostJSONDoesNotRetryAndErrorsDoNotExposeCredential(t *testing.T) {
	const apiKey = "highly-sensitive-key"
	var calls atomic.Int32
	client, closeServer := testHTTPClientWithKey(t, apiKey, func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	defer closeServer()
	err := client.PostJSON(t.Context(), "add", "/resource", map[string]bool{"add": true}, &map[string]any{})
	assertManagerError(t, err, core.DownloadManagerUnavailable, true)
	if calls.Load() != 1 || strings.Contains(err.Error(), apiKey) {
		t.Fatalf("POST calls = %d error = %q", calls.Load(), err)
	}
}

func TestGetJSONRejectsMalformedAndOversizedBodies(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "malformed", body: `{`},
		{name: "oversized", body: strings.Repeat("x", maxResponseBytes+1)},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			client, closeServer := testHTTPClient(t, func(w http.ResponseWriter, _ *http.Request) {
				if _, err := w.Write([]byte(testCase.body)); err != nil {
					t.Errorf("write response: %v", err)
				}
			})
			defer closeServer()
			err := client.GetJSON(t.Context(), "test", "/resource", &map[string]any{})
			assertManagerError(t, err, core.DownloadManagerMalformed, false)
		})
	}
}

func testHTTPClient(t *testing.T, handler http.HandlerFunc) (*Client, func()) {
	t.Helper()
	return testHTTPClientWithKey(t, "secret", handler)
}

func testHTTPClientWithKey(t *testing.T, apiKey string, handler http.HandlerFunc) (*Client, func()) {
	t.Helper()
	server := httptest.NewServer(handler)
	target, err := url.Parse(server.URL)
	if err != nil {
		server.Close()
		t.Fatalf("parse test URL: %v", err)
	}
	client, err := New(Config{
		Kind: core.DownloadManagerKindRadarr, BaseURL: "https://radarr.example.test", APIKey: apiKey,
		HTTPClient: &http.Client{Transport: rewriteTransport{target: target, base: server.Client().Transport}},
	})
	if err != nil {
		server.Close()
		t.Fatalf("New: %v", err)
	}
	client.random = func(int64) int64 { return 0 }
	return client, server.Close
}

type rewriteTransport struct {
	target *url.URL
	base   http.RoundTripper
}

func (t rewriteTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	clone := request.Clone(request.Context())
	clone.URL.Scheme = t.target.Scheme
	clone.URL.Host = t.target.Host
	return t.base.RoundTrip(clone)
}

func assertManagerError(t *testing.T, err error, kind core.DownloadManagerErrorKind, retryable bool) {
	t.Helper()
	var classified *core.DownloadManagerError
	if !errors.As(err, &classified) || classified.Kind != kind || classified.Retryable != retryable {
		t.Fatalf("error = %T %v, want %s retryable=%t", err, err, kind, retryable)
	}
}
