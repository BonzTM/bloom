package http

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/httputil"
)

func TestRequestSliceRoutesRejectMissingAndInsufficientSessions(t *testing.T) {
	h := newAuthHarness(t, nil)
	cookie := sessionCookie(t, h.login(t, "alice", "secret-password"))
	h.authorization.mu.Lock()
	h.authorization.permissions["11111111-1111-4111-8111-111111111111"] = nil
	h.authorization.mu.Unlock()
	inScope := false
	for _, route := range apiRouteInventory {
		if route.path == "/api/v1/playback/now" {
			inScope = true
		}
		if !inScope {
			continue
		}
		t.Run(route.method+" "+route.path, func(t *testing.T) {
			handler, err := h.server.routeHandler(route)
			if err != nil {
				t.Fatalf("routeHandler: %v", err)
			}
			path := concreteRequestPath(route.path)
			missing := httptest.NewRecorder()
			handler.ServeHTTP(missing, httptest.NewRequest(route.method, path, nil))
			if missing.Code != http.StatusUnauthorized {
				t.Fatalf("missing session status = %d, want 401", missing.Code)
			}
			request := httptest.NewRequest(route.method, path, nil)
			request.AddCookie(cookie)
			forbidden := httptest.NewRecorder()
			handler.ServeHTTP(forbidden, request)
			if forbidden.Code != http.StatusForbidden {
				t.Fatalf("insufficient permission status = %d, want 403", forbidden.Code)
			}
		})
	}
}

func concreteRequestPath(pattern string) string {
	path := strings.ReplaceAll(pattern, "{id}", "33333333-3333-4333-8333-333333333333")
	return "https://bloom.test" + path
}

func TestRequestSliceOpenAPIDocumentsRequiredFailures(t *testing.T) {
	document := loadOpenAPI(t)
	expected := map[string]map[string][]string{
		"/api/v1/download-managers":              {"get": {"401", "403", "422"}, "post": {"401", "403", "409", "415", "422", "502", "503"}},
		"/api/v1/download-managers/{id}":         {"delete": {"401", "403", "404", "409", "422"}},
		"/api/v1/download-managers/{id}/options": {"get": {"401", "403", "404", "422", "502", "503"}},
		"/api/v1/metadata/search":                {"get": {"401", "403", "422", "502", "503"}},
		"/api/v1/metadata/movies/{id}":           {"get": {"401", "403", "404", "422", "502", "503"}},
		"/api/v1/metadata/series/{id}":           {"get": {"401", "403", "404", "422", "502", "503"}},
		"/api/v1/metadata/providers/tmdb/key":    {"get": {"401", "403"}, "put": {"401", "403", "415", "422"}, "delete": {"401", "403", "404"}},
		"/api/v1/request-profiles":               {"get": {"401", "403"}, "post": {"401", "403", "409", "415", "422"}},
		"/api/v1/request-profiles/{id}":          {"put": {"401", "403", "404", "409", "415", "422"}, "delete": {"401", "403", "404", "409"}},
		"/api/v1/requests":                       {"get": {"401", "403", "422"}, "post": {"401", "403", "404", "409", "415", "422", "502", "503"}},
		"/api/v1/requests/{id}":                  {"get": {"401", "403", "404"}},
		"/api/v1/requests/{id}/progress":         {"get": {"401", "403", "404", "502", "503"}},
		"/api/v1/requests/{id}/approve":          {"post": {"401", "403", "404", "409", "415", "422"}},
		"/api/v1/requests/{id}/decline":          {"post": {"401", "403", "404", "409", "415", "422"}},
		"/api/v1/roles/{id}/request-quota":       quotaContractStatuses(),
		"/api/v1/accounts/{id}/request-quota":    quotaContractStatuses(),
	}
	for path, methods := range expected {
		for method, statuses := range methods {
			operation, ok := document.Paths[path][method]
			if !ok {
				t.Errorf("%s %s is missing", method, path)
				continue
			}
			for _, status := range append(statuses, "500", "405") {
				if _, ok := operation.Responses[status]; !ok {
					t.Errorf("%s %s does not document %s", method, path, status)
				}
			}
		}
	}
}

func quotaContractStatuses() map[string][]string {
	return map[string][]string{
		"get": {"401", "403", "404"}, "put": {"401", "403", "415", "422"}, "delete": {"401", "403", "404"},
	}
}

func TestRequestSliceErrorCodes(t *testing.T) {
	tests := []struct {
		err    error
		status int
		code   string
	}{
		{err: core.ErrNotFound, status: http.StatusNotFound, code: codeNotFound},
		{err: core.ErrAlreadyExists, status: http.StatusConflict, code: codeAlreadyExists},
		{err: core.ErrProfileInUse, status: http.StatusConflict, code: codeProfileInUse},
		{err: core.ErrInvalidTransition, status: http.StatusConflict, code: codeInvalidTransition},
		{err: core.ErrQuotaExceeded, status: http.StatusUnprocessableEntity, code: codeQuotaExceeded},
		{err: core.ErrMetadataNotConfigured, status: http.StatusServiceUnavailable, code: codeMetadataNotConfigured},
		{err: core.ErrMetadataMalformed, status: http.StatusBadGateway, code: codeMetadataProviderFailure},
		{err: core.ErrDownloadItemMissing, status: http.StatusNotFound, code: codeDownloadManagerNotFound},
		{err: core.ErrDownloadManagerInUse, status: http.StatusConflict, code: codeDownloadManagerInUse},
		{err: &core.DownloadManagerError{Kind: core.DownloadManagerUnauthorized}, status: http.StatusBadGateway, code: codeDownloadManagerFailure},
		{err: &core.DownloadManagerError{Kind: core.DownloadManagerUnavailable}, status: http.StatusServiceUnavailable, code: codeDownloadManagerFailure},
		{err: core.ErrMetadataUnavailable, status: http.StatusServiceUnavailable, code: codeMetadataProviderFailure},
	}
	for _, testCase := range tests {
		t.Run(testCase.code, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodGet, "https://bloom.test/api/v1/requests", nil)
			writeError(recorder, request, slog.New(slog.DiscardHandler), errors.Join(errors.New("boundary"), testCase.err))
			var response httputil.ErrorResponse
			if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
				t.Fatalf("decode error response: %v", err)
			}
			if recorder.Code != testCase.status || response.Code != testCase.code {
				t.Fatalf("response = %d %s, want %d %s", recorder.Code, response.Code, testCase.status, testCase.code)
			}
		})
	}
}

func TestRequestStateChangingRoutesHaveCSRFAuditResources(t *testing.T) {
	patterns := []string{
		"/api/v1/download-managers", "/api/v1/download-managers/{id}",
		"/api/v1/metadata/providers/tmdb/key", "/api/v1/request-profiles", "/api/v1/request-profiles/{id}",
		"/api/v1/requests", "/api/v1/requests/{id}/approve", "/api/v1/requests/{id}/decline",
		"/api/v1/roles/{id}/request-quota", "/api/v1/accounts/{id}/request-quota",
	}
	for _, pattern := range patterns {
		path := strings.TrimPrefix(concreteRequestPath(pattern), "https://bloom.test")
		if got := csrfAuditResource(path); got == auditResourceRouteUnmatched {
			t.Errorf("csrfAuditResource(%q) = %q", path, got)
		}
	}
}

func TestRequestSliceResponseSchemasAcceptWireFixtures(t *testing.T) {
	document := loadOpenAPI(t)
	fixtures := map[string]string{
		"#/components/schemas/MetadataSeries": `{"kind":"series","provider":"tmdb","provider_id":"12","title":"Show","year":2026,"overview":"Plot","poster_path":"/show.jpg","seasons":[{"number":1,"name":"Season 1","episode_count":8,"air_date":"2026-01-02T00:00:00Z"}]}`,
		"#/components/schemas/RequestProfile": `{"id":"33333333-3333-4333-8333-333333333333","name":"Default","kinds":["movie"],"download_manager_kind":"radarr","download_manager_instance":"main","quality_profile":"Any","root_folder":"/media","tags":[],"created_at":"2026-09-23T12:00:00Z","updated_at":"2026-09-23T12:00:00Z"}`,
		"#/components/schemas/MediaRequest":   `{"id":"33333333-3333-4333-8333-333333333333","kind":"movie","provider":"tmdb","provider_id":"11","title":"Film","year":2026,"poster_path":"/film.jpg","requester_account_id":"11111111-1111-4111-8111-111111111111","requester_username":"alice","profile_id":"22222222-2222-4222-8222-222222222222","status":"pending","seasons":[],"decision_reason":"","failure_reason":"","download_manager_id":"","download_manager_item_id":"","dispatch_quality_profile":"","dispatch_root_folder":"","dispatch_tags":[],"decided_by_account_id":"","created_at":"2026-09-23T12:00:00Z","updated_at":"2026-09-23T12:00:00Z"}`,
		"#/components/schemas/RequestQuota":   `{"scope_id":"33333333-3333-4333-8333-333333333333","movie_limit":5,"movie_period_days":30,"season_limit":10,"season_period_days":30}`,
	}
	for schema, fixture := range fixtures {
		assertJSONMatchesSchema(t, document, []byte(fixture), schema)
	}
}

func TestMediaRequestContractRequiresBoundedRequesterUsername(t *testing.T) {
	document := loadOpenAPI(t)
	schema := document.validator.Components.Schemas["MediaRequest"].Value
	required := slices.Contains(schema.Required, "requester_username")
	property := schema.Properties["requester_username"].Value
	if !required || property == nil {
		t.Fatal("MediaRequest requester_username is not required")
	}
	if got := fmt.Sprint(property.Extensions["x-max-bytes"]); got != "256" {
		t.Fatalf("requester_username x-max-bytes = %s, want 256", got)
	}
}
