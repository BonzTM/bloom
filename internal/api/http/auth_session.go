package http

import (
	"net/http"
	"slices"

	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/telemetry"
)

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	account, ok := accountFrom(r.Context())
	if !ok {
		writeError(w, r, s.logger, errAuthenticationRequired)
		return
	}
	snapshot, ok := authorizationSnapshotFrom(r.Context())
	if !ok {
		writeError(w, r, s.logger, errAuthenticationRequired)
		return
	}
	writeJSON(w, r, s.logger, http.StatusOK, currentAccountDTO(account, snapshot))
}

func currentAccountDTO(account core.Account, snapshot core.AuthorizationSnapshot) currentAccountResponse {
	roles := append([]string{}, snapshot.RoleNames...)
	permissions := append([]core.Permission{}, snapshot.Permissions...)
	slices.Sort(roles)
	slices.Sort(permissions)
	return currentAccountResponse{
		Account: accountDTO(account), Roles: roles, Permissions: permissions,
	}
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	account, ok := accountFrom(r.Context())
	if !ok {
		writeError(w, r, s.logger, errAuthenticationRequired)
		return
	}
	if err := s.sessions.Destroy(r.Context()); err != nil {
		s.emitAuthAudit(r, account.ID, "", "auth.logout", accountResource(account.ID), telemetry.AuditFailure, "internal_error", clientIP(r, s.trustedProxyCIDRs))
		writeError(w, r, s.logger, err)
		return
	}
	s.emitAuthAudit(r, account.ID, "", "auth.logout", accountResource(account.ID), telemetry.AuditSuccess, "logged_out", clientIP(r, s.trustedProxyCIDRs))
	w.WriteHeader(http.StatusNoContent)
}
