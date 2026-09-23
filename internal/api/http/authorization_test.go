package http

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"

	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/httputil"
	"github.com/BonzTM/bloom/internal/telemetry"
)

type permissionGuardTestCase struct {
	name        string
	account     *core.Account
	permissions []core.Permission
	wantStatus  int
	wantCalled  bool
	wantResult  telemetry.AuditResult
	wantMetric  int
}

func TestRequirePermissionPaths(t *testing.T) {
	t.Parallel()
	tests := []permissionGuardTestCase{
		{name: "no session", wantStatus: http.StatusUnauthorized, wantResult: telemetry.AuditFailure, wantMetric: 1},
		{name: "permission missing", account: &core.Account{ID: "account-1"}, wantStatus: http.StatusForbidden, wantResult: telemetry.AuditDenied, wantMetric: 1},
		{name: "allowed", account: &core.Account{ID: "account-1"}, permissions: []core.Permission{core.PermissionAdminRoles}, wantStatus: http.StatusNoContent, wantCalled: true},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			assertPermissionGuard(t, testCase)
		})
	}
}

func assertPermissionGuard(t *testing.T, testCase permissionGuardTestCase) {
	t.Helper()
	h := newAuthHarness(t, nil)
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	})
	permission, err := core.NewCatalogPermission(core.PermissionAdminRoles)
	if err != nil {
		t.Fatalf("NewCatalogPermission: %v", err)
	}
	handler := requestIDMiddleware(h.server.RequirePermission(permission)(next))
	req := httptest.NewRequest(http.MethodGet, "https://bloom.test/api/v1/roles", nil)
	req.Pattern = "/api/v1/roles"
	ctx := req.Context()
	if testCase.account != nil {
		ctx = context.WithValue(ctx, accountKey, *testCase.account)
		ctx = context.WithValue(ctx, permissionsKey, testCase.permissions)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req.WithContext(ctx))
	if recorder.Code != testCase.wantStatus || called != testCase.wantCalled {
		t.Fatalf("guard = %d called %v, want %d called %v", recorder.Code, called, testCase.wantStatus, testCase.wantCalled)
	}
	if h.metrics.authorizationDenialCount() != testCase.wantMetric {
		t.Fatalf("denial metric = %d, want %d", h.metrics.authorizationDenialCount(), testCase.wantMetric)
	}
	if !testCase.wantCalled {
		assertPermissionDenialAudit(t, h.audit.last(t), recorder, testCase)
	}
}

func assertPermissionDenialAudit(
	t *testing.T,
	event telemetry.AuditEvent,
	recorder *httptest.ResponseRecorder,
	testCase permissionGuardTestCase,
) {
	t.Helper()
	wantActor, wantReason := "anonymous", "missing_session"
	if testCase.account != nil {
		wantActor, wantReason = testCase.account.ID, "permission_missing"
	}
	if event.Actor != wantActor || event.SubjectID != "" || event.Action != "auth.permission" ||
		event.Resource != auditResourceRoles || event.Permission != string(core.PermissionAdminRoles) ||
		event.Result != testCase.wantResult || event.Reason != wantReason || event.Source != "192.0.2.1" ||
		event.RequestID == "" || event.RequestID != recorder.Header().Get("X-Request-ID") {
		t.Fatalf("denial audit = %+v", event)
	}
}

func TestPermissionCatalogHandlerIsPublicStableAndCacheable(t *testing.T) {
	t.Parallel()
	h := newAuthHarness(t, nil)
	recorder := h.request(t, http.MethodGet, "/api/v1/auth/permissions", "", nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("permissions = %d %s", recorder.Code, recorder.Body.String())
	}
	if recorder.Header().Get("Cache-Control") != "public, max-age=3600" {
		t.Fatalf("Cache-Control = %q", recorder.Header().Get("Cache-Control"))
	}
	var response permissionsResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode permissions: %v", err)
	}
	want := []permissionResponse{
		{ID: "users.read", Module: "users"},
		{ID: "users.invite", Module: "users"},
		{ID: "users.manage", Module: "users"},
		{ID: "requests.read.own", Module: "requests"},
		{ID: "requests.create", Module: "requests"},
		{ID: "requests.approve", Module: "requests"},
		{ID: "stats.read.own", Module: "stats"},
		{ID: "stats.read.all", Module: "stats"},
		{ID: "admin.settings", Module: "admin"},
		{ID: "admin.roles", Module: "admin"},
	}
	if !slices.Equal(response.Permissions, want) {
		t.Fatalf("permissions = %v, want %v", response.Permissions, want)
	}
}

func TestMeReturnsRolesAndEffectivePermissions(t *testing.T) {
	h := newAuthHarness(t, nil)
	cookie := sessionCookie(t, h.login(t, "alice", "secret-password"))
	recorder := h.request(t, http.MethodGet, "/api/v1/auth/me", "", cookie)
	if recorder.Code != http.StatusOK {
		t.Fatalf("me = %d %s", recorder.Code, recorder.Body.String())
	}
	var response currentAccountResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode me: %v", err)
	}
	if !slices.Equal(response.Roles, []string{"owner"}) || !slices.IsSorted(response.Permissions) {
		t.Fatalf("me response = %+v", response)
	}
}

func TestMeReturnsOneCoherentAuthorizationState(t *testing.T) {
	h := newAuthHarness(t, nil)
	accountID := "11111111-1111-4111-8111-111111111111"
	h.authorization.mu.Lock()
	h.authorization.permissions[accountID] = []core.Permission{core.PermissionAdminRoles}
	h.authorization.advanceAfterPermissions = true
	h.authorization.mu.Unlock()
	cookie := sessionCookie(t, h.login(t, "alice", "secret-password"))
	recorder := h.request(t, http.MethodGet, "/api/v1/auth/me", "", cookie)
	if recorder.Code != http.StatusOK {
		t.Fatalf("me = %d %s", recorder.Code, recorder.Body.String())
	}
	var response currentAccountResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode me: %v", err)
	}
	oldState := slices.Equal(response.Roles, []string{"owner"}) &&
		slices.Equal(response.Permissions, []core.Permission{core.PermissionAdminRoles})
	newState := slices.Equal(response.Roles, []string{"member"}) &&
		slices.Equal(response.Permissions, []core.Permission{core.PermissionRequestsCreate})
	if !oldState && !newState {
		t.Fatalf("me authorization state = roles %v permissions %v; want one coherent snapshot for %s",
			response.Roles, response.Permissions, accountID)
	}
	h.authorization.mu.Lock()
	snapshotCalls, permissionCalls := h.authorization.snapshotCalls, h.authorization.permissionsCalls
	h.authorization.mu.Unlock()
	if snapshotCalls != 1 || permissionCalls != 0 {
		t.Fatalf("authorization calls = snapshot %d permissions %d, want 1 and 0",
			snapshotCalls, permissionCalls)
	}
}

func TestSessionRoutesIsolateAuthorizationStoreFailure(t *testing.T) {
	tests := []struct {
		name, method, path  string
		wantStatus          int
		wantPermissionCalls int
		wantSnapshotCalls   int
	}{
		{name: "me", method: http.MethodGet, path: "/api/v1/auth/me", wantStatus: http.StatusInternalServerError, wantSnapshotCalls: 1},
		{name: "roles", method: http.MethodGet, path: "/api/v1/roles", wantStatus: http.StatusInternalServerError, wantPermissionCalls: 1},
		{name: "logout", method: http.MethodPost, path: "/api/v1/auth/logout", wantStatus: http.StatusNoContent},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			h := newAuthHarness(t, nil)
			cookie := sessionCookie(t, h.login(t, "alice", "secret-password"))
			h.authorization.permissionsErr = errors.New("permissions database unavailable")
			h.authorization.snapshotErr = errors.New("authorization database unavailable")
			recorder := h.request(t, testCase.method, testCase.path, "", cookie)
			if recorder.Code != testCase.wantStatus {
				t.Fatalf("status = %d, want %d: %s", recorder.Code, testCase.wantStatus, recorder.Body.String())
			}
			if testCase.wantStatus == http.StatusInternalServerError {
				assertOpaqueInternalError(t, recorder, "permissions database unavailable")
			} else {
				assertLogoutRevokedAndAudited(t, h, cookie)
			}
			h.authorization.mu.Lock()
			permissionCalls, snapshotCalls := h.authorization.permissionsCalls, h.authorization.snapshotCalls
			h.authorization.mu.Unlock()
			if permissionCalls != testCase.wantPermissionCalls {
				t.Fatalf("Permissions calls = %d, want %d", permissionCalls, testCase.wantPermissionCalls)
			}
			if snapshotCalls != testCase.wantSnapshotCalls {
				t.Fatalf("Snapshot calls = %d, want %d", snapshotCalls, testCase.wantSnapshotCalls)
			}
			assertAuthorizationHandlerNotCalled(t, h.authorization)
		})
	}
}

func assertLogoutRevokedAndAudited(t *testing.T, h authHarness, cookie *http.Cookie) {
	t.Helper()
	event := h.audit.last(t)
	if event.Actor != "11111111-1111-4111-8111-111111111111" || event.Action != "auth.logout" ||
		event.Resource != "account:11111111-1111-4111-8111-111111111111" ||
		event.Result != telemetry.AuditSuccess || event.Reason != "logged_out" {
		t.Fatalf("logout audit = %+v", event)
	}
	if recorder := h.request(t, http.MethodGet, "/api/v1/auth/me", "", cookie); recorder.Code != http.StatusUnauthorized {
		t.Fatalf("revoked session status = %d, want 401", recorder.Code)
	}
}

func assertOpaqueInternalError(t *testing.T, recorder *httptest.ResponseRecorder, detail string) {
	t.Helper()
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", recorder.Code)
	}
	envelope := decodeEnvelope(t, recorder)
	if envelope.Code != codeInternal || envelope.Message != http.StatusText(http.StatusInternalServerError) ||
		envelope.RequestID == "" ||
		strings.Contains(recorder.Body.String(), detail) {
		t.Fatalf("envelope = %+v, want opaque internal error", envelope)
	}
}

func assertAuthorizationHandlerNotCalled(t *testing.T, authorization *authAuthorization) {
	t.Helper()
	authorization.mu.Lock()
	defer authorization.mu.Unlock()
	if authorization.listRolesCalls != 0 {
		t.Fatalf("ListRoles calls = %d, want zero", authorization.listRolesCalls)
	}
}

func TestRolesRequiresAdminRolesAndListsPermissions(t *testing.T) {
	h := newAuthHarness(t, nil)
	cookie := sessionCookie(t, h.login(t, "alice", "secret-password"))
	recorder := h.request(t, http.MethodGet, "/api/v1/roles", "", cookie)
	if recorder.Code != http.StatusOK {
		t.Fatalf("roles = %d %s", recorder.Code, recorder.Body.String())
	}
	var response rolesResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode roles: %v", err)
	}
	if len(response.Items) != 1 || response.Items[0].Name != "owner" || !slices.IsSorted(response.Items[0].Permissions) || response.NextCursor != "" {
		t.Fatalf("roles response = %+v", response)
	}
	h.authorization.mu.Lock()
	h.authorization.permissions["11111111-1111-4111-8111-111111111111"] = []core.Permission{core.PermissionRequestsCreate}
	h.authorization.mu.Unlock()
	recorder = h.request(t, http.MethodGet, "/api/v1/roles", "", cookie)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("roles without permission = %d %s", recorder.Code, recorder.Body.String())
	}
	var envelope httputil.ErrorResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil || envelope.Code != codeForbidden {
		t.Fatalf("forbidden envelope = %+v, %v", envelope, err)
	}
}

func TestRolesUsesBoundedCursorPagination(t *testing.T) {
	h := newAuthHarness(t, nil)
	h.authorization.mu.Lock()
	h.authorization.roles = []core.Role{
		{ID: "00000000-0000-4000-8000-000000000002", Name: "member"},
		{ID: "00000000-0000-4000-8000-000000000001", Name: "owner"},
	}
	h.authorization.mu.Unlock()
	cookie := sessionCookie(t, h.login(t, "alice", "secret-password"))
	first := h.request(t, http.MethodGet, "/api/v1/roles?page_size=1", "", cookie)
	var page rolesResponse
	if err := json.Unmarshal(first.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode first page: %v", err)
	}
	if first.Code != http.StatusOK || len(page.Items) != 1 || page.Items[0].Name != "member" || page.NextCursor == "" {
		t.Fatalf("first page = %d %+v", first.Code, page)
	}
	second := h.request(t, http.MethodGet, "/api/v1/roles?page_size=1&cursor="+page.NextCursor, "", cookie)
	if err := json.Unmarshal(second.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode second page: %v", err)
	}
	if second.Code != http.StatusOK || len(page.Items) != 1 || page.Items[0].Name != "owner" || page.NextCursor != "" {
		t.Fatalf("second page = %d %+v", second.Code, page)
	}

	_ = h.request(t, http.MethodGet, "/api/v1/roles?page_size=1000", "", cookie)
	h.authorization.mu.Lock()
	gotSize := h.authorization.lastPageSize
	h.authorization.mu.Unlock()
	if gotSize != maxRolePageSize+1 {
		t.Fatalf("store page size = %d, want bounded %d", gotSize, maxRolePageSize+1)
	}
}

func TestRolesRejectsInvalidPaginationFields(t *testing.T) {
	invalidUTF8 := base64.RawURLEncoding.EncodeToString([]byte{0xff})
	tests := []struct {
		name, query, field string
	}{
		{name: "invalid page size", query: "?page_size=zero", field: "page_size"},
		{name: "empty page size", query: "?page_size=", field: "page_size"},
		{name: "zero page size", query: "?page_size=0", field: "page_size"},
		{name: "negative page size", query: "?page_size=-1", field: "page_size"},
		{name: "duplicate page size", query: "?page_size=1&page_size=2", field: "page_size"},
		{name: "empty cursor", query: "?cursor=", field: "cursor"},
		{name: "duplicate cursor", query: "?cursor=bWVtYmVy&cursor=b3duZXI", field: "cursor"},
		{name: "malformed base64 cursor", query: "?cursor=!", field: "cursor"},
		{name: "NUL cursor", query: "?cursor=AA", field: "cursor"},
		{name: "control cursor", query: "?cursor=" + base64.RawURLEncoding.EncodeToString([]byte("owner\n")), field: "cursor"},
		{name: "oversized decoded cursor", query: "?cursor=" + base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("r", 65))), field: "cursor"},
		{name: "oversized cursor", query: "?cursor=" + strings.Repeat("a", maxRoleCursorBytes+1), field: "cursor"},
		{name: "invalid UTF-8 cursor", query: "?cursor=" + invalidUTF8, field: "cursor"},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			h := newAuthHarness(t, nil)
			cookie := sessionCookie(t, h.login(t, "alice", "secret-password"))
			recorder := h.request(t, http.MethodGet, "/api/v1/roles"+testCase.query, "", cookie)
			assertPaginationValidation(t, recorder, testCase.field)
			if h.authorization.listRolesCalls != 0 {
				t.Fatalf("ListRoles calls = %d, want zero", h.authorization.listRolesCalls)
			}
		})
	}
}

func FuzzParseRoleCursor(f *testing.F) {
	for _, seed := range []string{"b3duZXI", "!", "AA", strings.Repeat("a", maxRoleCursorBytes+1)} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, value string) {
		decoded, message := parseRoleCursor([]string{value})
		if message != "" {
			return
		}
		if len(value) > maxRoleCursorBytes || !validDecodedRoleCursor(decoded) {
			t.Fatalf("accepted cursor %q as %q", value, decoded)
		}
	})
}

func validDecodedRoleCursor(value string) bool {
	return value != "" && len(value) <= core.MaxRoleNameBytes && utf8.ValidString(value) &&
		value == strings.TrimSpace(value) && strings.IndexFunc(value, unicode.IsControl) == -1
}

func assertPaginationValidation(t *testing.T, recorder *httptest.ResponseRecorder, field string) {
	t.Helper()
	if recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422: %s", recorder.Code, recorder.Body.String())
	}
	envelope := decodeEnvelope(t, recorder)
	if envelope.Code != codeValidationFailed || len(envelope.Fields) != 1 || envelope.Fields[0].Field != field {
		t.Fatalf("validation envelope = %+v, want field %q", envelope, field)
	}
}

func TestRolesDefaultPageSizeFetchesOneBoundedLookahead(t *testing.T) {
	h := newAuthHarness(t, nil)
	h.authorization.roles = make([]core.Role, defaultRolePageSize+2)
	for index := range h.authorization.roles {
		h.authorization.roles[index] = core.Role{Name: fmt.Sprintf("role-%03d", index)}
	}
	cookie := sessionCookie(t, h.login(t, "alice", "secret-password"))
	recorder := h.request(t, http.MethodGet, "/api/v1/roles", "", cookie)
	var response rolesResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode roles: %v", err)
	}
	if recorder.Code != http.StatusOK || len(response.Items) != defaultRolePageSize || response.NextCursor == "" {
		t.Fatalf("default page = %d %+v", recorder.Code, response)
	}
	if h.authorization.lastPageSize != defaultRolePageSize+1 {
		t.Fatalf("store page size = %d, want %d", h.authorization.lastPageSize, defaultRolePageSize+1)
	}
}

func TestAuthorizationAuditSinkFailureIsObservable(t *testing.T) {
	h := newAuthHarness(t, nil)
	cookie := sessionCookie(t, h.login(t, "alice", "secret-password"))
	h.audit.err = errors.New("audit unavailable")
	h.authorization.mu.Lock()
	h.authorization.permissions["11111111-1111-4111-8111-111111111111"] = nil
	h.authorization.mu.Unlock()
	recorder := h.request(t, http.MethodGet, "/api/v1/roles", "", cookie)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("roles denial = %d, want 403", recorder.Code)
	}
	if h.metrics.auditFailureCount() != 1 || !strings.Contains(h.logs.String(), `"action":"auth.permission"`) {
		t.Fatalf("audit failure metric/log = %d / %s", h.metrics.auditFailureCount(), h.logs.String())
	}
}
