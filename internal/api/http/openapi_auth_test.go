package http

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"go.yaml.in/yaml/v3"

	"github.com/BonzTM/bloom/internal/config"
	"github.com/BonzTM/bloom/internal/core"
)

const (
	currentSchema     = "#/components/schemas/CurrentAccountResponse"
	permissionsSchema = "#/components/schemas/PermissionCatalogResponse"
	rolesSchema       = "#/components/schemas/RolesResponse"
	errorSchema       = "#/components/schemas/ErrorResponse"
)

var contractHeaderNames = [...]string{"Allow", "Cache-Control", "Retry-After", "Set-Cookie", "Vary", "X-Request-ID"}

type authContractCase struct {
	name, path, method, schema string
	status                     int
	headers                    []string
	run                        func(*testing.T, authHarness) *httptest.ResponseRecorder
}

func TestAuthOpenAPIContractMatchesHandlerCases(t *testing.T) {
	document := loadOpenAPI(t)
	observed := make(map[string]map[int]authContractCase)
	for _, testCase := range authContractCases() {
		t.Run(testCase.name, func(t *testing.T) {
			h := newAuthHarness(t, nil)
			recorder := testCase.run(t, h)
			assertHandlerContract(t, document, recorder, testCase)
			mergeContractCase(t, observed, testCase)
		})
	}
	for operation, responses := range observed {
		assertOperationContract(t, document, operation, responses)
	}
}

func authContractCases() []authContractCase {
	session := []string{"Cache-Control", "Vary", "X-Request-ID"}
	sessionCookie := []string{"Cache-Control", "Set-Cookie", "Vary", "X-Request-ID"}
	csrf := []string{"Cache-Control", "X-Request-ID"}
	return []authContractCase{
		{name: "login 200", path: "/api/v1/auth/login", method: "post", status: 200, schema: currentSchema, headers: sessionCookie, run: loginSuccess},
		{name: "login 401 credentials", path: "/api/v1/auth/login", method: "post", status: 401, schema: errorSchema, headers: session, run: loginRejected},
		{name: "login 401 malformed session", path: "/api/v1/auth/login", method: "post", status: 401, schema: errorSchema, headers: sessionCookie, run: loginMalformedSession},
		{name: "login 403", path: "/api/v1/auth/login", method: "post", status: 403, schema: errorSchema, headers: csrf, run: loginCSRFRejected},
		{name: "login 415", path: "/api/v1/auth/login", method: "post", status: 415, schema: errorSchema, headers: session, run: loginUnsupportedMediaType},
		{name: "login 422", path: "/api/v1/auth/login", method: "post", status: 422, schema: errorSchema, headers: session, run: loginInvalid},
		{name: "login 429", path: "/api/v1/auth/login", method: "post", status: 429, schema: errorSchema, headers: appendCopy(session, "Retry-After"), run: loginRateRejected},
		{name: "login 500", path: "/api/v1/auth/login", method: "post", status: 500, schema: errorSchema, headers: session, run: loginInternalError},
		{name: "login 500 renewal delete", path: "/api/v1/auth/login", method: "post", status: 500, schema: errorSchema, headers: sessionCookie, run: loginRenewalDeleteError},
		{name: "login 503", path: "/api/v1/auth/login", method: "post", status: 503, schema: errorSchema, headers: appendCopy(session, "Retry-After"), run: loginOverloaded},
		{name: "login 405", path: "/api/v1/auth/login", method: "post", status: 405, schema: errorSchema, headers: []string{"Allow", "Cache-Control", "X-Request-ID"}, run: loginMethodRejected},
		{name: "logout 204", path: "/api/v1/auth/logout", method: "post", status: 204, headers: sessionCookie, run: logoutSuccess},
		{name: "logout 401 missing", path: "/api/v1/auth/logout", method: "post", status: 401, schema: errorSchema, headers: session, run: logoutMissing},
		{name: "logout 401 invalid", path: "/api/v1/auth/logout", method: "post", status: 401, schema: errorSchema, headers: sessionCookie, run: logoutInvalid},
		{name: "logout 403", path: "/api/v1/auth/logout", method: "post", status: 403, schema: errorSchema, headers: csrf, run: logoutCSRFRejected},
		{name: "logout 500", path: "/api/v1/auth/logout", method: "post", status: 500, schema: errorSchema, headers: session, run: logoutInternalError},
		{name: "logout 500 delete", path: "/api/v1/auth/logout", method: "post", status: 500, schema: errorSchema, headers: sessionCookie, run: logoutDeleteError},
		{name: "logout 405", path: "/api/v1/auth/logout", method: "post", status: 405, schema: errorSchema, headers: []string{"Allow", "Cache-Control", "X-Request-ID"}, run: logoutMethodRejected},
		{name: "me 200", path: "/api/v1/auth/me", method: "get", status: 200, schema: currentSchema, headers: sessionCookie, run: meSuccess},
		{name: "me 401 missing", path: "/api/v1/auth/me", method: "get", status: 401, schema: errorSchema, headers: session, run: meMissing},
		{name: "me 401 invalid", path: "/api/v1/auth/me", method: "get", status: 401, schema: errorSchema, headers: sessionCookie, run: meInvalid},
		{name: "me 500", path: "/api/v1/auth/me", method: "get", status: 500, schema: errorSchema, headers: sessionCookie, run: meInternalError},
		{name: "me 405", path: "/api/v1/auth/me", method: "get", status: 405, schema: errorSchema, headers: []string{"Allow", "Cache-Control", "X-Request-ID"}, run: meMethodRejected},
		{name: "permissions 200", path: "/api/v1/auth/permissions", method: "get", status: 200, schema: permissionsSchema, headers: []string{"Cache-Control", "X-Request-ID"}, run: permissionsSuccess},
		{name: "permissions 405", path: "/api/v1/auth/permissions", method: "get", status: 405, schema: errorSchema, headers: []string{"Allow", "Cache-Control", "X-Request-ID"}, run: permissionsMethodRejected},
		{name: "roles 200", path: "/api/v1/roles", method: "get", status: 200, schema: rolesSchema, headers: sessionCookie, run: rolesSuccess},
		{name: "roles 401 missing", path: "/api/v1/roles", method: "get", status: 401, schema: errorSchema, headers: session, run: rolesMissing},
		{name: "roles 401 invalid", path: "/api/v1/roles", method: "get", status: 401, schema: errorSchema, headers: sessionCookie, run: rolesInvalid},
		{name: "roles 401 revoked", path: "/api/v1/roles", method: "get", status: 401, schema: errorSchema, headers: sessionCookie, run: rolesRevoked},
		{name: "roles 403", path: "/api/v1/roles", method: "get", status: 403, schema: errorSchema, headers: sessionCookie, run: rolesForbidden},
		{name: "roles 422", path: "/api/v1/roles", method: "get", status: 422, schema: errorSchema, headers: sessionCookie, run: rolesInvalidCursor},
		{name: "roles 500", path: "/api/v1/roles", method: "get", status: 500, schema: errorSchema, headers: sessionCookie, run: rolesInternalError},
		{name: "roles 405", path: "/api/v1/roles", method: "get", status: 405, schema: errorSchema, headers: []string{"Allow", "Cache-Control", "X-Request-ID"}, run: rolesMethodRejected},
	}
}

func loginMethodRejected(t *testing.T, h authHarness) *httptest.ResponseRecorder {
	t.Helper()
	return assertMethodRejected(t, h, http.MethodGet, "/api/v1/auth/login", http.MethodPost)
}

func logoutMethodRejected(t *testing.T, h authHarness) *httptest.ResponseRecorder {
	t.Helper()
	return assertMethodRejected(t, h, http.MethodGet, "/api/v1/auth/logout", http.MethodPost)
}

func meMethodRejected(t *testing.T, h authHarness) *httptest.ResponseRecorder {
	t.Helper()
	return assertMethodRejected(t, h, http.MethodPost, "/api/v1/auth/me", http.MethodGet)
}

func permissionsMethodRejected(t *testing.T, h authHarness) *httptest.ResponseRecorder {
	t.Helper()
	return assertMethodRejected(t, h, http.MethodPost, "/api/v1/auth/permissions", http.MethodGet)
}

func rolesMethodRejected(t *testing.T, h authHarness) *httptest.ResponseRecorder {
	t.Helper()
	return assertMethodRejected(t, h, http.MethodPost, "/api/v1/roles", http.MethodGet)
}

func assertMethodRejected(t *testing.T, h authHarness, method, path, allow string) *httptest.ResponseRecorder {
	t.Helper()
	recorder := h.request(t, method, path, "", nil)
	if recorder.Header().Get("Allow") != allow {
		t.Fatalf("%s %s Allow = %q, want %q", method, path, recorder.Header().Get("Allow"), allow)
	}
	return recorder
}

func appendCopy(values []string, value string) []string {
	result := append([]string(nil), values...)
	return append(result, value)
}

func loginSuccess(t *testing.T, h authHarness) *httptest.ResponseRecorder {
	t.Helper()
	return h.login(t, "alice", "secret-password")
}

func loginRejected(t *testing.T, h authHarness) *httptest.ResponseRecorder {
	t.Helper()
	return h.login(t, "alice", "wrong")
}

func loginMalformedSession(t *testing.T, h authHarness) *httptest.ResponseRecorder {
	t.Helper()
	token := "malformed-contract"
	if err := h.sessions.Store.Commit(hashedSessionToken(token), []byte("bad"), futureSessionInstant()); err != nil {
		t.Fatalf("seed malformed session: %v", err)
	}
	return h.request(t, http.MethodPost, "/api/v1/auth/login",
		`{"username":"alice","password":"secret-password"}`,
		&http.Cookie{Name: sessionCookieName, Value: token})
}

func loginCSRFRejected(t *testing.T, h authHarness) *httptest.ResponseRecorder {
	t.Helper()
	return crossOriginRequest(h, http.MethodPost, "/api/v1/auth/login", `{"username":"alice","password":"secret-password"}`)
}

func loginInvalid(t *testing.T, h authHarness) *httptest.ResponseRecorder {
	t.Helper()
	return h.request(t, http.MethodPost, "/api/v1/auth/login", `{}`, nil)
}

func loginUnsupportedMediaType(t *testing.T, h authHarness) *httptest.ResponseRecorder {
	t.Helper()
	return h.requestWithContentType(t, http.MethodPost, "/api/v1/auth/login",
		`{"username":"alice","password":"secret-password"}`, nil, "text/plain")
}

func loginRateRejected(t *testing.T, _ authHarness) *httptest.ResponseRecorder {
	t.Helper()
	h := newAuthHarness(t, func(cfg *config.AuthConfig) { cfg.LoginRateBurst = 1 })
	_ = h.login(t, "alice", "wrong")
	return h.login(t, "alice", "wrong")
}

func loginInternalError(t *testing.T, h authHarness) *httptest.ResponseRecorder {
	t.Helper()
	h.store.fail(errors.New("database unavailable"))
	return h.login(t, "alice", "secret-password")
}

func loginRenewalDeleteError(t *testing.T, h authHarness) *httptest.ResponseRecorder {
	t.Helper()
	cookie := sessionCookie(t, h.login(t, "alice", "secret-password"))
	h.sessionStore.mu.Lock()
	h.sessionStore.deleteErr = errors.New("delete failed")
	h.sessionStore.mu.Unlock()
	return h.request(t, http.MethodPost, "/api/v1/auth/login",
		`{"username":"alice","password":"secret-password"}`, cookie)
}

func loginOverloaded(t *testing.T, h authHarness) *httptest.ResponseRecorder {
	t.Helper()
	limit := cap(h.server.passwordVerifications)
	for range limit {
		h.server.passwordVerifications <- struct{}{}
	}
	defer func() {
		for range limit {
			<-h.server.passwordVerifications
		}
	}()
	return h.login(t, "alice", "secret-password")
}

func logoutSuccess(t *testing.T, h authHarness) *httptest.ResponseRecorder {
	t.Helper()
	cookie := sessionCookie(t, h.login(t, "alice", "secret-password"))
	return h.request(t, http.MethodPost, "/api/v1/auth/logout", "", cookie)
}

func logoutMissing(t *testing.T, h authHarness) *httptest.ResponseRecorder {
	t.Helper()
	return h.request(t, http.MethodPost, "/api/v1/auth/logout", "", nil)
}

func logoutInvalid(t *testing.T, h authHarness) *httptest.ResponseRecorder {
	t.Helper()
	return h.request(t, http.MethodPost, "/api/v1/auth/logout", "", &http.Cookie{Name: sessionCookieName, Value: "invalid"})
}

func logoutCSRFRejected(_ *testing.T, h authHarness) *httptest.ResponseRecorder {
	return crossOriginRequest(h, http.MethodPost, "/api/v1/auth/logout", "")
}

func logoutInternalError(t *testing.T, h authHarness) *httptest.ResponseRecorder {
	t.Helper()
	cookie := sessionCookie(t, h.login(t, "alice", "secret-password"))
	h.sessionStore.mu.Lock()
	h.sessionStore.findErr = errors.Join(core.ErrSessionStore, errors.New("database unavailable"))
	h.sessionStore.mu.Unlock()
	return h.request(t, http.MethodPost, "/api/v1/auth/logout", "", cookie)
}

func logoutDeleteError(t *testing.T, h authHarness) *httptest.ResponseRecorder {
	t.Helper()
	cookie := sessionCookie(t, h.login(t, "alice", "secret-password"))
	h.sessionStore.mu.Lock()
	h.sessionStore.deleteErr = errors.New("delete failed")
	h.sessionStore.mu.Unlock()
	return h.request(t, http.MethodPost, "/api/v1/auth/logout", "", cookie)
}

func meSuccess(t *testing.T, h authHarness) *httptest.ResponseRecorder {
	t.Helper()
	cookie := sessionCookie(t, h.login(t, "alice", "secret-password"))
	return h.request(t, http.MethodGet, "/api/v1/auth/me", "", cookie)
}

func meMissing(t *testing.T, h authHarness) *httptest.ResponseRecorder {
	t.Helper()
	return h.request(t, http.MethodGet, "/api/v1/auth/me", "", nil)
}

func meInvalid(t *testing.T, h authHarness) *httptest.ResponseRecorder {
	t.Helper()
	return h.request(t, http.MethodGet, "/api/v1/auth/me", "", &http.Cookie{Name: sessionCookieName, Value: "invalid"})
}

func meInternalError(t *testing.T, h authHarness) *httptest.ResponseRecorder {
	t.Helper()
	cookie := sessionCookie(t, h.login(t, "alice", "secret-password"))
	h.authorization.mu.Lock()
	h.authorization.snapshotErr = errors.New("database unavailable")
	h.authorization.mu.Unlock()
	return h.request(t, http.MethodGet, "/api/v1/auth/me", "", cookie)
}

func permissionsSuccess(t *testing.T, h authHarness) *httptest.ResponseRecorder {
	t.Helper()
	return h.request(t, http.MethodGet, "/api/v1/auth/permissions", "", nil)
}

func rolesSuccess(t *testing.T, h authHarness) *httptest.ResponseRecorder {
	t.Helper()
	cookie := sessionCookie(t, h.login(t, "alice", "secret-password"))
	return h.request(t, http.MethodGet, "/api/v1/roles", "", cookie)
}

func rolesMissing(t *testing.T, h authHarness) *httptest.ResponseRecorder {
	t.Helper()
	return h.request(t, http.MethodGet, "/api/v1/roles", "", nil)
}

func rolesInvalid(t *testing.T, h authHarness) *httptest.ResponseRecorder {
	t.Helper()
	return h.request(t, http.MethodGet, "/api/v1/roles", "", &http.Cookie{Name: sessionCookieName, Value: "invalid"})
}

func rolesRevoked(t *testing.T, h authHarness) *httptest.ResponseRecorder {
	t.Helper()
	cookie := sessionCookie(t, h.login(t, "alice", "secret-password"))
	if err := h.sessions.Store.Delete(hashedSessionToken(cookie.Value)); err != nil {
		t.Fatalf("revoke session: %v", err)
	}
	return h.request(t, http.MethodGet, "/api/v1/roles", "", cookie)
}

func rolesInvalidCursor(t *testing.T, h authHarness) *httptest.ResponseRecorder {
	t.Helper()
	cookie := sessionCookie(t, h.login(t, "alice", "secret-password"))
	return h.request(t, http.MethodGet, "/api/v1/roles?cursor=***", "", cookie)
}

func rolesForbidden(t *testing.T, h authHarness) *httptest.ResponseRecorder {
	t.Helper()
	cookie := sessionCookie(t, h.login(t, "alice", "secret-password"))
	h.authorization.mu.Lock()
	h.authorization.permissions["11111111-1111-4111-8111-111111111111"] = nil
	h.authorization.mu.Unlock()
	return h.request(t, http.MethodGet, "/api/v1/roles", "", cookie)
}

func rolesInternalError(t *testing.T, h authHarness) *httptest.ResponseRecorder {
	t.Helper()
	cookie := sessionCookie(t, h.login(t, "alice", "secret-password"))
	h.authorization.mu.Lock()
	h.authorization.listRolesErr = errors.New("database unavailable")
	h.authorization.mu.Unlock()
	return h.request(t, http.MethodGet, "/api/v1/roles", "", cookie)
}

func crossOriginRequest(h authHarness, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "https://bloom.test"+path, strings.NewReader(body))
	req.Header.Set("Origin", "https://evil.example")
	rec := httptest.NewRecorder()
	h.h.ServeHTTP(rec, req)
	return rec
}

type openAPIDocument struct {
	Paths      map[string]map[string]openAPIOperation `yaml:"paths"`
	Components struct {
		Responses map[string]openAPIResponse `yaml:"responses"`
	} `yaml:"components"`
	validator *openapi3.T
}

type openAPIOperation struct {
	Responses map[string]openAPIResponse `yaml:"responses"`
}

type openAPIResponse struct {
	Ref     string                      `yaml:"$ref"`
	Headers map[string]map[string]any   `yaml:"headers"`
	Content map[string]openAPIMediaType `yaml:"content"`
}

type openAPIMediaType struct {
	Schema struct {
		Ref string `yaml:"$ref"`
	} `yaml:"schema"`
}

type roleBoundsDocument struct {
	Paths map[string]map[string]struct {
		Parameters []struct {
			Name   string `yaml:"name"`
			Schema struct {
				MinLength int `yaml:"minLength"`
				MaxLength int `yaml:"maxLength"`
			} `yaml:"schema"`
		} `yaml:"parameters"`
	} `yaml:"paths"`
	Components struct {
		Schemas map[string]struct {
			Properties map[string]struct {
				MaxLength int `yaml:"maxLength"`
			} `yaml:"properties"`
		} `yaml:"schemas"`
	} `yaml:"components"`
}

func TestRoleCursorOpenAPIBoundsMatchParser(t *testing.T) {
	data, err := os.ReadFile("../../../api/openapi.yaml")
	if err != nil {
		t.Fatalf("read OpenAPI: %v", err)
	}
	var document roleBoundsDocument
	if err := yaml.Unmarshal(data, &document); err != nil {
		t.Fatalf("parse OpenAPI: %v", err)
	}
	parameters := document.Paths["/api/v1/roles"]["get"].Parameters
	for _, parameter := range parameters {
		if parameter.Name == "cursor" {
			if parameter.Schema.MinLength != 1 || parameter.Schema.MaxLength != maxRoleCursorBytes {
				t.Fatalf("cursor bounds = %d..%d, want 1..%d",
					parameter.Schema.MinLength, parameter.Schema.MaxLength, maxRoleCursorBytes)
			}
			if got := document.Components.Schemas["RolesResponse"].Properties["next_cursor"].MaxLength; got != maxRoleCursorBytes {
				t.Fatalf("next_cursor maxLength = %d, want %d", got, maxRoleCursorBytes)
			}
			return
		}
	}
	t.Fatal("cursor parameter is missing")
}

func loadOpenAPI(t *testing.T) openAPIDocument {
	t.Helper()
	data, err := os.ReadFile("../../../api/openapi.yaml")
	if err != nil {
		t.Fatalf("read OpenAPI: %v", err)
	}
	var document openAPIDocument
	if parseErr := yaml.Unmarshal(data, &document); parseErr != nil {
		t.Fatalf("parse OpenAPI: %v", parseErr)
	}
	validator, err := openapi3.NewLoader().LoadFromData(data)
	if err != nil {
		t.Fatalf("load OpenAPI validator: %v", err)
	}
	validator.SetStringFormatValidator("uuid", openapi3.NewRegexpFormatValidator(openapi3.FormatOfStringForUUIDOfRFC9562))
	if err := validator.Validate(context.Background(), openapi3.EnableSchemaFormatValidation()); err != nil {
		t.Fatalf("validate OpenAPI document: %v", err)
	}
	document.validator = validator
	return document
}

func assertHandlerContract(
	t *testing.T,
	document openAPIDocument,
	recorder *httptest.ResponseRecorder,
	testCase authContractCase,
) {
	t.Helper()
	if recorder.Code != testCase.status {
		t.Fatalf("handler status = %d, want %d: %s", recorder.Code, testCase.status, recorder.Body.String())
	}
	assertResponseSchema(t, document, recorder, testCase.schema)
	gotHeaders := selectedHeaders(recorder.Header())
	wantHeaders := sortedCopy(testCase.headers)
	if !reflect.DeepEqual(gotHeaders, wantHeaders) {
		t.Errorf("handler contract headers = %v, want %v", gotHeaders, wantHeaders)
	}
}

func assertResponseSchema(
	t *testing.T,
	document openAPIDocument,
	recorder *httptest.ResponseRecorder,
	schema string,
) {
	t.Helper()
	switch schema {
	case "":
		if recorder.Body.Len() != 0 {
			t.Errorf("response body = %q, want empty", recorder.Body.String())
		}
	case currentSchema, permissionsSchema, rolesSchema, errorSchema:
		assertJSONMatchesSchema(t, document, recorder.Body.Bytes(), schema)
	default:
		t.Fatalf("unsupported test schema %q", schema)
	}
}

func assertJSONMatchesSchema(t *testing.T, document openAPIDocument, body []byte, schemaRef string) {
	t.Helper()
	value, err := decodeContractJSON(body)
	if err != nil {
		t.Fatalf("decode response body: %v", err)
	}
	if err := validateOpenAPIValue(document, schemaRef, value); err != nil {
		t.Fatalf("response body does not match %s: %v; body %s", schemaRef, err, body)
	}
}

func decodeContractJSON(body []byte) (any, error) {
	var value any
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	if err := decoder.Decode(&value); err != nil {
		return nil, fmt.Errorf("decode JSON: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, errors.New("response contains more than one JSON value")
	}
	return value, nil
}

func validateOpenAPIValue(document openAPIDocument, schemaRef string, value any) error {
	name := strings.TrimPrefix(schemaRef, "#/components/schemas/")
	schema, ok := document.validator.Components.Schemas[name]
	if !ok || name == schemaRef {
		return errors.New("unresolved OpenAPI schema reference")
	}
	return schema.Value.VisitJSON(value,
		openapi3.EnableJSONSchema2020(),
		openapi3.EnableFormatValidation(),
		openapi3.WithStringFormatValidator("uuid", openapi3.NewRegexpFormatValidator(openapi3.FormatOfStringForUUIDOfRFC9562)),
	)
}

func TestOpenAPIResponseSchemaValidationRejectsWireDrift(t *testing.T) {
	t.Parallel()
	document := loadOpenAPI(t)
	tests := []string{
		`{}`,
		`{"account":{"id":"11111111-1111-4111-8111-111111111111","username":"alice"}}`,
		`{"account":{"id":"11111111-1111-4111-8111-111111111111","username":"alice"},"roles":[],"permissions":[],"extra":true}`,
		`{"account":{"id":"11111111-1111-4111-8111-111111111111","username":"alice","extra":true},"roles":[],"permissions":[]}`,
		`{"account":{"id":"not-a-uuid","username":"alice"},"roles":[],"permissions":[]}`,
	}
	for _, body := range tests {
		value, err := decodeContractJSON([]byte(body))
		if err != nil {
			t.Fatalf("decode fixture: %v", err)
		}
		if err := validateOpenAPIValue(document, currentSchema, value); err == nil {
			t.Errorf("schema validation accepted drifted body %s", body)
		}
	}
}

func TestOpenAPIValidationRejectsEnumAndBounds(t *testing.T) {
	t.Parallel()
	document := loadOpenAPI(t)
	tests := []struct {
		schema string
		value  any
	}{
		{schema: errorSchema, value: map[string]any{"code": "invented", "message": "bad", "request_id": "request-1"}},
		{schema: "#/components/schemas/LoginRequest", value: map[string]any{"username": strings.Repeat("x", core.MaxSubmittedUsernameCharacters+1), "password": "secret"}},
	}
	for _, testCase := range tests {
		if err := validateOpenAPIValue(document, testCase.schema, testCase.value); err == nil {
			t.Errorf("schema %s accepted invalid value %#v", testCase.schema, testCase.value)
		}
	}
}

func TestOpenAPILoginRequestAcceptsPRECISEquivalentInputs(t *testing.T) {
	t.Parallel()
	document := loadOpenAPI(t)
	usernames := [...]string{"ＡLICE", "Élodie", "E\u0301lodie"}
	for _, username := range usernames {
		value := map[string]any{"username": username, "password": "secret"}
		if err := validateOpenAPIValue(document, "#/components/schemas/LoginRequest", value); err != nil {
			t.Errorf("LoginRequest rejected PRECIS-equivalent username %q: %v", username, err)
		}
	}
}

func selectedHeaders(header http.Header) []string {
	result := make([]string, 0, len(contractHeaderNames))
	for _, name := range contractHeaderNames {
		if header.Get(name) != "" {
			result = append(result, name)
		}
	}
	return result
}

func mergeContractCase(t *testing.T, observed map[string]map[int]authContractCase, testCase authContractCase) {
	t.Helper()
	operation := testCase.method + " " + testCase.path
	if observed[operation] == nil {
		observed[operation] = make(map[int]authContractCase)
	}
	existing, ok := observed[operation][testCase.status]
	if ok && existing.schema != testCase.schema {
		t.Fatalf("%s %d has conflicting schemas %q and %q", operation, testCase.status, existing.schema, testCase.schema)
	}
	testCase.headers = union(existing.headers, testCase.headers)
	observed[operation][testCase.status] = testCase
}

func assertOperationContract(t *testing.T, document openAPIDocument, operation string, expected map[int]authContractCase) {
	t.Helper()
	method, path, ok := strings.Cut(operation, " ")
	if !ok {
		t.Fatalf("invalid operation key %q", operation)
	}
	specOperation, ok := document.Paths[path][method]
	if !ok {
		t.Fatalf("OpenAPI missing %s", operation)
	}
	if len(specOperation.Responses) != len(expected) {
		t.Errorf("%s response count = %d, want %d", operation, len(specOperation.Responses), len(expected))
	}
	for status, testCase := range expected {
		assertDocumentedResponse(t, document, operation, status, specOperation.Responses, testCase)
	}
}

func assertDocumentedResponse(
	t *testing.T,
	document openAPIDocument,
	operation string,
	status int,
	responses map[string]openAPIResponse,
	testCase authContractCase,
) {
	t.Helper()
	key := httpStatusCode(status)
	response, ok := responses[key]
	if !ok {
		t.Errorf("OpenAPI %s missing response %s", operation, key)
		return
	}
	response = resolveOpenAPIResponse(t, document, response)
	gotSchema := response.Content["application/json"].Schema.Ref
	if gotSchema != testCase.schema {
		t.Errorf("OpenAPI %s %s schema = %q, want %q", operation, key, gotSchema, testCase.schema)
	}
	gotHeaders := make([]string, 0, len(response.Headers))
	for name := range response.Headers {
		gotHeaders = append(gotHeaders, name)
	}
	sort.Strings(gotHeaders)
	if want := sortedCopy(testCase.headers); !reflect.DeepEqual(gotHeaders, want) {
		t.Errorf("OpenAPI %s %s headers = %v, want %v", operation, key, gotHeaders, want)
	}
}

func resolveOpenAPIResponse(t *testing.T, document openAPIDocument, response openAPIResponse) openAPIResponse {
	t.Helper()
	if response.Ref == "" {
		return response
	}
	name := strings.TrimPrefix(response.Ref, "#/components/responses/")
	resolved, ok := document.Components.Responses[name]
	if !ok || name == response.Ref {
		t.Fatalf("unresolved OpenAPI response reference %q", response.Ref)
	}
	return resolved
}

func httpStatusCode(status int) string {
	return strconv.Itoa(status)
}

func sortedCopy(values []string) []string {
	result := append([]string(nil), values...)
	sort.Strings(result)
	return result
}

func union(left, right []string) []string {
	seen := make(map[string]bool, len(left)+len(right))
	for _, value := range append(append([]string(nil), left...), right...) {
		seen[value] = true
	}
	result := make([]string, 0, len(seen))
	for value := range seen {
		result = append(result, value)
	}
	return sortedCopy(result)
}
