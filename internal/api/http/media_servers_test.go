package http

import (
	"context"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/mediaserver"
	"github.com/BonzTM/bloom/internal/mediaserver/jellyfin"
	"github.com/BonzTM/bloom/internal/secrets"
	"github.com/BonzTM/bloom/internal/telemetry"
)

type fakeMediaServerService struct {
	mu           sync.Mutex
	servers      []core.MediaServer
	connection   core.MediaServerConnection
	info         core.ServerInfo
	libraries    []core.Library
	err          error
	registered   int
	getCalls     int
	probeCalls   int
	libraryCalls int
	deleted      int
	deadlineSeen bool
	lastAPIKey   string
	lastInsecure bool
	lastAfter    string
	lastPageSize int
}

func newFakeMediaServerService() *fakeMediaServerService {
	now := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)
	server := core.MediaServer{ID: "33333333-3333-4333-8333-333333333333", Kind: core.MediaServerKindJellyfin, Name: "Home", BaseURL: "https://media.example.test", CreatedAt: now, UpdatedAt: now}
	capabilities := core.Capabilities{CreateUserWithPassword: true, SetPassword: true, QuickConnectApproval: true}
	info := core.ServerInfo{Name: "Jellyfin", Version: "12.1.0", ID: "jellyfin-1"}
	return &fakeMediaServerService{
		servers: []core.MediaServer{server}, connection: core.MediaServerConnection{Server: server, Info: info, Capabilities: capabilities},
		info: info, libraries: []core.Library{{ID: "lib-1", Name: "Movies", Type: "movies"}},
	}
}

func (f *fakeMediaServerService) Register(
	ctx context.Context,
	_ core.MediaServerKind,
	_, _, credential string,
	allowInsecure bool,
) (core.MediaServerConnection, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.registered++
	_, f.deadlineSeen = ctx.Deadline()
	f.lastAPIKey = credential
	f.lastInsecure = allowInsecure
	connection := f.connection
	connection.Server.AllowInsecure = allowInsecure
	return connection, f.err
}

func (f *fakeMediaServerService) List(ctx context.Context, afterNameKey string, pageSize int) ([]core.MediaServerConnection, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastAfter, f.lastPageSize = afterNameKey, pageSize
	_, f.deadlineSeen = ctx.Deadline()
	connections := make([]core.MediaServerConnection, 0, len(f.servers))
	for _, server := range f.servers {
		connections = append(connections, core.MediaServerConnection{Server: server, Capabilities: f.connection.Capabilities})
	}
	return connections, f.err
}

func (f *fakeMediaServerService) Get(ctx context.Context, _ string) (core.MediaServerConnection, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.getCalls++
	_, f.deadlineSeen = ctx.Deadline()
	return f.connection, f.err
}

func (f *fakeMediaServerService) Probe(ctx context.Context, _ string) (core.ServerInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.probeCalls++
	_, f.deadlineSeen = ctx.Deadline()
	return f.info, f.err
}

func (f *fakeMediaServerService) Libraries(ctx context.Context, _ string) ([]core.Library, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.libraryCalls++
	_, f.deadlineSeen = ctx.Deadline()
	return slices.Clone(f.libraries), f.err
}

func (f *fakeMediaServerService) Delete(ctx context.Context, _ string) (core.MediaServer, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleted++
	_, f.deadlineSeen = ctx.Deadline()
	return f.connection.Server, f.err
}

func TestMediaServerEndpoints(t *testing.T) {
	h := newAuthHarness(t, nil)
	cookie := sessionCookie(t, h.login(t, "alice", "secret-password"))
	create := h.requestWithContentType(t, http.MethodPost, "/api/v1/media-servers", `{"kind":"jellyfin","name":"Home","base_url":"https://media.example.test/","api_key":"super-secret"}`, cookie, "application/json")
	if create.Code != http.StatusCreated || strings.Contains(create.Body.String(), "super-secret") {
		t.Fatalf("create = %d %s", create.Code, create.Body.String())
	}
	if strings.Contains(h.logs.String(), "super-secret") {
		t.Fatal("application log contains the API key")
	}
	event := h.audit.last(t)
	if event.Action != "media_server.create" || event.Resource != "media_server:33333333-3333-4333-8333-333333333333" || event.Kind != "jellyfin" || event.Result != telemetry.AuditSuccess {
		t.Fatalf("create audit = %+v", event)
	}
	for _, testCase := range []struct {
		method, path string
		want         int
	}{
		{method: http.MethodGet, path: "/api/v1/media-servers", want: http.StatusOK},
		{method: http.MethodGet, path: "/api/v1/media-servers/33333333-3333-4333-8333-333333333333", want: http.StatusOK},
		{method: http.MethodPost, path: "/api/v1/media-servers/33333333-3333-4333-8333-333333333333/probe", want: http.StatusOK},
		{method: http.MethodGet, path: "/api/v1/media-servers/33333333-3333-4333-8333-333333333333/libraries", want: http.StatusOK},
		{method: http.MethodDelete, path: "/api/v1/media-servers/33333333-3333-4333-8333-333333333333", want: http.StatusNoContent},
	} {
		recorder := h.request(t, testCase.method, testCase.path, "", cookie)
		if recorder.Code != testCase.want {
			t.Errorf("%s %s = %d: %s", testCase.method, testCase.path, recorder.Code, recorder.Body.String())
		}
	}
	if event := h.audit.last(t); event.Action != "media_server.delete" || event.Kind != "jellyfin" {
		t.Fatalf("delete audit = %+v", event)
	}
}

func TestCreateMediaServerValidation(t *testing.T) {
	h := newAuthHarness(t, nil)
	cookie := sessionCookie(t, h.login(t, "alice", "secret-password"))
	tests := []string{
		`{"kind":"plex","name":"Home","base_url":"https://media.example.test","api_key":"key"}`,
		`{"kind":"jellyfin","name":"","base_url":"https://media.example.test","api_key":"key"}`,
		`{"kind":"jellyfin","name":"Home","base_url":"ftp://media.example.test","api_key":"key"}`,
		`{"kind":"jellyfin","name":"Home","base_url":"https://user:pass@media.example.test","api_key":"key"}`,
		`{"kind":"jellyfin","name":"Home","base_url":"https://media.example.test","api_key":""}`,
		`{"kind":"jellyfin","name":"Home","base_url":"https://media.example.test","api_key":"bad\nkey"}`,
	}
	for _, body := range tests {
		recorder := h.requestWithContentType(t, http.MethodPost, "/api/v1/media-servers", body, cookie, "application/json")
		if recorder.Code != http.StatusUnprocessableEntity {
			t.Errorf("body %s = %d, want 422", body, recorder.Code)
		}
	}
	if h.mediaServers.registered != 0 {
		t.Fatalf("invalid requests registered %d servers", h.mediaServers.registered)
	}
}

func TestCreateMediaServerAcceptsQuotedAndBackslashedAPIKey(t *testing.T) {
	h := newAuthHarness(t, nil)
	cookie := sessionCookie(t, h.login(t, "alice", "secret-password"))
	recorder := h.requestWithContentType(t, http.MethodPost, "/api/v1/media-servers",
		`{"kind":"jellyfin","name":"Home","base_url":"https://media.example.test","api_key":"quoted\" and \\ key"}`,
		cookie, "application/json")
	if recorder.Code != http.StatusCreated || h.mediaServers.lastAPIKey != `quoted" and \ key` {
		t.Fatalf("response = %d, api_key = %q", recorder.Code, h.mediaServers.lastAPIKey)
	}
}

func TestCreateMediaServerByteBounds(t *testing.T) {
	tests := []struct {
		name, fieldValue string
		field            string
		status           int
	}{
		{name: "name at 100 bytes", field: "name", fieldValue: strings.Repeat("é", 50), status: 201},
		{name: "name over 100 bytes", field: "name", fieldValue: strings.Repeat("é", 51), status: 422},
		{name: "api key at 4096 bytes", field: "api_key", fieldValue: strings.Repeat("k", 4096), status: 201},
		{name: "api key over 4096 bytes", field: "api_key", fieldValue: strings.Repeat("k", 4097), status: 422},
		{name: "base URL at 2048 bytes", field: "base_url", fieldValue: "https://" + strings.Repeat("a", 2040), status: 201},
		{name: "base URL over 2048 bytes", field: "base_url", fieldValue: "https://" + strings.Repeat("a", 2041), status: 422},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			h := newAuthHarness(t, nil)
			cookie := sessionCookie(t, h.login(t, "alice", "secret-password"))
			values := map[string]string{
				"name": "Home", "base_url": "https://media.example.test", "api_key": "key",
			}
			values[testCase.field] = testCase.fieldValue
			body, err := json.Marshal(map[string]any{
				"kind": "jellyfin", "name": values["name"],
				"base_url": values["base_url"], "api_key": values["api_key"],
			})
			if err != nil {
				t.Fatalf("marshal request: %v", err)
			}
			recorder := h.requestWithContentType(t, http.MethodPost, "/api/v1/media-servers", string(body), cookie, "application/json")
			if recorder.Code != testCase.status {
				t.Fatalf("status = %d, want %d: %s", recorder.Code, testCase.status, recorder.Body.String())
			}
		})
	}
}

func TestCreateMediaServerInsecureOverrideIsVisibleWarnedAndAudited(t *testing.T) {
	h := newAuthHarness(t, nil)
	cookie := sessionCookie(t, h.login(t, "alice", "secret-password"))
	body := `{"kind":"jellyfin","name":"Home","base_url":"http://media.example.test","api_key":"secret","allow_insecure":true}`
	recorder := h.requestWithContentType(t, http.MethodPost, "/api/v1/media-servers", body, cookie, "application/json")
	if recorder.Code != http.StatusCreated || !strings.Contains(recorder.Body.String(), `"allow_insecure":true`) {
		t.Fatalf("response = %d %s", recorder.Code, recorder.Body.String())
	}
	if !h.mediaServers.lastInsecure || !strings.Contains(h.logs.String(), "registering media server over plaintext HTTP") {
		t.Fatalf("insecure=%t logs=%s", h.mediaServers.lastInsecure, h.logs.String())
	}
	if event := h.audit.last(t); !event.AllowInsecure {
		t.Fatalf("audit = %+v, want allow_insecure", event)
	}
}

func TestCreateMediaServerRejectsInsecureOverrideForHTTPS(t *testing.T) {
	h := newAuthHarness(t, nil)
	cookie := sessionCookie(t, h.login(t, "alice", "secret-password"))
	body := `{"kind":"jellyfin","name":"Home","base_url":"https://media.example.test/","api_key":"secret","allow_insecure":true}`
	recorder := h.requestWithContentType(t, http.MethodPost, "/api/v1/media-servers", body, cookie, "application/json")
	if recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422: %s", recorder.Code, recorder.Body.String())
	}
	if h.mediaServers.registered != 0 {
		t.Fatalf("invalid request registered %d servers", h.mediaServers.registered)
	}
}

func TestCreateMediaServerAuditSerializationOmitsAPIKey(t *testing.T) {
	h := newAuthHarness(t, nil)
	var audit strings.Builder
	h.server.audit = telemetry.NewAuditLogger(&audit, h.clock)
	cookie := sessionCookie(t, h.login(t, "alice", "secret-password"))
	const apiKey = "never-serialize-this-key"
	body := `{"kind":"jellyfin","name":"Home","base_url":"https://media.example.test","api_key":"` + apiKey + `"}`
	recorder := h.requestWithContentType(t, http.MethodPost, "/api/v1/media-servers", body, cookie, "application/json")
	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d: %s", recorder.Code, recorder.Body.String())
	}
	if strings.Contains(audit.String(), apiKey) || !strings.Contains(audit.String(), `"allow_insecure":false`) {
		t.Fatalf("audit serialization = %s", audit.String())
	}
}

func TestMediaServerPermissionDenied(t *testing.T) {
	h := newAuthHarness(t, nil)
	cookie := sessionCookie(t, h.login(t, "alice", "secret-password"))
	h.authorization.mu.Lock()
	h.authorization.permissions[h.store.accounts["alice"].ID] = nil
	h.authorization.mu.Unlock()
	recorder := h.request(t, http.MethodGet, "/api/v1/media-servers", "", cookie)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", recorder.Code)
	}
}

func TestMediaServerPathIDsValidateBeforeService(t *testing.T) {
	h := newAuthHarness(t, nil)
	cookie := sessionCookie(t, h.login(t, "alice", "secret-password"))
	for _, testCase := range []struct{ method, path string }{
		{method: http.MethodGet, path: "/api/v1/media-servers/not-a-uuid"},
		{method: http.MethodDelete, path: "/api/v1/media-servers/not-a-uuid"},
		{method: http.MethodPost, path: "/api/v1/media-servers/not-a-uuid/probe"},
		{method: http.MethodGet, path: "/api/v1/media-servers/not-a-uuid/libraries"},
	} {
		recorder := h.request(t, testCase.method, testCase.path, "", cookie)
		if recorder.Code != http.StatusUnprocessableEntity {
			t.Errorf("%s %s = %d", testCase.method, testCase.path, recorder.Code)
		}
	}
	if h.mediaServers.getCalls != 0 || h.mediaServers.deleted != 0 ||
		h.mediaServers.probeCalls != 0 || h.mediaServers.libraryCalls != 0 {
		t.Fatalf("invalid ids reached service: %+v", h.mediaServers)
	}
}

func TestMediaServerHandlerSuppliesTotalDeadline(t *testing.T) {
	h := newAuthHarness(t, nil)
	cookie := sessionCookie(t, h.login(t, "alice", "secret-password"))
	recorder := h.request(t, http.MethodGet, "/api/v1/media-servers", "", cookie)
	if recorder.Code != http.StatusOK || !h.mediaServers.deadlineSeen {
		t.Fatalf("list status=%d deadline=%t", recorder.Code, h.mediaServers.deadlineSeen)
	}
}

func TestMediaServerUpstreamFailureIsOpaque(t *testing.T) {
	h := newAuthHarness(t, nil)
	cookie := sessionCookie(t, h.login(t, "alice", "secret-password"))
	h.mediaServers.err = &core.MediaServerError{
		Kind: core.MediaServerUnavailable, Operation: "probe", Retryable: true,
		RetryAfter: 45 * time.Second, Err: errors.New("secret upstream detail"),
	}
	recorder := h.request(t, http.MethodPost, "/api/v1/media-servers/33333333-3333-4333-8333-333333333333/probe", "", cookie)
	if recorder.Code != http.StatusBadGateway || recorder.Header().Get("Retry-After") != "30" || strings.Contains(recorder.Body.String(), "secret upstream detail") {
		t.Fatalf("probe failure = %d retry=%q body=%s", recorder.Code, recorder.Header().Get("Retry-After"), recorder.Body.String())
	}
}

func TestMediaServerTerminalFailuresThroughHandlerAndClientOmitRetryAfter(t *testing.T) {
	tests := []struct {
		name       string
		baseURL    string
		httpClient *http.Client
	}{
		{
			name: "TLS certificate failure", baseURL: "https://media.example.test",
			httpClient: &http.Client{Transport: terminalErrorTransport{err: x509.UnknownAuthorityError{}}},
		},
		{name: "denied destination", baseURL: "https://localhost"},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			h := newAuthHarness(t, nil)
			store := &unusedMediaServerStore{}
			cipher, err := secrets.New([]byte("0123456789abcdef0123456789abcdef"))
			if err != nil {
				t.Fatalf("secrets.New: %v", err)
			}
			service, err := mediaserver.NewService(
				store, store, cipher, handlerJellyfinFactory{httpClient: testCase.httpClient}, h.clock,
			)
			if err != nil {
				t.Fatalf("NewService: %v", err)
			}
			h.server.mediaServerManager = service
			cookie := sessionCookie(t, h.login(t, "alice", "secret-password"))
			body := `{"kind":"jellyfin","name":"Home","base_url":"` + testCase.baseURL + `","api_key":"secret"}`
			recorder := h.requestWithContentType(
				t, http.MethodPost, "/api/v1/media-servers", body, cookie, "application/json",
			)
			if recorder.Code != http.StatusBadGateway || recorder.Header().Get("Retry-After") != "" {
				t.Fatalf("response = %d Retry-After %q: %s",
					recorder.Code, recorder.Header().Get("Retry-After"), recorder.Body.String())
			}
		})
	}
}

type handlerJellyfinFactory struct{ httpClient *http.Client }

func (f handlerJellyfinFactory) New(
	_ core.MediaServerKind,
	baseURL, credential string,
	allowInsecure bool,
) (core.MediaServerAdapter, error) {
	return jellyfin.New(jellyfin.Config{
		BaseURL: baseURL, APIKey: credential, Version: "test",
		DeviceID:      "33333333-3333-4333-8333-333333333333",
		AllowInsecure: allowInsecure, CallTimeout: time.Second, HTTPClient: f.httpClient,
	})
}

func (handlerJellyfinFactory) Capabilities(core.MediaServerKind) (core.Capabilities, error) {
	return core.Capabilities{}, nil
}

type terminalErrorTransport struct{ err error }

func (t terminalErrorTransport) RoundTrip(*http.Request) (*http.Response, error) { return nil, t.err }

type unusedMediaServerStore struct{}

func (*unusedMediaServerStore) CreateMediaServer(context.Context, core.MediaServerRecord) error {
	return errors.New("unexpected media server create")
}

func (*unusedMediaServerStore) DeleteMediaServer(context.Context, string) error {
	return core.ErrNotFound
}

func (*unusedMediaServerStore) GetMediaServer(context.Context, string) (core.MediaServerRecord, error) {
	return core.MediaServerRecord{}, core.ErrNotFound
}

func (*unusedMediaServerStore) ListMediaServers(context.Context, string, int) ([]core.MediaServer, error) {
	return nil, nil
}

func TestMediaServerSaturationReturnsServiceUnavailable(t *testing.T) {
	h := newAuthHarness(t, nil)
	cookie := sessionCookie(t, h.login(t, "alice", "secret-password"))
	h.mediaServers.err = &core.MediaServerError{
		Kind: core.MediaServerSaturated, Operation: "probe", Retryable: true, RetryAfter: time.Second,
	}
	recorder := h.request(t, http.MethodPost, "/api/v1/media-servers/33333333-3333-4333-8333-333333333333/probe", "", cookie)
	if recorder.Code != http.StatusServiceUnavailable || recorder.Header().Get("Retry-After") != "1" {
		t.Fatalf("response = %d Retry-After %q", recorder.Code, recorder.Header().Get("Retry-After"))
	}
}

func TestCreateMediaServerProbeFailureIsAuditedWithoutCredential(t *testing.T) {
	h := newAuthHarness(t, nil)
	cookie := sessionCookie(t, h.login(t, "alice", "secret-password"))
	h.mediaServers.err = &core.MediaServerError{
		Kind: core.MediaServerUnauthorized, Operation: "probe", Err: errors.New("upstream rejected credential"),
	}
	const credential = "never-log-this-key"
	body := `{"kind":"jellyfin","name":"Home","base_url":"https://media.example.test","api_key":"` + credential + `"}`
	recorder := h.requestWithContentType(t, http.MethodPost, "/api/v1/media-servers", body, cookie, "application/json")
	if recorder.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502: %s", recorder.Code, recorder.Body.String())
	}
	event := h.audit.last(t)
	if event.Action != "media_server.create" || event.Kind != "jellyfin" || event.Result != telemetry.AuditFailure {
		t.Fatalf("create failure audit = %+v", event)
	}
	if strings.Contains(recorder.Body.String(), credential) || strings.Contains(h.logs.String(), credential) ||
		strings.Contains(event.Resource+event.Reason, credential) {
		t.Fatal("failed registration disclosed the credential")
	}
}

func TestMediaServerPagination(t *testing.T) {
	h := newAuthHarness(t, nil)
	cookie := sessionCookie(t, h.login(t, "alice", "secret-password"))
	h.mediaServers.servers = []core.MediaServer{
		{ID: "1", Kind: core.MediaServerKindJellyfin, Name: "Alpha"},
		{ID: "2", Kind: core.MediaServerKindJellyfin, Name: "Beta"},
	}
	recorder := h.request(t, http.MethodGet, "/api/v1/media-servers?page_size=1", "", cookie)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", recorder.Code, recorder.Body.String())
	}
	var response mediaServersResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(response.Items) != 1 || !response.Items[0].Capabilities.CreateUserWithPassword ||
		response.NextCursor == "" || h.mediaServers.lastPageSize != 2 {
		t.Fatalf("response = %+v, queried page size %d", response, h.mediaServers.lastPageSize)
	}
	second := h.request(t, http.MethodGet, "/api/v1/media-servers?page_size=1&cursor="+response.NextCursor, "", cookie)
	if second.Code != http.StatusOK || h.mediaServers.lastAfter != "alpha" {
		t.Fatalf("second page = %d after=%q", second.Code, h.mediaServers.lastAfter)
	}
}

func TestMediaServerPaginationRejectsMalformedQueryEscapes(t *testing.T) {
	document := loadOpenAPI(t)
	h := newAuthHarness(t, nil)
	cookie := sessionCookie(t, h.login(t, "alice", "secret-password"))
	for _, rawQuery := range []string{"cursor=%zz", "page_size=%zz"} {
		recorder := h.request(t, http.MethodGet, "/api/v1/media-servers?"+rawQuery, "", cookie)
		if recorder.Code != http.StatusUnprocessableEntity {
			t.Errorf("%s = %d, want 422: %s", rawQuery, recorder.Code, recorder.Body.String())
		}
		assertJSONMatchesSchema(t, document, recorder.Body.Bytes(), errorSchema)
	}
}

func TestParseMediaServerCursorRejectsInvalidKeys(t *testing.T) {
	for _, key := range []string{"", " leading", "trailing ", "line\nbreak", "UPPER"} {
		cursor := base64.RawURLEncoding.EncodeToString([]byte(key))
		if _, message := parseMediaServerCursor([]string{cursor}); message == "" {
			t.Errorf("accepted invalid cursor key %q", key)
		}
	}
}

func TestMediaServerCursorOpenAPIBoundsMatchParser(t *testing.T) {
	data, err := os.ReadFile("../../../api/openapi.yaml")
	if err != nil {
		t.Fatalf("read OpenAPI: %v", err)
	}
	var document roleBoundsDocument
	if err := yaml.Unmarshal(data, &document); err != nil {
		t.Fatalf("parse OpenAPI: %v", err)
	}
	parameters := document.Paths["/api/v1/media-servers"]["get"].Parameters
	for _, parameter := range parameters {
		if parameter.Name == "cursor" {
			assertMediaServerCursorBounds(t, document, parameter)
			return
		}
	}
	t.Fatal("cursor parameter is missing")
}

func assertMediaServerCursorBounds(
	t *testing.T,
	document roleBoundsDocument,
	parameter struct {
		Name   string `yaml:"name"`
		Schema struct {
			MinLength int `yaml:"minLength"`
			MaxLength int `yaml:"maxLength"`
		} `yaml:"schema"`
	},
) {
	t.Helper()
	if parameter.Schema.MinLength != 1 || parameter.Schema.MaxLength != maxMediaServerCursorBytes {
		t.Fatalf("cursor bounds = %d..%d, want 1..%d",
			parameter.Schema.MinLength, parameter.Schema.MaxLength, maxMediaServerCursorBytes)
	}
	got := document.Components.Schemas["MediaServersResponse"].Properties["next_cursor"].MaxLength
	if got != maxMediaServerCursorBytes {
		t.Fatalf("next_cursor maxLength = %d, want %d", got, maxMediaServerCursorBytes)
	}
}

func TestMediaServerOpenAPIContractWithValidFixtures(t *testing.T) {
	document := loadOpenAPI(t)
	h := newAuthHarness(t, nil)
	cookie := sessionCookie(t, h.login(t, "alice", "secret-password"))
	tests := []struct {
		method, path, body, schema string
		status                     int
	}{
		{method: http.MethodPost, path: "/api/v1/media-servers", body: `{"kind":"jellyfin","name":"Home","base_url":"https://media.example.test","api_key":"secret"}`, schema: "#/components/schemas/CreateMediaServerResponse", status: 201},
		{method: http.MethodGet, path: "/api/v1/media-servers", schema: "#/components/schemas/MediaServersResponse", status: 200},
		{method: http.MethodGet, path: "/api/v1/media-servers/33333333-3333-4333-8333-333333333333", schema: "#/components/schemas/MediaServer", status: 200},
		{method: http.MethodPost, path: "/api/v1/media-servers/33333333-3333-4333-8333-333333333333/probe", schema: "#/components/schemas/ProbeMediaServerResponse", status: 200},
		{method: http.MethodGet, path: "/api/v1/media-servers/33333333-3333-4333-8333-333333333333/libraries", schema: "#/components/schemas/LibrariesResponse", status: 200},
	}
	for _, testCase := range tests {
		contentType := ""
		if testCase.body != "" {
			contentType = "application/json"
		}
		recorder := h.requestWithContentType(t, testCase.method, testCase.path, testCase.body, cookie, contentType)
		if recorder.Code != testCase.status {
			t.Fatalf("%s %s = %d: %s", testCase.method, testCase.path, recorder.Code, recorder.Body.String())
		}
		assertJSONMatchesSchema(t, document, recorder.Body.Bytes(), testCase.schema)
	}
}

type mediaOperationContract struct {
	method, path, schema string
	success              int
	statuses             []int
}

func TestMediaServerOpenAPIContractEveryStatusAndHeader(t *testing.T) {
	document := loadOpenAPI(t)
	observed := make(map[string]map[int]authContractCase)
	for _, operation := range mediaOperationContracts() {
		for _, status := range operation.statuses {
			variants := mediaContractVariants(operation, status)
			for _, variant := range variants {
				name := operation.method + " " + operation.path + " " + strconv.Itoa(status) + " " + variant
				t.Run(name, func(t *testing.T) {
					h := newAuthHarness(t, nil)
					recorder := runMediaContractCase(t, h, operation, status, variant)
					testCase := mediaContractExpectation(operation, status, variant)
					assertHandlerContract(t, document, recorder, testCase)
					mergeContractCase(t, observed, testCase)
				})
			}
		}
	}
	for operation, responses := range observed {
		assertOperationContract(t, document, operation, responses)
	}
}

func mediaContractVariants(operation mediaOperationContract, status int) []string {
	if status == http.StatusUnauthorized {
		return []string{"missing", "invalid"}
	}
	if status == http.StatusForbidden && mediaOperationChangesState(operation) {
		return []string{"permission", "csrf"}
	}
	if status == http.StatusBadGateway {
		return []string{"transient", "terminal"}
	}
	return []string{"default"}
}

func mediaOperationChangesState(operation mediaOperationContract) bool {
	return operation.method == "post" || operation.method == "delete"
}

func mediaOperationContracts() []mediaOperationContract {
	return []mediaOperationContract{
		{
			method: "post", path: "/api/v1/media-servers", schema: createMediaServerSchema, success: 201,
			statuses: []int{201, 401, 403, 409, 415, 422, 500, 502, 503, 405},
		},
		{
			method: "get", path: "/api/v1/media-servers", schema: mediaServersSchema, success: 200,
			statuses: []int{200, 401, 403, 422, 500, 405},
		},
		{
			method: "get", path: "/api/v1/media-servers/{id}", schema: mediaServerSchema, success: 200,
			statuses: []int{200, 401, 403, 404, 422, 500, 405},
		},
		{
			method: "delete", path: "/api/v1/media-servers/{id}", success: 204,
			statuses: []int{204, 401, 403, 404, 422, 500, 405},
		},
		{
			method: "post", path: "/api/v1/media-servers/{id}/probe", schema: probeMediaServerSchema, success: 200,
			statuses: []int{200, 401, 403, 404, 422, 500, 502, 503, 405},
		},
		{
			method: "get", path: "/api/v1/media-servers/{id}/libraries", schema: librariesSchema, success: 200,
			statuses: []int{200, 401, 403, 404, 422, 500, 502, 503, 405},
		},
	}
}

func mediaContractExpectation(operation mediaOperationContract, status int, variant string) authContractCase {
	headers := []string{"Cache-Control", "Set-Cookie", "Vary", "X-Request-ID"}
	schema := errorSchema
	if status == operation.success {
		schema = operation.schema
	}
	if status == http.StatusUnauthorized && variant == "missing" {
		headers = []string{"Cache-Control", "Vary", "X-Request-ID"}
	}
	if status == http.StatusMethodNotAllowed {
		headers = []string{"Allow", "Cache-Control", "X-Request-ID"}
	}
	if status == http.StatusForbidden && variant == "csrf" {
		headers = []string{"Cache-Control", "X-Request-ID"}
	}
	if status == http.StatusBadGateway && variant == "transient" {
		headers = append(headers, "Retry-After")
	}
	if status == http.StatusServiceUnavailable {
		headers = append(headers, "Retry-After")
	}
	return authContractCase{
		path: operation.path, method: operation.method, status: status, schema: schema, headers: headers,
	}
}

func runMediaContractCase(
	t *testing.T,
	h authHarness,
	operation mediaOperationContract,
	status int,
	variant string,
) *httptest.ResponseRecorder {
	t.Helper()
	method, path, body, contentType := mediaRequest(operation)
	if status == http.StatusMethodNotAllowed {
		return h.request(t, http.MethodPatch, concreteMediaPath(path), "", nil)
	}
	if status == http.StatusUnauthorized {
		if variant == "invalid" {
			return mediaRequestWithMalformedSession(t, h, method, path, body, contentType)
		}
		return h.requestWithContentType(t, method, concreteMediaPath(path), body, nil, contentType)
	}
	if status == http.StatusForbidden && variant == "csrf" {
		return crossOriginRequest(h, method, concreteMediaPath(path), body)
	}
	cookie := sessionCookie(t, h.login(t, "alice", "secret-password"))
	configureMediaContractFailure(t, h, operation, status, variant, &path, &body, &contentType)
	return h.requestWithContentType(t, method, concreteMediaPath(path), body, cookie, contentType)
}

func mediaRequest(operation mediaOperationContract) (method, path, body, contentType string) {
	method, path = strings.ToUpper(operation.method), operation.path
	if operation.path == "/api/v1/media-servers" && operation.method == "post" {
		body = `{"kind":"jellyfin","name":"Home","base_url":"https://media.example.test","api_key":"secret"}`
		contentType = "application/json"
	}
	return method, path, body, contentType
}

func configureMediaContractFailure(
	t *testing.T,
	h authHarness,
	operation mediaOperationContract,
	status int,
	variant string,
	path, body, contentType *string,
) {
	t.Helper()
	switch status {
	case http.StatusForbidden:
		h.authorization.mu.Lock()
		h.authorization.permissions[h.store.accounts["alice"].ID] = nil
		h.authorization.mu.Unlock()
	case http.StatusNotFound:
		h.mediaServers.err = core.ErrNotFound
	case http.StatusConflict:
		h.mediaServers.err = core.ErrAlreadyExists
	case http.StatusUnsupportedMediaType:
		*contentType = "text/plain"
	case http.StatusUnprocessableEntity:
		setInvalidMediaInput(operation, path, body)
	case http.StatusInternalServerError:
		h.mediaServers.err = errors.New("store failed")
	case http.StatusBadGateway:
		kind := core.MediaServerMalformed
		if variant == "transient" {
			kind = core.MediaServerUnavailable
		}
		h.mediaServers.err = &core.MediaServerError{Kind: kind, Retryable: variant == "transient", RetryAfter: time.Second}
	case http.StatusServiceUnavailable:
		h.mediaServers.err = &core.MediaServerError{
			Kind: core.MediaServerSaturated, Retryable: true, RetryAfter: time.Second,
		}
	}
}

func setInvalidMediaInput(operation mediaOperationContract, path, body *string) {
	switch operation.path {
	case "/api/v1/media-servers":
		if operation.method == "post" {
			*body = `{}`
		} else {
			*path += "?page_size=invalid"
		}
	default:
		*path = strings.Replace(operation.path, "{id}", "not-a-uuid", 1)
	}
}

func concreteMediaPath(path string) string {
	return strings.Replace(path, "{id}", "33333333-3333-4333-8333-333333333333", 1)
}

func mediaRequestWithMalformedSession(
	t *testing.T,
	h authHarness,
	method, path, body, contentType string,
) *httptest.ResponseRecorder {
	t.Helper()
	const token = "malformed-media-contract"
	if err := h.sessions.Store.Commit(hashedSessionToken(token), []byte("bad"), futureSessionInstant()); err != nil {
		t.Fatalf("seed malformed session: %v", err)
	}
	cookie := &http.Cookie{Name: sessionCookieName, Value: token}
	return h.requestWithContentType(t, method, concreteMediaPath(path), body, cookie, contentType)
}
