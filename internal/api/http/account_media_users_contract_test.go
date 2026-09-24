package http

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/BonzTM/bloom/internal/core"
)

const (
	accountMediaUserSchema  = "#/components/schemas/AccountMediaUser"
	accountMediaUsersSchema = "#/components/schemas/AccountMediaUsersResponse"
)

func TestAccountMediaUserOpenAPIContractMatchesHandlers(t *testing.T) {
	document := loadOpenAPI(t)
	for _, testCase := range accountMediaUserContractCases() {
		t.Run(testCase.name, func(t *testing.T) {
			h := newAuthHarness(t, nil)
			recorder := testCase.run(t, h)
			assertHandlerContract(t, document, recorder, testCase)
			operation := testCase.method + " " + testCase.path
			responses := document.Paths[testCase.path][testCase.method].Responses
			documented := testCase
			if documented.status == http.StatusUnauthorized {
				documented.headers = appendCopy(documented.headers, "Set-Cookie")
			}
			assertDocumentedResponse(t, document, operation, testCase.status, responses, documented)
		})
	}
}

func accountMediaUserContractCases() []authContractCase {
	accountID := "11111111-1111-4111-8111-111111111111"
	serverID := "33333333-3333-4333-8333-333333333333"
	adminList := "/api/v1/accounts/{id}/media-users"
	adminItem := "/api/v1/accounts/{id}/media-users/{media_server_id}"
	itemPath := "/api/v1/accounts/" + accountID + "/media-users/" + serverID
	cases := []authContractCase{
		{name: "own links 200", path: "/api/v1/me/media-users", method: "get", status: 200, schema: accountMediaUsersSchema, run: ownLinksSuccess},
		{name: "own links 401", path: "/api/v1/me/media-users", method: "get", status: 401, schema: errorSchema, run: func(t *testing.T, h authHarness) *httptest.ResponseRecorder {
			t.Helper()
			return h.request(t, http.MethodGet, "/api/v1/me/media-users", "", nil)
		}},
		{name: "own links 403", path: "/api/v1/me/media-users", method: "get", status: 403, schema: errorSchema, run: ownLinksForbidden},
		{name: "own stats 200", path: "/api/v1/stats/me", method: "get", status: 200, schema: statsUserSchema, run: ownStatsSuccess},
		{name: "own stats 401", path: "/api/v1/stats/me", method: "get", status: 401, schema: errorSchema, run: func(t *testing.T, h authHarness) *httptest.ResponseRecorder {
			t.Helper()
			return h.request(t, http.MethodGet, "/api/v1/stats/me", "", nil)
		}},
		{name: "own stats 403", path: "/api/v1/stats/me", method: "get", status: 403, schema: errorSchema, run: ownStatsForbidden},
		{name: "own stats 404", path: "/api/v1/stats/me", method: "get", status: 404, schema: errorSchema, run: ownStatsUnlinked},
		{name: "own stats 422", path: "/api/v1/stats/me", method: "get", status: 422, schema: errorSchema, run: ownStatsInvalid},
		{name: "admin links 200", path: adminList, method: "get", status: 200, schema: accountMediaUsersSchema, run: adminLinksSuccess},
		{name: "admin links 401", path: adminList, method: "get", status: 401, schema: errorSchema, run: func(t *testing.T, h authHarness) *httptest.ResponseRecorder {
			t.Helper()
			return h.request(t, http.MethodGet, "/api/v1/accounts/"+accountID+"/media-users", "", nil)
		}},
		{name: "admin links 403", path: adminList, method: "get", status: 403, schema: errorSchema, run: adminLinksForbidden},
		{name: "admin links 404", path: adminList, method: "get", status: 404, schema: errorSchema, run: adminLinksMissing},
		{name: "admin links 422", path: adminList, method: "get", status: 422, schema: errorSchema, run: adminLinksInvalid},
		{name: "admin set 200", path: adminItem, method: "put", status: 200, schema: accountMediaUserSchema, run: adminSetSuccess},
		{name: "admin set 401", path: adminItem, method: "put", status: 401, schema: errorSchema, run: func(t *testing.T, h authHarness) *httptest.ResponseRecorder {
			t.Helper()
			return h.requestWithContentType(t, http.MethodPut, itemPath, `{"media_user_id":"user"}`, nil, "application/json")
		}},
		{name: "admin set 403", path: adminItem, method: "put", status: 403, schema: errorSchema, run: adminSetForbidden},
		{name: "admin set 404", path: adminItem, method: "put", status: 404, schema: errorSchema, run: adminSetMissing},
		{name: "admin set 422", path: adminItem, method: "put", status: 422, schema: errorSchema, run: adminSetInvalid},
		{name: "admin delete 204", path: adminItem, method: "delete", status: 204, run: adminDeleteSuccess},
		{name: "admin delete 401", path: adminItem, method: "delete", status: 401, schema: errorSchema, run: func(t *testing.T, h authHarness) *httptest.ResponseRecorder {
			t.Helper()
			return h.request(t, http.MethodDelete, itemPath, "", nil)
		}},
		{name: "admin delete 403", path: adminItem, method: "delete", status: 403, schema: errorSchema, run: adminDeleteForbidden},
		{name: "admin delete 404", path: adminItem, method: "delete", status: 404, schema: errorSchema, run: adminDeleteMissing},
		{name: "admin delete 422", path: adminItem, method: "delete", status: 422, schema: errorSchema, run: adminDeleteInvalid},
	}
	for index := range cases {
		cases[index].headers = []string{"Cache-Control", "Set-Cookie", "Vary", "X-Request-ID"}
		if cases[index].status == http.StatusUnauthorized {
			cases[index].headers = []string{"Cache-Control", "Vary", "X-Request-ID"}
		}
	}
	return cases
}

func ownLinksSuccess(t *testing.T, h authHarness) *httptest.ResponseRecorder {
	t.Helper()
	h.accountMediaUsers.links = []core.AccountMediaUser{testAccountMediaUser()}
	return authenticatedGet(t, h, "/api/v1/me/media-users")
}

func ownLinksForbidden(t *testing.T, h authHarness) *httptest.ResponseRecorder {
	t.Helper()
	return forbiddenGet(t, h, "/api/v1/me/media-users")
}

func ownStatsSuccess(t *testing.T, h authHarness) *httptest.ResponseRecorder {
	t.Helper()
	h.accountMediaUsers.links = []core.AccountMediaUser{testAccountMediaUser()}
	return authenticatedGet(t, h, "/api/v1/stats/me")
}

func ownStatsForbidden(t *testing.T, h authHarness) *httptest.ResponseRecorder {
	t.Helper()
	return forbiddenGet(t, h, "/api/v1/stats/me")
}

func ownStatsUnlinked(t *testing.T, h authHarness) *httptest.ResponseRecorder {
	t.Helper()
	return authenticatedGet(t, h, "/api/v1/stats/me")
}

func ownStatsInvalid(t *testing.T, h authHarness) *httptest.ResponseRecorder {
	t.Helper()
	return authenticatedGet(t, h, "/api/v1/stats/me?days=0")
}

func adminLinksSuccess(t *testing.T, h authHarness) *httptest.ResponseRecorder {
	t.Helper()
	h.accountMediaUsers.links = []core.AccountMediaUser{testAccountMediaUser()}
	return authenticatedGet(t, h, "/api/v1/accounts/11111111-1111-4111-8111-111111111111/media-users")
}

func adminLinksForbidden(t *testing.T, h authHarness) *httptest.ResponseRecorder {
	t.Helper()
	return forbiddenGet(t, h, "/api/v1/accounts/11111111-1111-4111-8111-111111111111/media-users")
}

func adminLinksMissing(t *testing.T, h authHarness) *httptest.ResponseRecorder {
	t.Helper()
	h.accountMediaUsers.err = core.ErrNotFound
	return authenticatedGet(t, h, "/api/v1/accounts/11111111-1111-4111-8111-111111111111/media-users")
}

func adminLinksInvalid(t *testing.T, h authHarness) *httptest.ResponseRecorder {
	t.Helper()
	return authenticatedGet(t, h, "/api/v1/accounts/bad/media-users")
}

func adminSetSuccess(t *testing.T, h authHarness) *httptest.ResponseRecorder {
	t.Helper()
	return authenticatedAdminWrite(t, h, http.MethodPut, `{"media_user_id":"user"}`)
}

func adminSetForbidden(t *testing.T, h authHarness) *httptest.ResponseRecorder {
	t.Helper()
	return forbiddenAdminWrite(t, h, http.MethodPut, `{"media_user_id":"user"}`)
}

func adminSetMissing(t *testing.T, h authHarness) *httptest.ResponseRecorder {
	t.Helper()
	h.accountMediaUsers.err = core.ErrNotFound
	return authenticatedAdminWrite(t, h, http.MethodPut, `{"media_user_id":"user"}`)
}

func adminSetInvalid(t *testing.T, h authHarness) *httptest.ResponseRecorder {
	t.Helper()
	return authenticatedAdminWrite(t, h, http.MethodPut, `{}`)
}

func adminDeleteSuccess(t *testing.T, h authHarness) *httptest.ResponseRecorder {
	t.Helper()
	h.accountMediaUsers.links = []core.AccountMediaUser{testAccountMediaUser()}
	return authenticatedAdminWrite(t, h, http.MethodDelete, "")
}

func adminDeleteForbidden(t *testing.T, h authHarness) *httptest.ResponseRecorder {
	t.Helper()
	return forbiddenAdminWrite(t, h, http.MethodDelete, "")
}

func adminDeleteMissing(t *testing.T, h authHarness) *httptest.ResponseRecorder {
	t.Helper()
	return authenticatedAdminWrite(t, h, http.MethodDelete, "")
}

func adminDeleteInvalid(t *testing.T, h authHarness) *httptest.ResponseRecorder {
	t.Helper()
	cookie := sessionCookie(t, h.login(t, "alice", "secret-password"))
	return h.request(t, http.MethodDelete, "/api/v1/accounts/11111111-1111-4111-8111-111111111111/media-users/bad", "", cookie)
}

func authenticatedGet(t *testing.T, h authHarness, path string) *httptest.ResponseRecorder {
	t.Helper()
	cookie := sessionCookie(t, h.login(t, "alice", "secret-password"))
	return h.request(t, http.MethodGet, path, "", cookie)
}

func forbiddenGet(t *testing.T, h authHarness, path string) *httptest.ResponseRecorder {
	t.Helper()
	cookie := sessionCookie(t, h.login(t, "alice", "secret-password"))
	h.authorization.permissions[h.store.accounts["alice"].ID] = nil
	return h.request(t, http.MethodGet, path, "", cookie)
}

func authenticatedAdminWrite(
	t *testing.T, h authHarness, method, body string,
) *httptest.ResponseRecorder {
	t.Helper()
	cookie := sessionCookie(t, h.login(t, "alice", "secret-password"))
	path := "/api/v1/accounts/11111111-1111-4111-8111-111111111111/media-users/33333333-3333-4333-8333-333333333333"
	return h.requestWithContentType(t, method, path, body, cookie, "application/json")
}

func forbiddenAdminWrite(t *testing.T, h authHarness, method, body string) *httptest.ResponseRecorder {
	t.Helper()
	cookie := sessionCookie(t, h.login(t, "alice", "secret-password"))
	h.authorization.permissions[h.store.accounts["alice"].ID] = nil
	path := "/api/v1/accounts/11111111-1111-4111-8111-111111111111/media-users/33333333-3333-4333-8333-333333333333"
	return h.requestWithContentType(t, method, path, body, cookie, "application/json")
}
