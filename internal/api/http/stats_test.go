package http

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/BonzTM/bloom/internal/core"
)

type fakeStatsReader struct {
	mu      sync.Mutex
	result  core.StatsResult
	err     error
	queries []core.StatsQuery
}

func (f *fakeStatsReader) ReadStats(_ context.Context, query core.StatsQuery) (core.StatsResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.queries = append(f.queries, query)
	if f.err != nil {
		return core.StatsResult{}, f.err
	}
	result := f.result
	result.Window = query.Window
	return result, nil
}

func TestStatsRoutesReturnEmptyWindowsAndParsedQueries(t *testing.T) {
	t.Parallel()
	tests := []struct {
		path   string
		report core.StatsReport
	}{
		{"/api/v1/stats/overview?days=7&tz=America%2FLos_Angeles", core.StatsReportOverview},
		{"/api/v1/stats/daily", core.StatsReportDaily},
		{"/api/v1/stats/patterns", core.StatsReportPatterns},
		{"/api/v1/stats/titles?kind=movie", core.StatsReportTitles},
		{"/api/v1/stats/users", core.StatsReportUsers},
		{"/api/v1/stats/libraries", core.StatsReportLibraries},
		{"/api/v1/stats/users/33333333-3333-4333-8333-333333333333/user-1", core.StatsReportUser},
	}
	for _, test := range tests {
		t.Run(test.path, func(t *testing.T) {
			t.Parallel()
			h := newAuthHarness(t, nil)
			cookie := sessionCookie(t, h.login(t, "alice", "secret-password"))
			recorder := h.request(t, http.MethodGet, test.path, "", cookie)
			if recorder.Code != http.StatusOK {
				t.Fatalf("GET %s = %d: %s", test.path, recorder.Code, recorder.Body.String())
			}
			if strings.Contains(recorder.Body.String(), ":null") {
				t.Fatalf("empty response contains null collection: %s", recorder.Body.String())
			}
			h.stats.mu.Lock()
			query := h.stats.queries[len(h.stats.queries)-1]
			h.stats.mu.Unlock()
			if query.Report != test.report || query.Window.Zone == "" || query.Window.Days < 1 {
				t.Fatalf("query = %+v", query)
			}
			if test.report == core.StatsReportUser && query.Window.MediaServerID != query.UserServerID {
				t.Fatalf("user query does not resolve path server: %+v", query)
			}
		})
	}
}

func TestStatsRoutesRejectBadParameters(t *testing.T) {
	t.Parallel()
	tests := []string{
		"/api/v1/stats/overview?days=0",
		"/api/v1/stats/overview?days=366",
		"/api/v1/stats/overview?media_server_id=bad",
		"/api/v1/stats/overview?library_id=library",
		"/api/v1/stats/overview?media_server_id=33333333-3333-4333-8333-333333333333&library_id=nul%00library",
		"/api/v1/stats/overview?media_server_id=33333333-3333-4333-8333-333333333333&library_id=one&library_id=two",
		"/api/v1/stats/overview?tz=Mars%2FOlympus",
		"/api/v1/stats/overview?tz=Local",
		"/api/v1/stats/overview?tz=UTC&tz=UTC",
		"/api/v1/stats/titles?kind=bad",
		"/api/v1/stats/titles",
		"/api/v1/stats/users/bad/user-1",
		"/api/v1/stats/users/33333333-3333-4333-8333-333333333333/user-1?media_server_id=44444444-4444-4444-8444-444444444444",
	}
	for _, path := range tests {
		t.Run(path, func(t *testing.T) {
			t.Parallel()
			h := newAuthHarness(t, nil)
			cookie := sessionCookie(t, h.login(t, "alice", "secret-password"))
			recorder := h.request(t, http.MethodGet, path, "", cookie)
			if recorder.Code != http.StatusUnprocessableEntity {
				t.Fatalf("GET %s = %d, want 422: %s", path, recorder.Code, recorder.Body.String())
			}
			if path == "/api/v1/stats/overview?library_id=library" {
				envelope := decodeEnvelope(t, recorder)
				if envelope.Code != codeValidationFailed || len(envelope.Fields) != 1 ||
					envelope.Fields[0].Field != "media_server_id" {
					t.Fatalf("library dependency validation = %+v", envelope)
				}
			}
		})
	}
}

func TestStatsRoutesPassLibraryFilterWithMediaServer(t *testing.T) {
	t.Parallel()
	h := newAuthHarness(t, nil)
	cookie := sessionCookie(t, h.login(t, "alice", "secret-password"))
	path := "/api/v1/stats/overview?media_server_id=33333333-3333-4333-8333-333333333333&library_id=library-1"
	recorder := h.request(t, http.MethodGet, path, "", cookie)
	if recorder.Code != http.StatusOK {
		t.Fatalf("GET %s = %d: %s", path, recorder.Code, recorder.Body.String())
	}
	h.stats.mu.Lock()
	query := h.stats.queries[len(h.stats.queries)-1]
	h.stats.mu.Unlock()
	if query.LibraryID != "library-1" || query.Window.MediaServerID != "33333333-3333-4333-8333-333333333333" {
		t.Fatalf("query = %+v", query)
	}
}

func TestStatsUserRejectsUnsafeMediaUserIDBeforeStore(t *testing.T) {
	t.Parallel()
	for _, encodedID := range []string{"user%00id", "user%7Fid", "user%C2%85id", "user%FFid"} {
		t.Run(encodedID, func(t *testing.T) {
			t.Parallel()
			h := newAuthHarness(t, nil)
			cookie := sessionCookie(t, h.login(t, "alice", "secret-password"))
			path := "/api/v1/stats/users/33333333-3333-4333-8333-333333333333/" + encodedID
			recorder := h.request(t, http.MethodGet, path, "", cookie)
			if recorder.Code != http.StatusUnprocessableEntity {
				t.Fatalf("GET %s = %d, want 422: %s", path, recorder.Code, recorder.Body.String())
			}
			h.stats.mu.Lock()
			calls := len(h.stats.queries)
			h.stats.mu.Unlock()
			if calls != 0 {
				t.Fatalf("statistics store calls = %d, want 0", calls)
			}
		})
	}
}

func TestStatsRoutesRequirePermissionAndMapAuditResource(t *testing.T) {
	t.Parallel()
	for _, path := range statsRoutePaths() {
		t.Run(path, func(t *testing.T) {
			t.Parallel()
			h := newAuthHarness(t, nil)
			missing := h.request(t, http.MethodGet, path, "", nil)
			if missing.Code != http.StatusUnauthorized {
				t.Fatalf("missing session = %d", missing.Code)
			}
			cookie := sessionCookie(t, h.login(t, "alice", "secret-password"))
			h.authorization.mu.Lock()
			h.authorization.permissions[h.store.accounts["alice"].ID] = nil
			h.authorization.mu.Unlock()
			forbidden := h.request(t, http.MethodGet, path, "", cookie)
			if forbidden.Code != http.StatusForbidden {
				t.Fatalf("missing permission = %d", forbidden.Code)
			}
			event := h.audit.last(t)
			if event.Resource != auditResourceStats || event.Permission != string(core.PermissionStatsReadAll) {
				t.Fatalf("authorization audit = %+v", event)
			}
		})
	}
}

func TestStatsStoreFailureIsOpaque(t *testing.T) {
	t.Parallel()
	h := newAuthHarness(t, nil)
	h.stats.err = errors.New("database contains private detail")
	cookie := sessionCookie(t, h.login(t, "alice", "secret-password"))
	recorder := h.request(t, http.MethodGet, "/api/v1/stats/overview", "", cookie)
	if recorder.Code != http.StatusInternalServerError || strings.Contains(recorder.Body.String(), "private detail") {
		t.Fatalf("store failure = %d %s", recorder.Code, recorder.Body.String())
	}
}

func TestStatsGETDoesNotUseCSRFAuditResourceTable(t *testing.T) {
	t.Parallel()
	if got := csrfAuditResource("/api/v1/stats/overview"); got != auditResourceRouteUnmatched {
		t.Fatalf("stats GET CSRF resource = %q, want unmatched", got)
	}
}

func statsRoutePaths() []string {
	return []string{
		"/api/v1/stats/overview", "/api/v1/stats/daily", "/api/v1/stats/patterns",
		"/api/v1/stats/titles?kind=movie", "/api/v1/stats/users",
		"/api/v1/stats/libraries",
		"/api/v1/stats/users/33333333-3333-4333-8333-333333333333/user-1",
	}
}

func TestStatsResponseDTOIncludesStoredWatchSeconds(t *testing.T) {
	t.Parallel()
	h := newAuthHarness(t, nil)
	h.stats.result = core.StatsResult{Totals: core.StatsTotals{Plays: 1, WatchSeconds: 42}}
	cookie := sessionCookie(t, h.login(t, "alice", "secret-password"))
	recorder := h.request(t, http.MethodGet, "/api/v1/stats/overview", "", cookie)
	var response statsOverviewResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Totals.WatchSeconds != 42 {
		t.Fatalf("response = %+v", response)
	}
}

func TestStatsRoutePatternsAreRegistered(t *testing.T) {
	t.Parallel()
	h := newAuthHarness(t, nil)
	request := httptest.NewRequest(http.MethodGet, "https://bloom.test/api/v1/stats/overview", nil)
	recorder := httptest.NewRecorder()
	h.h.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("registered route status = %d", recorder.Code)
	}
}

func TestStatsOpenAPIContract(t *testing.T) {
	document := loadOpenAPI(t)
	tests := []struct {
		pattern, target, schema string
	}{
		{"/api/v1/stats/overview", "/api/v1/stats/overview", statsOverviewSchema},
		{"/api/v1/stats/daily", "/api/v1/stats/daily", statsDailySchema},
		{"/api/v1/stats/patterns", "/api/v1/stats/patterns", statsPatternsSchema},
		{"/api/v1/stats/titles", "/api/v1/stats/titles?kind=movie", statsTitlesSchema},
		{"/api/v1/stats/users", "/api/v1/stats/users", statsUsersSchema},
		{"/api/v1/stats/libraries", "/api/v1/stats/libraries", statsLibrariesSchema},
		{"/api/v1/stats/users/{media_server_id}/{media_user_id}", "/api/v1/stats/users/33333333-3333-4333-8333-333333333333/user-1", statsUserSchema},
	}
	for _, test := range tests {
		t.Run(test.pattern, func(t *testing.T) {
			observed := make(map[int]authContractCase)
			for _, status := range []int{200, 401, 403, 422, 500, 405} {
				h := newAuthHarness(t, nil)
				recorder := statsContractRequest(t, h, test.target, status)
				schema := errorSchema
				if status == http.StatusOK {
					schema = test.schema
				}
				headers := []string{"Cache-Control", "Set-Cookie", "Vary", "X-Request-ID"}
				if status == http.StatusMethodNotAllowed {
					headers = []string{"Allow", "Cache-Control", "X-Request-ID"}
				}
				expectation := authContractCase{
					path: test.pattern, method: "get", status: status, schema: schema, headers: headers,
				}
				assertHandlerContract(t, document, recorder, expectation)
				observed[status] = expectation
			}
			assertOperationContract(t, document, "get "+test.pattern, observed)
		})
	}
}

func statsContractRequest(t *testing.T, h authHarness, target string, status int) *httptest.ResponseRecorder {
	t.Helper()
	if status == http.StatusUnauthorized {
		return h.request(t, http.MethodGet, target, "", &http.Cookie{Name: sessionCookieName, Value: "invalid"})
	}
	if status == http.StatusMethodNotAllowed {
		return h.request(t, http.MethodPatch, target, "", nil)
	}
	cookie := sessionCookie(t, h.login(t, "alice", "secret-password"))
	switch status {
	case http.StatusOK:
		if strings.Contains(target, "/patterns") {
			h.stats.result.Weekdays = make([]core.StatsWeekdayBucket, 7)
			h.stats.result.Hours = make([]core.StatsHourBucket, 24)
		}
	case http.StatusForbidden:
		h.authorization.mu.Lock()
		h.authorization.permissions[h.store.accounts["alice"].ID] = nil
		h.authorization.mu.Unlock()
	case http.StatusUnprocessableEntity:
		separator := "?"
		if strings.Contains(target, "?") {
			separator = "&"
		}
		target += separator + "days=0"
	case http.StatusInternalServerError:
		h.stats.err = errors.New("store failed")
	}
	return h.request(t, http.MethodGet, target, "", cookie)
}
