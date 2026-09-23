package http

import (
	"encoding/base64"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/httputil"
	requestapp "github.com/BonzTM/bloom/internal/request"
	"github.com/BonzTM/bloom/internal/telemetry"
)

const (
	defaultRequestPageSize = 50
	maxRequestPageSize     = 100
)

type (
	createRequestBody struct {
		Kind       core.MediaKind `json:"kind"`
		ProviderID string         `json:"provider_id"`
		ProfileID  string         `json:"profile_id"`
		Seasons    []int          `json:"seasons"`
	}
	decideRequestBody struct {
		Reason string `json:"reason"`
	}
	requestSeasonResponse struct {
		Number int               `json:"number"`
		Status core.SeasonStatus `json:"status"`
	}
	requestResponse struct {
		ID             string                    `json:"id"`
		Kind           core.MediaKind            `json:"kind"`
		Provider       core.MetadataProviderKind `json:"provider"`
		ProviderID     string                    `json:"provider_id"`
		Title          string                    `json:"title"`
		Year           int                       `json:"year"`
		PosterPath     string                    `json:"poster_path"`
		RequesterID    string                    `json:"requester_account_id"`
		ProfileID      string                    `json:"profile_id"`
		Status         core.RequestStatus        `json:"status"`
		Seasons        []requestSeasonResponse   `json:"seasons"`
		DecisionReason string                    `json:"decision_reason"`
		DecidedBy      string                    `json:"decided_by_account_id"`
		DecidedAt      *time.Time                `json:"decided_at,omitempty"`
		CreatedAt      time.Time                 `json:"created_at"`
		UpdatedAt      time.Time                 `json:"updated_at"`
	}
)

type requestsResponse struct {
	Items      []requestResponse `json:"items"`
	NextCursor string            `json:"next_cursor"`
}

func (s *Server) handleCreateRequest(w http.ResponseWriter, r *http.Request) {
	if !loginContentTypeSupported(r.Header.Get("Content-Type")) {
		writeError(w, r, s.logger, errUnsupportedMediaType)
		return
	}
	body, err := httputil.DecodeJSON[createRequestBody](w, r, s.maxBodyBytes)
	if err != nil {
		s.writeValidation(w, r, []httputil.FieldError{{Field: "body", Code: "invalid", Message: "must be one valid JSON object"}})
		return
	}
	account, _ := accountFrom(r.Context())
	created, err := s.requestService.Create(r.Context(), account.ID, requestapp.CreateInput{Kind: body.Kind, ProviderID: body.ProviderID, ProfileID: body.ProfileID, Seasons: body.Seasons}, hasPermission(r, core.PermissionRequestsApprove))
	if err != nil {
		s.emitRequestAudit(r, "request.create", "request:unresolved", body.Kind, "", telemetry.AuditFailure)
		s.writeRequestError(w, r, err)
		return
	}
	s.emitRequestAudit(r, "request.create", "request:"+created.ID, created.Kind, created.Title, telemetry.AuditSuccess)
	if created.Status == core.RequestApproved {
		s.emitRequestAudit(r, "request.approve", "request:"+created.ID, created.Kind, created.Title, telemetry.AuditSuccess)
	}
	writeJSON(w, r, s.logger, http.StatusCreated, requestDTO(created))
}

func (s *Server) handleListRequests(w http.ResponseWriter, r *http.Request) {
	filter, err := parseRequestFilter(r)
	if err != nil {
		s.writeRequestError(w, r, err)
		return
	}
	account, _ := accountFrom(r.Context())
	approver := hasPermission(r, core.PermissionRequestsApprove)
	queryFilter := filter
	queryFilter.PageSize++
	items, err := s.requestService.List(r.Context(), account.ID, approver, queryFilter)
	if err != nil {
		writeError(w, r, s.logger, err)
		return
	}
	items, cursor := requestPage(items, filter.PageSize)
	response := requestsResponse{Items: make([]requestResponse, 0, len(items)), NextCursor: cursor}
	for _, item := range items {
		response.Items = append(response.Items, requestDTO(item))
	}
	writeJSON(w, r, s.logger, http.StatusOK, response)
}

func (s *Server) handleGetRequest(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !core.ValidID(id) {
		writeError(w, r, s.logger, core.ErrNotFound)
		return
	}
	account, _ := accountFrom(r.Context())
	item, err := s.requestService.Get(r.Context(), account.ID, id, hasPermission(r, core.PermissionRequestsApprove))
	if err != nil {
		writeError(w, r, s.logger, err)
		return
	}
	writeJSON(w, r, s.logger, http.StatusOK, requestDTO(item))
}

func (s *Server) handleApproveRequest(w http.ResponseWriter, r *http.Request) {
	s.handleDecision(w, r, true)
}

func (s *Server) handleDeclineRequest(w http.ResponseWriter, r *http.Request) {
	s.handleDecision(w, r, false)
}

func (s *Server) handleDecision(w http.ResponseWriter, r *http.Request, approve bool) {
	id := r.PathValue("id")
	if !core.ValidID(id) {
		writeError(w, r, s.logger, core.ErrNotFound)
		return
	}
	body := decideRequestBody{}
	if r.ContentLength != 0 {
		if !loginContentTypeSupported(r.Header.Get("Content-Type")) {
			writeError(w, r, s.logger, errUnsupportedMediaType)
			return
		}
		decoded, err := httputil.DecodeJSON[decideRequestBody](w, r, s.maxBodyBytes)
		if err != nil {
			s.writeRequestError(w, r, core.ErrInvalidArgument)
			return
		}
		body = decoded
	}
	account, _ := accountFrom(r.Context())
	item, err := s.requestService.Decide(r.Context(), account.ID, id, approve, body.Reason)
	action := "request.decline"
	if approve {
		action = "request.approve"
	}
	if err != nil {
		s.emitRequestAudit(r, action, "request:"+id, "", "", telemetry.AuditFailure)
		s.writeRequestError(w, r, err)
		return
	}
	s.emitRequestAudit(r, action, "request:"+id, item.Kind, item.Title, telemetry.AuditSuccess)
	writeJSON(w, r, s.logger, http.StatusOK, requestDTO(item))
}

func parseRequestFilter(r *http.Request) (core.RequestListFilter, error) {
	filter := core.RequestListFilter{RequesterID: r.URL.Query().Get("requester_id"), PageSize: defaultRequestPageSize}
	if filter.RequesterID != "" && !core.ValidID(filter.RequesterID) {
		return filter, core.ErrInvalidArgument
	}
	if raw := r.URL.Query().Get("status"); raw != "" {
		value := core.RequestStatus(raw)
		if !value.Valid() {
			return filter, core.ErrInvalidArgument
		}
		filter.Status = &value
	}
	if raw := r.URL.Query().Get("page_size"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 || value > maxRequestPageSize {
			return filter, core.ErrInvalidArgument
		}
		filter.PageSize = value
	}
	if raw := r.URL.Query().Get("cursor"); raw != "" {
		cursor, err := decodeRequestCursor(raw)
		if err != nil {
			return filter, err
		}
		filter.After = cursor
	}
	return filter, nil
}

func requestPage(items []core.MediaRequest, size int) ([]core.MediaRequest, string) {
	if len(items) <= size {
		return items, ""
	}
	page := items[:size]
	last := page[len(page)-1]
	return page, base64.RawURLEncoding.EncodeToString([]byte(last.CreatedAt.Format(time.RFC3339Nano) + "|" + last.ID))
}

func decodeRequestCursor(raw string) (*core.RequestCursor, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil || len(decoded) > 100 {
		return nil, core.ErrInvalidArgument
	}
	parts := strings.SplitN(string(decoded), "|", 2)
	if len(parts) != 2 || !core.ValidID(parts[1]) {
		return nil, core.ErrInvalidArgument
	}
	created, err := time.Parse(time.RFC3339Nano, parts[0])
	if err != nil {
		return nil, core.ErrInvalidArgument
	}
	return &core.RequestCursor{CreatedAt: created, ID: parts[1]}, nil
}

func requestDTO(item core.MediaRequest) requestResponse {
	seasons := make([]requestSeasonResponse, 0, len(item.Seasons))
	for _, season := range item.Seasons {
		seasons = append(seasons, requestSeasonResponse{Number: season.Number, Status: season.Status})
	}
	return requestResponse{
		ID: item.ID, Kind: item.Kind, Provider: item.Provider, ProviderID: item.ProviderID, Title: item.Title, Year: item.Year,
		PosterPath: item.PosterPath, RequesterID: item.RequesterID, ProfileID: item.ProfileID, Status: item.Status, Seasons: seasons,
		DecisionReason: item.DecisionReason, DecidedBy: item.DecidedBy, DecidedAt: item.DecidedAt, CreatedAt: item.CreatedAt, UpdatedAt: item.UpdatedAt,
	}
}

func (s *Server) emitRequestAudit(r *http.Request, action, resource string, kind core.MediaKind, title string, result telemetry.AuditResult) {
	account, _ := accountFrom(r.Context())
	err := s.audit.Emit(r.Context(), telemetry.AuditEvent{
		Actor: account.ID, Action: action, Resource: resource, Kind: string(kind), Title: title, Result: result,
		Source: clientIP(r, s.trustedProxyCIDRs), RequestID: requestIDFrom(r.Context()),
	})
	if err != nil {
		s.auditFailureMetrics.IncAuditWriteFailure()
		s.logger.ErrorContext(r.Context(), "write audit event", "error", err, "action", action)
	}
}

func (s *Server) writeRequestError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, core.ErrInvalidArgument) && !errors.Is(err, core.ErrMetadataMalformed) {
		s.writeValidation(w, r, []httputil.FieldError{{Field: "request", Code: "invalid", Message: "contains an invalid value"}})
		return
	}
	writeError(w, r, s.logger, err)
}
