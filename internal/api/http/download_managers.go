package http

import (
	"encoding/base64"
	"net/http"
	"time"

	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/httputil"
	"github.com/BonzTM/bloom/internal/telemetry"
)

type createDownloadManagerRequest struct {
	Kind          core.DownloadManagerKind `json:"kind"`
	Name          string                   `json:"name"`
	BaseURL       string                   `json:"base_url"`
	APIKey        string                   `json:"api_key"`
	AllowInsecure bool                     `json:"allow_insecure"`
}

type downloadManagerResponse struct {
	ID            string                   `json:"id"`
	Kind          core.DownloadManagerKind `json:"kind"`
	Name          string                   `json:"name"`
	BaseURL       string                   `json:"base_url"`
	AllowInsecure bool                     `json:"allow_insecure"`
	CreatedAt     time.Time                `json:"created_at"`
	UpdatedAt     time.Time                `json:"updated_at"`
}

type downloadManagerInfoResponse struct {
	Name         string                           `json:"name"`
	Version      string                           `json:"version"`
	Capabilities core.DownloadManagerCapabilities `json:"capabilities"`
}

type createDownloadManagerResponse struct {
	Manager downloadManagerResponse     `json:"manager"`
	Info    downloadManagerInfoResponse `json:"info"`
}

type downloadManagersResponse struct {
	Items      []downloadManagerResponse `json:"items"`
	NextCursor string                    `json:"next_cursor"`
}

type downloadManagerOptionsResponse struct {
	QualityProfiles []core.DownloadManagerOption `json:"quality_profiles"`
	RootFolders     []core.DownloadManagerOption `json:"root_folders"`
	Tags            []core.DownloadManagerOption `json:"tags"`
}

func (s *Server) handleCreateDownloadManager(w http.ResponseWriter, r *http.Request) {
	if !loginContentTypeSupported(r.Header.Get("Content-Type")) {
		writeError(w, r, s.logger, errUnsupportedMediaType)
		return
	}
	body, err := httputil.DecodeJSON[createDownloadManagerRequest](w, r, s.maxBodyBytes)
	if err != nil || !body.Kind.Valid() || core.ValidateMediaServerName(body.Name) != nil || !validMediaServerAPIKey(body.APIKey) {
		s.writeValidation(w, r, []httputil.FieldError{{Field: "body", Code: "invalid", Message: "contains an invalid download manager registration"}})
		return
	}
	baseURL, err := core.ValidateMediaServerURL(body.BaseURL, body.AllowInsecure)
	if err != nil {
		s.writeValidation(w, r, []httputil.FieldError{{Field: "base_url", Code: "invalid", Message: "must be a permitted HTTP or HTTPS destination"}})
		return
	}
	connection, err := s.downloadManagerManager.Register(
		r.Context(), body.Kind, body.Name, baseURL, body.APIKey, body.AllowInsecure,
	)
	if err != nil {
		s.emitDownloadManagerAudit(r, "download_manager.create", "download_manager:unresolved", body.Kind, body.AllowInsecure, telemetry.AuditFailure)
		writeError(w, r, s.logger, err)
		return
	}
	s.emitDownloadManagerAudit(r, "download_manager.create", "download_manager:"+connection.Manager.ID, connection.Manager.Kind, connection.Manager.AllowInsecure, telemetry.AuditSuccess)
	writeJSON(w, r, s.logger, http.StatusCreated, createDownloadManagerResponse{
		Manager: downloadManagerDTO(connection.Manager),
		Info:    downloadManagerInfoResponse{Name: connection.Info.Name, Version: connection.Info.Version, Capabilities: connection.Info.Capabilities},
	})
}

func (s *Server) handleListDownloadManagers(w http.ResponseWriter, r *http.Request) {
	after, pageSize, fields := mediaServerPageParams(r)
	if len(fields) != 0 {
		s.writeValidation(w, r, fields)
		return
	}
	values, err := s.downloadManagerReader.List(r.Context(), after, pageSize+1)
	if err != nil {
		writeError(w, r, s.logger, err)
		return
	}
	values, cursor := downloadManagerPage(values, pageSize)
	response := downloadManagersResponse{Items: make([]downloadManagerResponse, 0, len(values)), NextCursor: cursor}
	for _, value := range values {
		response.Items = append(response.Items, downloadManagerDTO(value))
	}
	writeJSON(w, r, s.logger, http.StatusOK, response)
}

func (s *Server) handleDeleteDownloadManager(w http.ResponseWriter, r *http.Request) {
	id, ok := s.downloadManagerID(w, r)
	if !ok {
		return
	}
	manager, err := s.downloadManagerManager.Delete(r.Context(), id)
	if err != nil {
		s.emitDownloadManagerAudit(r, "download_manager.delete", "download_manager:"+id, "", false, telemetry.AuditFailure)
		writeError(w, r, s.logger, err)
		return
	}
	s.emitDownloadManagerAudit(r, "download_manager.delete", "download_manager:"+id, manager.Kind, manager.AllowInsecure, telemetry.AuditSuccess)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleDownloadManagerOptions(w http.ResponseWriter, r *http.Request) {
	id, ok := s.downloadManagerID(w, r)
	if !ok {
		return
	}
	options, err := s.downloadManagerReader.Options(r.Context(), id)
	if err != nil {
		writeError(w, r, s.logger, err)
		return
	}
	writeJSON(w, r, s.logger, http.StatusOK, downloadManagerOptionsResponse{
		QualityProfiles: options.QualityProfiles, RootFolders: options.RootFolders, Tags: options.Tags,
	})
}

func (s *Server) downloadManagerID(w http.ResponseWriter, r *http.Request) (string, bool) {
	id := r.PathValue("id")
	if core.ValidID(id) {
		return id, true
	}
	s.writeValidation(w, r, []httputil.FieldError{{Field: "id", Code: "invalid", Message: "must be a valid UUID"}})
	return "", false
}

func downloadManagerDTO(value core.DownloadManager) downloadManagerResponse {
	return downloadManagerResponse{
		ID: value.ID, Kind: value.Kind, Name: value.Name, BaseURL: value.BaseURL,
		AllowInsecure: value.AllowInsecure, CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt,
	}
}

func downloadManagerPage(values []core.DownloadManager, pageSize int) ([]core.DownloadManager, string) {
	if len(values) <= pageSize {
		return values, ""
	}
	page := values[:pageSize]
	key := core.MediaServerNameKey(page[len(page)-1].Name)
	return page, base64.RawURLEncoding.EncodeToString([]byte(key))
}

func (s *Server) emitDownloadManagerAudit(
	r *http.Request, action, resource string, kind core.DownloadManagerKind,
	allowInsecure bool, result telemetry.AuditResult,
) {
	account, _ := accountFrom(r.Context())
	err := s.audit.Emit(r.Context(), telemetry.AuditEvent{
		Actor: account.ID, Action: action, Resource: resource, Kind: string(kind),
		AllowInsecure: allowInsecure, Result: result,
		Source: clientIP(r, s.trustedProxyCIDRs), RequestID: requestIDFrom(r.Context()),
	})
	if err != nil {
		s.auditFailureMetrics.IncAuditWriteFailure()
		s.logger.ErrorContext(r.Context(), "write audit event", "error", err, "action", action)
	}
}
