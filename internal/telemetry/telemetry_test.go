package telemetry

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/BonzTM/bloom/internal/config"
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

	families, err := m.Registry().Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	names := make(map[string]bool, len(families))
	for _, f := range families {
		names[f.GetName()] = true
	}
	for _, want := range []string{"bloomtest_http_requests_total", "bloomtest_http_request_duration_seconds", "go_goroutines"} {
		if !names[want] {
			t.Errorf("metric %q not exposed", want)
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
	a.Emit(context.Background(), AuditEvent{
		Actor: "acct-1", Action: "auth.login", Resource: "local", Result: AuditSuccess, RequestID: "req-1",
	})

	var rec map[string]any
	if err := json.Unmarshal([]byte(out.String()), &rec); err != nil {
		t.Fatalf("audit output %q is not JSON: %v", out.String(), err)
	}
	for k, want := range map[string]string{
		"log_type": "audit", "actor": "acct-1", "action": "auth.login", "resource": "local",
		"result": "success", "request_id": "req-1", "time": "2026-09-22T11:00:00Z",
	} {
		if rec[k] != want {
			t.Errorf("%s = %v, want %q", k, rec[k], want)
		}
	}
}

func TestNopAuditLoggerIsSafe(t *testing.T) {
	NopAuditLogger().Emit(context.Background(), AuditEvent{Action: "noop", Result: AuditDenied})
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
