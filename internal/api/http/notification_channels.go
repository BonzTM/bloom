package http

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/httputil"
	notifyapp "github.com/BonzTM/bloom/internal/notify"
	"github.com/BonzTM/bloom/internal/telemetry"
)

type notificationChannelRequest struct {
	Kind            core.NotificationKind   `json:"kind"`
	Name            string                  `json:"name"`
	Enabled         bool                    `json:"enabled"`
	Subscriptions   []core.RequestEventType `json:"subscriptions"`
	SubjectTemplate string                  `json:"subject_template"`
	BodyTemplate    string                  `json:"body_template"`
	Webhook         *webhookChannelRequest  `json:"webhook,omitempty"`
	Discord         *discordChannelRequest  `json:"discord,omitempty"`
	Email           *emailChannelRequest    `json:"email,omitempty"`
}

type webhookChannelRequest struct {
	URL           string `json:"url"`
	SharedSecret  string `json:"shared_secret"`
	AllowInsecure bool   `json:"allow_insecure"`
	AllowPrivate  bool   `json:"allow_private"`
}

type discordChannelRequest struct {
	WebhookURL    string `json:"webhook_url"`
	AllowInsecure bool   `json:"allow_insecure"`
	AllowPrivate  bool   `json:"allow_private"`
}

type emailChannelRequest struct {
	SMTPHost     string                    `json:"smtp_host"`
	SMTPPort     int                       `json:"smtp_port"`
	TLSMode      core.NotificationTLSMode  `json:"tls_mode"`
	AuthMode     core.NotificationAuthMode `json:"auth_mode"`
	Username     string                    `json:"username"`
	Password     *string                   `json:"password,omitempty"`
	FromAddress  string                    `json:"from_address"`
	FromName     string                    `json:"from_name"`
	Recipients   []string                  `json:"recipients"`
	AllowPrivate bool                      `json:"allow_private"`
}

type notificationCredentialPresence struct {
	URLSet          bool `json:"url_set"`
	SharedSecretSet bool `json:"shared_secret_set"`
	PasswordSet     bool `json:"password_set"`
}

type notificationChannelResponse struct {
	ID                  string                         `json:"id"`
	Kind                core.NotificationKind          `json:"kind"`
	Name                string                         `json:"name"`
	Enabled             bool                           `json:"enabled"`
	Subscriptions       []core.RequestEventType        `json:"subscriptions"`
	SubjectTemplate     string                         `json:"subject_template"`
	BodyTemplate        string                         `json:"body_template"`
	AllowInsecure       bool                           `json:"allow_insecure"`
	AllowPrivate        bool                           `json:"allow_private"`
	SMTPHost            string                         `json:"smtp_host,omitempty"`
	SMTPPort            int                            `json:"smtp_port,omitempty"`
	TLSMode             core.NotificationTLSMode       `json:"tls_mode,omitempty"`
	AuthMode            core.NotificationAuthMode      `json:"auth_mode,omitempty"`
	Username            string                         `json:"username,omitempty"`
	FromAddress         string                         `json:"from_address,omitempty"`
	FromName            string                         `json:"from_name,omitempty"`
	Recipients          []string                       `json:"recipients,omitempty"`
	Credentials         notificationCredentialPresence `json:"credentials"`
	DegradedAt          *time.Time                     `json:"degraded_at,omitempty"`
	ConsecutiveFailures int                            `json:"consecutive_failures"`
	CreatedAt           time.Time                      `json:"created_at"`
	UpdatedAt           time.Time                      `json:"updated_at"`
}

type notificationChannelsResponse struct {
	Items      []notificationChannelResponse `json:"items"`
	NextCursor string                        `json:"next_cursor"`
}

type notificationDeliveryResponse struct {
	ID            string                  `json:"id"`
	EventType     core.RequestEventType   `json:"event_type"`
	RequestID     string                  `json:"request_id"`
	Status        core.NotificationStatus `json:"status"`
	Attempts      int                     `json:"attempts"`
	LastError     string                  `json:"last_error"`
	NextAttemptAt time.Time               `json:"next_attempt_at"`
	SentAt        *time.Time              `json:"sent_at,omitempty"`
	CreatedAt     time.Time               `json:"created_at"`
	UpdatedAt     time.Time               `json:"updated_at"`
}

type notificationDeliveriesResponse struct {
	Items      []notificationDeliveryResponse `json:"items"`
	NextCursor string                         `json:"next_cursor"`
}

func (s *Server) handleCreateNotificationChannel(w http.ResponseWriter, r *http.Request) {
	input, ok := s.decodeNotificationChannel(w, r, true)
	if !ok {
		return
	}
	created, err := s.notificationManager.Register(r.Context(), input)
	if err != nil {
		s.emitNotificationAudit(r, "notification_channel.create", "notification_channel:unresolved", input.Kind, telemetry.AuditFailure)
		writeError(w, r, s.logger, err)
		return
	}
	s.emitNotificationAudit(r, "notification_channel.create", "notification_channel:"+created.ID, created.Kind, telemetry.AuditSuccess)
	writeJSON(w, r, s.logger, http.StatusCreated, notificationChannelDTO(created))
}

func (s *Server) handleListNotificationChannels(w http.ResponseWriter, r *http.Request) {
	after, size, fields := mediaServerPageParams(r)
	if len(fields) != 0 {
		s.writeValidation(w, r, fields)
		return
	}
	values, err := s.notificationReader.List(r.Context(), after, size+1)
	if err != nil {
		writeError(w, r, s.logger, err)
		return
	}
	values, cursor := notificationChannelPage(values, size)
	response := notificationChannelsResponse{Items: make([]notificationChannelResponse, 0, len(values)), NextCursor: cursor}
	for _, value := range values {
		response.Items = append(response.Items, notificationChannelDTO(value))
	}
	writeJSON(w, r, s.logger, http.StatusOK, response)
}

func (s *Server) handleGetNotificationChannel(w http.ResponseWriter, r *http.Request) {
	id, ok := s.notificationChannelID(w, r)
	if !ok {
		return
	}
	value, err := s.notificationReader.Get(r.Context(), id)
	if err != nil {
		writeError(w, r, s.logger, err)
		return
	}
	writeJSON(w, r, s.logger, http.StatusOK, notificationChannelDTO(value))
}

func (s *Server) handleUpdateNotificationChannel(w http.ResponseWriter, r *http.Request) {
	id, ok := s.notificationChannelID(w, r)
	if !ok {
		return
	}
	input, ok := s.decodeNotificationChannel(w, r, false)
	if !ok {
		return
	}
	updated, err := s.notificationManager.Update(r.Context(), id, input)
	if err != nil {
		s.emitNotificationAudit(r, "notification_channel.update", "notification_channel:"+id, input.Kind, telemetry.AuditFailure)
		writeError(w, r, s.logger, err)
		return
	}
	s.emitNotificationAudit(r, "notification_channel.update", "notification_channel:"+id, updated.Kind, telemetry.AuditSuccess)
	writeJSON(w, r, s.logger, http.StatusOK, notificationChannelDTO(updated))
}

func (s *Server) handleDeleteNotificationChannel(w http.ResponseWriter, r *http.Request) {
	id, ok := s.notificationChannelID(w, r)
	if !ok {
		return
	}
	value, err := s.notificationManager.Delete(r.Context(), id)
	if err != nil {
		s.emitNotificationAudit(r, "notification_channel.delete", "notification_channel:"+id, "", telemetry.AuditFailure)
		writeError(w, r, s.logger, err)
		return
	}
	s.emitNotificationAudit(r, "notification_channel.delete", "notification_channel:"+id, value.Kind, telemetry.AuditSuccess)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleTestNotificationChannel(w http.ResponseWriter, r *http.Request) {
	id, ok := s.notificationChannelID(w, r)
	if !ok {
		return
	}
	value, err := s.notificationReader.Get(r.Context(), id)
	if err == nil {
		err = s.notificationTester.Test(r.Context(), id)
	}
	if err != nil {
		s.emitNotificationAudit(r, "notification_channel.test", "notification_channel:"+id, value.Kind, telemetry.AuditFailure)
		writeError(w, r, s.logger, err)
		return
	}
	s.emitNotificationAudit(r, "notification_channel.test", "notification_channel:"+id, value.Kind, telemetry.AuditSuccess)
	writeJSON(w, r, s.logger, http.StatusOK, map[string]string{"outcome": "sent"})
}

func (s *Server) handleListNotificationDeliveries(w http.ResponseWriter, r *http.Request) {
	id, ok := s.notificationChannelID(w, r)
	if !ok {
		return
	}
	after, size, fields := notificationDeliveryPageParams(r)
	if len(fields) != 0 {
		s.writeValidation(w, r, fields)
		return
	}
	values, err := s.notificationReader.Deliveries(r.Context(), id, after, size+1)
	if err != nil {
		writeError(w, r, s.logger, err)
		return
	}
	values, cursor := notificationDeliveryPage(values, size)
	response := notificationDeliveriesResponse{Items: make([]notificationDeliveryResponse, 0, len(values)), NextCursor: cursor}
	for _, value := range values {
		response.Items = append(response.Items, notificationDeliveryDTO(value))
	}
	writeJSON(w, r, s.logger, http.StatusOK, response)
}

func (s *Server) decodeNotificationChannel(w http.ResponseWriter, r *http.Request, secretRequired bool) (notifyapp.RegistrationInput, bool) {
	if !loginContentTypeSupported(r.Header.Get("Content-Type")) {
		writeError(w, r, s.logger, errUnsupportedMediaType)
		return notifyapp.RegistrationInput{}, false
	}
	body, err := httputil.DecodeJSON[notificationChannelRequest](w, r, s.maxBodyBytes)
	if err != nil {
		s.writeValidation(w, r, []httputil.FieldError{{Field: "body", Code: "invalid", Message: "must be one valid notification channel"}})
		return notifyapp.RegistrationInput{}, false
	}
	if fields := missingNotificationCredentialFields(body, secretRequired); len(fields) != 0 {
		s.writeValidation(w, r, fields)
		return notifyapp.RegistrationInput{}, false
	}
	input, err := notificationInput(body, secretRequired)
	if err != nil {
		s.writeValidation(w, r, []httputil.FieldError{{Field: "body", Code: "invalid", Message: "contains invalid channel settings"}})
		return notifyapp.RegistrationInput{}, false
	}
	if err := core.ValidateNotificationRecipients(input.Settings.Recipients); errors.Is(err, core.ErrDuplicateNotificationRecipient) {
		s.writeValidation(w, r, []httputil.FieldError{{
			Field: "email.recipients", Code: "unique", Message: "must not contain duplicate email addresses",
		}})
		return notifyapp.RegistrationInput{}, false
	}
	return input, true
}

func missingNotificationCredentialFields(body notificationChannelRequest, required bool) []httputil.FieldError {
	if !required {
		return nil
	}
	missing := func(field string) httputil.FieldError {
		return httputil.FieldError{Field: field, Code: "required", Message: "is required when creating this channel kind"}
	}
	var fields []httputil.FieldError
	switch body.Kind {
	case core.NotificationKindWebhook:
		if body.Webhook != nil && body.Webhook.URL == "" {
			fields = append(fields, missing("webhook.url"))
		}
		if body.Webhook != nil && body.Webhook.SharedSecret == "" {
			fields = append(fields, missing("webhook.shared_secret"))
		}
	case core.NotificationKindDiscord:
		if body.Discord != nil && body.Discord.WebhookURL == "" {
			fields = append(fields, missing("discord.webhook_url"))
		}
	case core.NotificationKindEmail:
		if body.Email != nil && (body.Email.Password == nil || *body.Email.Password == "") {
			fields = append(fields, missing("email.password"))
		}
	}
	return fields
}

func notificationInput(body notificationChannelRequest, secretRequired bool) (notifyapp.RegistrationInput, error) {
	input := notifyapp.RegistrationInput{
		Kind: body.Kind, Name: body.Name, Enabled: body.Enabled,
		Subscriptions: append([]core.RequestEventType(nil), body.Subscriptions...),
		Settings:      core.NotificationSettings{SubjectTemplate: body.SubjectTemplate, BodyTemplate: body.BodyTemplate},
	}
	credentials := notifyapp.Credentials{}
	supplied := false
	switch body.Kind {
	case core.NotificationKindWebhook:
		if body.Webhook == nil || body.Discord != nil || body.Email != nil {
			return input, core.ErrInvalidArgument
		}
		input.Settings.AllowInsecure, input.Settings.AllowPrivate = body.Webhook.AllowInsecure, body.Webhook.AllowPrivate
		credentials.WebhookURL, credentials.SharedSecret = body.Webhook.URL, body.Webhook.SharedSecret
		supplied = credentials.WebhookURL != "" || credentials.SharedSecret != ""
	case core.NotificationKindDiscord:
		if body.Discord == nil || body.Webhook != nil || body.Email != nil {
			return input, core.ErrInvalidArgument
		}
		input.Settings.AllowInsecure, input.Settings.AllowPrivate = body.Discord.AllowInsecure, body.Discord.AllowPrivate
		credentials.DiscordURL, supplied = body.Discord.WebhookURL, body.Discord.WebhookURL != ""
	case core.NotificationKindEmail:
		if body.Email == nil || body.Webhook != nil || body.Discord != nil {
			return input, core.ErrInvalidArgument
		}
		input.Target = body.Email.SMTPHost
		input.Settings.AllowPrivate = body.Email.AllowPrivate
		input.Settings.SMTPPort, input.Settings.TLSMode, input.Settings.AuthMode = body.Email.SMTPPort, body.Email.TLSMode, body.Email.AuthMode
		input.Settings.Username, input.Settings.FromAddress, input.Settings.FromName = body.Email.Username, body.Email.FromAddress, body.Email.FromName
		input.Settings.Recipients = append([]string(nil), body.Email.Recipients...)
		if body.Email.Password != nil {
			credentials.Password, supplied = *body.Email.Password, true
		}
	default:
		return input, core.ErrInvalidArgument
	}
	if secretRequired && !supplied {
		return input, core.ErrInvalidArgument
	}
	if supplied {
		input.Credentials = &credentials
	}
	return input, nil
}

func notificationChannelDTO(value core.NotificationRegistration) notificationChannelResponse {
	settings := value.Settings
	return notificationChannelResponse{
		ID: value.ID, Kind: value.Kind, Name: value.Name, Enabled: value.Enabled,
		Subscriptions:   append([]core.RequestEventType{}, value.Subscriptions...),
		SubjectTemplate: settings.SubjectTemplate, BodyTemplate: settings.BodyTemplate,
		AllowInsecure: settings.AllowInsecure, AllowPrivate: settings.AllowPrivate,
		SMTPHost: value.Target, SMTPPort: settings.SMTPPort, TLSMode: settings.TLSMode, AuthMode: settings.AuthMode,
		Username: settings.Username, FromAddress: settings.FromAddress, FromName: settings.FromName,
		Recipients: append([]string(nil), settings.Recipients...),
		Credentials: notificationCredentialPresence{
			URLSet:          value.SecretSet && (value.Kind == core.NotificationKindWebhook || value.Kind == core.NotificationKindDiscord),
			SharedSecretSet: value.SecretSet && value.Kind == core.NotificationKindWebhook,
			PasswordSet:     value.SecretSet && value.Kind == core.NotificationKindEmail,
		},
		DegradedAt: value.DegradedAt, ConsecutiveFailures: value.ConsecutiveFailures,
		CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt,
	}
}

func notificationDeliveryDTO(value core.NotificationDelivery) notificationDeliveryResponse {
	return notificationDeliveryResponse{
		ID: value.ID, EventType: value.EventType, RequestID: value.Payload.RequestID,
		Status: value.Status, Attempts: value.Attempts, LastError: value.LastError,
		NextAttemptAt: value.NextAttemptAt, SentAt: value.SentAt, CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt,
	}
}

func (s *Server) notificationChannelID(w http.ResponseWriter, r *http.Request) (string, bool) {
	id := r.PathValue("id")
	if core.ValidID(id) {
		return id, true
	}
	s.writeValidation(w, r, []httputil.FieldError{{Field: "id", Code: "invalid", Message: "must be a valid UUID"}})
	return "", false
}

func notificationChannelPage(values []core.NotificationRegistration, size int) ([]core.NotificationRegistration, string) {
	if len(values) <= size {
		return values, ""
	}
	page := values[:size]
	key := core.NotificationNameKey(page[len(page)-1].Name)
	return page, base64.RawURLEncoding.EncodeToString([]byte(key))
}

func notificationDeliveryPageParams(r *http.Request) (*core.NotificationDeliveryCursor, int, []httputil.FieldError) {
	size := defaultRequestPageSize
	var fields []httputil.FieldError
	if raw := r.URL.Query().Get("page_size"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 {
			fields = append(fields, httputil.FieldError{Field: "page_size", Code: "invalid", Message: "must be a positive integer"})
		} else {
			size = min(parsed, maxRequestPageSize)
		}
	}
	after, err := decodeNotificationDeliveryCursor(r.URL.Query().Get("cursor"))
	if err != nil {
		fields = append(fields, httputil.FieldError{Field: "cursor", Code: "invalid", Message: "must be a valid delivery cursor"})
	}
	return after, size, fields
}

func decodeNotificationDeliveryCursor(value string) (*core.NotificationDeliveryCursor, error) {
	if value == "" {
		return nil, nil
	}
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return nil, err
	}
	parts := strings.SplitN(string(decoded), "\x00", 2)
	if len(parts) != 2 || !core.ValidID(parts[1]) {
		return nil, core.ErrInvalidArgument
	}
	created, err := time.Parse(time.RFC3339Nano, parts[0])
	if err != nil {
		return nil, err
	}
	return &core.NotificationDeliveryCursor{CreatedAt: created, ID: parts[1]}, nil
}

func notificationDeliveryPage(values []core.NotificationDelivery, size int) ([]core.NotificationDelivery, string) {
	if len(values) <= size {
		return values, ""
	}
	page := values[:size]
	last := page[len(page)-1]
	raw := fmt.Sprintf("%s\x00%s", last.CreatedAt.UTC().Format(time.RFC3339Nano), last.ID)
	return page, base64.RawURLEncoding.EncodeToString([]byte(raw))
}

func (s *Server) emitNotificationAudit(
	r *http.Request, action, resource string, kind core.NotificationKind, result telemetry.AuditResult,
) {
	account, _ := accountFrom(r.Context())
	err := s.audit.Emit(r.Context(), telemetry.AuditEvent{
		Actor: account.ID, Action: action, Resource: resource, Kind: string(kind), Result: result,
		Source: clientIP(r, s.trustedProxyCIDRs), RequestID: requestIDFrom(r.Context()),
	})
	if err != nil {
		s.auditFailureMetrics.IncAuditWriteFailure()
		s.logger.ErrorContext(r.Context(), "write audit event", "error", err, "action", action)
	}
}
