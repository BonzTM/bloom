package telemetry

import (
	"database/sql"
	"fmt"
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/BonzTM/bloom/internal/core"
)

// PromMetrics is the production Metrics implementation backed by a dedicated
// Prometheus registry. It satisfies the same telemetry.Metrics seam the rest of
// the service consumes, so swapping it for NopMetrics is a wiring change in
// internal/runtime, never a call-site change. It additionally records request
// latency via the optional ObserveRequest method and exports database pool
// statistics via RegisterDBStats.
//
// Every label is deliberately low-cardinality (matched route pattern, status
// class): request IDs, user IDs, and raw paths are NEVER used as labels because
// they would blow up the time-series cardinality.
type PromMetrics struct {
	registry               *prometheus.Registry
	requests               *prometheus.CounterVec
	requestSeconds         *prometheus.HistogramVec
	loginAttempts          *prometheus.CounterVec
	csrfRejections         prometheus.Counter
	auditWriteFailures     prometheus.Counter
	sessionCleanupFailures prometheus.Counter
	authorizationDenials   *prometheus.CounterVec
	mediaServerRequests    *prometheus.CounterVec
	mediaServerSeconds     *prometheus.HistogramVec
	mediaServerRetries     *prometheus.CounterVec
	oidcDependencyEvents   *prometheus.CounterVec
	oidcDependencySeconds  *prometheus.HistogramVec
}

// NewPromMetrics constructs a PromMetrics on a fresh, private registry (not the
// global default registry) so tests and multiple instances do not collide. The
// namespace prefixes every metric name, e.g. "bloom_http_requests_total".
func NewPromMetrics(namespace string) *PromMetrics {
	reg := prometheus.NewRegistry()
	httpCollectors := newHTTPCollectors(namespace)
	authCollectors := newAuthenticationCollectors(namespace)
	mediaCollectors := newMediaServerCollectors(namespace)
	oidcCollectors := newOIDCCollectors(namespace)
	reg.MustRegister(
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		collectors.NewGoCollector(),
	)
	metrics := &PromMetrics{
		registry: reg, requests: httpCollectors.requests, requestSeconds: httpCollectors.seconds,
		loginAttempts: authCollectors.loginAttempts, csrfRejections: authCollectors.csrfRejections,
		auditWriteFailures:     authCollectors.auditWriteFailures,
		sessionCleanupFailures: authCollectors.sessionCleanupFailures,
		authorizationDenials:   authCollectors.authorizationDenials,
		mediaServerRequests:    mediaCollectors.requests, mediaServerSeconds: mediaCollectors.seconds,
		mediaServerRetries:    mediaCollectors.retries,
		oidcDependencyEvents:  oidcCollectors.events,
		oidcDependencySeconds: oidcCollectors.seconds,
	}
	metrics.registerApplicationCollectors()
	return metrics
}

type httpCollectors struct {
	requests *prometheus.CounterVec
	seconds  *prometheus.HistogramVec
}

func newHTTPCollectors(namespace string) httpCollectors {
	return httpCollectors{
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace, Name: "http_requests_total",
			Help: "Total handled HTTP requests by route pattern and status class.",
		}, []string{"route", "status_class"}),
		seconds: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: namespace, Name: "http_request_duration_seconds",
			Help:    "HTTP request latency in seconds by route pattern and status class.",
			Buckets: prometheus.DefBuckets,
		}, []string{"route", "status_class"}),
	}
}

type authenticationCollectors struct {
	loginAttempts          *prometheus.CounterVec
	csrfRejections         prometheus.Counter
	auditWriteFailures     prometheus.Counter
	sessionCleanupFailures prometheus.Counter
	authorizationDenials   *prometheus.CounterVec
}

func newAuthenticationCollectors(namespace string) authenticationCollectors {
	return authenticationCollectors{
		loginAttempts: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace, Name: "login_attempts_total",
			Help: "Total login attempts by provider and finite outcome.",
		}, []string{"provider", "outcome"}),
		csrfRejections: newCounter(namespace, "csrf_rejections_total",
			"Total cross-origin state-changing requests rejected."),
		auditWriteFailures: newCounter(namespace, "audit_write_failures_total",
			"Total failed writes to the dedicated audit sink."),
		sessionCleanupFailures: newCounter(namespace, "session_cleanup_failures_total",
			"Total failed expired-session cleanup attempts."),
		authorizationDenials: newAuthorizationDenialCounter(namespace),
	}
}

func newCounter(namespace, name, help string) prometheus.Counter {
	return prometheus.NewCounter(prometheus.CounterOpts{Namespace: namespace, Name: name, Help: help})
}

type mediaServerCollectors struct {
	requests *prometheus.CounterVec
	seconds  *prometheus.HistogramVec
	retries  *prometheus.CounterVec
}

func newMediaServerCollectors(namespace string) mediaServerCollectors {
	labels := []string{"kind", "operation", "outcome"}
	return mediaServerCollectors{
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace, Name: "media_server_requests_total",
			Help: "Total outbound media-server requests by kind, operation, and finite outcome.",
		}, labels),
		seconds: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: namespace, Name: "media_server_request_duration_seconds",
			Help:    "Outbound media-server request latency by kind, operation, and finite outcome.",
			Buckets: prometheus.DefBuckets,
		}, labels),
		retries: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace, Name: "media_server_retries_total",
			Help: "Total outbound media-server retry decisions by bounded outcome.",
		}, labels),
	}
}

type oidcCollectors struct {
	events  *prometheus.CounterVec
	seconds *prometheus.HistogramVec
}

func newOIDCCollectors(namespace string) oidcCollectors {
	return oidcCollectors{
		events:  newOIDCDependencyCounter(namespace),
		seconds: newOIDCDependencyHistogram(namespace),
	}
}

func newOIDCDependencyCounter(namespace string) *prometheus.CounterVec {
	return prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace, Name: "oidc_dependency_events_total",
		Help: "OIDC dependency events by finite operation and outcome.",
	}, []string{"dependency", "operation", "outcome"})
}

func newOIDCDependencyHistogram(namespace string) *prometheus.HistogramVec {
	return prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: namespace, Name: "oidc_dependency_duration_seconds",
		Help:    "OIDC dependency request latency by finite operation and outcome.",
		Buckets: prometheus.DefBuckets,
	}, []string{"dependency", "operation", "outcome"})
}

func newAuthorizationDenialCounter(namespace string) *prometheus.CounterVec {
	return prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "authorization_denials_total",
		Help:      "Total authorization denials by finite permission identifier.",
	}, []string{"permission"})
}

func (m *PromMetrics) registerApplicationCollectors() {
	m.registry.MustRegister(
		m.requests,
		m.requestSeconds,
		m.loginAttempts,
		m.csrfRejections,
		m.auditWriteFailures,
		m.sessionCleanupFailures,
		m.authorizationDenials,
		m.mediaServerRequests,
		m.mediaServerSeconds,
		m.mediaServerRetries,
		m.oidcDependencyEvents,
		m.oidcDependencySeconds,
	)
}

// ObserveMediaServerRetry records one bounded retry decision.
func (m *PromMetrics) ObserveMediaServerRetry(kind, operation, outcome string) {
	m.mediaServerRetries.WithLabelValues(kind, operation, outcome).Inc()
}

// ObserveMediaServerRequest records one outbound request with bounded labels.
func (m *PromMetrics) ObserveMediaServerRequest(kind, operation, outcome string, seconds float64) {
	m.mediaServerRequests.WithLabelValues(kind, operation, outcome).Inc()
	m.mediaServerSeconds.WithLabelValues(kind, operation, outcome).Observe(seconds)
}

// ObserveOIDCDependency records a bounded OIDC dependency event and request latency.
func (m *PromMetrics) ObserveOIDCDependency(operation, outcome string, seconds float64) {
	operation = boundedOIDCOperation(operation)
	outcome = boundedOIDCOutcome(outcome)
	m.oidcDependencyEvents.WithLabelValues("oidc", operation, outcome).Inc()
	if outcome == "request_success" || outcome == "request_failure" || outcome == "timeout" {
		m.oidcDependencySeconds.WithLabelValues("oidc", operation, outcome).Observe(seconds)
	}
}

func boundedOIDCOperation(value string) string {
	switch value {
	case "discovery", "token_exchange", "jwks":
		return value
	default:
		return "invalid"
	}
}

func boundedOIDCOutcome(value string) string {
	switch value {
	case "request_success", "request_failure", "retry", "timeout", "exhausted":
		return value
	default:
		return "invalid"
	}
}

// IncCSRFRejection records one rejected cross-origin write.
func (m *PromMetrics) IncCSRFRejection() { m.csrfRejections.Inc() }

// IncAuditWriteFailure records one failed write to the dedicated audit sink.
func (m *PromMetrics) IncAuditWriteFailure() { m.auditWriteFailures.Inc() }

// IncSessionCleanupFailure records one failed expired-session cleanup attempt.
func (m *PromMetrics) IncSessionCleanupFailure() { m.sessionCleanupFailures.Inc() }

// IncAuthorizationDenial records one denied permission check.
func (m *PromMetrics) IncAuthorizationDenial(permission core.CatalogPermission) {
	label := permission.Permission()
	if !label.Valid() {
		label = "invalid"
	}
	m.authorizationDenials.WithLabelValues(string(label)).Inc()
}

// IncLoginAttempt records one login attempt with finite provider and outcome labels.
func (m *PromMetrics) IncLoginAttempt(provider, outcome string) {
	m.loginAttempts.WithLabelValues(boundedLoginProvider(provider), boundedLoginOutcome(outcome)).Inc()
}

func boundedLoginProvider(value string) string {
	switch value {
	case "local", "oidc":
		return value
	default:
		return "invalid"
	}
}

func boundedLoginOutcome(value string) string {
	switch value {
	case "success", "unknown_user", "bad_password", "disabled", "rate_limited", "overloaded",
		"internal_error", "state_invalid", "token_invalid", "provider_unavailable",
		"provisioning_disabled", "unknown_identity":
		return value
	default:
		return "invalid"
	}
}

// IncRequest records one handled request. Both labels must be low-cardinality
// (a route pattern, not a raw path; a status class, not a code).
func (m *PromMetrics) IncRequest(routePattern, statusClass string) {
	m.requests.WithLabelValues(routePattern, statusClass).Inc()
}

// ObserveRequest records request latency under the same low-cardinality labels.
// The HTTP middleware calls IncRequest plus ObserveRequest once per served
// request; the split keeps the narrow Metrics interface unchanged while letting
// the Prometheus adapter also publish a latency histogram.
func (m *PromMetrics) ObserveRequest(routePattern, statusClass string, seconds float64) {
	m.requestSeconds.WithLabelValues(routePattern, statusClass).Observe(seconds)
}

// RegisterDBStats exports the database/sql pool statistics (open, in-use, idle,
// wait count, wait duration) under the "bloom" db_name label, so pool
// saturation is visible before it becomes a timeout, per the handbook's
// services/database.md. Call it once per opened pool.
func (m *PromMetrics) RegisterDBStats(pool *sql.DB, dbName string) error {
	if err := m.registry.Register(collectors.NewDBStatsCollector(pool, dbName)); err != nil {
		return fmt.Errorf("register db stats collector %q: %w", dbName, err)
	}
	return nil
}

// Handler returns the promhttp handler serving this instance's registry. It is
// mounted at GET /metrics ahead of the heavy middleware so the scrape endpoint
// is neither access-logged nor counted in request metrics.
func (m *PromMetrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{Registry: m.registry})
}

// Registry exposes the underlying registry for tests that gather metrics
// directly.
func (m *PromMetrics) Registry() *prometheus.Registry { return m.registry }
