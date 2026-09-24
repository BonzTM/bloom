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

func permissionsFrom(ctx context.Context) ([]core.Permission, bool) {
	permissions, ok := ctx.Value(permissionsKey).([]core.Permission)
	return permissions, ok
}

func authorizationSnapshotFrom(ctx context.Context) (core.AuthorizationSnapshot, bool) {
	snapshot, ok := ctx.Value(authorizationSnapshotKey).(core.AuthorizationSnapshot)
	return snapshot, ok
}

func (s *Server) sessionAccountMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		account, reason, err := s.loadSessionAccount(r.Context())
		if reason != "" {
			actor := account.ID
			if actor == "" {
				actor = "anonymous"
			}
			s.emitAuthAudit(r, actor, "auth.session", routeResource(r), telemetry.AuditFailure, reason, clientIP(r, s.trustedProxyCIDRs))
		}
		if err != nil {
			writeError(w, r, s.logger, err)
			return
		}
		ctx := context.WithValue(r.Context(), accountKey, account)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (s *Server) optionalSessionAccountMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		state := sessionState(r.Context())
		if state == nil || !state.inbound {
			next.ServeHTTP(w, r)
			return
		}
		account, reason, err := s.loadSessionAccount(r.Context())
		if err != nil {
			actor := account.ID
			if actor == "" {
				actor = "anonymous"
			}
			s.emitAuthAudit(r, actor, "auth.session", routeResource(r), telemetry.AuditFailure, reason, clientIP(r, s.trustedProxyCIDRs))
			writeError(w, r, s.logger, err)
			return
		}
		ctx := context.WithValue(r.Context(), accountKey, account)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (s *Server) permissionSetMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		account, ok := accountFrom(r.Context())
		if !ok {
			writeError(w, r, s.logger, errAuthenticationRequired)
			return
		}
		permissions, err := s.loadAccountPermissions(r.Context(), account.ID)
		if err != nil {
			writeError(w, r, s.logger, err)
			return
		}
		ctx := context.WithValue(r.Context(), permissionsKey, permissions)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (s *Server) authorizationSnapshotMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		account, ok := accountFrom(r.Context())
		if !ok {
			writeError(w, r, s.logger, errAuthenticationRequired)
			return
		}
		snapshot, err := s.loadAuthorizationSnapshot(r.Context(), account.ID)
		if err != nil {
			writeError(w, r, s.logger, err)
			return
		}
		ctx := context.WithValue(r.Context(), authorizationSnapshotKey, snapshot)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (s *Server) loadAccountPermissions(ctx context.Context, accountID string) ([]core.Permission, error) {
	authCtx, cancel := context.WithTimeout(ctx, s.authOperationTimeout)
	defer cancel()
	permissions, err := s.authorizer.Permissions(authCtx, accountID)
	if err != nil {
		return nil, err
	}
	return permissions, authCtx.Err()
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
