package http

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/BonzTM/bloom/internal/buildinfo"
	"github.com/BonzTM/bloom/internal/config"
	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/httputil"
	"github.com/BonzTM/bloom/internal/telemetry"
)

func TestPublicInviteSpansNeverContainCode(t *testing.T) {
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

	h := newAuthHarness(t, nil)
	paths := []struct {
		method string
		path   string
		body   string
	}{
		{method: http.MethodGet, path: "/api/v1/invite/" + testInviteCode},
		{
			method: http.MethodPost, path: "/api/v1/invite/" + testInviteCode + "/accept",
			body: `{"username":"new-user","password":"Th1s-is-a-unique-password!"}`,
		},
	}
	for _, request := range paths {
		recorder := h.requestWithContentType(t, request.method, request.path, request.body, nil, "application/json")
		if recorder.Code >= http.StatusBadRequest {
			t.Fatalf("%s %s = %d: %s", request.method, request.path, recorder.Code, recorder.Body.String())
		}
	}
	spans := exporter.GetSpans()
	if len(spans) != len(paths) {
		t.Fatalf("exported spans = %d, want %d", len(spans), len(paths))
	}
	if rendered := fmt.Sprintf("%+v", spans); strings.Contains(rendered, testInviteCode) {
		t.Fatalf("exported span data contains invite code: %s", rendered)
	}
	for _, span := range spans {
		if !strings.Contains(span.Name, "{code}") {
			t.Errorf("span name = %q, want sanitized route pattern", span.Name)
		}
		for _, attribute := range span.Attributes {
			if string(attribute.Key) != "http.route" {
				t.Errorf("span %q attribute = %q, want only http.route", span.Name, attribute.Key)
			}
		}
		if len(span.Events) != 0 || len(span.Links) != 0 {
			t.Errorf("span %q events=%v links=%v, want none", span.Name, span.Events, span.Links)
		}
	}
}

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
	mu             sync.Mutex
	requests       int
	logins         []string
	loginProviders []string
	csrf           int
	auditFailures  int
	authzDenials   int
	inviteCreates  []string
	inviteAccepts  []string
}

func (m *countingMetrics) IncLoginAttempt(provider, outcome string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.logins = append(m.logins, outcome)
	m.loginProviders = append(m.loginProviders, provider)
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

func (m *countingMetrics) IncInviteCreation(outcome string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.inviteCreates = append(m.inviteCreates, outcome)
}

func (m *countingMetrics) IncInviteAcceptance(outcome string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.inviteAccepts = append(m.inviteAccepts, outcome)
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

func expectedAPIRoutes() []apiRoute {
	routes := make([]apiRoute, 0, len(apiRouteInventory))
	routes = append(routes, []apiRoute{
		{method: http.MethodGet, path: "/api/v1/version", access: routePublic},
		{method: http.MethodGet, path: "/api/v1/auth/permissions", access: routePublic},
		{method: http.MethodGet, path: "/api/v1/auth/providers", access: routePublic},
		{method: http.MethodPost, path: "/api/v1/auth/oidc/start", access: routePublic, sessions: true, authRequired: true, oidc: true},
		{method: http.MethodGet, path: "/api/v1/auth/oidc/callback", access: routePublic, sessions: true, authRequired: true, oidc: true},
		{method: http.MethodPost, path: "/api/v1/auth/login", access: routePublic, sessions: true, authRequired: true},
		{method: http.MethodPost, path: "/api/v1/auth/logout", access: routeAuthenticated, authRequired: true},
		{method: http.MethodGet, path: "/api/v1/auth/me", access: routeAuthenticated, authRequired: true, snapshot: true},
		{method: http.MethodGet, path: "/api/v1/roles", access: routePermission, permission: core.PermissionAdminRoles, authRequired: true},
		{method: http.MethodPost, path: "/api/v1/media-servers", access: routePermission, permission: core.PermissionAdminSettings, authRequired: true},
		{method: http.MethodGet, path: "/api/v1/media-servers", access: routePermission, permission: core.PermissionAdminSettings, authRequired: true},
		{method: http.MethodGet, path: "/api/v1/media-servers/{id}", access: routePermission, permission: core.PermissionAdminSettings, authRequired: true},
		{method: http.MethodPost, path: "/api/v1/media-servers/{id}/probe", access: routePermission, permission: core.PermissionAdminSettings, authRequired: true},
		{method: http.MethodGet, path: "/api/v1/media-servers/{id}/libraries", access: routePermission, permission: core.PermissionAdminSettings, authRequired: true},
		{method: http.MethodDelete, path: "/api/v1/media-servers/{id}", access: routePermission, permission: core.PermissionAdminSettings, authRequired: true},
		{method: http.MethodPost, path: "/api/v1/download-managers", access: routePermission, permission: core.PermissionAdminSettings, authRequired: true},
		{method: http.MethodGet, path: "/api/v1/download-managers", access: routePermission, permission: core.PermissionAdminSettings, authRequired: true},
		{method: http.MethodDelete, path: "/api/v1/download-managers/{id}", access: routePermission, permission: core.PermissionAdminSettings, authRequired: true},
		{method: http.MethodGet, path: "/api/v1/download-managers/{id}/options", access: routePermission, permission: core.PermissionAdminSettings, authRequired: true},
		{method: http.MethodPost, path: "/api/v1/notification-channels", access: routePermission, permission: core.PermissionAdminSettings, authRequired: true},
		{method: http.MethodGet, path: "/api/v1/notification-channels", access: routePermission, permission: core.PermissionAdminSettings, authRequired: true},
		{method: http.MethodGet, path: "/api/v1/notification-channels/{id}", access: routePermission, permission: core.PermissionAdminSettings, authRequired: true},
		{method: http.MethodPut, path: "/api/v1/notification-channels/{id}", access: routePermission, permission: core.PermissionAdminSettings, authRequired: true},
		{method: http.MethodDelete, path: "/api/v1/notification-channels/{id}", access: routePermission, permission: core.PermissionAdminSettings, authRequired: true},
		{method: http.MethodPost, path: "/api/v1/notification-channels/{id}/test", access: routePermission, permission: core.PermissionAdminSettings, authRequired: true},
		{method: http.MethodGet, path: "/api/v1/notification-channels/{id}/deliveries", access: routePermission, permission: core.PermissionAdminSettings, authRequired: true},
		{method: http.MethodPost, path: "/api/v1/invites", access: routePermission, permission: core.PermissionUsersInvite, authRequired: true},
		{method: http.MethodGet, path: "/api/v1/invites", access: routePermission, permission: core.PermissionUsersInvite, authRequired: true},
		{method: http.MethodGet, path: "/api/v1/invites/servers", access: routePermission, permission: core.PermissionUsersInvite, authRequired: true},
		{method: http.MethodGet, path: "/api/v1/invites/provisioning-failures", access: routePermission, permission: core.PermissionAdminSettings, authRequired: true},
		{method: http.MethodDelete, path: "/api/v1/invites/provisioning-failures/{id}", access: routePermission, permission: core.PermissionAdminSettings, authRequired: true},
		{method: http.MethodGet, path: "/api/v1/invites/{id}", access: routePermission, permission: core.PermissionUsersInvite, authRequired: true},
		{method: http.MethodDelete, path: "/api/v1/invites/{id}", access: routePermission, permission: core.PermissionUsersInvite, authRequired: true},
		{method: http.MethodGet, path: "/api/v1/invite/{code}", access: routePublic},
		{method: http.MethodPost, path: "/api/v1/invite/{code}/accept", access: routePublic, sessions: true, authRequired: true, optionalAccount: true},
		{method: http.MethodGet, path: "/api/v1/playback/now", access: routePermission, permission: core.PermissionStatsReadAll, authRequired: true},
		{method: http.MethodGet, path: "/api/v1/playback/history", access: routePermission, permission: core.PermissionStatsReadAll, authRequired: true},
		{method: http.MethodGet, path: "/api/v1/playback/watches/{id}/positions", access: routePermission, permission: core.PermissionStatsReadAll, authRequired: true},
		{method: http.MethodGet, path: "/api/v1/stats/overview", access: routePermission, permission: core.PermissionStatsReadAll, authRequired: true},
		{method: http.MethodGet, path: "/api/v1/stats/daily", access: routePermission, permission: core.PermissionStatsReadAll, authRequired: true},
		{method: http.MethodGet, path: "/api/v1/stats/patterns", access: routePermission, permission: core.PermissionStatsReadAll, authRequired: true},
		{method: http.MethodGet, path: "/api/v1/stats/titles", access: routePermission, permission: core.PermissionStatsReadAll, authRequired: true},
		{method: http.MethodGet, path: "/api/v1/stats/users", access: routePermission, permission: core.PermissionStatsReadAll, authRequired: true},
		{method: http.MethodGet, path: "/api/v1/stats/libraries", access: routePermission, permission: core.PermissionStatsReadAll, authRequired: true},
		{method: http.MethodGet, path: "/api/v1/stats/users/{media_server_id}/{media_user_id}", access: routePermission, permission: core.PermissionStatsReadAll, authRequired: true},
		{method: http.MethodGet, path: "/api/v1/stats/me", access: routePermission, permission: core.PermissionStatsReadOwn, authRequired: true},
		{method: http.MethodGet, path: "/api/v1/me/media-users", access: routePermission, permission: core.PermissionStatsReadOwn, authRequired: true},
		{method: http.MethodGet, path: "/api/v1/accounts/{id}/media-users", access: routePermission, permission: core.PermissionAdminSettings, authRequired: true},
		{method: http.MethodPut, path: "/api/v1/accounts/{id}/media-users/{media_server_id}", access: routePermission, permission: core.PermissionAdminSettings, authRequired: true},
		{method: http.MethodDelete, path: "/api/v1/accounts/{id}/media-users/{media_server_id}", access: routePermission, permission: core.PermissionAdminSettings, authRequired: true},
	}...)
	return append(routes, expectedRequestAPIRoutes()...)
}

func expectedRequestAPIRoutes() []apiRoute {
	return []apiRoute{
		{method: http.MethodGet, path: "/api/v1/metadata/search", access: routePermission, permission: core.PermissionRequestsCreate, authRequired: true},
		{method: http.MethodGet, path: "/api/v1/metadata/movies/{id}", access: routePermission, permission: core.PermissionRequestsCreate, authRequired: true},
		{method: http.MethodGet, path: "/api/v1/metadata/series/{id}", access: routePermission, permission: core.PermissionRequestsCreate, authRequired: true},
		{method: http.MethodGet, path: "/api/v1/metadata/providers/tmdb/key", access: routePermission, permission: core.PermissionAdminSettings, authRequired: true},
		{method: http.MethodPut, path: "/api/v1/metadata/providers/tmdb/key", access: routePermission, permission: core.PermissionAdminSettings, authRequired: true},
		{method: http.MethodDelete, path: "/api/v1/metadata/providers/tmdb/key", access: routePermission, permission: core.PermissionAdminSettings, authRequired: true},
		{method: http.MethodPost, path: "/api/v1/request-profiles", access: routePermission, permission: core.PermissionAdminSettings, authRequired: true},
		{method: http.MethodGet, path: "/api/v1/request-profiles", access: routePermission, anyPermissions: []core.Permission{core.PermissionRequestsCreate, core.PermissionAdminSettings}, authRequired: true},
		{method: http.MethodPut, path: "/api/v1/request-profiles/{id}", access: routePermission, permission: core.PermissionAdminSettings, authRequired: true},
		{method: http.MethodDelete, path: "/api/v1/request-profiles/{id}", access: routePermission, permission: core.PermissionAdminSettings, authRequired: true},
		{method: http.MethodPost, path: "/api/v1/requests", access: routePermission, permission: core.PermissionRequestsCreate, authRequired: true},
		{method: http.MethodGet, path: "/api/v1/requests", access: routePermission, anyPermissions: []core.Permission{core.PermissionRequestsReadOwn, core.PermissionRequestsApprove}, authRequired: true},
		{method: http.MethodGet, path: "/api/v1/requests/{id}", access: routePermission, anyPermissions: []core.Permission{core.PermissionRequestsReadOwn, core.PermissionRequestsApprove}, authRequired: true},
		{method: http.MethodGet, path: "/api/v1/requests/{id}/progress", access: routePermission, anyPermissions: []core.Permission{core.PermissionRequestsReadOwn, core.PermissionRequestsApprove}, authRequired: true},
		{method: http.MethodPost, path: "/api/v1/requests/{id}/approve", access: routePermission, permission: core.PermissionRequestsApprove, authRequired: true},
		{method: http.MethodPost, path: "/api/v1/requests/{id}/decline", access: routePermission, permission: core.PermissionRequestsApprove, authRequired: true},
		{method: http.MethodGet, path: "/api/v1/roles/{id}/request-quota", access: routePermission, permission: core.PermissionAdminRoles, authRequired: true},
		{method: http.MethodPut, path: "/api/v1/roles/{id}/request-quota", access: routePermission, permission: core.PermissionAdminRoles, authRequired: true},
		{method: http.MethodDelete, path: "/api/v1/roles/{id}/request-quota", access: routePermission, permission: core.PermissionAdminRoles, authRequired: true},
		{method: http.MethodGet, path: "/api/v1/accounts/{id}/request-quota", access: routePermission, permission: core.PermissionAdminSettings, authRequired: true},
		{method: http.MethodPut, path: "/api/v1/accounts/{id}/request-quota", access: routePermission, permission: core.PermissionAdminSettings, authRequired: true},
		{method: http.MethodDelete, path: "/api/v1/accounts/{id}/request-quota", access: routePermission, permission: core.PermissionAdminSettings, authRequired: true},
	}
}

func TestAPIRouteInventoryIsCompleteAndDefaultDeny(t *testing.T) {
	want := expectedAPIRoutes()
	if len(apiRouteInventory) != len(want) {
		t.Fatalf("route inventory length = %d, want %d", len(apiRouteInventory), len(want))
	}
	for index, expected := range want {
		got := apiRouteInventory[index]
		if got.method != expected.method || got.path != expected.path || got.access != expected.access ||
			got.permission != expected.permission || !slices.Equal(got.anyPermissions, expected.anyPermissions) || got.sessions != expected.sessions ||
			got.authRequired != expected.authRequired || got.snapshot != expected.snapshot || got.oidc != expected.oidc ||
			got.optionalAccount != expected.optionalAccount {
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
