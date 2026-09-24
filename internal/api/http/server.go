package http

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"

	"github.com/alexedwards/scs/v2"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"

	"github.com/BonzTM/bloom/internal/config"
	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/httputil"
	inviteapp "github.com/BonzTM/bloom/internal/invite"
	notifyapp "github.com/BonzTM/bloom/internal/notify"
	requestapp "github.com/BonzTM/bloom/internal/request"
	"github.com/BonzTM/bloom/internal/telemetry"
)

// Pinger is the readiness dependency: the database pool in production
// (*sql.DB satisfies it), a fake in tests. Defined here, at the consumer, so
// the transport does not import the storage package.
type Pinger interface {
	// PingContext reports whether the dependency can serve traffic.
	PingContext(ctx context.Context) error
}

type mediaServerReader interface {
	List(ctx context.Context, afterNameKey string, pageSize int) ([]core.MediaServerConnection, error)
	Get(ctx context.Context, id string) (core.MediaServerConnection, error)
	Libraries(ctx context.Context, id string) ([]core.Library, error)
}

type mediaServerManager interface {
	Register(ctx context.Context, kind core.MediaServerKind, name, baseURL, credential string, allowInsecure bool) (core.MediaServerConnection, error)
	Probe(ctx context.Context, id string) (core.ServerInfo, error)
	Delete(ctx context.Context, id string) (core.MediaServer, error)
}

type downloadManagerReader interface {
	List(ctx context.Context, afterNameKey string, pageSize int) ([]core.DownloadManager, error)
	Options(ctx context.Context, id string) (core.DownloadManagerOptions, error)
}

type downloadManagerManager interface {
	Register(ctx context.Context, kind core.DownloadManagerKind, name, baseURL, apiKey string, allowInsecure bool) (core.DownloadManagerConnection, error)
	Delete(ctx context.Context, id string) (core.DownloadManager, error)
}

type inviteReader interface {
	List(ctx context.Context, after *core.InviteCursor, pageSize int) ([]core.Invite, error)
	Get(ctx context.Context, id string) (core.Invite, error)
	Preview(ctx context.Context, code string) (inviteapp.Preview, error)
}

type inviteManager interface {
	Create(ctx context.Context, input inviteapp.CreateInput) (inviteapp.Created, error)
	Revoke(ctx context.Context, id string) (core.Invite, error)
	Accept(ctx context.Context, accountID, code, username, password string) (inviteapp.Accepted, error)
}

type accountMediaUserManager interface {
	EnsureLinks(ctx context.Context, account core.Account, mediaServerID string) ([]core.AccountMediaUser, error)
	List(ctx context.Context, accountID string, includeSuppressed bool) ([]core.AccountMediaUser, error)
	Set(ctx context.Context, accountID, mediaServerID, mediaUserID string) (core.AccountMediaUser, error)
	Delete(ctx context.Context, accountID, mediaServerID string) error
}

type playbackReader interface {
	ListWatches(ctx context.Context, query core.PlaybackQuery) ([]core.PlaybackWatch, error)
}

type metadataReader interface {
	Search(context.Context, core.MetadataSearch) ([]core.MetadataTitle, error)
	Movie(context.Context, string) (core.MetadataTitle, error)
	Series(context.Context, string, bool) (core.MetadataSeries, error)
}

type metadataManager interface {
	SetKey(context.Context, core.MetadataProviderKind, string) error
	HasKey(context.Context, core.MetadataProviderKind) (bool, error)
	RemoveKey(context.Context, core.MetadataProviderKind) error
}

type notificationReader interface {
	Get(context.Context, string) (core.NotificationRegistration, error)
	List(context.Context, string, int) ([]core.NotificationRegistration, error)
	Deliveries(context.Context, string, *core.NotificationDeliveryCursor, int) ([]core.NotificationDelivery, error)
}

type notificationManager interface {
	Register(context.Context, notifyapp.RegistrationInput) (core.NotificationRegistration, error)
	Update(context.Context, string, notifyapp.RegistrationInput) (core.NotificationRegistration, error)
	Delete(context.Context, string) (core.NotificationRegistration, error)
}

type notificationTester interface {
	Test(context.Context, string) error
}

// Server owns the HTTP listener, mux, and middleware wiring. It holds the
// dependencies the handlers need and the readiness flag the shutdown sequence
// flips. It never stores a request context.
type Server struct {
	httpServer             *http.Server
	handlers               handlerTracker
	logger                 *slog.Logger
	metrics                telemetry.Metrics
	metricsHandler         http.Handler
	readiness              *telemetry.Readiness
	pinger                 Pinger
	web                    http.Handler
	maxBodyBytes           int64
	identity               core.IdentityProvider
	accounts               core.AccountStore
	sessions               *scs.SessionManager
	audit                  auditEmitter
	auditFailureMetrics    telemetry.AuditFailureMetrics
	authorizationMetrics   telemetry.AuthorizationMetrics
	usernameAuditKey       [32]byte
	loginLimiter           *loginLimiter
	passwordVerifications  chan struct{}
	oidcExchanges          chan struct{}
	authOperationTimeout   time.Duration
	mediaOperationTimeout  time.Duration
	trustedProxyCIDRs      []netip.Prefix
	authorizer             core.Authorizer
	roles                  core.RoleReader
	mediaServerReader      mediaServerReader
	mediaServerManager     mediaServerManager
	downloadManagerReader  downloadManagerReader
	downloadManagerManager downloadManagerManager
	inviteReader           inviteReader
	inviteManager          inviteManager
	inviteLimiter          *loginLimiter
	inviteAcceptances      chan struct{}
	inviteMetrics          telemetry.InviteMetrics
	accountMediaUsers      accountMediaUserManager
	playbackReader         playbackReader
	statsReader            core.StatsReader
	metadataReader         metadataReader
	metadataManager        metadataManager
	requestService         *requestapp.Service
	notificationReader     notificationReader
	notificationManager    notificationManager
	notificationTester     notificationTester
	clock                  core.Clock
	oidcProvider           core.OIDCProvider
	oidcAccounts           core.OIDCAccountStore
	oidcFlows              core.OIDCFlowStore
	oidcConfig             config.OIDCConfig
	publicURL              string
}

// Deps bundles the dependencies the server wires on top of config. Grouping
// them keeps New's signature stable as the auth layer (auth.go) grows and
// documents each seam in one place.
type Deps struct {
	// Logger is the process logger. Required.
	Logger *slog.Logger
	// Metrics is the request-metrics seam. Required (telemetry.NopMetrics for
	// tests). If it also exposes a scrape handler, GET /metrics is mounted.
	Metrics telemetry.Metrics
	// Readiness is the flag the shutdown sequence flips before draining. Required.
	Readiness *telemetry.Readiness
	// Pinger is checked by /readyz. Required.
	Pinger Pinger
	// Web serves the embedded SPA at "/" (ADR 0002). Optional: when nil, every
	// non-API path gets the JSON 404 envelope, which is what API-only tests want.
	Web http.Handler
	// Identity authenticates local username/password credentials.
	Identity core.IdentityProvider
	// Accounts reloads the session account on every protected request.
	Accounts core.AccountStore
	// Authorizer computes effective permissions from current database state.
	Authorizer core.Authorizer
	// Roles reads account role names and the administrative role list.
	Roles core.RoleReader
	// MediaServerReader supplies registered-server reads.
	MediaServerReader mediaServerReader
	// MediaServerManager supplies registered-server changes and probes.
	MediaServerManager mediaServerManager
	// DownloadManagerReader supplies registered Radarr and Sonarr reads.
	DownloadManagerReader downloadManagerReader
	// DownloadManagerManager supplies download-manager registration changes.
	DownloadManagerManager downloadManagerManager
	// InviteReader supplies invite administration and public reads.
	InviteReader inviteReader
	// InviteManager supplies invite creation, revocation, and acceptance.
	InviteManager inviteManager
	// AccountMediaUsers resolves and administers account-to-media-user links.
	AccountMediaUsers accountMediaUserManager
	// PlaybackReader supplies now-playing and history reads.
	PlaybackReader playbackReader
	// StatsReader supplies cached statistics dashboard reports.
	StatsReader core.StatsReader
	// MetadataReader supplies provider-backed search and detail reads.
	MetadataReader metadataReader
	// MetadataManager supplies write-only provider credential administration.
	MetadataManager metadataManager
	// RequestService supplies request profiles, quotas, and request lifecycle policy.
	RequestService *requestapp.Service
	// NotificationReader supplies channel and delivery-log reads.
	NotificationReader notificationReader
	// NotificationManager supplies channel registration changes.
	NotificationManager notificationManager
	// NotificationTester sends explicit administrator test messages.
	NotificationTester notificationTester
	// Sessions holds server-side session state.
	Sessions *scs.SessionManager
	// Audit receives security events on the dedicated audit stream.
	Audit auditEmitter
	// AuditCorrelationKey is BLOOM_SECRET_KEY material used only to derive the
	// purpose-separated failed-login correlation key.
	AuditCorrelationKey []byte
	// Clock drives the deterministic in-process login limiter.
	Clock core.Clock
	// Auth contains validated session and rate-limit settings.
	Auth config.AuthConfig
	// OIDC is the optional generic OpenID Connect protocol adapter.
	OIDC core.OIDCProvider
	// OIDCAccounts resolves and provisions linked Bloom accounts atomically.
	OIDCAccounts core.OIDCAccountStore
	// OIDCFlows atomically claims persistent single-use callback state.
	OIDCFlows core.OIDCFlowStore
	// OIDCConfig holds validated provider mapping and display settings.
	OIDCConfig config.OIDCConfig
	// PublicURL is Bloom's validated externally visible origin.
	PublicURL string
}

// metricsExposer is the optional seam a Metrics implementation can satisfy to
// publish a scrape endpoint. The Prometheus adapter implements it; the no-op
type metricsExposer interface {
	Handler() http.Handler
}

// New constructs a Server with hardened timeouts and the standard middleware
// chain. The readiness flag is shared with the shutdown sequence so it can flip
// the server to unready before draining.
func New(cfg config.HTTPConfig, deps Deps) *Server {
	s := newServerState(cfg, deps)
	s.configureAuthentication(cfg.WriteTimeout, deps)
	s.configureMetrics(deps.Metrics)
	s.httpServer = newHTTPServer(cfg, s.handlers.track(s.routes()))
	return s
}

type handlerTracker struct {
	mu     sync.Mutex
	active int
	idle   chan struct{}
	sealed bool
}

func (t *handlerTracker) track(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !t.start() {
			return
		}
		defer t.stop()
		next.ServeHTTP(w, r)
	})
}

func (t *handlerTracker) start() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.sealed {
		return false
	}
	if t.active == 0 {
		t.idle = make(chan struct{})
	}
	t.active++
	return true
}

func (t *handlerTracker) stop() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.active--
	if t.active == 0 {
		close(t.idle)
	}
}

func (t *handlerTracker) wait(ctx context.Context) error {
	t.mu.Lock()
	if t.active == 0 {
		t.mu.Unlock()
		return nil
	}
	idle := t.idle
	t.mu.Unlock()
	select {
	case <-idle:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (t *handlerTracker) seal() {
	t.mu.Lock()
	t.sealed = true
	t.mu.Unlock()
}

func newServerState(cfg config.HTTPConfig, deps Deps) *Server {
	s := &Server{
		logger:                 deps.Logger,
		metrics:                deps.Metrics,
		readiness:              deps.Readiness,
		pinger:                 deps.Pinger,
		web:                    deps.Web,
		maxBodyBytes:           cfg.MaxBodyBytes,
		identity:               deps.Identity,
		accounts:               deps.Accounts,
		sessions:               deps.Sessions,
		audit:                  deps.Audit,
		auditFailureMetrics:    telemetry.NopMetrics{},
		authorizationMetrics:   telemetry.NopMetrics{},
		trustedProxyCIDRs:      deps.Auth.TrustedProxyCIDRs,
		authorizer:             deps.Authorizer,
		roles:                  deps.Roles,
		mediaServerReader:      deps.MediaServerReader,
		mediaServerManager:     deps.MediaServerManager,
		downloadManagerReader:  deps.DownloadManagerReader,
		downloadManagerManager: deps.DownloadManagerManager,
		inviteReader:           deps.InviteReader,
		inviteManager:          deps.InviteManager,
		inviteMetrics:          telemetry.NopMetrics{},
		accountMediaUsers:      deps.AccountMediaUsers,
		playbackReader:         deps.PlaybackReader,
		statsReader:            deps.StatsReader,
		metadataReader:         deps.MetadataReader,
		metadataManager:        deps.MetadataManager,
		requestService:         deps.RequestService,
		notificationReader:     deps.NotificationReader,
		notificationManager:    deps.NotificationManager,
		notificationTester:     deps.NotificationTester,
		mediaOperationTimeout:  derivedAuthOperationTimeout(cfg.WriteTimeout),
		clock:                  deps.Clock,
		oidcProvider:           deps.OIDC,
		oidcAccounts:           deps.OIDCAccounts,
		oidcFlows:              deps.OIDCFlows,
		oidcConfig:             deps.OIDCConfig,
		publicURL:              deps.PublicURL,
	}
	if s.audit == nil {
		s.audit = telemetry.NopAuditLogger()
	}
	return s
}

func (s *Server) configureAuthentication(writeTimeout time.Duration, deps Deps) {
	if authDependenciesPresent(deps) {
		s.usernameAuditKey = deriveUsernameAuditKey(deps.AuditCorrelationKey)
		s.loginLimiter = newLoginLimiter(
			deps.Clock, deps.Auth.LoginRateRefillInterval,
			deps.Auth.LoginRateBurst, deps.Auth.LoginRateMaxKeys,
		)
		s.passwordVerifications = make(chan struct{}, deps.Auth.LoginMaxConcurrent)
		s.oidcExchanges = make(chan struct{}, oidcMaxConcurrentExchanges)
		s.authOperationTimeout = derivedAuthOperationTimeout(writeTimeout)
	}
	if deps.InviteReader != nil && deps.InviteManager != nil && deps.Clock != nil {
		s.inviteLimiter = newLoginLimiter(
			deps.Clock, deps.Auth.LoginRateRefillInterval,
			deps.Auth.LoginRateBurst, deps.Auth.LoginRateMaxKeys,
		)
		s.inviteAcceptances = make(chan struct{}, deps.Auth.LoginMaxConcurrent)
	}
}

func (s *Server) configureMetrics(metrics telemetry.Metrics) {
	if metrics, ok := metrics.(telemetry.AuditFailureMetrics); ok {
		s.auditFailureMetrics = metrics
	}
	if metrics, ok := metrics.(telemetry.AuthorizationMetrics); ok {
		s.authorizationMetrics = metrics
	}
	if metrics, ok := metrics.(telemetry.InviteMetrics); ok {
		s.inviteMetrics = metrics
	}
	if exposer, ok := metrics.(metricsExposer); ok {
		s.metricsHandler = exposer.Handler()
	}
}

func newHTTPServer(cfg config.HTTPConfig, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:    cfg.Addr,
		Handler: handler,
		// Server-hardening defaults, per the handbook's services/http-services.md.
		ReadHeaderTimeout: cfg.ReadHeaderTimeout,
		ReadTimeout:       cfg.ReadTimeout,
		WriteTimeout:      cfg.WriteTimeout,
		IdleTimeout:       cfg.IdleTimeout,
	}
}

func authDependenciesPresent(deps Deps) bool {
	return deps.Identity != nil &&
		deps.Accounts != nil &&
		deps.Authorizer != nil &&
		deps.Roles != nil &&
		deps.Sessions != nil &&
		deps.Clock != nil &&
		len(deps.AuditCorrelationKey) > 0
}

const maxAuthOperationTimeout = 5 * time.Second

func derivedAuthOperationTimeout(writeTimeout time.Duration) time.Duration {
	if writeTimeout <= 0 {
		return maxAuthOperationTimeout
	}
	timeout := writeTimeout / 2
	if timeout > maxAuthOperationTimeout {
		return maxAuthOperationTimeout
	}
	if timeout <= 0 {
		return time.Nanosecond
	}
	return timeout
}

// routes builds the request handler. Health probes, the scrape endpoint, and
// the application API are split onto separate muxes so probes and /metrics are
// NOT access-logged, traced, or counted in request metrics, while every API
// request is. Order on the API branch: request id -> security headers ->
// recovery -> CSRF -> otelhttp span -> logging+metrics -> body cap -> mux ->
// route-scoped session/account middleware -> handler.
func (s *Server) routes() http.Handler {
	apiMux := http.NewServeMux()
	if err := s.registerAPIRoutes(apiMux); err != nil {
		panic(fmt.Sprintf("register API routes: %v", err))
	}
	// An unknown /api/* route gets the JSON 404 envelope, never HTML. Every
	// other path belongs to the SPA (ADR 0002) when one is wired, and to the
	// same JSON 404 when it is not.
	apiMux.HandleFunc("/api/", s.handleNotFound)
	if s.web != nil {
		apiMux.Handle("/", s.web)
	} else {
		apiMux.HandleFunc("/", s.handleNotFound)
	}

	var apiHandler http.Handler = apiMux
	apiHandler = httputil.MaxBytes(s.maxBodyBytes)(apiHandler)
	apiHandler = loggingMiddleware(s.logger, s.metrics)(apiHandler)
	apiHandler = otelhttp.NewHandler(apiHandler, "http.server", otelhttp.WithFilter(traceGeneralAPIRequest))

	probeMux := http.NewServeMux()
	probeMux.HandleFunc("GET /livez", s.handleLivez)
	probeMux.HandleFunc("GET /readyz", s.handleReadyz)
	if s.metricsHandler != nil {
		probeMux.Handle("GET /metrics", s.metricsHandler)
	}

	root := http.NewServeMux()
	root.Handle("GET /livez", probeMux)
	root.Handle("GET /readyz", probeMux)
	if s.metricsHandler != nil {
		root.Handle("GET /metrics", probeMux)
	}
	root.Handle("/", apiHandler)

	// Request ID is OUTERMOST so the id is on the context before recovery runs:
	// a panic-recovered 500 can then echo the request_id in its envelope.
	csrf := http.NewCrossOriginProtection()
	csrf.SetDenyHandler(http.HandlerFunc(s.handleCSRFRejected))
	h := csrf.Handler(root)
	h = recoverMiddleware(s.logger)(h)
	h = securityHeadersMiddleware(h)
	h = requestIDMiddleware(h)
	return h
}

func traceGeneralAPIRequest(r *http.Request) bool {
	return !strings.HasPrefix(r.URL.Path, "/api/v1/invite/")
}

func (s *Server) sessionHandler(handler http.Handler, accountRequired bool) http.Handler {
	if accountRequired {
		handler = s.sessionAccountMiddleware(handler)
	}
	return s.loadAndSaveSessions(handler)
}

func (s *Server) handleCSRFRejected(w http.ResponseWriter, r *http.Request) {
	s.metrics.IncCSRFRejection()
	s.emitAuthAudit(r, "anonymous", "auth.csrf", csrfAuditResource(r.URL.Path), telemetry.AuditFailure, "cross_origin", clientIP(r, s.trustedProxyCIDRs))
	writeJSON(w, r, s.logger, http.StatusForbidden, httputil.ErrorResponse{
		Code: codeCSRFRejected, Message: "cross-origin request rejected",
		RequestID: requestIDFrom(r.Context()),
	})
}

func csrfAuditResource(path string) string {
	switch inventoryPattern(path) {
	case "/api/v1/auth/login":
		return auditResourceAuthLogin
	case "/api/v1/auth/logout":
		return auditResourceAuthLogout
	case "/api/v1/auth/me":
		return auditResourceAuthMe
	case "/api/v1/auth/permissions":
		return auditResourceAuthPermissions
	case "/api/v1/auth/oidc/start":
		return auditResourceAuthOIDCStart
	case "/api/v1/roles":
		return auditResourceRoles
	case "/api/v1/media-servers", "/api/v1/media-servers/{id}",
		"/api/v1/media-servers/{id}/probe", "/api/v1/media-servers/{id}/libraries":
		return auditResourceMediaServers
	case "/api/v1/download-managers", "/api/v1/download-managers/{id}", "/api/v1/download-managers/{id}/options":
		return auditResourceDownloadManagers
	case "/api/v1/notification-channels", "/api/v1/notification-channels/{id}",
		"/api/v1/notification-channels/{id}/test", "/api/v1/notification-channels/{id}/deliveries":
		return auditResourceNotifications
	case "/api/v1/invites", "/api/v1/invites/{id}":
		return auditResourceInvites
	case "/api/v1/invite/{code}", "/api/v1/invite/{code}/accept":
		return auditResourceInvitePublic
	case "/api/v1/accounts/{id}/media-users/{media_server_id}":
		return auditResourceMediaServers
	case "/api/v1/metadata/providers/tmdb/key":
		return auditResourceMetadataSettings
	case "/api/v1/request-profiles", "/api/v1/request-profiles/{id}":
		return auditResourceRequestProfiles
	case "/api/v1/requests", "/api/v1/requests/{id}", "/api/v1/requests/{id}/progress",
		"/api/v1/requests/{id}/approve", "/api/v1/requests/{id}/decline":
		return auditResourceRequests
	case "/api/v1/roles/{id}/request-quota", "/api/v1/accounts/{id}/request-quota":
		return auditResourceRequestQuotas
	default:
		return auditResourceRouteUnmatched
	}
}

func inventoryPattern(path string) string {
	for _, route := range apiRouteInventory {
		if routePathMatches(route.path, path) {
			return route.path
		}
	}
	return ""
}

func routePathMatches(pattern, path string) bool {
	const maxRouteSegments = 16
	patternParts := strings.Split(strings.Trim(pattern, "/"), "/")
	pathParts := strings.Split(strings.Trim(path, "/"), "/")
	if len(patternParts) != len(pathParts) || len(patternParts) > maxRouteSegments {
		return false
	}
	for index := range maxRouteSegments {
		if index >= len(patternParts) {
			return true
		}
		part := patternParts[index]
		placeholder := strings.HasPrefix(part, "{") && strings.HasSuffix(part, "}")
		if pathParts[index] == "" || (!placeholder && part != pathParts[index]) {
			return false
		}
	}
	return false
}

// ListenAndServe starts serving and blocks until the server is shut down. It
// returns http.ErrServerClosed on a clean Shutdown, which the caller treats as
// the expected outcome.
func (s *Server) ListenAndServe() error {
	return s.httpServer.ListenAndServe()
}

// Serve accepts HTTP connections from an already-bound listener. Runtime uses
// this form so readiness is signaled only after binding succeeds.
func (s *Server) Serve(listener net.Listener) error {
	return s.httpServer.Serve(listener)
}

// Addr returns the configured listen address.
func (s *Server) Addr() string { return s.httpServer.Addr }

// SetReady flips the readiness flag. The shutdown sequence calls SetReady(false)
// before draining so the load balancer stops routing new traffic.
func (s *Server) SetReady(ready bool) { s.readiness.Set(ready) }

// Shutdown gracefully stops the server, draining in-flight requests bounded by
// ctx's deadline. The caller MUST pass a fresh context.WithTimeout, not the
// cancelled root context.
func (s *Server) Shutdown(ctx context.Context) error {
	return s.httpServer.Shutdown(ctx)
}

// Close prevents new handlers and force-closes active HTTP connections.
func (s *Server) Close() error {
	s.handlers.seal()
	return s.httpServer.Close()
}

// WaitHandlers waits until every handler has returned.
func (s *Server) WaitHandlers(ctx context.Context) error { return s.handlers.wait(ctx) }

// Handler exposes the composed handler for in-process tests (httptest).
func (s *Server) Handler() http.Handler { return s.httpServer.Handler }
