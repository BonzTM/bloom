package telemetry

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/BonzTM/bloom/internal/config"
	"github.com/BonzTM/bloom/internal/core"
)

func TestNewLoggerFormatAndLevel(t *testing.T) {
	var jsonOut, textOut strings.Builder

	jl := NewLogger(&jsonOut, config.TelemetryConfig{LogLevel: slog.LevelInfo, LogFormat: config.LogFormatJSON})
	jl.Debug("hidden")
	jl.Info("shown", "k", "v")
	var rec map[string]any
	if err := json.Unmarshal([]byte(jsonOut.String()), &rec); err != nil {
		t.Fatalf("json logger output %q is not JSON: %v", jsonOut.String(), err)
	}
	if rec["msg"] != "shown" || rec["k"] != "v" {
		t.Errorf("json record = %v", rec)
	}
	if strings.Contains(jsonOut.String(), "hidden") {
		t.Error("debug record emitted at info level")
	}

	tl := NewLogger(&textOut, config.TelemetryConfig{LogLevel: slog.LevelDebug, LogFormat: config.LogFormatText})
	tl.Debug("visible")
	if !strings.Contains(textOut.String(), "msg=visible") {
		t.Errorf("text logger output = %q, want msg=visible", textOut.String())
	}
}

func TestReadiness(t *testing.T) {
	r := NewReadiness(false)
	if r.Ready() {
		t.Fatal("NewReadiness(false).Ready() = true")
	}
	r.Set(true)
	if !r.Ready() {
		t.Fatal("after Set(true): Ready() = false")
	}
	var zero Readiness
	if zero.Ready() {
		t.Fatal("zero Readiness is ready, want not ready")
	}
}

func TestPromMetricsRecordsAndExposes(t *testing.T) {
	m := populatedPromMetrics(t)
	assertPromMetricNames(t, m)
	assertPromMetricLabels(t, m)
	if m.Handler() == nil {
		t.Error("Handler() = nil")
	}
}

func TestPromMetricsBoundsLoginAttemptLabels(t *testing.T) {
	t.Parallel()
	metrics := NewPromMetrics("bounded")
	metrics.IncLoginAttempt("arbitrary-provider", "arbitrary-outcome")
	families, err := metrics.Registry().Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	for _, family := range families {
		if family.GetName() != "bounded_login_attempts_total" {
			continue
		}
		labels := family.GetMetric()[0].GetLabel()
		if len(labels) != 2 || labels[0].GetValue() != "invalid" || labels[1].GetValue() != "invalid" {
			t.Fatalf("login attempt labels = %+v, want invalid provider and outcome", labels)
		}
		return
	}
	t.Fatal("login attempt metric was not gathered")
}

func populatedPromMetrics(t *testing.T) *PromMetrics {
	t.Helper()
	m := NewPromMetrics("bloomtest")
	m.IncRequest("GET /api/v1/version", "2xx")
	m.ObserveRequest("GET /api/v1/version", "2xx", 0.01)
	m.IncLoginAttempt("local", "success")
	m.IncCSRFRejection()
	m.IncAuditWriteFailure()
	m.IncSessionCleanupFailure()
	permission, err := core.NewCatalogPermission(core.PermissionAdminRoles)
	if err != nil {
		t.Fatalf("NewCatalogPermission: %v", err)
	}
	m.IncAuthorizationDenial(permission)
	m.ObserveMediaServerRequest("jellyfin", "probe", "success", 0.02)
	m.IncInviteCreation("success")
	m.IncInviteAcceptance("accepted")
	for _, outcome := range []string{"scheduled", "exhausted", "budget_exhausted"} {
		m.ObserveMediaServerRetry("jellyfin", "probe", outcome)
	}
	m.ObserveOIDCDependency("discovery", "request_success", 0.02)
	m.ObserveOIDCDependency("jwks", "retry", 0)
	m.ObservePlaybackPoll("jellyfin", "success", 0.03)
	m.IncPlaybackRefreshFailure()
	m.IncLibraryResolution("11111111-1111-4111-8111-111111111111", "resolved")
	m.ObserveStatsQuery("overview", "success", 0.04)
	m.SetOpenWatches("jellyfin", "server-1", 2)
	m.SetOpenWatches("jellyfin", "server-2", 3)
	m.IncWatchesClosed("jellyfin", "timeout")
	return m
}

func assertPromMetricNames(t *testing.T, metrics *PromMetrics) {
	t.Helper()
	families, err := metrics.Registry().Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	names := make(map[string]bool, len(families))
	for _, f := range families {
		names[f.GetName()] = true
	}
	for _, want := range []string{
		"bloomtest_http_requests_total", "bloomtest_http_request_duration_seconds",
		"bloomtest_login_attempts_total", "bloomtest_csrf_rejections_total",
		"bloomtest_audit_write_failures_total",
		"bloomtest_session_cleanup_failures_total", "go_goroutines",
		"bloomtest_authorization_denials_total",
		"bloomtest_media_server_requests_total", "bloomtest_media_server_request_duration_seconds",
		"bloomtest_media_server_retries_total",
		"bloomtest_oidc_dependency_events_total", "bloomtest_oidc_dependency_duration_seconds",
		"bloomtest_invite_creations_total", "bloomtest_invite_acceptances_total",
		"bloomtest_playback_polls_total", "bloomtest_playback_poll_duration_seconds",
		"bloomtest_playback_open_watches", "bloomtest_playback_watches_closed_total",
		"bloomtest_playback_refresh_failures_total",
		"bloomtest_playback_library_resolutions_total",
		"bloomtest_stats_query_duration_seconds",
	} {
		if !names[want] {
			t.Errorf("metric %q not exposed", want)
		}
	}
}

func TestPromMetricsAggregatesPlaybackGaugeByKind(t *testing.T) {
	metrics := NewPromMetrics("playbacktest")
	metrics.SetOpenWatches("jellyfin", "server-1", 2)
	metrics.SetOpenWatches("jellyfin", "server-2", 3)
	assertGaugeValue(t, metrics.Registry(), "playbacktest_playback_open_watches", 5)
	metrics.SetOpenWatches("jellyfin", "server-1", 0)
	assertGaugeValue(t, metrics.Registry(), "playbacktest_playback_open_watches", 3)
}

func TestPromMetricsPublishesConcurrentPlaybackGaugeInUpdateOrder(t *testing.T) {
	metrics := NewPromMetrics("playbackconcurrent")
	start := make(chan struct{})
	var workers sync.WaitGroup
	const servers = 128
	workers.Add(servers)
	for index := range servers {
		go func() {
			defer workers.Done()
			<-start
			metrics.SetOpenWatches("jellyfin", strconv.Itoa(index), 1)
			metrics.SetOpenWatches("jellyfin", strconv.Itoa(index), 2)
		}()
	}
	close(start)
	workers.Wait()
	assertGaugeValue(t, metrics.Registry(), "playbackconcurrent_playback_open_watches", 2*servers)
	metrics.IncWatchesClosed("jellyfin", "overflow")
}

func assertGaugeValue(t *testing.T, gatherer prometheus.Gatherer, name string, want float64) {
	t.Helper()
	families, err := gatherer.Gather()
	if err != nil {
		t.Fatalf("gather metrics: %v", err)
	}
	for _, family := range families {
		if family.GetName() == name {
			got := family.GetMetric()[0].GetGauge().GetValue()
			if got != want {
				t.Fatalf("%s = %f, want %f", name, got, want)
			}
			return
		}
	}
	t.Fatal("playback gauge was not gathered")
}

func assertPromMetricLabels(t *testing.T, metrics *PromMetrics) {
	t.Helper()
	families, err := metrics.Registry().Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	loginLabels := make(map[string]string)
	for _, family := range families {
		if family.GetName() == "bloomtest_login_attempts_total" {
			for _, label := range family.GetMetric()[0].GetLabel() {
				loginLabels[label.GetName()] = label.GetValue()
			}
		}
		if family.GetName() == "bloomtest_authorization_denials_total" {
			values := family.GetMetric()
			if len(values) != 1 || values[0].GetCounter().GetValue() != 1 ||
				len(values[0].GetLabel()) != 1 || values[0].GetLabel()[0].GetName() != "permission" ||
				values[0].GetLabel()[0].GetValue() != "admin.roles" {
				t.Errorf("authorization denial metric = %+v", values)
			}
		}
	}
	assertRetryMetricOutcomes(t, metrics.Registry())
	if loginLabels["provider"] != "local" || loginLabels["outcome"] != "success" {
		t.Errorf("login metric labels = %v", loginLabels)
	}
}

func assertRetryMetricOutcomes(t *testing.T, gatherer prometheus.Gatherer) {
	t.Helper()
	families, err := gatherer.Gather()
	if err != nil {
		t.Fatalf("gather retry metrics: %v", err)
	}
	for _, family := range families {
		if family.GetName() != "bloomtest_media_server_retries_total" {
			continue
		}
		outcomes := make([]string, 0, len(family.GetMetric()))
		for _, metric := range family.GetMetric() {
			labels := make(map[string]string, len(metric.GetLabel()))
			for _, label := range metric.GetLabel() {
				labels[label.GetName()] = label.GetValue()
				if label.GetName() == "outcome" {
					outcomes = append(outcomes, label.GetValue())
				}
			}
			if labels["kind"] != "jellyfin" || labels["operation"] != "probe" ||
				metric.GetCounter().GetValue() != 1 {
				t.Errorf("retry metric = %+v", metric)
			}
		}
		slices.Sort(outcomes)
		want := []string{"budget_exhausted", "exhausted", "scheduled"}
		if !slices.Equal(outcomes, want) {
			t.Fatalf("retry metric outcomes = %v, want %v", outcomes, want)
		}
		return
	}
	t.Fatal("retry metric family not gathered")
}

type fixedClock struct{ t time.Time }

func (c fixedClock) Now() time.Time { return c.t }

func TestAuditLoggerSchema(t *testing.T) {
	var out strings.Builder
	now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.FixedZone("plus1", 3600))
	a := NewAuditLogger(&out, fixedClock{t: now})
	if err := a.Emit(context.Background(), AuditEvent{
		Actor: "acct-1", Action: "auth.login", Resource: "account:acct-1", Result: AuditSuccess,
		Reason: "authenticated", Source: "192.0.2.1", RequestID: "req-1", SubjectID: "username:opaque",
		Permission: "admin.roles", Role: "owner",
	}); err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if got := strings.Count(out.String(), `"time":`); got != 1 {
		t.Fatalf("audit time keys = %d, want exactly 1: %s", got, out.String())
	}

	var rec map[string]any
	if err := json.Unmarshal([]byte(out.String()), &rec); err != nil {
		t.Fatalf("audit output %q is not JSON: %v", out.String(), err)
	}
	for k, want := range map[string]string{
		"log_type": "audit", "actor": "acct-1", "action": "auth.login", "resource": "account:acct-1",
		"result": "success", "request_id": "req-1", "time": "2026-09-22T11:00:00Z",
		"reason": "authenticated", "source": "192.0.2.1", "subject_id": "username:opaque",
		"permission": "admin.roles", "role": "owner", "kind": "", "title": "", "provider": "",
	} {
		if rec[k] != want {
			t.Errorf("%s = %v, want %q", k, rec[k], want)
		}
	}
	if len(rec) != 18 {
		t.Fatalf("audit field count = %d, want 18: %v", len(rec), rec)
	}
}

func TestRoleAssignmentAuditEvent(t *testing.T) {
	tests := []struct {
		name      string
		accountID string
		resource  string
	}{
		{name: "resolved account", accountID: "acct-1", resource: "account:acct-1"},
		{name: "unresolved account", resource: "account:unresolved"},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			got := RoleAssignmentAuditEvent("cli", testCase.accountID, "owner", AuditSuccess, "assigned", "cli")
			want := AuditEvent{
				Actor: "cli", Action: AuditActionRoleAssign, Resource: testCase.resource,
				Role: "owner", Result: AuditSuccess, Reason: "assigned", Source: "cli",
			}
			if got != want {
				t.Fatalf("RoleAssignmentAuditEvent = %+v, want %+v", got, want)
			}
		})
	}
}

func TestNopAuditLoggerIsSafe(t *testing.T) {
	if err := NopAuditLogger().Emit(context.Background(), AuditEvent{Action: "noop", Result: AuditDenied}); err != nil {
		t.Fatalf("Emit: %v", err)
	}
}

type failingWriter struct{ err error }

func (w failingWriter) Write([]byte) (int, error) { return 0, w.err }

func TestAuditLoggerReturnsSinkFailure(t *testing.T) {
	want := errors.New("audit sink unavailable")
	a := NewAuditLogger(failingWriter{err: want}, fixedClock{t: time.Now()})
	if err := a.Emit(context.Background(), AuditEvent{Actor: "anonymous", Action: "auth.login"}); !errors.Is(err, want) {
		t.Fatalf("Emit error = %v, want %v", err, want)
	}
}

func TestTracerProviderOfflineAndShutdown(t *testing.T) {
	tp, err := NewTracerProvider(context.Background(), config.TelemetryConfig{}, "bloom", "test")
	if err != nil {
		t.Fatalf("NewTracerProvider (offline): %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := tp.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
}
