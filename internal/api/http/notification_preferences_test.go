package http

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"

	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/telemetry"
)

const notificationPreferenceAccountID = "11111111-1111-4111-8111-111111111111"

type notificationRoutingStub struct {
	preferences  []core.NotificationPreference
	subscribeErr error
}

func (s notificationRoutingStub) Preferences(context.Context, string) ([]core.NotificationPreference, error) {
	return s.preferences, nil
}

func (notificationRoutingStub) UpdatePreferences(
	_ context.Context, _ string, values []core.NotificationPreference,
) ([]core.NotificationPreference, error) {
	if err := core.ValidateNotificationPreferences(values); err != nil {
		return nil, err
	}
	return values, nil
}

func (s notificationRoutingStub) SubscribeTitle(context.Context, string, core.MetadataProviderKind, string) error {
	return s.subscribeErr
}

func (notificationRoutingStub) UnsubscribeTitle(context.Context, string, core.MetadataProviderKind, string) error {
	return nil
}

func (notificationRoutingStub) TitleSubscribed(context.Context, string, core.MetadataProviderKind, string) (bool, error) {
	return false, nil
}

func TestNotificationPreferenceHandlersReturnDirectMatrix(t *testing.T) {
	values := completeHTTPNotificationPreferences()
	body, err := json.Marshal(values)
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{
		logger: slog.New(slog.DiscardHandler), maxBodyBytes: 1 << 20,
		notificationRouting: notificationRoutingStub{preferences: notificationPreferenceValues(values)},
		audit:               &recordingAudit{}, auditFailureMetrics: telemetry.NopMetrics{},
	}
	tests := []struct {
		name, method string
		body         string
		handler      http.HandlerFunc
	}{
		{name: "get", method: http.MethodGet, handler: server.handleGetNotificationPreferences},
		{name: "put", method: http.MethodPut, body: string(body), handler: server.handleUpdateNotificationPreferences},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(test.method, "https://bloom.test/api/v1/me/notification-preferences", strings.NewReader(test.body))
			request.Header.Set("Content-Type", "application/json")
			ctx := context.WithValue(request.Context(), accountKey, core.Account{ID: notificationPreferenceAccountID})
			recorder := httptest.NewRecorder()
			test.handler(recorder, request.WithContext(ctx))
			if recorder.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200: %s", recorder.Code, recorder.Body.String())
			}
			assertJSONMatchesSchema(t, loadOpenAPI(t), recorder.Body.Bytes(), "#/components/schemas/NotificationPreferences")
		})
	}
}

func TestNotificationPreferenceUpdateRejectsIncompleteMatrixAndAuditsFailure(t *testing.T) {
	audit := &recordingAudit{}
	server := &Server{
		logger: slog.New(slog.DiscardHandler), maxBodyBytes: 1 << 20,
		notificationRouting: notificationRoutingStub{}, audit: audit,
		auditFailureMetrics: telemetry.NopMetrics{},
	}
	request := httptest.NewRequest(
		http.MethodPut, "https://bloom.test/api/v1/me/notification-preferences",
		strings.NewReader(`[]`),
	)
	request.Header.Set("Content-Type", "application/json")
	ctx := context.WithValue(request.Context(), accountKey, core.Account{ID: notificationPreferenceAccountID})
	ctx = context.WithValue(ctx, requestIDKey, "preference-request")
	recorder := httptest.NewRecorder()
	server.handleUpdateNotificationPreferences(recorder, request.WithContext(ctx))
	if recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422: %s", recorder.Code, recorder.Body.String())
	}
	event := audit.last(t)
	if event.Action != "notification_preferences.update" || event.Result != telemetry.AuditFailure ||
		event.Actor != notificationPreferenceAccountID {
		t.Fatalf("audit event = %+v", event)
	}
}

func TestNotificationPreferenceUpdateRejectsDuplicateEvent(t *testing.T) {
	values := completeHTTPNotificationPreferences()
	values[0].EventType = values[1].EventType
	values[0].Enabled = false
	body, err := json.Marshal(values)
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{
		logger: slog.New(slog.DiscardHandler), maxBodyBytes: 1 << 20,
		notificationRouting: notificationRoutingStub{}, audit: &recordingAudit{},
		auditFailureMetrics: telemetry.NopMetrics{},
	}
	request := httptest.NewRequest(http.MethodPut,
		"https://bloom.test/api/v1/me/notification-preferences", strings.NewReader(string(body)))
	request.Header.Set("Content-Type", "application/json")
	ctx := context.WithValue(request.Context(), accountKey, core.Account{ID: notificationPreferenceAccountID})
	ctx = context.WithValue(ctx, requestIDKey, "duplicate-preference-request")
	recorder := httptest.NewRecorder()
	server.handleUpdateNotificationPreferences(recorder, request.WithContext(ctx))
	if recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422: %s", recorder.Code, recorder.Body.String())
	}
	assertJSONMatchesSchema(t, loadOpenAPI(t), recorder.Body.Bytes(), errorSchema)
}

func TestNotificationPreferenceOpenAPIRejectsMissingAndDuplicateEntries(t *testing.T) {
	document := loadOpenAPI(t)
	complete := completeHTTPNotificationPreferences()
	missing := complete[:len(complete)-1]
	duplicate := append(append([]notificationPreferenceResponse(nil), missing...), missing[0])
	duplicate[len(duplicate)-1].Enabled = false
	for name, value := range map[string]any{"missing": preferenceContractValue(missing), "duplicate": preferenceContractValue(duplicate)} {
		if err := validateNotificationPreferenceContract(document, value); err == nil {
			t.Errorf("contract accepted %s event matrix", name)
		}
	}
}

func validateNotificationPreferenceContract(document openAPIDocument, value any) error {
	schema := *document.validator.Components.Schemas["NotificationPreferences"].Value
	item := *schema.Items.Value
	eventType := *item.Properties["event_type"].Value
	item.Properties = openapi3.Schemas{
		"event_type": &openapi3.SchemaRef{Value: &eventType},
		"enabled":    item.Properties["enabled"],
	}
	schema.Items = &openapi3.SchemaRef{Value: &item}
	return schema.VisitJSON(value, openapi3.EnableJSONSchema2020())
}

func TestTitleSubscriptionLimitHTTPMapping(t *testing.T) {
	server := &Server{
		logger: slog.New(slog.DiscardHandler), notificationRouting: notificationRoutingStub{
			subscribeErr: errors.Join(errors.New("subscribe title"), core.ErrTitleSubscriptionLimit),
		},
		audit: &recordingAudit{}, auditFailureMetrics: telemetry.NopMetrics{},
	}
	request := httptest.NewRequest(http.MethodPost, "https://bloom.test/api/v1/titles/tmdb/501/subscription", nil)
	request.SetPathValue("provider", "tmdb")
	request.SetPathValue("provider_id", "501")
	ctx := context.WithValue(request.Context(), accountKey, core.Account{ID: notificationPreferenceAccountID})
	ctx = context.WithValue(ctx, requestIDKey, "subscription-limit-request")
	recorder := httptest.NewRecorder()
	server.handleSubscribeTitle(recorder, request.WithContext(ctx))
	if recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422: %s", recorder.Code, recorder.Body.String())
	}
	response := decodeEnvelope(t, recorder)
	if response.Code != codeTitleSubscriptionLimit {
		t.Fatalf("code = %q, want %q", response.Code, codeTitleSubscriptionLimit)
	}
	assertJSONMatchesSchema(t, loadOpenAPI(t), recorder.Body.Bytes(), errorSchema)
}

func completeHTTPNotificationPreferences() []notificationPreferenceResponse {
	values := make([]notificationPreferenceResponse, 0, len(core.NotificationEventTypes()))
	for _, eventType := range core.NotificationEventTypes() {
		values = append(values, notificationPreferenceResponse{EventType: eventType, Enabled: true})
	}
	return values
}

func preferenceContractValue(values []notificationPreferenceResponse) []any {
	result := make([]any, 0, len(values))
	for _, value := range values {
		result = append(result, map[string]any{"event_type": string(value.EventType), "enabled": value.Enabled})
	}
	return result
}

func TestTitleSubscriptionRoutesRequireRequestViewPermission(t *testing.T) {
	harness := newAuthHarness(t, nil)
	cookie := sessionCookie(t, harness.login(t, "alice", "secret-password"))
	harness.authorization.mu.Lock()
	harness.authorization.permissions[notificationPreferenceAccountID] = nil
	harness.authorization.mu.Unlock()
	for _, route := range apiRouteInventory {
		if route.path != "/api/v1/titles/{provider}/{provider_id}/subscription" {
			continue
		}
		handler, err := harness.server.routeHandler(route)
		if err != nil {
			t.Fatalf("routeHandler: %v", err)
		}
		missing := httptest.NewRecorder()
		handler.ServeHTTP(missing, httptest.NewRequest(route.method, "https://bloom.test/api/v1/titles/tmdb/1/subscription", nil))
		if missing.Code != http.StatusUnauthorized {
			t.Errorf("%s missing session status = %d, want 401", route.method, missing.Code)
		}
		request := httptest.NewRequest(route.method, "https://bloom.test/api/v1/titles/tmdb/1/subscription", nil)
		request.AddCookie(cookie)
		forbidden := httptest.NewRecorder()
		handler.ServeHTTP(forbidden, request)
		if forbidden.Code != http.StatusForbidden {
			t.Errorf("%s insufficient permission status = %d, want 403", route.method, forbidden.Code)
		}
	}
}
