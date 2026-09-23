package http

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/httputil"
	"github.com/BonzTM/bloom/internal/telemetry"
)

const (
	defaultMediaServerPageSize = 50
	maxMediaServerPageSize     = 100
	maxMediaServerCursorBytes  = (core.MaxMediaServerNameKeyBytes*8 + 5) / 6
	maxMediaServerAPIKeyBytes  = 4096
	maxMediaServerRetryAfter   = 30 * time.Second
)

type createMediaServerRequest struct {
	Kind          core.MediaServerKind `json:"kind"`
	Name          string               `json:"name"`
	BaseURL       string               `json:"base_url"`
	APIKey        string               `json:"api_key"`
	AllowInsecure bool                 `json:"allow_insecure"`
}

type capabilitiesResponse struct {
	CreateUserWithPassword bool `json:"create_user_with_password"`
	SetPassword            bool `json:"set_password"`
	QuickConnectApproval   bool `json:"quick_connect_approval"`
	ProviderIDLookup       bool `json:"provider_id_lookup"`
}

type mediaServerResponse struct {
	ID            string               `json:"id"`
	Kind          core.MediaServerKind `json:"kind"`
	Name          string               `json:"name"`
	BaseURL       string               `json:"base_url"`
	AllowInsecure bool                 `json:"allow_insecure"`
	CreatedAt     time.Time            `json:"created_at"`
	UpdatedAt     time.Time            `json:"updated_at"`
	Capabilities  capabilitiesResponse `json:"capabilities"`
}

type serverInfoResponse struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	ID      string `json:"id"`
}

type createMediaServerResponse struct {
	Server mediaServerResponse `json:"server"`
	Info   serverInfoResponse  `json:"info"`
}

type mediaServersResponse struct {
	Items      []mediaServerResponse `json:"items"`
	NextCursor string                `json:"next_cursor"`
}

type probeMediaServerResponse struct {
	Info serverInfoResponse `json:"info"`
}

type libraryResponse struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Type string `json:"type"`
}

type librariesResponse struct {
	Items []libraryResponse `json:"items"`
}

func (s *Server) handleCreateMediaServer(w http.ResponseWriter, r *http.Request) {
	request, ok := s.decodeMediaServerRegistration(w, r)
	if !ok {
		return
	}
	if request.AllowInsecure && strings.HasPrefix(request.BaseURL, "http://") {
		s.logger.WarnContext(r.Context(), "registering media server over plaintext HTTP",
			"kind", request.Kind, "allow_insecure", true)
	}
	connection, err := s.mediaServerManager.Register(
		r.Context(), request.Kind, request.Name, request.BaseURL, request.APIKey, request.AllowInsecure,
	)
	if err != nil {
		s.emitMediaServerAudit(r, "media_server.create", "media_server:unresolved", request.Kind, request.AllowInsecure, telemetry.AuditFailure, "failed")
		s.writeMediaServerError(w, r, err)
		return
	}
	s.emitMediaServerAudit(r, "media_server.create", mediaServerResource(connection.Server.ID), connection.Server.Kind, connection.Server.AllowInsecure, telemetry.AuditSuccess, "created")
	writeJSON(w, r, s.logger, http.StatusCreated, createMediaServerResponse{
		Server: mediaServerDTO(connection.Server, connection.Capabilities), Info: serverInfoDTO(connection.Info),
	})
}

func (s *Server) decodeMediaServerRegistration(w http.ResponseWriter, r *http.Request) (createMediaServerRequest, bool) {
	if !loginContentTypeSupported(r.Header.Get("Content-Type")) {
		writeError(w, r, s.logger, errUnsupportedMediaType)
		return createMediaServerRequest{}, false
	}
	request, err := httputil.DecodeJSON[createMediaServerRequest](w, r, s.maxBodyBytes)
	if err != nil {
		s.writeValidation(w, r, []httputil.FieldError{{Field: "body", Code: "invalid", Message: "must be one valid JSON object"}})
		return createMediaServerRequest{}, false
	}
	fields := validateMediaServerRegistration(request)
	if len(fields) > 0 {
		s.writeValidation(w, r, fields)
		return createMediaServerRequest{}, false
	}
	normalizedURL, err := core.ValidateMediaServerURL(request.BaseURL, request.AllowInsecure)
	if err != nil {
		writeError(w, r, s.logger, err)
		return createMediaServerRequest{}, false
	}
	request.BaseURL = normalizedURL
	return request, true
}

func validateMediaServerRegistration(request createMediaServerRequest) []httputil.FieldError {
	fields := make([]httputil.FieldError, 0, 4)
	if !request.Kind.Valid() {
		fields = append(fields, httputil.FieldError{Field: "kind", Code: "invalid", Message: "must be jellyfin"})
	}
	if err := core.ValidateMediaServerName(request.Name); err != nil {
		fields = append(fields, httputil.FieldError{Field: "name", Code: "invalid", Message: "must be 1 through 100 bytes without surrounding whitespace or control characters"})
	}
	overrideInvalid := insecureOverrideForHTTPS(request.BaseURL, request.AllowInsecure)
	if overrideInvalid {
		fields = append(fields, httputil.FieldError{
			Field: "allow_insecure", Code: "invalid", Message: "may be true only for a plaintext HTTP base_url",
		})
	}
	allowInsecure := request.AllowInsecure && !overrideInvalid
	if _, err := core.ValidateMediaServerURL(request.BaseURL, allowInsecure); err != nil {
		fields = append(fields, httputil.FieldError{Field: "base_url", Code: "invalid", Message: "must be at most 2048 bytes and use HTTPS; HTTP requires allow_insecure; credentials, query, fragment, and unsafe destinations are forbidden"})
	}
	if !validMediaServerAPIKey(request.APIKey) {
		fields = append(fields, httputil.FieldError{Field: "api_key", Code: "invalid", Message: "must be 1 through 4096 bytes of valid text"})
	}
	return fields
}

func insecureOverrideForHTTPS(raw string, allowInsecure bool) bool {
	parsed, err := url.Parse(raw)
	return err == nil && allowInsecure && parsed.Scheme == "https"
}

func validMediaServerAPIKey(value string) bool {
	return value != "" && len(value) <= maxMediaServerAPIKeyBytes && utf8.ValidString(value) &&
		strings.IndexFunc(value, unicode.IsControl) < 0
}

func (s *Server) handleListMediaServers(w http.ResponseWriter, r *http.Request) {
	afterNameKey, pageSize, fields := mediaServerPageParams(r)
	if len(fields) > 0 {
		s.writeValidation(w, r, fields)
		return
	}
	connections, err := s.mediaServerReader.List(r.Context(), afterNameKey, pageSize+1)
	if err != nil {
		writeError(w, r, s.logger, err)
		return
	}
	connections, nextCursor := mediaServerPage(connections, pageSize)
	items := make([]mediaServerResponse, 0, len(connections))
	for _, connection := range connections {
		items = append(items, mediaServerDTO(connection.Server, connection.Capabilities))
	}
	writeJSON(w, r, s.logger, http.StatusOK, mediaServersResponse{Items: items, NextCursor: nextCursor})
}

func (s *Server) handleGetMediaServer(w http.ResponseWriter, r *http.Request) {
	id, ok := s.mediaServerID(w, r)
	if !ok {
		return
	}
	connection, err := s.mediaServerReader.Get(r.Context(), id)
	if err != nil {
		writeError(w, r, s.logger, err)
		return
	}
	writeJSON(w, r, s.logger, http.StatusOK, mediaServerDTO(connection.Server, connection.Capabilities))
}

func (s *Server) handleProbeMediaServer(w http.ResponseWriter, r *http.Request) {
	id, ok := s.mediaServerID(w, r)
	if !ok {
		return
	}
	info, err := s.mediaServerManager.Probe(r.Context(), id)
	if err != nil {
		s.writeMediaServerError(w, r, err)
		return
	}
	writeJSON(w, r, s.logger, http.StatusOK, probeMediaServerResponse{Info: serverInfoDTO(info)})
}

func (s *Server) handleMediaServerLibraries(w http.ResponseWriter, r *http.Request) {
	id, ok := s.mediaServerID(w, r)
	if !ok {
		return
	}
	libraries, err := s.mediaServerReader.Libraries(r.Context(), id)
	if err != nil {
		s.writeMediaServerError(w, r, err)
		return
	}
	items := make([]libraryResponse, 0, len(libraries))
	for _, library := range libraries {
		items = append(items, libraryResponse{ID: library.ID, Name: library.Name, Type: library.Type})
	}
	writeJSON(w, r, s.logger, http.StatusOK, librariesResponse{Items: items})
}

func (s *Server) handleDeleteMediaServer(w http.ResponseWriter, r *http.Request) {
	id, ok := s.mediaServerID(w, r)
	if !ok {
		return
	}
	server, err := s.mediaServerManager.Delete(r.Context(), id)
	if err != nil {
		s.emitMediaServerAudit(r, "media_server.delete", mediaServerResource(id), "", false, telemetry.AuditFailure, "failed")
		writeError(w, r, s.logger, err)
		return
	}
	s.emitMediaServerAudit(r, "media_server.delete", mediaServerResource(server.ID), server.Kind, server.AllowInsecure, telemetry.AuditSuccess, "deleted")
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) writeMediaServerError(w http.ResponseWriter, r *http.Request, err error) {
	var mediaErr *core.MediaServerError
	if errors.As(err, &mediaErr) && mediaErr.Transient() {
		retry := mediaErr.RetryAfter
		if retry <= 0 {
			retry = time.Second
		}
		seconds := max(1, int((min(retry, maxMediaServerRetryAfter)+time.Second-1)/time.Second))
		w.Header().Set("Retry-After", strconv.Itoa(seconds))
	}
	writeError(w, r, s.logger, err)
}

func (s *Server) emitMediaServerAudit(
	r *http.Request,
	action, resource string,
	kind core.MediaServerKind,
	allowInsecure bool,
	result telemetry.AuditResult,
	reason string,
) {
	account, _ := accountFrom(r.Context())
	err := s.audit.Emit(r.Context(), telemetry.AuditEvent{
		Actor: account.ID, Action: action, Resource: resource, Kind: string(kind),
		AllowInsecure: allowInsecure, Result: result,
		Reason: reason, Source: clientIP(r, s.trustedProxyCIDRs), RequestID: requestIDFrom(r.Context()),
	})
	if err != nil {
		s.auditFailureMetrics.IncAuditWriteFailure()
		s.logger.ErrorContext(r.Context(), "write audit event", "error", err, "action", action)
	}
}

func mediaServerDTO(server core.MediaServer, capabilities core.Capabilities) mediaServerResponse {
	return mediaServerResponse{
		ID: server.ID, Kind: server.Kind, Name: server.Name, BaseURL: server.BaseURL,
		AllowInsecure: server.AllowInsecure,
		CreatedAt:     server.CreatedAt, UpdatedAt: server.UpdatedAt, Capabilities: capabilitiesDTO(capabilities),
	}
}

func capabilitiesDTO(value core.Capabilities) capabilitiesResponse {
	return capabilitiesResponse{
		CreateUserWithPassword: value.CreateUserWithPassword, SetPassword: value.SetPassword,
		QuickConnectApproval: value.QuickConnectApproval, ProviderIDLookup: value.ProviderIDLookup,
	}
}

func serverInfoDTO(info core.ServerInfo) serverInfoResponse {
	return serverInfoResponse{Name: info.Name, Version: info.Version, ID: info.ID}
}

func (s *Server) mediaServerID(w http.ResponseWriter, r *http.Request) (string, bool) {
	id := r.PathValue("id")
	if core.ValidID(id) {
		return id, true
	}
	s.writeValidation(w, r, []httputil.FieldError{{
		Field: "id", Code: "invalid", Message: "must be a valid UUID",
	}})
	return "", false
}

func (s *Server) mediaServerOperationMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), s.mediaOperationTimeout)
		defer cancel()
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func mediaServerResource(id string) string { return "media_server:" + id }

func mediaServerPageParams(r *http.Request) (string, int, []httputil.FieldError) {
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return "", defaultMediaServerPageSize, []httputil.FieldError{{
			Field: "query", Code: "invalid", Message: "must use valid percent encoding",
		}}
	}
	pageSize, sizeErr := parseMediaServerPageSize(query["page_size"])
	afterNameKey, cursorErr := parseMediaServerCursor(query["cursor"])
	fields := make([]httputil.FieldError, 0, 2)
	if sizeErr != "" {
		fields = append(fields, httputil.FieldError{Field: "page_size", Code: "invalid", Message: sizeErr})
	}
	if cursorErr != "" {
		fields = append(fields, httputil.FieldError{Field: "cursor", Code: "invalid", Message: cursorErr})
	}
	return afterNameKey, pageSize, fields
}

func parseMediaServerPageSize(values []string) (int, string) {
	if len(values) == 0 {
		return defaultMediaServerPageSize, ""
	}
	if len(values) != 1 || values[0] == "" {
		return defaultMediaServerPageSize, "must appear once"
	}
	value, err := strconv.Atoi(values[0])
	if err != nil || value < 1 {
		return defaultMediaServerPageSize, "must be a positive integer"
	}
	return min(value, maxMediaServerPageSize), ""
}

func parseMediaServerCursor(values []string) (string, string) {
	if len(values) == 0 {
		return "", ""
	}
	if len(values) != 1 || values[0] == "" || len(values[0]) > maxMediaServerCursorBytes {
		return "", "must be one valid cursor"
	}
	decoded, err := base64.RawURLEncoding.DecodeString(values[0])
	key := string(decoded)
	if err != nil || !validMediaServerCursorKey(key) {
		return "", "must be one valid cursor"
	}
	return key, ""
}

func validMediaServerCursorKey(key string) bool {
	return key != "" && len(key) <= core.MaxMediaServerNameKeyBytes && utf8.ValidString(key) &&
		key == strings.TrimSpace(key) && strings.IndexFunc(key, unicode.IsControl) < 0 &&
		core.MediaServerNameKey(key) == key
}

func mediaServerPage(connections []core.MediaServerConnection, pageSize int) ([]core.MediaServerConnection, string) {
	if len(connections) <= pageSize {
		return connections, ""
	}
	page := connections[:pageSize]
	key := core.MediaServerNameKey(page[len(page)-1].Server.Name)
	next := base64.RawURLEncoding.EncodeToString([]byte(key))
	return page, next
}
