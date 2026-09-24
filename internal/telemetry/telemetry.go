// Package telemetry constructs the process logger, holds the readiness flag,
// and provides the metrics, tracing, and audit seams. It is copied from the
// handbook reference service and trimmed to what Bloom wires today.
//
// No global logger lives here; the constructed *slog.Logger is returned and
// threaded explicitly into reusable packages. Audit Emit failures return to
// the HTTP caller, which logs once, increments
// bloom_audit_write_failures_total, and continues the request.
package telemetry

import (
	"io"
	"log/slog"
	"sync/atomic"

	"github.com/BonzTM/bloom/internal/config"
	"github.com/BonzTM/bloom/internal/core"
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
	// IncLoginAttempt records one login attempt by provider and finite outcome:
	// success, unknown_user, bad_password, disabled, rate_limited, overloaded,
	// or internal_error.
	IncLoginAttempt(provider, outcome string)
	// IncCSRFRejection records one cross-origin write rejection.
	IncCSRFRejection()
}

// AuditFailureMetrics records failures from the dedicated audit sink. It is a
// separate one-method seam so the request Metrics contract stays focused.
type AuditFailureMetrics interface {
	IncAuditWriteFailure()
}

// AuthorizationMetrics records permission denials. Permission labels come
// only from core's finite catalog.
type AuthorizationMetrics interface {
	IncAuthorizationDenial(permission core.CatalogPermission)
}

// InviteMetrics records bounded invite outcomes.
type InviteMetrics interface {
	IncInviteCreation(outcome string)
	IncInviteAcceptance(outcome string)
}

// NopMetrics is the default no-op metrics implementation.
type NopMetrics struct{}

// IncRequest does nothing.
func (NopMetrics) IncRequest(string, string) {}

// IncLoginAttempt does nothing.
func (NopMetrics) IncLoginAttempt(string, string) {}

// IncCSRFRejection does nothing.
func (NopMetrics) IncCSRFRejection() {}

// IncAuditWriteFailure does nothing.
func (NopMetrics) IncAuditWriteFailure() {}

// IncAuthorizationDenial does nothing.
func (NopMetrics) IncAuthorizationDenial(core.CatalogPermission) {}

// IncInviteCreation does nothing.
func (NopMetrics) IncInviteCreation(string) {}

// IncInviteAcceptance does nothing.
func (NopMetrics) IncInviteAcceptance(string) {}

// IncSessionCleanupFailure does nothing.
func (NopMetrics) IncSessionCleanupFailure() {}

// ObserveMediaServerRequest does nothing.
func (NopMetrics) ObserveMediaServerRequest(string, string, string, float64) {}

// ObserveMediaServerRetry does nothing.
func (NopMetrics) ObserveMediaServerRetry(string, string, string) {}

// ObservePlaybackPoll does nothing.
func (NopMetrics) ObservePlaybackPoll(string, string, float64) {}

// SetOpenWatches does nothing.
func (NopMetrics) SetOpenWatches(string, string, int) {}

// IncWatchesClosed does nothing.
func (NopMetrics) IncWatchesClosed(string, string) {}

// IncLibraryResolution does nothing.
func (NopMetrics) IncLibraryResolution(string, string) {}

// IncPlaybackRefreshFailure does nothing.
func (NopMetrics) IncPlaybackRefreshFailure() {}

// ObserveStatsQuery does nothing.
func (NopMetrics) ObserveStatsQuery(string, string, float64) {}

// ObserveMetadataRequest discards a metadata-provider observation.
func (NopMetrics) ObserveMetadataRequest(string, string, string, float64) {}

// ObserveMetadataRetry discards a metadata-provider retry observation.
func (NopMetrics) ObserveMetadataRetry(string, string, string) {}

// IncMediaRequest discards a media request outcome.
func (NopMetrics) IncMediaRequest(string, string) {}
