package telemetry

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

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
	m := NewPromMetrics("bloomtest")
	m.IncRequest("GET /api/v1/version", "2xx")
	m.ObserveRequest("GET /api/v1/version", "2xx", 0.01)
	m.IncLoginAttempt("success")
	m.IncCSRFRejection()
	m.IncAuditWriteFailure()
	m.IncSessionCleanupFailure()
	permission, err := core.NewCatalogPermission(core.PermissionAdminRoles)
	if err != nil {
		t.Fatalf("NewCatalogPermission: %v", err)
	}
	m.IncAuthorizationDenial(permission)

	families, err := m.Registry().Gather()
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
	} {
		if !names[want] {
			t.Errorf("metric %q not exposed", want)
		}
	}
	for _, family := range families {
		if family.GetName() != "bloomtest_authorization_denials_total" {
			continue
		}
		metrics := family.GetMetric()
		if len(metrics) != 1 || metrics[0].GetCounter().GetValue() != 1 ||
			len(metrics[0].GetLabel()) != 1 || metrics[0].GetLabel()[0].GetName() != "permission" ||
			metrics[0].GetLabel()[0].GetValue() != "admin.roles" {
			t.Errorf("authorization denial metric = %+v", metrics)
		}
	}
	if m.Handler() == nil {
		t.Error("Handler() = nil")
	}
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
		"permission": "admin.roles", "role": "owner",
	} {
		if rec[k] != want {
			t.Errorf("%s = %v, want %q", k, rec[k], want)
		}
	}
	if len(rec) != 14 {
		t.Fatalf("audit field count = %d, want 14: %v", len(rec), rec)
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
