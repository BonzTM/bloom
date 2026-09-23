package http

import (
	"context"
	"net/http"
	"slices"

	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/telemetry"
)

// RequirePermission rejects requests that have no authenticated account or do
// not carry permission in the per-request effective set.
func (s *Server) RequirePermission(permission core.CatalogPermission) func(http.Handler) http.Handler {
	permissionID := permission.Permission()
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			account, authenticated := accountFrom(r.Context())
			if !authenticated {
				s.authorizationMetrics.IncAuthorizationDenial(permission)
				s.emitAuthorizationDenial(r, "anonymous", permissionID, telemetry.AuditFailure, "missing_session")
				writeError(w, r, s.logger, errAuthenticationRequired)
				return
			}
			permissions, loaded := permissionsFrom(r.Context())
			if !loaded || !slices.Contains(permissions, permissionID) {
				s.authorizationMetrics.IncAuthorizationDenial(permission)
				s.emitAuthorizationDenial(r, account.ID, permissionID, telemetry.AuditDenied, "permission_missing")
				writeError(w, r, s.logger, core.ErrForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// RequireAnyPermission allows an authenticated account holding at least one listed permission.
func (s *Server) RequireAnyPermission(permissions []core.Permission) func(http.Handler) http.Handler {
	denialPermission, metricAllowed := firstCatalogPermission(permissions)
	permissionID := core.Permission("")
	if len(permissions) > 0 {
		permissionID = permissions[0]
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			account, authenticated := accountFrom(r.Context())
			if !authenticated {
				if metricAllowed {
					s.authorizationMetrics.IncAuthorizationDenial(denialPermission)
				}
				s.emitAuthorizationDenial(r, "anonymous", permissionID, telemetry.AuditFailure, "missing_session")
				writeError(w, r, s.logger, errAuthenticationRequired)
				return
			}
			effective, loaded := permissionsFrom(r.Context())
			for _, required := range permissions {
				if loaded && slices.Contains(effective, required) {
					next.ServeHTTP(w, r)
					return
				}
			}
			if metricAllowed {
				s.authorizationMetrics.IncAuthorizationDenial(denialPermission)
			}
			s.emitAuthorizationDenial(r, account.ID, permissionID, telemetry.AuditDenied, "permission_missing")
			writeError(w, r, s.logger, core.ErrForbidden)
		})
	}
}

func firstCatalogPermission(permissions []core.Permission) (core.CatalogPermission, bool) {
	if len(permissions) == 0 {
		return core.CatalogPermission{}, false
	}
	permission, err := core.NewCatalogPermission(permissions[0])
	return permission, err == nil
}

func (s *Server) emitAuthorizationDenial(
	r *http.Request,
	actor string,
	permission core.Permission,
	result telemetry.AuditResult,
	reason string,
) {
	err := s.audit.Emit(r.Context(), telemetry.AuditEvent{
		Actor: actor, Action: "auth.permission", Resource: routeResource(r),
		Permission: string(permission), Result: result, Reason: reason,
		Source: clientIP(r, s.trustedProxyCIDRs), RequestID: requestIDFrom(r.Context()),
	})
	if err == nil {
		return
	}
	s.auditFailureMetrics.IncAuditWriteFailure()
	s.logger.ErrorContext(r.Context(), "write audit event", "error", err, "action", "auth.permission")
}

func (s *Server) loadAuthorizationSnapshot(ctx context.Context, accountID string) (core.AuthorizationSnapshot, error) {
	authCtx, cancel := context.WithTimeout(ctx, s.authOperationTimeout)
	defer cancel()
	snapshot, err := s.authorizer.Snapshot(authCtx, accountID)
	if err != nil {
		return core.AuthorizationSnapshot{}, err
	}
	return snapshot, authCtx.Err()
}
