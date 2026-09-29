package http

import (
	"errors"
	"net/http"

	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/httputil"
	"github.com/BonzTM/bloom/internal/telemetry"
)

type notificationPreferenceResponse struct {
	EventType core.RequestEventType `json:"event_type"`
	Enabled   bool                  `json:"enabled"`
}

func (s *Server) handleGetNotificationPreferences(w http.ResponseWriter, r *http.Request) {
	account, ok := accountFrom(r.Context())
	if !ok {
		writeError(w, r, s.logger, errAuthenticationRequired)
		return
	}
	preferences, err := s.notificationRouting.Preferences(r.Context(), account.ID)
	if err != nil {
		writeError(w, r, s.logger, err)
		return
	}
	writeJSON(w, r, s.logger, http.StatusOK, notificationPreferencesDTO(preferences))
}

func (s *Server) handleUpdateNotificationPreferences(w http.ResponseWriter, r *http.Request) {
	if !loginContentTypeSupported(r.Header.Get("Content-Type")) {
		writeError(w, r, s.logger, errUnsupportedMediaType)
		return
	}
	account, ok := accountFrom(r.Context())
	if !ok {
		writeError(w, r, s.logger, errAuthenticationRequired)
		return
	}
	body, err := httputil.DecodeJSON[[]notificationPreferenceResponse](w, r, s.maxBodyBytes)
	if err != nil {
		s.writeNotificationPreferenceValidation(w, r)
		return
	}
	values := notificationPreferenceValues(body)
	updated, err := s.notificationRouting.UpdatePreferences(r.Context(), account.ID, values)
	if errors.Is(err, core.ErrInvalidArgument) {
		s.emitNotificationAudit(r, "notification_preferences.update", "account:"+account.ID, "", telemetry.AuditFailure)
		s.writeNotificationPreferenceValidation(w, r)
		return
	}
	if err != nil {
		s.emitNotificationAudit(r, "notification_preferences.update", "account:"+account.ID, "", telemetry.AuditFailure)
		writeError(w, r, s.logger, err)
		return
	}
	s.emitNotificationAudit(r, "notification_preferences.update", "account:"+account.ID, "", telemetry.AuditSuccess)
	writeJSON(w, r, s.logger, http.StatusOK, notificationPreferencesDTO(updated))
}

func (s *Server) writeNotificationPreferenceValidation(w http.ResponseWriter, r *http.Request) {
	s.writeValidation(w, r, []httputil.FieldError{{
		Field: "preferences", Code: "invalid",
		Message: "must contain every supported event exactly once",
	}})
}

func notificationPreferenceValues(values []notificationPreferenceResponse) []core.NotificationPreference {
	result := make([]core.NotificationPreference, 0, len(values))
	for _, value := range values {
		result = append(result, core.NotificationPreference{
			EventType: value.EventType, Enabled: value.Enabled,
		})
	}
	return result
}

func notificationPreferencesDTO(value []core.NotificationPreference) []notificationPreferenceResponse {
	response := make([]notificationPreferenceResponse, 0, len(value))
	for _, preference := range value {
		response = append(response, notificationPreferenceResponse{
			EventType: preference.EventType, Enabled: preference.Enabled,
		})
	}
	return response
}

func (s *Server) handleSubscribeTitle(w http.ResponseWriter, r *http.Request) {
	account, provider, providerID, ok := s.titleSubscriptionInput(w, r)
	if !ok {
		return
	}
	resource := titleSubscriptionResource(provider, providerID)
	if err := s.notificationRouting.SubscribeTitle(r.Context(), account.ID, provider, providerID); err != nil {
		s.emitNotificationAudit(r, "title_subscription.create", resource, "", telemetry.AuditFailure)
		writeError(w, r, s.logger, err)
		return
	}
	s.emitNotificationAudit(r, "title_subscription.create", resource, "", telemetry.AuditSuccess)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleUnsubscribeTitle(w http.ResponseWriter, r *http.Request) {
	account, provider, providerID, ok := s.titleSubscriptionInput(w, r)
	if !ok {
		return
	}
	resource := titleSubscriptionResource(provider, providerID)
	if err := s.notificationRouting.UnsubscribeTitle(r.Context(), account.ID, provider, providerID); err != nil {
		s.emitNotificationAudit(r, "title_subscription.delete", resource, "", telemetry.AuditFailure)
		writeError(w, r, s.logger, err)
		return
	}
	s.emitNotificationAudit(r, "title_subscription.delete", resource, "", telemetry.AuditSuccess)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) titleSubscriptionInput(
	w http.ResponseWriter, r *http.Request,
) (core.Account, core.MetadataProviderKind, string, bool) {
	account, ok := accountFrom(r.Context())
	provider := core.MetadataProviderKind(r.PathValue("provider"))
	providerID := r.PathValue("provider_id")
	if !ok {
		writeError(w, r, s.logger, errAuthenticationRequired)
		return core.Account{}, "", "", false
	}
	if !provider.Valid() || core.ValidateProviderID(providerID) != nil {
		s.writeRequestError(w, r, core.ErrInvalidArgument)
		return core.Account{}, "", "", false
	}
	return account, provider, providerID, true
}

func titleSubscriptionResource(provider core.MetadataProviderKind, providerID string) string {
	return "title:" + string(provider) + ":" + providerID
}
