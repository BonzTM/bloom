package http

import (
	"net/http"
	"strconv"
	"time"

	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/httputil"
	"github.com/BonzTM/bloom/internal/telemetry"
)

const (
	defaultRequestProfilePageSize = 50
	maxRequestProfilePageSize     = 100
)

type requestProfileBody struct {
	Name                    string           `json:"name"`
	Kinds                   []core.MediaKind `json:"kinds"`
	DownloadManagerKind     string           `json:"download_manager_kind"`
	DownloadManagerInstance string           `json:"download_manager_instance"`
	QualityProfile          string           `json:"quality_profile"`
	RootFolder              string           `json:"root_folder"`
	Tags                    []string         `json:"tags"`
}

type requestProfileResponse struct {
	ID string `json:"id"`
	requestProfileBody
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type requestProfilesResponse struct {
	Items      []requestProfileResponse `json:"items"`
	NextCursor string                   `json:"next_cursor"`
}

func (s *Server) handleCreateRequestProfile(w http.ResponseWriter, r *http.Request) {
	body, ok := s.decodeRequestProfile(w, r)
	if !ok {
		return
	}
	profile, err := s.requestService.CreateProfile(r.Context(), profileFromBody("", body))
	if err != nil {
		s.emitSettingsAudit(r, "request_profile.create", "request_profile:unresolved", telemetry.AuditFailure)
		writeError(w, r, s.logger, err)
		return
	}
	s.emitSettingsAudit(r, "request_profile.create", "request_profile:"+profile.ID, telemetry.AuditSuccess)
	writeJSON(w, r, s.logger, http.StatusCreated, requestProfileDTO(profile))
}

func (s *Server) handleUpdateRequestProfile(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !core.ValidID(id) {
		writeError(w, r, s.logger, core.ErrNotFound)
		return
	}
	body, ok := s.decodeRequestProfile(w, r)
	if !ok {
		return
	}
	profile, err := s.requestService.UpdateProfile(r.Context(), profileFromBody(id, body))
	if err != nil {
		s.emitSettingsAudit(r, "request_profile.update", "request_profile:"+id, telemetry.AuditFailure)
		writeError(w, r, s.logger, err)
		return
	}
	s.emitSettingsAudit(r, "request_profile.update", "request_profile:"+id, telemetry.AuditSuccess)
	writeJSON(w, r, s.logger, http.StatusOK, requestProfileDTO(profile))
}

func (s *Server) handleDeleteRequestProfile(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !core.ValidID(id) {
		writeError(w, r, s.logger, core.ErrNotFound)
		return
	}
	if err := s.requestService.DeleteProfile(r.Context(), id); err != nil {
		s.emitSettingsAudit(r, "request_profile.delete", "request_profile:"+id, telemetry.AuditFailure)
		writeError(w, r, s.logger, err)
		return
	}
	s.emitSettingsAudit(r, "request_profile.delete", "request_profile:"+id, telemetry.AuditSuccess)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleListRequestProfiles(w http.ResponseWriter, r *http.Request) {
	pageSize := defaultRequestProfilePageSize
	if raw := r.URL.Query().Get("page_size"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 || value > maxRequestProfilePageSize {
			s.writeRequestError(w, r, core.ErrInvalidArgument)
			return
		}
		pageSize = value
	}
	profiles, err := s.requestService.ListProfiles(r.Context(), r.URL.Query().Get("cursor"), pageSize+1)
	if err != nil {
		writeError(w, r, s.logger, err)
		return
	}
	next := ""
	if len(profiles) > pageSize {
		next = profiles[pageSize-1].Name
		profiles = profiles[:pageSize]
	}
	response := requestProfilesResponse{Items: make([]requestProfileResponse, 0, len(profiles)), NextCursor: next}
	for _, profile := range profiles {
		response.Items = append(response.Items, requestProfileDTO(profile))
	}
	writeJSON(w, r, s.logger, http.StatusOK, response)
}

func (s *Server) decodeRequestProfile(w http.ResponseWriter, r *http.Request) (requestProfileBody, bool) {
	if !loginContentTypeSupported(r.Header.Get("Content-Type")) {
		writeError(w, r, s.logger, errUnsupportedMediaType)
		return requestProfileBody{}, false
	}
	body, err := httputil.DecodeJSON[requestProfileBody](w, r, s.maxBodyBytes)
	if err != nil {
		s.writeValidation(w, r, []httputil.FieldError{{Field: "body", Code: "invalid", Message: "must be one valid JSON object"}})
		return requestProfileBody{}, false
	}
	probe := profileFromBody("00000000-0000-4000-8000-000000000000", body)
	if err := core.ValidateRequestProfile(probe); err != nil {
		s.writeRequestError(w, r, err)
		return requestProfileBody{}, false
	}
	return body, true
}

func profileFromBody(id string, body requestProfileBody) core.RequestProfile {
	return core.RequestProfile{
		ID: id, Name: body.Name, Kinds: body.Kinds, DownloadManagerKind: body.DownloadManagerKind,
		DownloadManagerInstance: body.DownloadManagerInstance, QualityProfile: body.QualityProfile, RootFolder: body.RootFolder, Tags: body.Tags,
	}
}

func requestProfileDTO(profile core.RequestProfile) requestProfileResponse {
	return requestProfileResponse{ID: profile.ID, requestProfileBody: requestProfileBody{
		Name: profile.Name, Kinds: profile.Kinds,
		DownloadManagerKind: profile.DownloadManagerKind, DownloadManagerInstance: profile.DownloadManagerInstance,
		QualityProfile: profile.QualityProfile, RootFolder: profile.RootFolder, Tags: profile.Tags,
	}, CreatedAt: profile.CreatedAt, UpdatedAt: profile.UpdatedAt}
}

func (s *Server) emitSettingsAudit(r *http.Request, action, resource string, result telemetry.AuditResult) {
	account, _ := accountFrom(r.Context())
	err := s.audit.Emit(r.Context(), telemetry.AuditEvent{
		Actor: account.ID, Action: action, Resource: resource, Result: result,
		Source: clientIP(r, s.trustedProxyCIDRs), RequestID: requestIDFrom(r.Context()),
	})
	if err != nil {
		s.auditFailureMetrics.IncAuditWriteFailure()
		s.logger.ErrorContext(r.Context(), "write audit event", "error", err, "action", action)
	}
}
