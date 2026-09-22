// Package telemetry constructs the process logger, holds the readiness flag,
// and provides the metrics, tracing, and audit seams. It is copied from the
// handbook reference service and trimmed to what Bloom wires today.
//
// No global logger lives here; the constructed *slog.Logger is returned and
// threaded explicitly into reusable packages.
package telemetry

import (
	"io"
	"log/slog"
	"sync/atomic"

	"github.com/BonzTM/bloom/internal/config"
)

// NewLogger builds the single structured logger for the process. JSON for
// machine-collected environments, text for local development. The level comes
// from config so operators can change verbosity without a code redeploy.
func NewLogger(w io.Writer, cfg config.TelemetryConfig) *slog.Logger {
	opts := &slog.HandlerOptions{Level: cfg.LogLevel}
	var h slog.Handler
	if cfg.LogFormat == config.LogFormatText {
		h = slog.NewTextHandler(w, opts)
	} else {
		h = slog.NewJSONHandler(w, opts)
	}
	return slog.New(h)
}

// Readiness is a concurrency-safe flag the HTTP /readyz probe reads and the
// shutdown sequence flips to false before draining. Liveness is separate and
// stays green during drain so the platform does not kill the pod mid-shutdown.
//
// The zero value is not ready; call NewReadiness for an explicit initial state.
type Readiness struct {
	ready atomic.Bool
}

// NewReadiness returns a Readiness initialized to the given state.
func NewReadiness(ready bool) *Readiness {
	r := &Readiness{}
	r.ready.Store(ready)
	return r
}

// Set updates the readiness state.
func (r *Readiness) Set(ready bool) { r.ready.Store(ready) }

// Ready reports whether the service is ready to receive traffic.
func (r *Readiness) Ready() bool { return r.ready.Load() }

// Metrics is the low-cardinality metrics seam consumed by the rest of the
// service. It is intentionally tiny (1-3 methods, per the handbook's
// foundations/package-design.md). Production wires the Prometheus adapter
// (NewPromMetrics) behind this same interface; NopMetrics is the drop-in for
// tests. Swap the adapter in internal/runtime, never the call sites.
//
// Implementations MUST keep label values low-cardinality (route patterns and
// status classes, never request IDs, user IDs, or raw paths).
type Metrics interface {
	// IncRequest records one handled HTTP request by route pattern and status.
	IncRequest(routePattern, statusClass string)
}

// NopMetrics is the default no-op metrics implementation.
type NopMetrics struct{}

// IncRequest does nothing.
func (NopMetrics) IncRequest(string, string) {}
