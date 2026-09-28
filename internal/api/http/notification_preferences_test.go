package http

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/telemetry"
)

const notificationPreferenceAccountID = "11111111-1111-4111-8111-111111111111"

type notificationRoutingStub struct{}

func (notificationRoutingStub) Preferences(context.Context, string) (core.NotificationPreferences, error) {
	return core.NotificationPreferences{}, nil
}

func (notificationRoutingStub) UpdatePreferences(
	_ context.Context, _ string, values []core.NotificationPreference,
) (core.NotificationPreferences, error) {
	if err := core.ValidateNotificationPreferences(values); err != nil {
		return core.NotificationPreferences{}, err
	}
	return core.NotificationPreferences{Preferences: values}, nil
}

func (notificationRoutingStub) SubscribeTitle(context.Context, string, core.MetadataProviderKind, string) error {
	return nil
}

func (notificationRoutingStub) UnsubscribeTitle(context.Context, string, core.MetadataProviderKind, string) error {
	return nil
}

func (notificationRoutingStub) TitleSubscribed(context.Context, string, core.MetadataProviderKind, string) (bool, error) {
	return false, nil
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
		strings.NewReader(`{"preferences":[]}`),
	)
	request.Header.Set("Content-Type", "application/json")
	ctx := context.WithValue(request.Context(), accountKey, core.Account{ID: notificationPreferenceAccountID})
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
