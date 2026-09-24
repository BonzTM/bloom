package httpx

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/BonzTM/bloom/internal/core"
)

func TestPostClassifiesBoundedHTTPResponses(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		status    int
		body      string
		retryable bool
		kind      core.NotificationFailureKind
	}{
		{name: "success", status: http.StatusNoContent},
		{name: "client rejection", status: http.StatusBadRequest, kind: core.NotificationRejected},
		{name: "unauthorized", status: http.StatusUnauthorized, kind: core.NotificationUnauthorized},
		{name: "request timeout", status: http.StatusRequestTimeout, retryable: true, kind: core.NotificationUnavailable},
		{name: "rate limited", status: http.StatusTooManyRequests, retryable: true, kind: core.NotificationUnavailable},
		{name: "server error", status: http.StatusInternalServerError, retryable: true, kind: core.NotificationUnavailable},
		{name: "gateway error", status: http.StatusBadGateway, retryable: true, kind: core.NotificationUnavailable},
		{name: "nonstandard server error", status: 599, retryable: true, kind: core.NotificationUnavailable},
		{name: "oversized", status: http.StatusOK, body: strings.Repeat("x", maxResponseBytes+1), kind: core.NotificationMalformed},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(testCase.status)
				if _, err := w.Write([]byte(testCase.body)); err != nil {
					t.Error(err)
				}
			}))
			defer server.Close()
			serverURL, httpClient := externalTestServer(t, server)
			client, err := New(Config{URL: serverURL, AllowInsecure: true, AllowPrivate: true, HTTPClient: httpClient})
			if err != nil {
				t.Fatal(err)
			}
			err = client.Post(t.Context(), []byte(`{}`), nil)
			if testCase.name == "success" && err != nil {
				t.Fatalf("Post: %v", err)
			}
			if testCase.name != "success" {
				var classified *core.NotificationError
				if !errors.As(err, &classified) || classified.Retryable != testCase.retryable || classified.Kind != testCase.kind {
					t.Fatalf("Post error = %#v", err)
				}
			}
		})
	}
}

func TestPostRefusesRedirectAndTimesOut(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redirect" {
			http.Redirect(w, r, "/final", http.StatusFound)
			return
		}
		if r.URL.Path == "/slow" {
			<-r.Context().Done()
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	serverURL, httpClient := externalTestServer(t, server)
	redirect, err := New(Config{URL: serverURL + "/redirect", AllowInsecure: true, AllowPrivate: true, HTTPClient: httpClient})
	if err != nil {
		t.Fatal(err)
	}
	if postErr := redirect.Post(t.Context(), nil, nil); postErr == nil {
		t.Fatal("redirect was accepted")
	}
	slow, err := New(Config{
		URL: serverURL + "/slow", AllowInsecure: true, AllowPrivate: true,
		Timeout: 20 * time.Millisecond, HTTPClient: httpClient,
	})
	if err != nil {
		t.Fatal(err)
	}
	var classified *core.NotificationError
	if err := slow.Post(context.Background(), nil, nil); !errors.As(err, &classified) || !classified.Retryable {
		t.Fatalf("timeout error = %#v", err)
	}
}

func externalTestServer(t *testing.T, server *httptest.Server) (string, *http.Client) {
	t.Helper()
	_, port, err := net.SplitHostPort(server.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	base, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		t.Fatal("default HTTP transport has unexpected type")
	}
	transport := base.Clone()
	transport.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
	}
	return "http://example.test:" + port, &http.Client{Transport: transport}
}

func TestValidateURLDeniesPrivateAndMetadataDestinations(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{"http://127.0.0.1/hook", "http://[::1]/hook"} {
		if _, err := ValidateURL(raw, true, true); !errors.Is(err, core.ErrInvalidArgument) {
			t.Fatalf("loopback URL %q error = %v", raw, err)
		}
	}
	if _, err := ValidateURL("http://169.254.169.254/hook", true, true); !errors.Is(err, core.ErrInvalidArgument) {
		t.Fatalf("metadata URL error = %v", err)
	}
}

type fixedResolver map[string][]netip.Addr

func (r fixedResolver) LookupNetIP(_ context.Context, _, host string) ([]netip.Addr, error) {
	return r[host], nil
}

type recordingDialer struct{ calls int }

func (d *recordingDialer) DialContext(context.Context, string, string) (net.Conn, error) {
	d.calls++
	return nil, nil
}

func TestSafeDialContextAppliesPrivateExceptionWithoutAllowingLoopback(t *testing.T) {
	t.Parallel()
	resolver := fixedResolver{
		"loopback.test": {netip.MustParseAddr("127.0.0.1")},
		"private.test":  {netip.MustParseAddr("10.0.0.5")},
	}
	for _, allowPrivate := range []bool{false, true} {
		dialer := &recordingDialer{}
		_, err := SafeDialContext(resolver, dialer, allowPrivate)(t.Context(), "tcp", "loopback.test:443")
		if err == nil || dialer.calls != 0 {
			t.Fatalf("loopback allow_private=%v: error=%v calls=%d", allowPrivate, err, dialer.calls)
		}
	}
	denied := &recordingDialer{}
	if _, err := SafeDialContext(resolver, denied, false)(t.Context(), "tcp", "private.test:443"); err == nil || denied.calls != 0 {
		t.Fatalf("private denied: error=%v calls=%d", err, denied.calls)
	}
	allowed := &recordingDialer{}
	if _, err := SafeDialContext(resolver, allowed, true)(t.Context(), "tcp", "private.test:443"); err != nil || allowed.calls != 1 {
		t.Fatalf("private allowed: error=%v calls=%d", err, allowed.calls)
	}
}
