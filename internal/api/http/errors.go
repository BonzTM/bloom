// Package http is the JSON API transport adapter for Bloom. It is a translation
// layer only: it decodes and validates input, calls one core method, maps
// domain errors to HTTP status codes, and encodes the response. It depends on
// internal/core, internal/config, internal/httputil, and internal/telemetry;
// it never touches SQL directly, per the handbook's services/http-services.md.
package http

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/httputil"
)

// Machine-readable top-level error codes. They are a documented part of the
// wire contract (api/openapi.yaml) and evolve additively, per the handbook's
// foundations/contracts-and-compatibility.md: add codes, never repurpose or
// silently drop one. They are deliberately NOT the HTTP status.
const (
	codeNotFound                = "not_found"
	codeAlreadyExists           = "already_exists"
	codeInvalidArgument         = "invalid_argument"
	codeUnavailable             = "unavailable"
	codeInternal                = "internal"
	codeLoginRejected           = "invalid_credentials"
	codeUnauthorized            = "unauthorized"
	codeValidationFailed        = "validation_failed"
	codeRateLimited             = "rate_limited"
	codeCSRFRejected            = "csrf_rejected"
	codeUnsupportedMediaType    = "unsupported_media_type"
	codeMethodNotAllowed        = "method_not_allowed"
	codeForbidden               = "forbidden"
	codeMediaServerFailure      = "media_server_failure"
	codeOIDCRejected            = "oidc_rejected"
	codeUsernameUnavailable     = "username_unavailable"
	codeQuotaExceeded           = "request_quota_exceeded"
	codeMetadataNotConfigured   = "metadata_not_configured"
	codeMetadataProviderFailure = "metadata_provider_failure"
	codeProfileInUse            = "request_profile_in_use"
	codeInvalidTransition       = "invalid_request_transition"
	codeDownloadManagerFailure  = "download_manager_failure"
	codeDownloadManagerNotFound = "download_manager_not_found"
	codeDownloadManagerInUse    = "download_manager_in_use"
	codeNotificationFailure     = "notification_channel_failure"
	codeMediaUserNotLinked      = "media_user_not_linked"
	codeInviteFailureLeased     = "invite_provisioning_failure_leased"
	oidcFailureClassification   = "OpenID Connect callback failure"
	maxLoggedErrorBytes         = 512
)

// errorClass is the boundary mapping from a domain error to its documented
// (status, code) pair. This is the single place domain semantics become
// transport semantics; handlers do not branch on errors themselves beyond
// calling writeError, which calls this.
func errorClass(err error) (status int, code string) {
	var notificationErr *core.NotificationError
	if errors.As(err, &notificationErr) || errors.Is(err, core.ErrNotificationChannelFailure) {
		return http.StatusBadGateway, codeNotificationFailure
	}
	if status, code, ok := specializedErrorClass(err); ok {
		return status, code
	}
	switch {
	case errors.Is(err, core.ErrQuotaExceeded):
		return http.StatusUnprocessableEntity, codeQuotaExceeded
	case errors.Is(err, core.ErrMetadataNotConfigured):
		return http.StatusServiceUnavailable, codeMetadataNotConfigured
	case errors.Is(err, core.ErrMetadataUnauthorized), errors.Is(err, core.ErrMetadataMalformed):
		return http.StatusBadGateway, codeMetadataProviderFailure
	case errors.Is(err, core.ErrMetadataUnavailable):
		return http.StatusServiceUnavailable, codeMetadataProviderFailure
	case errors.Is(err, core.ErrProfileInUse):
		return http.StatusConflict, codeProfileInUse
	case errors.Is(err, core.ErrInvalidTransition):
		return http.StatusConflict, codeInvalidTransition
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
	case errors.Is(err, core.ErrOIDCRejected):
		return http.StatusUnauthorized, codeOIDCRejected
	case errors.Is(err, core.ErrOIDCProviderUnavailable):
		return http.StatusServiceUnavailable, codeUnavailable
	case errors.Is(err, core.ErrOIDCProvisioningDisabled):
		return http.StatusForbidden, codeForbidden
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

func specializedErrorClass(err error) (int, string, bool) {
	if status, code, ok := inviteErrorClass(err); ok {
		return status, code, true
	}
	if errors.Is(err, core.ErrMediaUserNotLinked) {
		return http.StatusNotFound, codeMediaUserNotLinked, true
	}
	return downloadManagerErrorClass(err)
}

func inviteErrorClass(err error) (int, string, bool) {
	switch {
	case errors.Is(err, core.ErrInviteProvisioningPending):
		return http.StatusBadGateway, codeMediaServerFailure, true
	case errors.Is(err, core.ErrInviteProvisioningFailureLeased):
		return http.StatusConflict, codeInviteFailureLeased, true
	case isMediaUserNameError(err):
		return http.StatusConflict, codeUsernameUnavailable, true
	case errors.Is(err, core.ErrInviteUnavailable):
		return http.StatusNotFound, codeNotFound, true
	default:
		return 0, "", false
	}
}

func downloadManagerErrorClass(err error) (int, string, bool) {
	switch {
	case errors.Is(err, core.ErrDownloadItemMissing), isDownloadManagerErrorKind(err, core.DownloadManagerNotFound):
		return http.StatusNotFound, codeDownloadManagerNotFound, true
	case errors.Is(err, core.ErrDownloadManagerInUse):
		return http.StatusConflict, codeDownloadManagerInUse, true
	case isDownloadManagerErrorKind(err, core.DownloadManagerUnavailable):
		return http.StatusServiceUnavailable, codeDownloadManagerFailure, true
	case isDownloadManagerError(err):
		return http.StatusBadGateway, codeDownloadManagerFailure, true
	default:
		return 0, "", false
	}
}

func isMediaUserNameError(err error) bool {
	var nameErr *core.MediaUserNameError
	return errors.As(err, &nameErr)
}

func isMediaServerError(err error) bool {
	var mediaErr *core.MediaServerError
	return errors.As(err, &mediaErr)
}

func isMediaServerErrorKind(err error, kind core.MediaServerErrorKind) bool {
	var mediaErr *core.MediaServerError
	return errors.As(err, &mediaErr) && mediaErr.Kind == kind
}

func isDownloadManagerError(err error) bool {
	var managerErr *core.DownloadManagerError
	return errors.As(err, &managerErr)
}

func isDownloadManagerErrorKind(err error, kind core.DownloadManagerErrorKind) bool {
	var managerErr *core.DownloadManagerError
	return errors.As(err, &managerErr) && managerErr.Kind == kind
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
	status, code := logRequestError(r, logger, err)
	setDownloadManagerRetryAfter(w, err)
	writeJSON(w, r, logger, status, httputil.ErrorResponse{
		Code:      code,
		Message:   safeMessage(status, err),
		RequestID: requestIDFrom(r.Context()),
	})
}

func logRequestError(r *http.Request, logger *slog.Logger, err error) (int, string) {
	return logRequestErrorWithAttrs(r, logger, err, safeErrorLogAttrs(err))
}

func writeOIDCError(w http.ResponseWriter, r *http.Request, logger *slog.Logger, err error) {
	status, code := logOIDCRequestError(r, logger, err)
	writeJSON(w, r, logger, status, httputil.ErrorResponse{
		Code: code, Message: safeMessage(status, err), RequestID: requestIDFrom(r.Context()),
	})
}

func logOIDCRequestError(r *http.Request, logger *slog.Logger, err error) (int, string) {
	return logRequestErrorWithAttrs(r, logger, err, safeOIDCErrorLogAttrs(err))
}

func logRequestErrorWithAttrs(r *http.Request, logger *slog.Logger, err error, errorAttrs []any) (int, string) {
	status, code := errorClass(err)
	// Log once, here at the boundary, with stable fields. 5xx is the unexpected
	// class and gets error level; client errors are info.
	attrs := make([]any, 0, 12)
	attrs = append(attrs,
		"method", r.Method,
		"route", routePattern(r),
		"status", status,
		"code", code,
	)
	attrs = append(attrs, errorAttrs...)
	if status >= http.StatusInternalServerError {
		logger.ErrorContext(r.Context(), "request failed", attrs...)
	} else {
		logger.InfoContext(r.Context(), "request rejected", attrs...)
	}
	return status, code
}

func safeOIDCErrorLogAttrs(err error) []any {
	attrs := make([]any, 0, 8)
	attrs = append(attrs,
		"error_type", fmt.Sprintf("%T", err),
		"error", oidcFailureClassification,
	)
	cause := errors.Unwrap(err)
	if cause == nil {
		return attrs
	}
	return append(attrs,
		"cause_type", fmt.Sprintf("%T", cause),
		"cause_message", "[redacted]",
	)
}

func safeErrorLogAttrs(err error) []any {
	attrs := make([]any, 0, 8)
	attrs = append(attrs,
		"error_type", fmt.Sprintf("%T", err),
		"error", safeErrorLogMessage(err.Error()),
	)
	cause := errors.Unwrap(err)
	if cause == nil {
		return attrs
	}
	return append(attrs,
		"cause_type", fmt.Sprintf("%T", cause),
		"cause_message", safeErrorLogMessage(cause.Error()),
	)
}

func safeErrorLogMessage(message string) string {
	if !utf8.ValidString(message) {
		return "[invalid error text]"
	}
	if sensitiveErrorText(message) {
		return "[redacted]"
	}
	var clean strings.Builder
	clean.Grow(min(len(message), maxLoggedErrorBytes))
	for _, char := range message {
		if unicode.IsControl(char) {
			if clean.Len() == maxLoggedErrorBytes {
				break
			}
			clean.WriteByte(' ')
		} else {
			if clean.Len()+utf8.RuneLen(char) > maxLoggedErrorBytes {
				break
			}
			clean.WriteRune(char)
		}
	}
	return clean.String()
}

func sensitiveErrorText(message string) bool {
	lower := strings.ToLower(message)
	for _, marker := range []string{"token", "secret", "password", "credential", "authorization", "assertion", "response:"} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	for word := range strings.FieldsSeq(message) {
		word = strings.Trim(word, `"'()[]{}<>,;:`)
		if len(word) >= 32 || (len(word) >= 16 && strings.Count(word, ".") >= 2) {
			return true
		}
	}
	return false
}

func setDownloadManagerRetryAfter(w http.ResponseWriter, err error) {
	var managerErr *core.DownloadManagerError
	if !errors.As(err, &managerErr) || !managerErr.Retryable || managerErr.RetryAfter <= 0 {
		return
	}
	delay := min(managerErr.RetryAfter, 30*time.Second)
	seconds := max(1, int((delay+time.Second-1)/time.Second))
	w.Header().Set("Retry-After", strconv.Itoa(seconds))
}

// safeMessage returns a client-safe human message. Client-class (4xx) errors
// carry an actionable message; server-class (5xx, including 503) errors are
// opaque: the detail goes to the boundary log under the request_id, never the
// body.
func safeMessage(status int, err error) string {
	if errors.Is(err, core.ErrInviteUnavailable) {
		return core.ErrInviteUnavailable.Error()
	}
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
