package http

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/BonzTM/bloom/internal/buildinfo"
	"github.com/BonzTM/bloom/internal/config"
	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/httputil"
	"github.com/BonzTM/bloom/internal/telemetry"
)

// fakePinger is a hand-rolled Pinger whose outcome a test controls.
type fakePinger struct {
	mu  sync.Mutex
	err error
}

func (p *fakePinger) PingContext(context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.err
}

func (p *fakePinger) fail(err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.err = err
}

// countingMetrics records how many times IncRequest fires so a test can prove
// which routes flow through the logging+metrics middleware.
type countingMetrics struct {
	mu            sync.Mutex
	requests      int
	logins        []string
	csrf          int
	auditFailures int
	authzDenials  int
}

func (m *countingMetrics) IncLoginAttempt(outcome string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.logins = append(m.logins, outcome)
}

func (m *countingMetrics) IncRequest(string, string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.requests++
}

func (m *countingMetrics) IncCSRFRejection() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.csrf++
}

func (m *countingMetrics) IncAuditWriteFailure() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.auditFailures++
}

func (m *countingMetrics) IncAuthorizationDenial(core.CatalogPermission) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.authzDenials++
}

func (m *countingMetrics) authorizationDenialCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.authzDenials
}

func (m *countingMetrics) count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.requests
}

func (m *countingMetrics) auditFailureCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.auditFailures
}

type harness struct {
	h       http.Handler
	pinger  *fakePinger
	ready   *telemetry.Readiness
	metrics *countingMetrics
}

func newHarness(t *testing.T) harness {
	t.Helper()
	pinger := &fakePinger{}
	ready := telemetry.NewReadiness(true)
	metrics := &countingMetrics{}
	cfg := config.HTTPConfig{Addr: ":0", ReadHeaderTimeout: time.Second, MaxBodyBytes: 1 << 20}
	srv := New(cfg, Deps{
		Logger:    slog.New(slog.DiscardHandler),
		Metrics:   metrics,
		Readiness: ready,
		Pinger:    pinger,
	})
	return harness{h: srv.Handler(), pinger: pinger, ready: ready, metrics: metrics}
}

func (h harness) get(t *testing.T, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

func decodeEnvelope(t *testing.T, rec *httptest.ResponseRecorder) httputil.ErrorResponse {
	t.Helper()
	if ct := rec.Header().Get("Content-Type"); ct != httputil.ContentTypeJSON {
		t.Fatalf("Content-Type = %q, want %q", ct, httputil.ContentTypeJSON)
	}
	var env httputil.ErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("body %q is not an error envelope: %v", rec.Body.String(), err)
	}
	return env
}

func TestVersionContract(t *testing.T) {
	h := newHarness(t)
	rec := h.get(t, "/api/v1/version")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var got map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	want := map[string]string{"name": "bloom", "version": buildinfo.Version, "commit": buildinfo.Commit}
	if len(got) != len(want) {
		t.Fatalf("body = %v, want exactly %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
}

func TestAPIRouteInventoryIsCompleteAndDefaultDeny(t *testing.T) {
	want := []apiRoute{
		{method: http.MethodGet, path: "/api/v1/version", access: routePublic},
		{method: http.MethodGet, path: "/api/v1/auth/permissions", access: routePublic},
		{method: http.MethodPost, path: "/api/v1/auth/login", access: routePublic, sessions: true, authRequired: true},
		{method: http.MethodPost, path: "/api/v1/auth/logout", access: routeAuthenticated, authRequired: true},
		{method: http.MethodGet, path: "/api/v1/auth/me", access: routeAuthenticated, authRequired: true, snapshot: true},
		{method: http.MethodGet, path: "/api/v1/roles", access: routePermission, permission: core.PermissionAdminRoles, authRequired: true},
	}
	if len(apiRouteInventory) != len(want) {
		t.Fatalf("route inventory length = %d, want %d", len(apiRouteInventory), len(want))
	}
	for index, expected := range want {
		got := apiRouteInventory[index]
		if got.method != expected.method || got.path != expected.path || got.access != expected.access ||
			got.permission != expected.permission || got.sessions != expected.sessions ||
			got.authRequired != expected.authRequired || got.snapshot != expected.snapshot {
			t.Errorf("route %d = %+v, want %+v", index, got, expected)
		}
		if got.access != routePublic && !got.usesSessionAccount() {
			t.Errorf("non-public route lacks session account middleware: %+v", got)
		}
	}
}

func TestPermissionRouteRejectsUnpublishedCatalogValue(t *testing.T) {
	h := newAuthHarness(t, nil)
	route := apiRoute{
		method: http.MethodGet, path: "/api/v1/invented", access: routePermission,
		permission: "invented.permission", handler: (*Server).handleVersion,
	}
	if _, err := h.server.routeHandler(route); err == nil {
		t.Fatal("permission route accepted an unpublished permission")
	}
}

func TestSessionRouteRequiresAuthDependencies(t *testing.T) {
	h := newAuthHarness(t, nil)
	route := apiRoute{
		method: http.MethodPost, path: "/api/v1/session-public", access: routePublic,
		sessions: true, handler: (*Server).handleLogin,
	}
	if _, err := h.server.routeHandler(route); err == nil {
		t.Fatal("session route without auth dependencies was accepted")
	}
}

func TestLivez(t *testing.T) {
	h := newHarness(t)
	// Liveness must not depend on the database or the readiness flag.
	h.pinger.fail(errors.New("db down"))
	h.ready.Set(false)
	rec := h.get(t, "/livez")
	if rec.Code != http.StatusOK || rec.Body.String() != "ok" {
		t.Fatalf("/livez = %d %q, want 200 ok", rec.Code, rec.Body.String())
	}
	if !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/plain") {
		t.Errorf("Content-Type = %q, want text/plain", rec.Header().Get("Content-Type"))
	}
}

func TestReadyz(t *testing.T) {
	h := newHarness(t)

	rec := h.get(t, "/readyz")
	if rec.Code != http.StatusOK || rec.Body.String() != "ready" {
		t.Fatalf("/readyz healthy = %d %q, want 200 ready", rec.Code, rec.Body.String())
	}

	h.pinger.fail(errors.New("connection refused"))
	rec = h.get(t, "/readyz")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("/readyz with failing ping = %d, want 503", rec.Code)
	}
	env := decodeEnvelope(t, rec)
	if env.Code != codeUnavailable {
		t.Errorf("code = %q, want %q", env.Code, codeUnavailable)
	}
	if strings.Contains(env.Message, "connection refused") {
		t.Errorf("5xx message leaks internal detail: %q", env.Message)
	}
	if env.RequestID == "" {
		t.Error("envelope is missing request_id")
	}

	h.pinger.fail(nil)
	h.ready.Set(false)
	rec = h.get(t, "/readyz")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("/readyz with readiness down = %d, want 503", rec.Code)
	}
}

func TestUnknownRoutesReturnJSONEnvelope(t *testing.T) {
	h := newHarness(t)
	for _, path := range []string{"/api/v1/nope", "/api/", "/api/v2/version", "/nothing-here"} {
		rec := h.get(t, path)
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s status = %d, want 404", path, rec.Code)
		}
		env := decodeEnvelope(t, rec)
		if env.Code != codeNotFound {
			t.Errorf("%s code = %q, want %q", path, env.Code, codeNotFound)
		}
		if strings.Contains(strings.ToLower(rec.Body.String()), "<html") {
			t.Errorf("%s returned HTML: %s", path, rec.Body.String())
		}
	}
}

func TestSecurityHeadersAndRequestID(t *testing.T) {
	h := newHarness(t)
	rec := h.get(t, "/api/v1/version")
	for header, want := range map[string]string{
		"X-Content-Type-Options":  "nosniff",
		"X-Frame-Options":         "DENY",
		"Referrer-Policy":         "strict-origin-when-cross-origin",
		"Content-Security-Policy": "default-src 'none'; frame-ancestors 'none'",
		"Cache-Control":           "no-store",
	} {
		if got := rec.Header().Get(header); got != want {
			t.Errorf("%s = %q, want %q", header, got, want)
		}
	}
	if rec.Header().Get("X-Request-ID") == "" {
		t.Error("X-Request-ID not set on the response")
	}

	// An inbound id is echoed; an oversized one is replaced.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/version", nil)
	req.Header.Set("X-Request-ID", "client-abc")
	rec = httptest.NewRecorder()
	h.h.ServeHTTP(rec, req)
	if got := rec.Header().Get("X-Request-ID"); got != "client-abc" {
		t.Errorf("echoed X-Request-ID = %q, want client-abc", got)
	}
	req.Header.Set("X-Request-ID", strings.Repeat("z", maxRequestIDLen+1))
	rec = httptest.NewRecorder()
	h.h.ServeHTTP(rec, req)
	if got := rec.Header().Get("X-Request-ID"); strings.HasPrefix(got, "zzz") {
		t.Errorf("oversized X-Request-ID was echoed: %q", got)
	}
}

// TestProbesNotMetered proves /livez and /readyz are mounted ahead of the
// logging+metrics middleware, so polling them does NOT increment the request
// metric, while an API call DOES.
func TestProbesNotMetered(t *testing.T) {
	h := newHarness(t)
	for range 5 {
		h.get(t, "/livez")
		h.get(t, "/readyz")
	}
	if got := h.metrics.count(); got != 0 {
		t.Fatalf("probes were metered: IncRequest called %d times, want 0", got)
	}
	h.get(t, "/api/v1/version")
	if got := h.metrics.count(); got != 1 {
		t.Fatalf("API request metering: IncRequest called %d times, want 1", got)
	}
}

func TestMetricsMountedWithPrometheus(t *testing.T) {
	srv := New(config.HTTPConfig{Addr: ":0", ReadHeaderTimeout: time.Second, MaxBodyBytes: 1}, Deps{
		Logger:    slog.New(slog.DiscardHandler),
		Metrics:   telemetry.NewPromMetrics("bloomtest"),
		Readiness: telemetry.NewReadiness(true),
		Pinger:    &fakePinger{},
	})
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("/metrics = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "go_goroutines") {
		t.Error("/metrics body lacks Go runtime collectors")
	}
}

func TestRecoverMiddlewareReturnsEnvelope(t *testing.T) {
	h := recoverMiddleware(slog.New(slog.DiscardHandler))(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("boom")
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/x", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	env := decodeEnvelope(t, rec)
	if env.Code != codeInternal || strings.Contains(rec.Body.String(), "boom") {
		t.Errorf("envelope = %+v (body %s), want opaque internal", env, rec.Body.String())
	}
}

func TestStatusClass(t *testing.T) {
	for status, want := range map[int]string{100: "1xx", 204: "2xx", 302: "3xx", 404: "4xx", 503: "5xx"} {
		if got := statusClass(status); got != want {
			t.Errorf("statusClass(%d) = %q, want %q", status, got, want)
		}
	}
}
