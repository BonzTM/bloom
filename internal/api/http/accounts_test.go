package http

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/BonzTM/bloom/internal/core"
)

type fakeAccountAdminReader struct {
	items     []core.AdminAccount
	err       error
	listCalls int
	getCalls  int
	lastQuery core.AccountListQuery
}

func newFakeAccountAdminReader() *fakeAccountAdminReader {
	createdAt := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	return &fakeAccountAdminReader{items: []core.AdminAccount{
		{
			ID: "11111111-1111-4111-8111-111111111111", Username: "alice", UsernameKey: "alice",
			CreatedAt: createdAt, SignInMethod: core.AccountSignInBoth,
			Roles: []core.AccountRoleAssignment{{Name: "owner", Source: core.AccountRoleSourceManual}},
			MediaUsers: []core.AccountMediaUser{{
				MediaServerID: "33333333-3333-4333-8333-333333333333", MediaServerName: "Home",
				MediaUserID: "media-alice", Username: "Alice J", SuppressedAt: &createdAt,
			}},
		},
		{ID: "22222222-2222-4222-8222-222222222222", Username: "bob", UsernameKey: "bob", CreatedAt: createdAt, SignInMethod: core.AccountSignInLocal, Roles: []core.AccountRoleAssignment{}, MediaUsers: []core.AccountMediaUser{}},
		{ID: "44444444-4444-4444-8444-444444444444", Username: "carol", UsernameKey: "carol", CreatedAt: createdAt, SignInMethod: core.AccountSignInOIDC, Roles: []core.AccountRoleAssignment{}, MediaUsers: []core.AccountMediaUser{}},
	}}
}

func (f *fakeAccountAdminReader) ListAccounts(
	_ context.Context, query core.AccountListQuery,
) (core.AccountPage, error) {
	f.listCalls++
	f.lastQuery = query
	if f.err != nil {
		return core.AccountPage{}, f.err
	}
	items := make([]core.AdminAccount, 0, len(f.items))
	for _, item := range f.items {
		if query.SearchKey != "" && !strings.Contains(item.UsernameKey, query.SearchKey) {
			continue
		}
		if query.After != nil && (item.UsernameKey < query.After.UsernameKey ||
			(item.UsernameKey == query.After.UsernameKey && item.ID <= query.After.ID)) {
			continue
		}
		items = append(items, item)
	}
	hasMore := len(items) > query.Limit
	if hasMore {
		items = items[:query.Limit]
	}
	return core.AccountPage{Items: items, HasMore: hasMore}, nil
}

func (f *fakeAccountAdminReader) GetAdminAccount(_ context.Context, id string) (core.AdminAccount, error) {
	f.getCalls++
	if f.err != nil {
		return core.AdminAccount{}, f.err
	}
	for _, item := range f.items {
		if item.ID == id {
			return item, nil
		}
	}
	return core.AdminAccount{}, core.ErrNotFound
}

func TestAccountsReturnsSecretFreeAdministrativeShape(t *testing.T) {
	h := newAuthHarness(t, nil)
	cookie := sessionCookie(t, h.login(t, "alice", "secret-password"))
	recorder := h.request(t, http.MethodGet, "/api/v1/accounts", "", cookie)
	if recorder.Code != http.StatusOK {
		t.Fatalf("accounts = %d %s", recorder.Code, recorder.Body.String())
	}
	var response accountsResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode accounts: %v", err)
	}
	if len(response.Items) != 3 || !response.Items[0].IsSelf || response.Items[1].IsSelf ||
		response.Items[0].SignInMethod != core.AccountSignInBoth || len(response.Items[0].Roles) != 1 ||
		len(response.Items[0].MediaUsers) != 1 || !response.Items[0].MediaUsers[0].Suppressed {
		t.Fatalf("accounts response = %+v", response)
	}
	for _, forbidden := range []string{"password", "hash", "issuer", "subject"} {
		if strings.Contains(recorder.Body.String(), forbidden) {
			t.Errorf("response contains forbidden credential field %q: %s", forbidden, recorder.Body.String())
		}
	}
}

func TestAccountsCursorRoundTripAndQueryBinding(t *testing.T) {
	h := newAuthHarness(t, nil)
	cookie := sessionCookie(t, h.login(t, "alice", "secret-password"))
	first := h.request(t, http.MethodGet, "/api/v1/accounts?limit=1&q=%EF%BC%A1", "", cookie)
	var page accountsResponse
	if err := json.Unmarshal(first.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode first account page: %v", err)
	}
	if first.Code != http.StatusOK || len(page.Items) != 1 || page.NextCursor == "" || page.Items[0].Username != "alice" {
		t.Fatalf("first account page = %d %+v", first.Code, page)
	}
	cursor := page.NextCursor
	second := h.request(t, http.MethodGet, "/api/v1/accounts?limit=1&q=%EF%BC%A1&cursor="+cursor, "", cookie)
	if err := json.Unmarshal(second.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode second account page: %v", err)
	}
	if second.Code != http.StatusOK || len(page.Items) != 1 || page.Items[0].Username != "carol" || page.NextCursor != "" {
		t.Fatalf("second account page = %d %+v", second.Code, page)
	}
	mismatch := h.request(t, http.MethodGet, "/api/v1/accounts?limit=1&q=b&cursor="+cursor, "", cookie)
	assertPaginationValidation(t, mismatch, "cursor")
}

func TestAccountsLimitAndSearchValidation(t *testing.T) {
	tests := []struct {
		name, query, field string
	}{
		{name: "zero limit", query: "?limit=0", field: "limit"},
		{name: "over limit", query: "?limit=101", field: "limit"},
		{name: "duplicate limit", query: "?limit=1&limit=2", field: "limit"},
		{name: "oversized search", query: "?q=" + strings.Repeat("a", core.MaxAccountSearchBytes+1), field: "q"},
		{name: "duplicate search", query: "?q=a&q=b", field: "q"},
		{name: "invalid cursor", query: "?cursor=***", field: "cursor"},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			h := newAuthHarness(t, nil)
			cookie := sessionCookie(t, h.login(t, "alice", "secret-password"))
			recorder := h.request(t, http.MethodGet, "/api/v1/accounts"+testCase.query, "", cookie)
			assertPaginationValidation(t, recorder, testCase.field)
			if h.accountAdmin.listCalls != 0 {
				t.Fatalf("ListAccounts calls = %d, want zero", h.accountAdmin.listCalls)
			}
		})
	}
}

func TestAccountsUsesDefaultAndMaximumLimits(t *testing.T) {
	h := newAuthHarness(t, nil)
	cookie := sessionCookie(t, h.login(t, "alice", "secret-password"))
	_ = h.request(t, http.MethodGet, "/api/v1/accounts", "", cookie)
	if h.accountAdmin.lastQuery.Limit != defaultAccountPageSize {
		t.Fatalf("default limit = %d, want %d", h.accountAdmin.lastQuery.Limit, defaultAccountPageSize)
	}
	_ = h.request(t, http.MethodGet, "/api/v1/accounts?limit=100", "", cookie)
	if h.accountAdmin.lastQuery.Limit != core.MaxAccountListPageSize {
		t.Fatalf("maximum limit = %d, want %d", h.accountAdmin.lastQuery.Limit, core.MaxAccountListPageSize)
	}
}

func TestAccountsRequiresUsersManage(t *testing.T) {
	h := newAuthHarness(t, nil)
	cookie := sessionCookie(t, h.login(t, "alice", "secret-password"))
	h.authorization.permissions[h.store.accounts["alice"].ID] = []core.Permission{core.PermissionAdminRoles, core.PermissionUsersInvite}
	recorder := h.request(t, http.MethodGet, "/api/v1/accounts", "", cookie)
	if recorder.Code != http.StatusForbidden || h.accountAdmin.listCalls != 0 {
		t.Fatalf("accounts denial = %d, list calls %d", recorder.Code, h.accountAdmin.listCalls)
	}
}

func TestAccountDetailPaths(t *testing.T) {
	h := newAuthHarness(t, nil)
	cookie := sessionCookie(t, h.login(t, "alice", "secret-password"))
	recorder := h.request(t, http.MethodGet, "/api/v1/accounts/11111111-1111-4111-8111-111111111111", "", cookie)
	var response adminAccountResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil || recorder.Code != http.StatusOK || !response.IsSelf {
		t.Fatalf("account detail = %d %+v, %v", recorder.Code, response, err)
	}
	missing := h.request(t, http.MethodGet, "/api/v1/accounts/55555555-5555-4555-8555-555555555555", "", cookie)
	if missing.Code != http.StatusNotFound {
		t.Fatalf("missing account = %d %s", missing.Code, missing.Body.String())
	}
	invalid := h.request(t, http.MethodGet, "/api/v1/accounts/bad", "", cookie)
	assertPaginationValidation(t, invalid, "id")
}

func TestAccountAdminStoreFailureIsOpaque(t *testing.T) {
	h := newAuthHarness(t, nil)
	cookie := sessionCookie(t, h.login(t, "alice", "secret-password"))
	h.accountAdmin.err = errors.New("database secret detail")
	recorder := h.request(t, http.MethodGet, "/api/v1/accounts", "", cookie)
	assertOpaqueInternalError(t, recorder, "database secret detail")
}

func TestAdminAccountDTOPreservesStoredOrder(t *testing.T) {
	account := newFakeAccountAdminReader().items[0]
	account.Roles = append(account.Roles, core.AccountRoleAssignment{Name: "member", Source: core.AccountRoleSourceOIDC})
	response := adminAccountDTO(account, account.ID)
	if !slices.EqualFunc(response.Roles, account.Roles, func(left accountRoleAssignmentResponse, right core.AccountRoleAssignment) bool {
		return left.Name == right.Name && left.Source == right.Source
	}) {
		t.Fatalf("roles = %+v, want %+v", response.Roles, account.Roles)
	}
}
