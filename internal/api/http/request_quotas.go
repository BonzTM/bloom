package http

import (
	"net/http"
	"time"

	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/httputil"
	"github.com/BonzTM/bloom/internal/telemetry"
)

const maxRequestQuotaPeriodDays = 3650

type requestQuotaBody struct {
	MovieLimit       int `json:"movie_limit"`
	MoviePeriodDays  int `json:"movie_period_days"`
	SeasonLimit      int `json:"season_limit"`
	SeasonPeriodDays int `json:"season_period_days"`
}

type requestQuotaResponse struct {
	requestQuotaBody
	ScopeID string `json:"scope_id"`
}

func (s *Server) handleGetRoleRequestQuota(w http.ResponseWriter, r *http.Request) {
	id, ok := s.quotaID(w, r)
	if !ok {
		return
	}
	quota, err := s.requestService.GetRoleQuota(r.Context(), id)
	if err != nil {
		writeError(w, r, s.logger, err)
		return
	}
	writeJSON(w, r, s.logger, http.StatusOK, quotaDTO(id, quota.Quota))
}

func (s *Server) handleSetRoleRequestQuota(w http.ResponseWriter, r *http.Request) {
	id, ok := s.quotaID(w, r)
	if !ok {
		return
	}
	quota, ok := s.decodeQuota(w, r)
	if !ok {
		return
	}
	err := s.requestService.SetRoleQuota(r.Context(), core.RoleRequestQuota{RoleID: id, Quota: quota})
	if err != nil {
		s.emitSettingsAudit(r, "request_quota.role.set", "role:"+id, telemetry.AuditFailure)
		writeError(w, r, s.logger, err)
		return
	}
	s.emitSettingsAudit(r, "request_quota.role.set", "role:"+id, telemetry.AuditSuccess)
	writeJSON(w, r, s.logger, http.StatusOK, quotaDTO(id, quota))
}

func (s *Server) handleDeleteRoleRequestQuota(w http.ResponseWriter, r *http.Request) {
	id, ok := s.quotaID(w, r)
	if !ok {
		return
	}
	if err := s.requestService.DeleteRoleQuota(r.Context(), id); err != nil {
		writeError(w, r, s.logger, err)
		return
	}
	s.emitSettingsAudit(r, "request_quota.role.delete", "role:"+id, telemetry.AuditSuccess)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleGetAccountRequestQuota(w http.ResponseWriter, r *http.Request) {
	id, ok := s.quotaID(w, r)
	if !ok {
		return
	}
	quota, err := s.requestService.GetAccountQuota(r.Context(), id)
	if err != nil {
		writeError(w, r, s.logger, err)
		return
	}
	writeJSON(w, r, s.logger, http.StatusOK, quotaDTO(id, quota.Quota))
}

func (s *Server) handleSetAccountRequestQuota(w http.ResponseWriter, r *http.Request) {
	id, ok := s.quotaID(w, r)
	if !ok {
		return
	}
	quota, ok := s.decodeQuota(w, r)
	if !ok {
		return
	}
	err := s.requestService.SetAccountQuota(r.Context(), core.AccountRequestQuota{AccountID: id, Quota: quota})
	if err != nil {
		s.emitSettingsAudit(r, "request_quota.account.set", "account:"+id, telemetry.AuditFailure)
		writeError(w, r, s.logger, err)
		return
	}
	s.emitSettingsAudit(r, "request_quota.account.set", "account:"+id, telemetry.AuditSuccess)
	writeJSON(w, r, s.logger, http.StatusOK, quotaDTO(id, quota))
}

func (s *Server) handleDeleteAccountRequestQuota(w http.ResponseWriter, r *http.Request) {
	id, ok := s.quotaID(w, r)
	if !ok {
		return
	}
	if err := s.requestService.DeleteAccountQuota(r.Context(), id); err != nil {
		writeError(w, r, s.logger, err)
		return
	}
	s.emitSettingsAudit(r, "request_quota.account.delete", "account:"+id, telemetry.AuditSuccess)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) quotaID(w http.ResponseWriter, r *http.Request) (string, bool) {
	id := r.PathValue("id")
	if !core.ValidID(id) {
		writeError(w, r, s.logger, core.ErrNotFound)
		return "", false
	}
	return id, true
}

func (s *Server) decodeQuota(w http.ResponseWriter, r *http.Request) (core.RequestQuota, bool) {
	if !loginContentTypeSupported(r.Header.Get("Content-Type")) {
		writeError(w, r, s.logger, errUnsupportedMediaType)
		return core.RequestQuota{}, false
	}
	body, err := httputil.DecodeJSON[requestQuotaBody](w, r, s.maxBodyBytes)
	if err != nil {
		s.writeRequestError(w, r, core.ErrInvalidArgument)
		return core.RequestQuota{}, false
	}
	if body.MoviePeriodDays < 0 || body.MoviePeriodDays > maxRequestQuotaPeriodDays ||
		body.SeasonPeriodDays < 0 || body.SeasonPeriodDays > maxRequestQuotaPeriodDays {
		s.writeRequestError(w, r, core.ErrInvalidArgument)
		return core.RequestQuota{}, false
	}
	quota := core.RequestQuota{
		MovieLimit: body.MovieLimit, MoviePeriod: time.Duration(body.MoviePeriodDays) * 24 * time.Hour,
		SeasonLimit: body.SeasonLimit, SeasonPeriod: time.Duration(body.SeasonPeriodDays) * 24 * time.Hour,
	}
	if err := core.ValidateRequestQuota(quota); err != nil {
		s.writeRequestError(w, r, err)
		return core.RequestQuota{}, false
	}
	return quota, true
}

func quotaDTO(id string, quota core.RequestQuota) requestQuotaResponse {
	return requestQuotaResponse{ScopeID: id, requestQuotaBody: requestQuotaBody{
		MovieLimit:      quota.MovieLimit,
		MoviePeriodDays: int(quota.MoviePeriod / (24 * time.Hour)), SeasonLimit: quota.SeasonLimit,
		SeasonPeriodDays: int(quota.SeasonPeriod / (24 * time.Hour)),
	}}
}
