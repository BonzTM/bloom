package http

import (
	"context"
	"log/slog"
	"net/http"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"

	"github.com/BonzTM/bloom/internal/config"
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
	httpServer     *http.Server
	logger         *slog.Logger
	metrics        telemetry.Metrics
	metricsHandler http.Handler
	readiness      *telemetry.Readiness
	pinger         Pinger
	web            http.Handler
	maxBodyBytes   int64
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
		logger:       deps.Logger,
		metrics:      deps.Metrics,
		readiness:    deps.Readiness,
		pinger:       deps.Pinger,
		web:          deps.Web,
		maxBodyBytes: cfg.MaxBodyBytes,
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

// routes builds the request handler. Health probes, the scrape endpoint, and
// the application API are split onto separate muxes so probes and /metrics are
// NOT access-logged, traced, or counted in request metrics, while every API
// request is. Order of execution on the API branch: request id ->
// security headers -> recovery -> otelhttp span -> [auth, per auth.go] ->
// logging+metrics -> body cap -> handler.
func (s *Server) routes() http.Handler {
	apiMux := http.NewServeMux()
	apiMux.HandleFunc("GET /api/v1/version", s.handleVersion)
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
	var h http.Handler = root
	h = recoverMiddleware(s.logger)(h)
	h = securityHeadersMiddleware(h)
	h = requestIDMiddleware(h)
	return h
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
