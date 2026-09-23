package http

import (
	"context"
	"errors"
	"net/http"

	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/telemetry"
)

const (
	sessionCookieName   = "bloom_session"
	sessionAccountIDKey = "account_id"
)

var (
	errAuthenticationRequired = errors.New("authentication required")
	errRateLimited            = errors.New("too many login attempts")
	errAuthenticationBusy     = errors.New("authentication capacity exhausted")
	errMethodNotAllowed       = errors.New("method not allowed")
)

type auditEmitter interface {
	Emit(ctx context.Context, event telemetry.AuditEvent) error
}

func accountFrom(ctx context.Context) (core.Account, bool) {
	account, ok := ctx.Value(accountKey).(core.Account)
	return account, ok
}

func (s *Server) sessionAccountMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !sessionRequired(r) {
			next.ServeHTTP(w, r)
			return
		}
		account, reason, err := s.loadSessionAccount(r.Context())
		if reason != "" {
			actor := account.ID
			if actor == "" {
				actor = "anonymous"
			}
			s.emitAuthAudit(r, actor, "", "auth.session", routeResource(r), telemetry.AuditFailure, reason, clientIP(r, s.trustedProxyCIDRs))
		}
		if err != nil {
			writeError(w, r, s.logger, err)
			return
		}
		ctx := context.WithValue(r.Context(), accountKey, account)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func sessionRequired(r *http.Request) bool {
	return (r.Method == http.MethodGet && r.URL.Path == "/api/v1/auth/me") ||
		(r.Method == http.MethodPost && r.URL.Path == "/api/v1/auth/logout")
}

func (s *Server) loadSessionAccount(ctx context.Context) (core.Account, string, error) {
	state := sessionState(ctx)
	id := s.sessions.GetString(ctx, sessionAccountIDKey)
	if id == "" {
		if state != nil && state.inbound {
			if state.resolved {
				return core.Account{}, "invalid_session", s.destroyInvalidSession(ctx)
			}
			state.clearCookie = true
			return core.Account{}, "invalid_session", errAuthenticationRequired
		}
		return core.Account{}, "missing_session", errAuthenticationRequired
	}
	accountCtx, cancel := context.WithTimeout(ctx, s.authOperationTimeout)
	defer cancel()
	account, err := s.accounts.GetAccount(accountCtx, id)
	if errors.Is(err, core.ErrNotFound) {
		return core.Account{ID: id}, "account_deleted", s.destroyInvalidSession(ctx)
	}
	if err != nil {
		return core.Account{ID: id}, "internal_error", err
	}
	if account.Disabled {
		return account, "account_disabled", s.destroyInvalidSession(ctx)
	}
	return account, "", nil
}

func (s *Server) destroyInvalidSession(ctx context.Context) error {
	if err := s.sessions.Destroy(ctx); err != nil {
		return err
	}
	return errAuthenticationRequired
}
