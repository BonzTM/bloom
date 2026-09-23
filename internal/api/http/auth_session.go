package http

import (
	"net/http"

	"github.com/BonzTM/bloom/internal/telemetry"
)

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	account, ok := accountFrom(r.Context())
	if !ok {
		writeError(w, r, s.logger, errAuthenticationRequired)
		return
	}
	writeJSON(w, r, s.logger, http.StatusOK, authResponse{Account: accountDTO(account)})
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
