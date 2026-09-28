package http

import (
	"net/http"
	"strconv"

	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/httputil"
	"github.com/BonzTM/bloom/internal/telemetry"
)

type exclusionsRequest struct {
	ExcludedMediaUserIDs *[]string `json:"excluded_media_user_ids"`
	ExcludedLibraryIDs   *[]string `json:"excluded_library_ids"`
}

type exclusionsResponse struct {
	MediaServerID        string   `json:"media_server_id"`
	ExcludedMediaUserIDs []string `json:"excluded_media_user_ids"`
	ExcludedLibraryIDs   []string `json:"excluded_library_ids"`
}

func (s *Server) handleGetExclusions(w http.ResponseWriter, r *http.Request) {
	serverID, ok := s.mediaServerID(w, r)
	if !ok {
		return
	}
	value, err := s.exclusions.Read(r.Context(), serverID)
	if err != nil {
		writeError(w, r, s.logger, err)
		return
	}
	writeJSON(w, r, s.logger, http.StatusOK, exclusionsDTO(value))
}

func (s *Server) handlePutExclusions(w http.ResponseWriter, r *http.Request) {
	serverID, ok := s.mediaServerID(w, r)
	if !ok {
		return
	}
	request, err := httputil.DecodeJSON[exclusionsRequest](w, r, s.maxBodyBytes)
	if err != nil {
		s.auditExclusions(r, serverID, telemetry.AuditFailure, "invalid_input")
		s.writeValidation(w, r, []httputil.FieldError{activityField("body", "must be one valid JSON object")})
		return
	}
	if request.ExcludedMediaUserIDs == nil || request.ExcludedLibraryIDs == nil {
		s.auditExclusions(r, serverID, telemetry.AuditFailure, "invalid_input")
		s.writeValidation(w, r, []httputil.FieldError{activityField("body", "must contain both exclusion arrays")})
		return
	}
	value := core.MediaServerExclusions{
		MediaServerID: serverID,
		MediaUserIDs:  *request.ExcludedMediaUserIDs, LibraryIDs: *request.ExcludedLibraryIDs,
	}
	if fields := validateExclusions(value); len(fields) > 0 {
		s.auditExclusions(r, serverID, telemetry.AuditFailure, "invalid_input")
		s.writeValidation(w, r, fields)
		return
	}
	value, err = s.exclusions.Replace(r.Context(), value)
	if err != nil {
		s.auditExclusions(r, serverID, telemetry.AuditFailure, "failed")
		writeError(w, r, s.logger, err)
		return
	}
	s.auditExclusions(r, serverID, telemetry.AuditSuccess, "replaced")
	writeJSON(w, r, s.logger, http.StatusOK, exclusionsDTO(value))
}

func validateExclusions(value core.MediaServerExclusions) []httputil.FieldError {
	fields := make([]httputil.FieldError, 0, len(value.MediaUserIDs)+len(value.LibraryIDs))
	if len(value.MediaUserIDs)+len(value.LibraryIDs) > core.MaxMediaServerExclusions {
		return []httputil.FieldError{activityField("body", "must contain at most 500 exclusions")}
	}
	fields = append(fields, validateExclusionIDs("excluded_media_user_ids", value.MediaUserIDs)...)
	fields = append(fields, validateExclusionIDs("excluded_library_ids", value.LibraryIDs)...)
	return fields
}

func validateExclusionIDs(name string, values []string) []httputil.FieldError {
	fields := make([]httputil.FieldError, 0)
	seen := make(map[string]struct{}, len(values))
	for index, value := range values {
		field := name + "." + strconv.Itoa(index)
		if core.ValidateExclusionID(value) != nil {
			fields = append(fields, activityField(field, "must be 1 through 128 bytes of valid text"))
			continue
		}
		if _, exists := seen[value]; exists {
			fields = append(fields, activityField(field, "must not be duplicated"))
		}
		seen[value] = struct{}{}
	}
	return fields
}

func (s *Server) auditExclusions(
	r *http.Request, serverID string, result telemetry.AuditResult, reason string,
) {
	account, _ := accountFrom(r.Context())
	err := s.audit.Emit(r.Context(), telemetry.AuditEvent{
		Actor: account.ID, Action: "media_server.exclusions.replace", Resource: mediaServerResource(serverID),
		Result: result, Reason: reason, Source: clientIP(r, s.trustedProxyCIDRs), RequestID: requestIDFrom(r.Context()),
	})
	if err != nil {
		s.auditFailureMetrics.IncAuditWriteFailure()
		s.logger.ErrorContext(r.Context(), "write audit event", "error", err, "action", "media_server.exclusions.replace")
	}
}

func exclusionsDTO(value core.MediaServerExclusions) exclusionsResponse {
	users := append([]string{}, value.MediaUserIDs...)
	libraries := append([]string{}, value.LibraryIDs...)
	return exclusionsResponse{
		MediaServerID: value.MediaServerID, ExcludedMediaUserIDs: users,
		ExcludedLibraryIDs: libraries,
	}
}
