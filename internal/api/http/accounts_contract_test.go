package http

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/BonzTM/bloom/internal/core"
)

func TestAccountOpenAPIContractMatchesHandlers(t *testing.T) {
	document := loadOpenAPI(t)
	observed := make(map[string]map[int]authContractCase)
	for _, testCase := range accountContractCases() {
		t.Run(testCase.name, func(t *testing.T) {
			h := newAuthHarness(t, nil)
			recorder := testCase.run(t, h)
			assertHandlerContract(t, document, recorder, testCase)
			documented := testCase
			if documented.status == http.StatusUnauthorized {
				documented.headers = appendCopy(documented.headers, "Set-Cookie")
			}
			responses := document.Paths[testCase.path][testCase.method].Responses
			assertDocumentedResponse(t, document, testCase.method+" "+testCase.path, testCase.status, responses, documented)
			mergeContractCase(t, observed, documented)
		})
	}
	for operation, responses := range observed {
		assertOperationContract(t, document, operation, responses)
	}
}

func accountContractCases() []authContractCase {
	session := []string{"Cache-Control", "Vary", "X-Request-ID"}
	sessionCookie := []string{"Cache-Control", "Set-Cookie", "Vary", "X-Request-ID"}
	method := []string{"Allow", "Cache-Control", "X-Request-ID"}
	listPath, itemPath := "/api/v1/accounts", "/api/v1/accounts/{id}"
	return []authContractCase{
		{name: "accounts 200", path: listPath, method: "get", status: 200, schema: adminAccountsSchema, headers: sessionCookie, run: accountsContractSuccess},
		{name: "accounts 401", path: listPath, method: "get", status: 401, schema: errorSchema, headers: session, run: accountsContractMissing},
		{name: "accounts 403", path: listPath, method: "get", status: 403, schema: errorSchema, headers: sessionCookie, run: accountsContractForbidden},
		{name: "accounts 422", path: listPath, method: "get", status: 422, schema: errorSchema, headers: sessionCookie, run: accountsContractInvalid},
		{name: "accounts 500", path: listPath, method: "get", status: 500, schema: errorSchema, headers: sessionCookie, run: accountsContractFailure},
		{name: "accounts 405", path: listPath, method: "get", status: 405, schema: errorSchema, headers: method, run: accountsContractMethod},
		{name: "account 200", path: itemPath, method: "get", status: 200, schema: adminAccountSchema, headers: sessionCookie, run: accountContractSuccess},
		{name: "account 401", path: itemPath, method: "get", status: 401, schema: errorSchema, headers: session, run: accountContractMissing},
		{name: "account 403", path: itemPath, method: "get", status: 403, schema: errorSchema, headers: sessionCookie, run: accountContractForbidden},
		{name: "account 404", path: itemPath, method: "get", status: 404, schema: errorSchema, headers: sessionCookie, run: accountContractNotFound},
		{name: "account 422", path: itemPath, method: "get", status: 422, schema: errorSchema, headers: sessionCookie, run: accountContractInvalid},
		{name: "account 500", path: itemPath, method: "get", status: 500, schema: errorSchema, headers: sessionCookie, run: accountContractFailure},
		{name: "account 405", path: itemPath, method: "get", status: 405, schema: errorSchema, headers: method, run: accountContractMethod},
	}
}

func accountsContractSuccess(t *testing.T, h authHarness) *httptest.ResponseRecorder {
	t.Helper()
	return authenticatedGet(t, h, "/api/v1/accounts")
}

func accountsContractMissing(t *testing.T, h authHarness) *httptest.ResponseRecorder {
	t.Helper()
	return h.request(t, http.MethodGet, "/api/v1/accounts", "", nil)
}

func accountsContractForbidden(t *testing.T, h authHarness) *httptest.ResponseRecorder {
	t.Helper()
	return forbiddenGet(t, h, "/api/v1/accounts")
}

func accountsContractInvalid(t *testing.T, h authHarness) *httptest.ResponseRecorder {
	t.Helper()
	return authenticatedGet(t, h, "/api/v1/accounts?limit=0")
}

func accountsContractFailure(t *testing.T, h authHarness) *httptest.ResponseRecorder {
	t.Helper()
	h.accountAdmin.err = errors.New("database unavailable")
	return authenticatedGet(t, h, "/api/v1/accounts")
}

func accountsContractMethod(t *testing.T, h authHarness) *httptest.ResponseRecorder {
	t.Helper()
	return assertMethodRejected(t, h, http.MethodPost, "/api/v1/accounts", http.MethodGet)
}

func accountContractSuccess(t *testing.T, h authHarness) *httptest.ResponseRecorder {
	t.Helper()
	return authenticatedGet(t, h, "/api/v1/accounts/11111111-1111-4111-8111-111111111111")
}

func accountContractMissing(t *testing.T, h authHarness) *httptest.ResponseRecorder {
	t.Helper()
	return h.request(t, http.MethodGet, "/api/v1/accounts/11111111-1111-4111-8111-111111111111", "", nil)
}

func accountContractForbidden(t *testing.T, h authHarness) *httptest.ResponseRecorder {
	t.Helper()
	return forbiddenGet(t, h, "/api/v1/accounts/11111111-1111-4111-8111-111111111111")
}

func accountContractNotFound(t *testing.T, h authHarness) *httptest.ResponseRecorder {
	t.Helper()
	return authenticatedGet(t, h, "/api/v1/accounts/55555555-5555-4555-8555-555555555555")
}

func accountContractInvalid(t *testing.T, h authHarness) *httptest.ResponseRecorder {
	t.Helper()
	return authenticatedGet(t, h, "/api/v1/accounts/bad")
}

func accountContractFailure(t *testing.T, h authHarness) *httptest.ResponseRecorder {
	t.Helper()
	h.accountAdmin.err = errors.New("database unavailable")
	return authenticatedGet(t, h, "/api/v1/accounts/11111111-1111-4111-8111-111111111111")
}

func accountContractMethod(t *testing.T, h authHarness) *httptest.ResponseRecorder {
	t.Helper()
	return assertMethodRejected(t, h, http.MethodPost, "/api/v1/accounts/11111111-1111-4111-8111-111111111111", http.MethodGet)
}

func TestAccountOpenAPIDocumentsPagingBounds(t *testing.T) {
	operation := loadOpenAPI(t).validator.Paths.Find("/api/v1/accounts").Get
	want := map[string]struct{ minimum, maximum float64 }{
		"limit":  {minimum: 1, maximum: core.MaxAccountListPageSize},
		"cursor": {minimum: 1, maximum: core.MaxAccountListCursorBytes},
	}
	for _, parameter := range operation.Parameters {
		if parameter.Value.Name == "q" {
			if got := fmt.Sprint(parameter.Value.Schema.Value.Extensions["x-max-bytes"]); got != strconv.Itoa(core.MaxAccountSearchBytes) {
				t.Fatalf("q x-max-bytes = %v, want %d", got, core.MaxAccountSearchBytes)
			}
			continue
		}
		bounds, ok := want[parameter.Value.Name]
		if !ok {
			continue
		}
		schema := parameter.Value.Schema.Value
		if parameter.Value.Name == "limit" {
			if schema.Min == nil || *schema.Min != bounds.minimum || schema.Max == nil || *schema.Max != bounds.maximum {
				t.Fatalf("limit bounds = %v..%v", schema.Min, schema.Max)
			}
		} else if int(schema.MinLength) != int(bounds.minimum) || int(*schema.MaxLength) != int(bounds.maximum) {
			t.Fatalf("cursor bounds = %d..%d", schema.MinLength, *schema.MaxLength)
		}
		delete(want, parameter.Value.Name)
	}
	if len(want) != 0 {
		t.Fatalf("missing account paging parameters: %v", want)
	}
}
