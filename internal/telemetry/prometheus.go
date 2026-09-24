package telemetry

import (
	"database/sql"
	"fmt"
	"net/http"
	"sync"

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
	registry                   *prometheus.Registry
	requests                   *prometheus.CounterVec
	requestSeconds             *prometheus.HistogramVec
	loginAttempts              *prometheus.CounterVec
	csrfRejections             prometheus.Counter
	auditWriteFailures         prometheus.Counter
	sessionCleanupFailures     prometheus.Counter
	authorizationDenials       *prometheus.CounterVec
	mediaServerRequests        *prometheus.CounterVec
	mediaServerSeconds         *prometheus.HistogramVec
	mediaServerRetries         *prometheus.CounterVec
	oidcDependencyEvents       *prometheus.CounterVec
	oidcDependencySeconds      *prometheus.HistogramVec
	inviteCreations            *prometheus.CounterVec
	inviteAcceptances          *prometheus.CounterVec
	mediaUserMatches           *prometheus.CounterVec
	playbackPolls              *prometheus.CounterVec
	playbackPollSeconds        *prometheus.HistogramVec
	playbackOpenWatches        *prometheus.GaugeVec
	playbackWatchesClosed      *prometheus.CounterVec
	playbackRefreshFailures    prometheus.Counter
	playbackLibraryResolutions *prometheus.CounterVec
	statsQuerySeconds          *prometheus.HistogramVec
	playbackMu                 sync.Mutex
	playbackOpenByServer       map[string]int
	metadataRequests           *prometheus.CounterVec
	metadataSeconds            *prometheus.HistogramVec
	metadataRetries            *prometheus.CounterVec
	mediaRequests              *prometheus.CounterVec
	downloadManagerRequests    *prometheus.CounterVec
	downloadManagerSeconds     *prometheus.HistogramVec
	downloadManagerRetries     *prometheus.CounterVec
	notificationDeliveries     *prometheus.CounterVec
	notificationOutboxDepth    prometheus.Gauge
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
	inviteCreations := newOutcomeCounter(namespace, "invite_creations_total", "Total invite creation attempts by finite outcome.")
	inviteAcceptances := newOutcomeCounter(namespace, "invite_acceptances_total", "Total invite acceptance attempts by finite outcome.")
	mediaUserMatches := newOutcomeCounter(namespace, "media_user_matches_total", "On-demand account media-user match outcomes.")
	playbackCollectors := newPlaybackCollectors(namespace)
	playbackRefreshFailures := newCounter(namespace, "playback_refresh_failures_total",
		"Total failed playback manager refresh attempts.")
	playbackLibraryResolutions := prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace, Name: "playback_library_resolutions_total",
		Help: "Playback item library resolution outcomes.",
	}, []string{"outcome"})
	statsQuerySeconds := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: namespace, Name: "stats_query_duration_seconds",
		Help: "Statistics query latency by bounded report and outcome.", Buckets: prometheus.DefBuckets,
	}, []string{"report", "outcome"})
	requestCollectors := newRequestCollectors(namespace)
	downloadCollectors := newDownloadManagerCollectors(namespace)
	notificationDeliveries := prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace, Name: "notification_deliveries_total",
		Help: "Notification delivery attempts by channel kind and bounded outcome.",
	}, []string{"kind", "outcome"})
	notificationOutboxDepth := prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: namespace, Name: "notification_outbox_depth",
		Help: "Current count of pending notification outbox rows.",
	})
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
		inviteCreations:       inviteCreations,
		inviteAcceptances:     inviteAcceptances,
		mediaUserMatches:      mediaUserMatches,
		playbackPolls:         playbackCollectors.polls, playbackPollSeconds: playbackCollectors.seconds,
		playbackOpenWatches: playbackCollectors.open, playbackWatchesClosed: playbackCollectors.closed,
		playbackRefreshFailures:    playbackRefreshFailures,
		playbackLibraryResolutions: playbackLibraryResolutions,
		statsQuerySeconds:          statsQuerySeconds,
		playbackOpenByServer:       make(map[string]int),
		metadataRequests:           requestCollectors.metadataRequests,
		metadataSeconds:            requestCollectors.metadataSeconds,
		metadataRetries:            requestCollectors.metadataRetries,
		mediaRequests:              requestCollectors.mediaRequests,
		downloadManagerRequests:    downloadCollectors.requests,
		downloadManagerSeconds:     downloadCollectors.seconds,
		downloadManagerRetries:     downloadCollectors.retries,
		notificationDeliveries:     notificationDeliveries,
		notificationOutboxDepth:    notificationOutboxDepth,
	}
	metrics.registerApplicationCollectors()
	return metrics
}

type downloadManagerCollectors struct {
	requests *prometheus.CounterVec
	seconds  *prometheus.HistogramVec
	retries  *prometheus.CounterVec
}

func newDownloadManagerCollectors(namespace string) downloadManagerCollectors {
	labels := []string{"kind", "operation", "outcome"}
	return downloadManagerCollectors{
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace, Name: "download_manager_requests_total",
			Help: "Total outbound download-manager requests by kind, operation, and outcome.",
		}, labels),
		seconds: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: namespace, Name: "download_manager_request_duration_seconds",
			Help: "Outbound download-manager request latency by kind, operation, and outcome.", Buckets: prometheus.DefBuckets,
		}, labels),
		retries: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace, Name: "download_manager_retries_total",
			Help: "Total outbound download-manager retries by kind, operation, and outcome.",
		}, labels),
	}
}

type requestCollectors struct {
	metadataRequests *prometheus.CounterVec
	metadataSeconds  *prometheus.HistogramVec
	metadataRetries  *prometheus.CounterVec
	mediaRequests    *prometheus.CounterVec
}

func newRequestCollectors(namespace string) requestCollectors {
	metadataLabels := []string{"provider", "operation", "outcome"}
	return requestCollectors{
		metadataRequests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace, Name: "metadata_requests_total",
			Help: "Total outbound metadata requests by provider, operation, and outcome.",
		}, metadataLabels),
		metadataSeconds: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: namespace, Name: "metadata_request_duration_seconds",
			Help: "Outbound metadata request latency by provider, operation, and outcome.", Buckets: prometheus.DefBuckets,
		}, metadataLabels),
		metadataRetries: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace, Name: "metadata_retries_total",
			Help: "Total outbound metadata retries by provider, operation, and outcome.",
		}, metadataLabels),
		mediaRequests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace, Name: "media_requests_total",
			Help: "Total media request lifecycle outcomes by kind and outcome.",
		}, []string{"kind", "outcome"}),
	}
}

type playbackCollectors struct {
	polls   *prometheus.CounterVec
	seconds *prometheus.HistogramVec
	open    *prometheus.GaugeVec
	closed  *prometheus.CounterVec
}

func newPlaybackCollectors(namespace string) playbackCollectors {
	return playbackCollectors{
		polls: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace, Name: "playback_polls_total",
			Help: "Playback session polls by media-server kind and finite outcome.",
		}, []string{"kind", "outcome"}),
		seconds: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: namespace, Name: "playback_poll_duration_seconds",
			Help:    "Playback session poll latency by media-server kind and finite outcome.",
			Buckets: prometheus.DefBuckets,
		}, []string{"kind", "outcome"}),
		open: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: namespace, Name: "playback_open_watches",
			Help: "Current persisted open watches by media-server kind.",
		}, []string{"kind"}),
		closed: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace, Name: "playback_watches_closed_total",
			Help: "Playback watches closed by media-server kind and bounded reason.",
		}, []string{"kind", "reason"}),
	}
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

func newOutcomeCounter(namespace, name, help string) *prometheus.CounterVec {
	return prometheus.NewCounterVec(prometheus.CounterOpts{Namespace: namespace, Name: name, Help: help}, []string{"outcome"})
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
		m.inviteCreations,
		m.inviteAcceptances,
		m.mediaUserMatches,
		m.playbackPolls,
		m.playbackPollSeconds,
		m.playbackOpenWatches,
		m.playbackWatchesClosed,
		m.playbackRefreshFailures,
		m.playbackLibraryResolutions,
		m.statsQuerySeconds,
		m.metadataRequests,
		m.metadataSeconds,
		m.metadataRetries,
		m.mediaRequests,
		m.downloadManagerRequests,
		m.downloadManagerSeconds,
		m.downloadManagerRetries,
		m.notificationDeliveries,
		m.notificationOutboxDepth,
	)
}

// ObserveNotificationDelivery records one bounded worker outcome.
func (m *PromMetrics) ObserveNotificationDelivery(kind, outcome string) {
	switch core.NotificationKind(kind) {
	case core.NotificationKindWebhook, core.NotificationKindDiscord, core.NotificationKindEmail:
	default:
		kind = "invalid"
	}
	if outcome != "sent" && outcome != "retry" && outcome != "failed" {
		outcome = "invalid"
	}
	m.notificationDeliveries.WithLabelValues(kind, outcome).Inc()
}

// SetNotificationOutboxDepth publishes the current pending row count.
func (m *PromMetrics) SetNotificationOutboxDepth(depth int64) {
	m.notificationOutboxDepth.Set(float64(max(depth, 0)))
}

// ObserveDownloadManagerRequest records one bounded Radarr or Sonarr operation.
func (m *PromMetrics) ObserveDownloadManagerRequest(kind, operation, outcome string, seconds float64) {
	m.downloadManagerRequests.WithLabelValues(kind, operation, outcome).Inc()
	m.downloadManagerSeconds.WithLabelValues(kind, operation, outcome).Observe(seconds)
}

// ObserveDownloadManagerRetry records one bounded retry decision.
func (m *PromMetrics) ObserveDownloadManagerRetry(kind, operation, outcome string) {
	m.downloadManagerRetries.WithLabelValues(kind, operation, outcome).Inc()
}

// ObserveMetadataRequest records one bounded metadata-provider operation.
func (m *PromMetrics) ObserveMetadataRequest(provider, operation, outcome string, seconds float64) {
	m.metadataRequests.WithLabelValues(provider, operation, outcome).Inc()
	m.metadataSeconds.WithLabelValues(provider, operation, outcome).Observe(seconds)
}

// ObserveMetadataRetry records one metadata-provider retry outcome.
func (m *PromMetrics) ObserveMetadataRetry(provider, operation, outcome string) {
	m.metadataRetries.WithLabelValues(provider, operation, outcome).Inc()
}

// IncMediaRequest records one media request lifecycle outcome.
func (m *PromMetrics) IncMediaRequest(kind, outcome string) {
	m.mediaRequests.WithLabelValues(kind, outcome).Inc()
}

// IncInviteCreation records one invite creation outcome.
func (m *PromMetrics) IncInviteCreation(outcome string) {
	m.inviteCreations.WithLabelValues(outcome).Inc()
}

// IncInviteAcceptance records one invite acceptance outcome.
func (m *PromMetrics) IncInviteAcceptance(outcome string) {
	m.inviteAcceptances.WithLabelValues(outcome).Inc()
}

// IncMediaUserMatch records one bounded on-demand match outcome.
func (m *PromMetrics) IncMediaUserMatch(outcome string) {
	switch outcome {
	case "found", "not_found", "error", "unsupported", "conflict":
	default:
		outcome = "invalid"
	}
	m.mediaUserMatches.WithLabelValues(outcome).Inc()
}

// ObservePlaybackPoll records one completed collector poll.
func (m *PromMetrics) ObservePlaybackPoll(kind, outcome string, seconds float64) {
	kind, outcome = boundedMediaKind(kind), boundedPollOutcome(outcome)
	m.playbackPolls.WithLabelValues(kind, outcome).Inc()
	m.playbackPollSeconds.WithLabelValues(kind, outcome).Observe(seconds)
}

// SetOpenWatches updates one server and publishes the aggregate by kind.
func (m *PromMetrics) SetOpenWatches(kind, serverID string, count int) {
	kind = boundedMediaKind(kind)
	m.playbackMu.Lock()
	key := kind + "\x00" + serverID
	if count <= 0 {
		delete(m.playbackOpenByServer, key)
	} else {
		m.playbackOpenByServer[key] = count
	}
	total := 0
	for key, open := range m.playbackOpenByServer {
		if len(key) > len(kind) && key[:len(kind)] == kind && key[len(kind)] == 0 {
			total += open
		}
	}
	m.playbackOpenWatches.WithLabelValues(kind).Set(float64(total))
	m.playbackMu.Unlock()
}

// IncWatchesClosed records one timeout or startup closure.
func (m *PromMetrics) IncWatchesClosed(kind, reason string) {
	kind = boundedMediaKind(kind)
	if reason != "timeout" && reason != "startup" && reason != "overflow" {
		reason = "invalid"
	}
	m.playbackWatchesClosed.WithLabelValues(kind, reason).Inc()
}

// IncPlaybackRefreshFailure records one failed media-server listing attempt.
func (m *PromMetrics) IncPlaybackRefreshFailure() { m.playbackRefreshFailures.Inc() }

// IncLibraryResolution records one resolved, missing, failed, or dropped outcome.
func (m *PromMetrics) IncLibraryResolution(outcome string) {
	if outcome != "resolved" && outcome != "missing" && outcome != "failed" && outcome != "dropped" {
		outcome = "invalid"
	}
	m.playbackLibraryResolutions.WithLabelValues(outcome).Inc()
}

// ObserveStatsQuery records one statistics report query.
func (m *PromMetrics) ObserveStatsQuery(report, outcome string, seconds float64) {
	switch report {
	case "overview", "daily", "patterns", "titles", "users", "libraries", "user":
	default:
		report = "invalid"
	}
	if outcome != "success" && outcome != "error" {
		outcome = "invalid"
	}
	m.statsQuerySeconds.WithLabelValues(report, outcome).Observe(seconds)
}

func boundedMediaKind(kind string) string {
	if kind == string(core.MediaServerKindJellyfin) {
		return kind
	}
	return "invalid"
}

func boundedPollOutcome(outcome string) string {
	if outcome == "success" || outcome == "failure" {
		return outcome
	}
	return "invalid"
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
