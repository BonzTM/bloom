package http

import (
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/httputil"
	"github.com/BonzTM/bloom/internal/telemetry"
)

type metadataTitleResponse struct {
	Kind       core.MediaKind            `json:"kind"`
	Provider   core.MetadataProviderKind `json:"provider"`
	ProviderID string                    `json:"provider_id"`
	Title      string                    `json:"title"`
	Year       int                       `json:"year"`
	Overview   string                    `json:"overview"`
	PosterPath string                    `json:"poster_path"`
}

type metadataSearchResponse struct {
	Items []metadataTitleResponse `json:"items"`
}

type metadataSeasonResponse struct {
	Number       int        `json:"number"`
	Name         string     `json:"name"`
	EpisodeCount int        `json:"episode_count"`
	AirDate      *time.Time `json:"air_date,omitempty"`
}

type metadataSeriesResponse struct {
	metadataTitleResponse
	Seasons []metadataSeasonResponse `json:"seasons"`
}

type (
	metadataKeyRequest struct {
		APIKey string `json:"api_key"`
	}
	metadataKeyPresenceResponse struct {
		Configured bool `json:"configured"`
	}
)

func (s *Server) handleMetadataSearch(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query().Get("q")
	var kind *core.MediaKind
	if raw := r.URL.Query().Get("kind"); raw != "" {
		value := core.MediaKind(raw)
		kind = &value
	}
	input := core.MetadataSearch{Query: query, Kind: kind}
	if err := core.ValidateMetadataSearch(input); err != nil {
		s.writeRequestError(w, r, err)
		return
	}
	results, err := s.metadataReader.Search(r.Context(), input)
	if err != nil {
		writeError(w, r, s.logger, err)
		return
	}
	items := make([]metadataTitleResponse, 0, len(results))
	for _, result := range results {
		items = append(items, metadataTitleDTO(result))
	}
	writeJSON(w, r, s.logger, http.StatusOK, metadataSearchResponse{Items: items})
}

func (s *Server) handleMetadataMovie(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if core.ValidateProviderID(id) != nil {
		s.writeRequestError(w, r, core.ErrInvalidArgument)
		return
	}
	title, err := s.metadataReader.Movie(r.Context(), id)
	if err != nil {
		writeError(w, r, s.logger, err)
		return
	}
	writeJSON(w, r, s.logger, http.StatusOK, metadataTitleDTO(title))
}

func (s *Server) handleMetadataSeries(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if core.ValidateProviderID(id) != nil {
		s.writeRequestError(w, r, core.ErrInvalidArgument)
		return
	}
	includeSpecials := false
	if raw := r.URL.Query().Get("include_specials"); raw != "" {
		var err error
		includeSpecials, err = strconv.ParseBool(raw)
		if err != nil {
			s.writeRequestError(w, r, core.ErrInvalidArgument)
			return
		}
	}
	series, err := s.metadataReader.Series(r.Context(), id, includeSpecials)
	if err != nil {
		writeError(w, r, s.logger, err)
		return
	}
	response := metadataSeriesResponse{metadataTitleResponse: metadataTitleDTO(series.MetadataTitle), Seasons: make([]metadataSeasonResponse, 0, len(series.Seasons))}
	for _, season := range series.Seasons {
		response.Seasons = append(response.Seasons, metadataSeasonResponse{Number: season.Number, Name: season.Name, EpisodeCount: season.EpisodeCount, AirDate: season.AirDate})
	}
	writeJSON(w, r, s.logger, http.StatusOK, response)
}

func (s *Server) handleMetadataKeyPresence(w http.ResponseWriter, r *http.Request) {
	present, err := s.metadataManager.HasKey(r.Context(), core.MetadataProviderTMDB)
	if err != nil {
		writeError(w, r, s.logger, err)
		return
	}
	writeJSON(w, r, s.logger, http.StatusOK, metadataKeyPresenceResponse{Configured: present})
}

func (s *Server) handleSetMetadataKey(w http.ResponseWriter, r *http.Request) {
	if !loginContentTypeSupported(r.Header.Get("Content-Type")) {
		writeError(w, r, s.logger, errUnsupportedMediaType)
		return
	}
	request, err := httputil.DecodeJSON[metadataKeyRequest](w, r, s.maxBodyBytes)
	if err != nil || request.APIKey == "" {
		s.writeValidation(w, r, []httputil.FieldError{{Field: "api_key", Code: "invalid", Message: "must be valid non-empty text"}})
		return
	}
	if err := s.metadataManager.SetKey(r.Context(), core.MetadataProviderTMDB, request.APIKey); err != nil {
		s.emitMetadataAudit(r, "metadata_provider.key.set", telemetry.AuditFailure)
		writeError(w, r, s.logger, err)
		return
	}
	s.emitMetadataAudit(r, "metadata_provider.key.set", telemetry.AuditSuccess)
	writeJSON(w, r, s.logger, http.StatusOK, metadataKeyPresenceResponse{Configured: true})
}

func (s *Server) handleDeleteMetadataKey(w http.ResponseWriter, r *http.Request) {
	if err := s.metadataManager.RemoveKey(r.Context(), core.MetadataProviderTMDB); err != nil {
		s.emitMetadataAudit(r, "metadata_provider.key.remove", telemetry.AuditFailure)
		writeError(w, r, s.logger, err)
		return
	}
	s.emitMetadataAudit(r, "metadata_provider.key.remove", telemetry.AuditSuccess)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) emitMetadataAudit(r *http.Request, action string, result telemetry.AuditResult) {
	account, _ := accountFrom(r.Context())
	err := s.audit.Emit(r.Context(), telemetry.AuditEvent{
		Actor: account.ID, Action: action, Resource: "metadata_provider:tmdb",
		Kind: "tmdb", Result: result, Source: clientIP(r, s.trustedProxyCIDRs), RequestID: requestIDFrom(r.Context()),
	})
	if err != nil {
		s.auditFailureMetrics.IncAuditWriteFailure()
		s.logger.ErrorContext(r.Context(), "write audit event", "error", err, "action", action)
	}
}

func metadataTitleDTO(title core.MetadataTitle) metadataTitleResponse {
	return metadataTitleResponse{
		Kind: title.Kind, Provider: title.Provider, ProviderID: title.ProviderID, Title: title.Title,
		Year: title.Year, Overview: title.Overview, PosterPath: title.PosterPath,
	}
}

func hasPermission(r *http.Request, permission core.Permission) bool { //nolint:unparam // kept generic for route-policy checks.
	permissions, ok := permissionsFrom(r.Context())
	return ok && slices.Contains(permissions, permission)
}
