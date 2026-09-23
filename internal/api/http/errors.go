// Package http is the JSON API transport adapter for Bloom. It is a translation
// layer only: it decodes and validates input, calls one core method, maps
// domain errors to HTTP status codes, and encodes the response. It depends on
// internal/core, internal/config, internal/httputil, and internal/telemetry;
// it never touches SQL directly, per the handbook's services/http-services.md.
package http

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/httputil"
)

// Machine-readable top-level error codes. They are a documented part of the
// wire contract (api/openapi.yaml) and evolve additively, per the handbook's
// foundations/contracts-and-compatibility.md: add codes, never repurpose or
// silently drop one. They are deliberately NOT the HTTP status.
const (
	codeNotFound             = "not_found"
	codeAlreadyExists        = "already_exists"
	codeInvalidArgument      = "invalid_argument"
	codeUnavailable          = "unavailable"
	codeInternal             = "internal"
	codeLoginRejected        = "invalid_credentials"
	codeUnauthorized         = "unauthorized"
	codeValidationFailed     = "validation_failed"
	codeRateLimited          = "rate_limited"
	codeCSRFRejected         = "csrf_rejected"
	codeUnsupportedMediaType = "unsupported_media_type"
	codeMethodNotAllowed     = "method_not_allowed"
	codeForbidden            = "forbidden"
	codeMediaServerFailure   = "media_server_failure"
)

// errorClass is the boundary mapping from a domain error to its documented
// (status, code) pair. This is the single place domain semantics become
// transport semantics; handlers do not branch on errors themselves beyond
// calling writeError, which calls this.
func errorClass(err error) (status int, code string) {
	switch {
	case isMediaServerErrorKind(err, core.MediaServerSaturated):
		return http.StatusServiceUnavailable, codeUnavailable
	case isMediaServerError(err):
		return http.StatusBadGateway, codeMediaServerFailure
	case errors.Is(err, core.ErrNotFound):
		return http.StatusNotFound, codeNotFound
	case errors.Is(err, core.ErrAlreadyExists):
		return http.StatusConflict, codeAlreadyExists
	case errors.Is(err, core.ErrInvalidArgument):
		return http.StatusBadRequest, codeInvalidArgument
	case errors.Is(err, core.ErrInvalidCredentials):
		return http.StatusUnauthorized, codeLoginRejected
	case errors.Is(err, errAuthenticationRequired):
		return http.StatusUnauthorized, codeUnauthorized
	case errors.Is(err, core.ErrForbidden):
		return http.StatusForbidden, codeForbidden
	case errors.Is(err, errRateLimited):
		return http.StatusTooManyRequests, codeRateLimited
	case errors.Is(err, errAuthenticationBusy):
		return http.StatusServiceUnavailable, codeUnavailable
	case errors.Is(err, errNotReady):
		return http.StatusServiceUnavailable, codeUnavailable
	case errors.Is(err, errUnsupportedMediaType):
		return http.StatusUnsupportedMediaType, codeUnsupportedMediaType
	case errors.Is(err, errMethodNotAllowed):
		return http.StatusMethodNotAllowed, codeMethodNotAllowed
	default:
		return http.StatusInternalServerError, codeInternal
	}
}

func isMediaServerError(err error) bool {
	var mediaErr *core.MediaServerError
	return errors.As(err, &mediaErr)
}

func isMediaServerErrorKind(err error, kind core.MediaServerErrorKind) bool {
	var mediaErr *core.MediaServerError
	return errors.As(err, &mediaErr) && mediaErr.Kind == kind
}

// errNotReady is the transport-local sentinel behind a 503 from /readyz: the
// readiness flag is down or the database ping failed. The underlying cause is
// preserved in the chain for the boundary log, never for the client.
var errNotReady = errors.New("not ready")

// writeError maps err to a (status, code) pair, logs it once at the boundary,
// and encodes the single structured envelope. Internal errors are logged with
// detail but the client only sees a generic message so implementation details
// do not leak. The request_id is echoed into the body (not only the header) so
// a client can quote it for correlation.
func writeError(w http.ResponseWriter, r *http.Request, logger *slog.Logger, err error) {
	status, code := errorClass(err)

	// Log once, here at the boundary, with stable fields. 5xx is the unexpected
	// class and gets error level; client errors are info.
	attrs := []any{
		"method", r.Method,
		"route", routePattern(r),
		"status", status,
		"code", code,
		"error", err.Error(),
	}
	if status >= http.StatusInternalServerError {
		logger.ErrorContext(r.Context(), "request failed", attrs...)
	} else {
		logger.InfoContext(r.Context(), "request rejected", attrs...)
	}

	writeJSON(w, r, logger, status, httputil.ErrorResponse{
		Code:      code,
		Message:   safeMessage(status, err),
		RequestID: requestIDFrom(r.Context()),
	})
}

// safeMessage returns a client-safe human message. Client-class (4xx) errors
// carry an actionable message; server-class (5xx, including 503) errors are
// opaque: the detail goes to the boundary log under the request_id, never the
// body.
func safeMessage(status int, err error) string {
	if status >= http.StatusInternalServerError {
		return http.StatusText(status)
	}
	return err.Error()
}

// writeJSON encodes v with the given status and logs a late encode failure once
// so a broken connection or marshal bug is observable rather than silent. The
// status and headers are already committed, so nothing can be sent to the
// client at that point.
func writeJSON(w http.ResponseWriter, r *http.Request, logger *slog.Logger, status int, v any) {
	if err := httputil.WriteJSON(w, status, v); err != nil {
		logger.WarnContext(r.Context(), "write response body failed",
			"route", routePattern(r),
			"status", status,
			"error", err.Error(),
		)
	}
}
