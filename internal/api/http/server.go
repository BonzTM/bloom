package http

import (
	"context"
	"log/slog"
	"net/http"
	"net/netip"
	"time"

	"github.com/alexedwards/scs/v2"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"

	"github.com/BonzTM/bloom/internal/config"
	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/httputil"
	"github.com/BonzTM/bloom/internal/telemetry"
)

// Pinger is the readiness dependency: the database pool in production
// (*sql.DB satisfies it), a fake in tests. Defined here, at the consumer, so
// the transport does not import the storage package.
type Pinger interface {
	// PingContext reports whether the dependency can serve traffic.
	PingContext(ctx context.Context) error
}

// Server owns the HTTP listener, mux, and middleware wiring. It holds the
// dependencies the handlers need and the readiness flag the shutdown sequence
// flips. It never stores a request context.
type Server struct {
	httpServer            *http.Server
	logger                *slog.Logger
	metrics               telemetry.Metrics
	metricsHandler        http.Handler
	readiness             *telemetry.Readiness
	pinger                Pinger
	web                   http.Handler
	maxBodyBytes          int64
	identity              core.IdentityProvider
	accounts              core.AccountStore
	sessions              *scs.SessionManager
	audit                 auditEmitter
	auditFailureMetrics   telemetry.AuditFailureMetrics
	usernameAuditKey      [32]byte
	loginLimiter          *loginLimiter
	passwordVerifications chan struct{}
	authOperationTimeout  time.Duration
	trustedProxyCIDRs     []netip.Prefix
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
}

// metricsExposer is the optional seam a Metrics implementation can satisfy to
// publish a scrape endpoint. The Prometheus adapter implements it; the no-op
// seam does not, so /metrics is mounted only when a real registry is wired.
type metricsExposer interface {
	Handler() http.Handler
}

// New constructs a Server with hardened timeouts and the standard middleware
// chain. The readiness flag is shared with the shutdown sequence so it can flip
// the server to unready before draining.
func New(cfg config.HTTPConfig, deps Deps) *Server {
	s := &Server{
		logger:              deps.Logger,
		metrics:             deps.Metrics,
		readiness:           deps.Readiness,
		pinger:              deps.Pinger,
		web:                 deps.Web,
		maxBodyBytes:        cfg.MaxBodyBytes,
		identity:            deps.Identity,
		accounts:            deps.Accounts,
		sessions:            deps.Sessions,
		audit:               deps.Audit,
		auditFailureMetrics: telemetry.NopMetrics{},
		trustedProxyCIDRs:   deps.Auth.TrustedProxyCIDRs,
	}
	if s.audit == nil {
		s.audit = telemetry.NopAuditLogger()
	}
	if authDependenciesPresent(deps) {
		s.usernameAuditKey = deriveUsernameAuditKey(deps.AuditCorrelationKey)
		s.loginLimiter = newLoginLimiter(
			deps.Clock, deps.Auth.LoginRateRefillInterval,
			deps.Auth.LoginRateBurst, deps.Auth.LoginRateMaxKeys,
		)
		s.passwordVerifications = make(chan struct{}, deps.Auth.LoginMaxConcurrent)
		s.authOperationTimeout = derivedAuthOperationTimeout(cfg.WriteTimeout)
	}
	if metrics, ok := deps.Metrics.(telemetry.AuditFailureMetrics); ok {
		s.auditFailureMetrics = metrics
	}
	if exposer, ok := deps.Metrics.(metricsExposer); ok {
		s.metricsHandler = exposer.Handler()
	}

	s.httpServer = &http.Server{
		Addr:    cfg.Addr,
		Handler: s.routes(),
		// Server-hardening defaults, per the handbook's services/http-services.md.
		ReadHeaderTimeout: cfg.ReadHeaderTimeout,
		ReadTimeout:       cfg.ReadTimeout,
		WriteTimeout:      cfg.WriteTimeout,
		IdleTimeout:       cfg.IdleTimeout,
	}
	return s
}

func authDependenciesPresent(deps Deps) bool {
	return deps.Identity != nil &&
		deps.Accounts != nil &&
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
	apiMux.HandleFunc("GET /api/v1/version", s.handleVersion)
	if s.loginLimiter != nil {
		apiMux.Handle("/api/v1/auth/login", s.authRoute(http.MethodPost, s.sessionHandler(http.HandlerFunc(s.handleLogin), false)))
		apiMux.Handle("/api/v1/auth/logout", s.authRoute(http.MethodPost, s.sessionHandler(http.HandlerFunc(s.handleLogout), true)))
		apiMux.Handle("/api/v1/auth/me", s.authRoute(http.MethodGet, s.sessionHandler(http.HandlerFunc(s.handleMe), true)))
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
	apiHandler = otelhttp.NewHandler(apiHandler, "http.server")

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

func (s *Server) authRoute(method string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != method {
			w.Header().Set("Allow", method)
			writeError(w, r, s.logger, errMethodNotAllowed)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) sessionHandler(handler http.Handler, accountRequired bool) http.Handler {
	if accountRequired {
		handler = s.sessionAccountMiddleware(handler)
	}
	return s.loadAndSaveSessions(handler)
}

func (s *Server) handleCSRFRejected(w http.ResponseWriter, r *http.Request) {
	s.metrics.IncCSRFRejection()
	s.emitAuthAudit(r, "anonymous", "", "auth.csrf", csrfAuditResource(r.URL.Path), telemetry.AuditFailure, "cross_origin", clientIP(r, s.trustedProxyCIDRs))
	writeJSON(w, r, s.logger, http.StatusForbidden, httputil.ErrorResponse{
		Code: codeCSRFRejected, Message: "cross-origin request rejected",
		RequestID: requestIDFrom(r.Context()),
	})
}

func csrfAuditResource(path string) string {
	switch path {
	case "/api/v1/auth/login":
		return auditResourceAuthLogin
	case "/api/v1/auth/logout":
		return auditResourceAuthLogout
	case "/api/v1/auth/me":
		return auditResourceAuthMe
	default:
		return auditResourceRouteUnmatched
	}
}

// ListenAndServe starts serving and blocks until the server is shut down. It
// returns http.ErrServerClosed on a clean Shutdown, which the caller treats as
// the expected outcome.
func (s *Server) ListenAndServe() error {
	return s.httpServer.ListenAndServe()
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

// Handler exposes the composed handler for in-process tests (httptest).
func (s *Server) Handler() http.Handler { return s.httpServer.Handler }
