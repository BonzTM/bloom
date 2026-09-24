package http

import (
	"errors"
	"net/http"
	"time"

	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/httputil"
	"github.com/BonzTM/bloom/internal/telemetry"
)

type accountMediaUserResponse struct {
	AccountID       string                      `json:"account_id"`
	MediaServerID   string                      `json:"media_server_id"`
	MediaServerName string                      `json:"media_server_name"`
	MediaUserID     string                      `json:"media_user_id"`
	Username        string                      `json:"username"`
	Source          core.AccountMediaUserSource `json:"source"`
	CreatedAt       time.Time                   `json:"created_at"`
	UpdatedAt       time.Time                   `json:"updated_at"`
}

type accountMediaUsersResponse struct {
	Items []accountMediaUserResponse `json:"items"`
}

type setAccountMediaUserRequest struct {
	MediaUserID string `json:"media_user_id"`
}

func (s *Server) handleMyMediaUsers(w http.ResponseWriter, r *http.Request) {
	account, _ := accountFrom(r.Context())
	links, err := s.accountMediaUsers.EnsureLinks(r.Context(), account, "")
	if err != nil {
		writeError(w, r, s.logger, err)
		return
	}
	writeJSON(w, r, s.logger, http.StatusOK, accountMediaUsersDTO(links))
}

func (s *Server) handleAccountMediaUsers(w http.ResponseWriter, r *http.Request) {
	accountID, ok := accountMediaPathID(w, r, s, "id")
	if !ok {
		return
	}
	links, err := s.accountMediaUsers.List(r.Context(), accountID)
	if err != nil {
		writeError(w, r, s.logger, err)
		return
	}
	writeJSON(w, r, s.logger, http.StatusOK, accountMediaUsersDTO(links))
}

func (s *Server) handleSetAccountMediaUser(w http.ResponseWriter, r *http.Request) {
	accountID, serverID, ok := accountMediaPathIDs(w, r, s)
	if !ok {
		s.auditAccountMediaUser(r, "account_media_user.set", accountID, serverID, telemetry.AuditFailure, "invalid_input")
		return
	}
	request, ok := s.decodeSetAccountMediaUser(w, r)
	if !ok {
		s.auditAccountMediaUser(r, "account_media_user.set", accountID, serverID, telemetry.AuditFailure, "invalid_input")
		return
	}
	link, err := s.accountMediaUsers.Set(r.Context(), accountID, serverID, request.MediaUserID)
	if err != nil {
		s.auditAccountMediaUser(r, "account_media_user.set", accountID, serverID, telemetry.AuditFailure, mediaLinkFailureReason(err))
		writeError(w, r, s.logger, err)
		return
	}
	s.auditAccountMediaUser(r, "account_media_user.set", accountID, serverID, telemetry.AuditSuccess, "set")
	writeJSON(w, r, s.logger, http.StatusOK, accountMediaUserDTO(link))
}

func (s *Server) decodeSetAccountMediaUser(
	w http.ResponseWriter, r *http.Request,
) (setAccountMediaUserRequest, bool) {
	if !loginContentTypeSupported(r.Header.Get("Content-Type")) {
		writeError(w, r, s.logger, errUnsupportedMediaType)
		return setAccountMediaUserRequest{}, false
	}
	request, err := httputil.DecodeJSON[setAccountMediaUserRequest](w, r, s.maxBodyBytes)
	if err != nil || !core.ValidAccountMediaUserID(request.MediaUserID) {
		s.writeValidation(w, r, []httputil.FieldError{{
			Field: "media_user_id", Code: "invalid",
			Message: "must contain 1 to 128 valid UTF-8 bytes without control characters",
		}})
		return setAccountMediaUserRequest{}, false
	}
	return request, true
}

func (s *Server) handleDeleteAccountMediaUser(w http.ResponseWriter, r *http.Request) {
	accountID, serverID, ok := accountMediaPathIDs(w, r, s)
	if !ok {
		s.auditAccountMediaUser(r, "account_media_user.delete", accountID, serverID, telemetry.AuditFailure, "invalid_input")
		return
	}
	err := s.accountMediaUsers.Delete(r.Context(), accountID, serverID)
	if err != nil {
		s.auditAccountMediaUser(r, "account_media_user.delete", accountID, serverID, telemetry.AuditFailure, mediaLinkFailureReason(err))
		writeError(w, r, s.logger, err)
		return
	}
	s.auditAccountMediaUser(r, "account_media_user.delete", accountID, serverID, telemetry.AuditSuccess, "deleted")
	w.WriteHeader(http.StatusNoContent)
}

func accountMediaPathIDs(
	w http.ResponseWriter, r *http.Request, s *Server,
) (string, string, bool) {
	accountID, accountOK := accountMediaPathID(w, r, s, "id")
	if !accountOK {
		return "", "", false
	}
	serverID, serverOK := accountMediaPathID(w, r, s, "media_server_id")
	return accountID, serverID, serverOK
}

func accountMediaPathID(w http.ResponseWriter, r *http.Request, s *Server, field string) (string, bool) {
	value := r.PathValue(field)
	if core.ValidID(value) {
		return value, true
	}
	s.writeValidation(w, r, []httputil.FieldError{{Field: field, Code: "invalid", Message: "must be a valid UUID"}})
	return "", false
}

func accountMediaUsersDTO(links []core.AccountMediaUser) accountMediaUsersResponse {
	items := make([]accountMediaUserResponse, 0, len(links))
	for _, link := range links {
		items = append(items, accountMediaUserDTO(link))
	}
	return accountMediaUsersResponse{Items: items}
}

func accountMediaUserDTO(link core.AccountMediaUser) accountMediaUserResponse {
	return accountMediaUserResponse{
		AccountID: link.AccountID, MediaServerID: link.MediaServerID, MediaServerName: link.MediaServerName,
		MediaUserID: link.MediaUserID, Username: link.Username, Source: link.Source,
		CreatedAt: link.CreatedAt, UpdatedAt: link.UpdatedAt,
	}
}

func (s *Server) auditAccountMediaUser(
	r *http.Request, action, accountID, serverID string, result telemetry.AuditResult, reason string,
) {
	actor, _ := accountFrom(r.Context())
	err := s.audit.Emit(r.Context(), telemetry.AuditEvent{
		Actor: actor.ID, Action: action, Resource: "account:" + accountID + "/media_server:" + serverID,
		Result: result, Reason: reason, Source: clientIP(r, s.trustedProxyCIDRs), RequestID: requestIDFrom(r.Context()),
	})
	if err != nil {
		s.auditFailureMetrics.IncAuditWriteFailure()
		s.logger.ErrorContext(r.Context(), "write audit event", "error", err, "action", action)
	}
}

func mediaLinkFailureReason(err error) string {
	switch {
	case errors.Is(err, core.ErrNotFound):
		return "not_found"
	case errors.Is(err, core.ErrAlreadyExists):
		return "conflict"
	default:
		return "internal_error"
	}
}
