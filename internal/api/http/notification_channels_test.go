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
	"time"

	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/httputil"
	notifyapp "github.com/BonzTM/bloom/internal/notify"
	"github.com/BonzTM/bloom/internal/telemetry"
)

func TestNotificationRoutesRejectMissingAndInsufficientSessions(t *testing.T) {
	h := newAuthHarness(t, nil)
	cookie := sessionCookie(t, h.login(t, "alice", "secret-password"))
	h.authorization.mu.Lock()
	h.authorization.permissions["11111111-1111-4111-8111-111111111111"] = nil
	h.authorization.mu.Unlock()
	for _, route := range notificationRoutes() {
		t.Run(route.method+" "+route.path, func(t *testing.T) {
			handler, err := h.server.routeHandler(route)
			if err != nil {
				t.Fatal(err)
			}
			path := concreteRequestPath(route.path)
			missing := httptest.NewRecorder()
			handler.ServeHTTP(missing, httptest.NewRequest(route.method, path, nil))
			if missing.Code != http.StatusUnauthorized {
				t.Fatalf("missing status = %d", missing.Code)
			}
			request := httptest.NewRequest(route.method, path, nil)
			request.AddCookie(cookie)
			forbidden := httptest.NewRecorder()
			handler.ServeHTTP(forbidden, request)
			if forbidden.Code != http.StatusForbidden {
				t.Fatalf("forbidden status = %d", forbidden.Code)
			}
		})
	}
}

func TestNotificationOpenAPIDocumentsOperationsAndFailures(t *testing.T) {
	document := loadOpenAPI(t)
	expected := map[string]map[string][]string{
		"/api/v1/notification-channels": {
			"post": {"401", "403", "409", "415", "422", "502"}, "get": {"401", "403", "422"},
		},
		"/api/v1/notification-channels/{id}": {
			"get": {"401", "403", "404", "422"}, "put": {"401", "403", "404", "409", "415", "422", "502"},
			"delete": {"401", "403", "404", "422"},
		},
		"/api/v1/notification-channels/{id}/test":       {"post": {"401", "403", "404", "422", "502"}},
		"/api/v1/notification-channels/{id}/deliveries": {"get": {"401", "403", "404", "422"}},
	}
	for path, methods := range expected {
		for method, statuses := range methods {
			operation, ok := document.Paths[path][method]
			if !ok {
				t.Fatalf("missing %s %s", method, path)
			}
			for _, status := range append(statuses, "500", "405") {
				if _, ok := operation.Responses[status]; !ok {
					t.Errorf("%s %s missing %s", method, path, status)
				}
			}
		}
	}
}

func TestNotificationStateChangesHaveCSRFAuditResources(t *testing.T) {
	for _, pattern := range []string{
		"/api/v1/notification-channels", "/api/v1/notification-channels/{id}",
		"/api/v1/notification-channels/{id}/test",
	} {
		path := strings.TrimPrefix(concreteRequestPath(pattern), "https://bloom.test")
		if got := csrfAuditResource(path); got == auditResourceRouteUnmatched {
			t.Errorf("csrfAuditResource(%q) = %q", path, got)
		}
	}
}

func TestNotificationResponseNeverContainsSecretsOrWebhookURL(t *testing.T) {
	secretURL := "https://discord.example.test/api/webhooks/full-secret"
	value := core.NotificationRegistration{
		ID: "00000000-0000-4000-8000-000000000001", Kind: core.NotificationKindDiscord,
		Name: "Discord", Target: "", Subscriptions: []core.RequestEventType{core.RequestEventCreated},
		Enabled: true, SecretSet: true, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	encoded, err := json.Marshal(notificationChannelDTO(value))
	if err != nil {
		t.Fatal(err)
	}
	text := string(encoded)
	for _, forbidden := range []string{secretURL, "full-secret", "webhook_url", `"shared_secret":`, `"password":`} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("response contains %q: %s", forbidden, text)
		}
	}
	if !strings.Contains(text, `"url_set":true`) {
		t.Fatalf("response lacks credential presence: %s", text)
	}
}

func TestNotificationInputRequiresOneKindAndRetainsOmittedUpdateSecret(t *testing.T) {
	base := notificationChannelRequest{
		Kind: core.NotificationKindWebhook, Name: "Webhook", Enabled: true,
		Subscriptions: []core.RequestEventType{core.RequestEventCreated},
		Webhook:       &webhookChannelRequest{URL: "https://example.test/hook", SharedSecret: "secret"},
	}
	created, err := notificationInput(base, true)
	if err != nil || created.Credentials == nil {
		t.Fatalf("create input = %+v, %v", created, err)
	}
	base.Webhook.URL, base.Webhook.SharedSecret = "", ""
	updated, err := notificationInput(base, false)
	if err != nil || updated.Credentials != nil {
		t.Fatalf("update input = %+v, %v", updated, err)
	}
	base.Discord = &discordChannelRequest{WebhookURL: "https://example.test/discord"}
	if _, err := notificationInput(base, false); !errors.Is(err, core.ErrInvalidArgument) {
		t.Fatalf("mixed kind error = %v", err)
	}
}

func TestNotificationCredentialContractsMatchHandlers(t *testing.T) {
	t.Parallel()
	document := loadOpenAPI(t)
	tests := []struct {
		name, field, createBody, updateBody string
	}{
		{
			name: "webhook URL", field: "webhook.url",
			createBody: `{"kind":"webhook","name":"Webhook","enabled":true,"subscriptions":["created"],"subject_template":"","body_template":"","webhook":{"shared_secret":"secret","allow_insecure":false,"allow_private":false}}`,
			updateBody: `{"kind":"webhook","name":"Webhook","enabled":true,"subscriptions":["created"],"subject_template":"","body_template":"","webhook":{"allow_insecure":false,"allow_private":false}}`,
		},
		{
			name: "webhook shared secret", field: "webhook.shared_secret",
			createBody: `{"kind":"webhook","name":"Webhook","enabled":true,"subscriptions":["created"],"subject_template":"","body_template":"","webhook":{"url":"https://example.test/hook","allow_insecure":false,"allow_private":false}}`,
			updateBody: `{"kind":"webhook","name":"Webhook","enabled":true,"subscriptions":["created"],"subject_template":"","body_template":"","webhook":{"allow_insecure":false,"allow_private":false}}`,
		},
		{
			name: "discord", field: "discord.webhook_url",
			createBody: `{"kind":"discord","name":"Discord","enabled":true,"subscriptions":["created"],"subject_template":"","body_template":"","discord":{"allow_insecure":false,"allow_private":false}}`,
			updateBody: `{"kind":"discord","name":"Discord","enabled":true,"subscriptions":["created"],"subject_template":"","body_template":"","discord":{"allow_insecure":false,"allow_private":false}}`,
		},
		{
			name: "email", field: "email.password",
			createBody: `{"kind":"email","name":"Email","enabled":true,"subscriptions":["created"],"subject_template":"","body_template":"","email":{"smtp_host":"smtp.example.com","smtp_port":587,"tls_mode":"starttls","auth_mode":"plain","username":"user","from_address":"from@example.com","from_name":"Bloom","recipients":["to@example.com"],"allow_private":false}}`,
			updateBody: `{"kind":"email","name":"Email","enabled":true,"subscriptions":["created"],"subject_template":"","body_template":"","email":{"smtp_host":"smtp.example.com","smtp_port":587,"tls_mode":"starttls","auth_mode":"plain","username":"user","from_address":"from@example.com","from_name":"Bloom","recipients":["to@example.com"],"allow_private":false}}`,
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			assertNotificationCredentialContract(t, document, testCase.createBody, testCase.updateBody)
			assertNotificationCredentialHandlers(t, testCase.createBody, testCase.updateBody, testCase.field)
		})
	}
}

func assertNotificationCredentialContract(t *testing.T, document openAPIDocument, createBody, updateBody string) {
	t.Helper()
	createSchema := document.validator.Paths.Find("/api/v1/notification-channels").Post.RequestBody.Value.Content.Get("application/json").Schema.Value
	updateSchema := document.validator.Paths.Find("/api/v1/notification-channels/{id}").Put.RequestBody.Value.Content.Get("application/json").Schema.Value
	createValue, err := decodeContractJSON([]byte(createBody))
	if err != nil {
		t.Fatal(err)
	}
	if validationErr := createSchema.VisitJSON(createValue); validationErr == nil {
		t.Fatal("create schema accepted omitted credentials")
	}
	updateValue, err := decodeContractJSON([]byte(updateBody))
	if err != nil {
		t.Fatal(err)
	}
	if err := updateSchema.VisitJSON(updateValue); err != nil {
		t.Fatalf("update schema rejected omitted credentials: %v", err)
	}
}

func assertNotificationCredentialHandlers(t *testing.T, createBody, updateBody, field string) {
	t.Helper()
	manager := &notificationContractManager{}
	server := &Server{
		logger: slog.New(slog.DiscardHandler), maxBodyBytes: 64 * 1024, notificationManager: manager,
		audit: telemetry.NopAuditLogger(), auditFailureMetrics: telemetry.NopMetrics{},
	}
	create := notificationHandlerRequest(http.MethodPost, "/api/v1/notification-channels", createBody)
	createRecorder := httptest.NewRecorder()
	server.handleCreateNotificationChannel(createRecorder, create)
	assertNotificationCredentialError(t, createRecorder, field)
	update := notificationHandlerRequest(http.MethodPut, "/api/v1/notification-channels/id", updateBody)
	update.SetPathValue("id", "00000000-0000-4000-8000-000000000001")
	updateRecorder := httptest.NewRecorder()
	server.handleUpdateNotificationChannel(updateRecorder, update)
	if updateRecorder.Code != http.StatusOK || manager.updates != 1 {
		t.Fatalf("update = %d %s after %d calls", updateRecorder.Code, updateRecorder.Body.String(), manager.updates)
	}
}

func notificationHandlerRequest(method, path, body string) *http.Request {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	return request
}

func assertNotificationCredentialError(t *testing.T, recorder *httptest.ResponseRecorder, field string) {
	t.Helper()
	var response httputil.ErrorResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if recorder.Code != http.StatusUnprocessableEntity || len(response.Fields) != 1 ||
		response.Fields[0].Field != field || response.Fields[0].Code != "required" {
		t.Fatalf("create response = %d %+v", recorder.Code, response)
	}
}

type notificationContractManager struct{ updates int }

func (*notificationContractManager) Register(context.Context, notifyapp.RegistrationInput) (core.NotificationRegistration, error) {
	return core.NotificationRegistration{}, errors.New("unexpected register")
}

func (m *notificationContractManager) Update(
	_ context.Context, id string, input notifyapp.RegistrationInput,
) (core.NotificationRegistration, error) {
	m.updates++
	return core.NotificationRegistration{
		ID: id, Kind: input.Kind, Name: input.Name, Subscriptions: input.Subscriptions,
		Enabled: input.Enabled, Settings: input.Settings, CreatedAt: time.Unix(0, 0).UTC(), UpdatedAt: time.Unix(0, 0).UTC(),
	}, nil
}

func (*notificationContractManager) Delete(context.Context, string) (core.NotificationRegistration, error) {
	return core.NotificationRegistration{}, errors.New("unexpected delete")
}

func TestDecodeNotificationChannelRejectsDuplicateEmailRecipients(t *testing.T) {
	t.Parallel()
	server := &Server{logger: slog.New(slog.DiscardHandler), maxBodyBytes: 64 * 1024}
	body := `{
		"kind":"email","name":"Email","enabled":true,"subscriptions":["created"],
		"subject_template":"","body_template":"",
		"email":{"smtp_host":"smtp.example.com","smtp_port":587,"tls_mode":"starttls",
		"auth_mode":"plain","username":"user","password":"secret","from_address":"from@example.com",
		"from_name":"Bloom","recipients":["Alice@example.com","alice@EXAMPLE.COM"],"allow_private":false}
	}`
	request := httptest.NewRequest(http.MethodPost, "/api/v1/notification-channels", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	if _, ok := server.decodeNotificationChannel(recorder, request, true); ok {
		t.Fatal("decodeNotificationChannel accepted duplicate recipients")
	}
	var response httputil.ErrorResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if recorder.Code != http.StatusUnprocessableEntity || len(response.Fields) != 1 ||
		response.Fields[0].Field != "email.recipients" || response.Fields[0].Code != "unique" {
		t.Fatalf("response = %d %+v", recorder.Code, response)
	}
}

type tombstonedNotificationAPI struct{}

func (tombstonedNotificationAPI) Get(context.Context, string) (core.NotificationRegistration, error) {
	return core.NotificationRegistration{}, core.ErrNotFound
}

func (tombstonedNotificationAPI) List(context.Context, string, int) ([]core.NotificationRegistration, error) {
	return []core.NotificationRegistration{}, nil
}

func (tombstonedNotificationAPI) Deliveries(
	context.Context, string, *core.NotificationDeliveryCursor, int,
) ([]core.NotificationDelivery, error) {
	return nil, core.ErrNotFound
}

func (tombstonedNotificationAPI) Register(
	context.Context, notifyapp.RegistrationInput,
) (core.NotificationRegistration, error) {
	return core.NotificationRegistration{}, core.ErrNotFound
}

func (tombstonedNotificationAPI) Update(
	context.Context, string, notifyapp.RegistrationInput,
) (core.NotificationRegistration, error) {
	return core.NotificationRegistration{}, core.ErrNotFound
}

func (tombstonedNotificationAPI) Delete(context.Context, string) (core.NotificationRegistration, error) {
	return core.NotificationRegistration{}, core.ErrNotFound
}

func (tombstonedNotificationAPI) Test(context.Context, string) error { return core.ErrNotFound }

func TestTombstonedNotificationChannelIsInvisibleToAPI(t *testing.T) {
	t.Parallel()
	api := tombstonedNotificationAPI{}
	server := &Server{
		logger: slog.New(slog.DiscardHandler), maxBodyBytes: 64 * 1024,
		notificationReader: api, notificationManager: api, notificationTester: api,
		audit: telemetry.NopAuditLogger(), auditFailureMetrics: telemetry.NopMetrics{},
	}
	assertTombstoneListEmpty(t, server)
	for _, testCase := range []struct {
		name, method, path, body string
		handler                  http.HandlerFunc
	}{
		{name: "get", method: http.MethodGet, path: "/api/v1/notification-channels/id", handler: server.handleGetNotificationChannel},
		{name: "update", method: http.MethodPut, path: "/api/v1/notification-channels/id", body: validWebhookUpdate, handler: server.handleUpdateNotificationChannel},
		{name: "test", method: http.MethodPost, path: "/api/v1/notification-channels/id/test", handler: server.handleTestNotificationChannel},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			request := httptest.NewRequest(testCase.method, testCase.path, strings.NewReader(testCase.body))
			request.SetPathValue("id", "00000000-0000-4000-8000-000000000001")
			if testCase.body != "" {
				request.Header.Set("Content-Type", "application/json")
			}
			recorder := httptest.NewRecorder()
			testCase.handler.ServeHTTP(recorder, request)
			if recorder.Code != http.StatusNotFound {
				t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
			}
		})
	}
}

const validWebhookUpdate = `{
	"kind":"webhook","name":"Webhook","enabled":true,"subscriptions":["created"],
	"subject_template":"","body_template":"",
	"webhook":{"url":"","shared_secret":"","allow_insecure":false,"allow_private":false}
}`

func assertTombstoneListEmpty(t *testing.T, server *Server) {
	t.Helper()
	recorder := httptest.NewRecorder()
	server.handleListNotificationChannels(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/notification-channels", nil))
	var response notificationChannelsResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if recorder.Code != http.StatusOK || len(response.Items) != 0 {
		t.Fatalf("response = %d %+v", recorder.Code, response)
	}
}

func TestNotificationFailureIsOpaqueBadGateway(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "https://bloom.test/api/v1/notification-channels", nil)
	writeError(recorder, request, slog.New(slog.DiscardHandler), &core.NotificationError{
		Kind: core.NotificationUnauthorized, Operation: "send", Err: errors.New("https://secret.example/hook"),
	})
	var response httputil.ErrorResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if recorder.Code != http.StatusBadGateway || response.Code != codeNotificationFailure ||
		strings.Contains(recorder.Body.String(), "secret.example") {
		t.Fatalf("response = %d %s", recorder.Code, recorder.Body.String())
	}
}

func notificationRoutes() []apiRoute {
	values := make([]apiRoute, 0, 7)
	for _, route := range apiRouteInventory {
		if strings.HasPrefix(route.path, "/api/v1/notification-channels") {
			values = append(values, route)
		}
	}
	return values
}
