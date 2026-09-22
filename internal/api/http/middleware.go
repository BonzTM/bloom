package http

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"
	"time"

	"go.opentelemetry.io/otel/trace"

	"github.com/BonzTM/bloom/internal/httputil"
)

// ctxKey is an unexported context key type so values cannot collide with keys
// from other packages (revive: context-keys-type).
type ctxKey int

const requestIDKey ctxKey = iota

// requestIDFrom returns the request ID stored in ctx, or "" if absent.
func requestIDFrom(ctx context.Context) string {
	id, ok := ctx.Value(requestIDKey).(string)
	if !ok {
		return ""
	}
	return id
}

// routePattern returns the matched ServeMux route pattern for low-cardinality
// logging and metrics labels (never the raw path).
func routePattern(r *http.Request) string {
	if r.Pattern != "" {
		return r.Pattern
	}
	return "unmatched"
}

// statusRecorder captures the response status for access logging without
// buffering the body.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

// requestIDMiddleware attaches a request ID (from the inbound header or freshly
// generated) to the context and echoes it in the response, so logs and clients
// can correlate a request. It is the OUTERMOST layer so the id is on the
// context before recovery runs.
func requestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-ID")
		if id == "" || len(id) > maxRequestIDLen {
			id = newRequestID()
		}
		w.Header().Set("X-Request-ID", id)
		ctx := context.WithValue(r.Context(), requestIDKey, id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// maxRequestIDLen bounds a client-supplied X-Request-ID so an attacker cannot
// stuff kilobytes into every log line; longer values are replaced.
const maxRequestIDLen = 128

// recoverMiddleware converts a panic in any inner handler or middleware into a
// 500 so a single bad request cannot crash the process. It logs the panic once
// with the request context.
func recoverMiddleware(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rec := recover(); rec != nil {
					logger.ErrorContext(r.Context(), "panic recovered",
						"method", r.Method,
						"route", routePattern(r),
						"panic", rec,
					)
					// Opaque 5xx envelope: a machine-readable code and a generic
					// message, with the request_id so the client can quote it. The
					// panic detail stays in the log above, never in the body.
					writeJSON(w, r, logger, http.StatusInternalServerError, httputil.ErrorResponse{
						Code:      codeInternal,
						Message:   http.StatusText(http.StatusInternalServerError),
						RequestID: requestIDFrom(r.Context()),
					})
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// securityHeadersMiddleware sets the response headers every Bloom response
// carries, per the handbook's services/web-apps.md ### Security Headers. The
// CSP here is the API's (nothing may be loaded from a JSON response); the SPA
// handler from ADR 0002 will set its own, looser, document CSP on HTML
// responses when it lands.
func securityHeadersMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		h.Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
		h.Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

// loggingMiddleware emits one access log line per request with method, route
// pattern, status, and duration, plus the request ID and (when present) the
// W3C trace and span IDs pulled from the request's span context. It records
// metrics with low-cardinality labels (route pattern + status class) and, when
// the metrics impl supports it, a latency histogram.
func loggingMiddleware(logger *slog.Logger, metrics requestMetrics) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}

			next.ServeHTTP(rec, r)

			route := routePattern(r)
			class := statusClass(rec.status)
			elapsed := time.Since(start)

			// The route pattern is only known after the inner mux matched, so the
			// span (started by otelhttp before routing) is renamed here to the
			// low-cardinality "METHOD /pattern" rather than the raw path.
			span := trace.SpanFromContext(r.Context())
			if span.IsRecording() {
				span.SetName(r.Method + " " + route)
			}

			attrs := []any{
				"method", r.Method,
				"route", route,
				"status", rec.status,
				"duration_ms", elapsed.Milliseconds(),
				"request_id", requestIDFrom(r.Context()),
			}
			if sc := span.SpanContext(); sc.IsValid() {
				attrs = append(attrs, "trace_id", sc.TraceID().String(), "span_id", sc.SpanID().String())
			}
			logger.InfoContext(r.Context(), "request", attrs...)

			metrics.IncRequest(route, class)
			if obs, ok := metrics.(latencyObserver); ok {
				obs.ObserveRequest(route, class, elapsed.Seconds())
			}
		})
	}
}

// requestMetrics is the subset of telemetry.Metrics the logging middleware
// needs. Defined at the consumer to keep the dependency narrow.
type requestMetrics interface {
	IncRequest(routePattern, statusClass string)
}

// latencyObserver is the optional latency-histogram seam. The Prometheus-backed
// Metrics implements it; the no-op seam does not, so the middleware
// type-asserts before calling.
type latencyObserver interface {
	ObserveRequest(routePattern, statusClass string, seconds float64)
}

// statusClass collapses a status code into a low-cardinality class label
// ("2xx", "4xx", ...) so metric cardinality stays bounded.
func statusClass(status int) string {
	switch {
	case status < 200:
		return "1xx"
	case status < 300:
		return "2xx"
	case status < 400:
		return "3xx"
	case status < 500:
		return "4xx"
	default:
		return "5xx"
	}
}

func newRequestID() string {
	var b [16]byte
	// crypto/rand.Read never returns an error on supported platforms; on the
	// off chance it does, fall back to a fixed marker rather than panicking in
	// a hot path.
	if _, err := rand.Read(b[:]); err != nil {
		return "req-unknown"
	}
	return hex.EncodeToString(b[:])
}
