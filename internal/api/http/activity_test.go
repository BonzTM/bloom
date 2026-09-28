package http

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/httputil"
)

type fakeActivityReader struct {
	activityQuery core.ActivityQuery
	timelineQuery core.TimelineWatchQuery
	watches       []core.PlaybackWatch
	activityCalls int
	timelineCalls int
}

func (f *fakeActivityReader) ListActivity(_ context.Context, query core.ActivityQuery) ([]core.PlaybackWatch, error) {
	f.activityCalls++
	f.activityQuery = query
	return f.watches, nil
}

func (f *fakeActivityReader) ListTimelineWatches(_ context.Context, query core.TimelineWatchQuery) ([]core.PlaybackWatch, error) {
	f.timelineCalls++
	f.timelineQuery = query
	return f.watches, nil
}

type fakeExclusionManager struct {
	value        core.MediaServerExclusions
	readCalls    int
	replaceCalls int
}

func (f *fakeExclusionManager) Read(_ context.Context, serverID string) (core.MediaServerExclusions, error) {
	f.readCalls++
	f.value.MediaServerID = serverID
	return f.value, nil
}

func (f *fakeExclusionManager) Replace(_ context.Context, value core.MediaServerExclusions) (core.MediaServerExclusions, error) {
	f.replaceCalls++
	f.value = value
	return value, nil
}

func TestActivityRoutesUseRequiredPermissions(t *testing.T) {
	serverID := "00000000-0000-4000-8000-000000000001"
	tests := []struct {
		name, method, path string
		permission         core.Permission
	}{
		{name: "activity", method: http.MethodGet, path: "/api/v1/activity", permission: core.PermissionStatsReadAll},
		{name: "timeline", method: http.MethodGet, path: "/api/v1/media-servers/" + serverID + "/users/user/timeline", permission: core.PermissionStatsReadAll},
		{name: "get exclusions", method: http.MethodGet, path: "/api/v1/media-servers/" + serverID + "/exclusions", permission: core.PermissionAdminSettings},
		{name: "put exclusions", method: http.MethodPut, path: "/api/v1/media-servers/" + serverID + "/exclusions", permission: core.PermissionAdminSettings},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			h := newAuthHarness(t, nil)
			cookie := sessionCookie(t, h.login(t, "alice", "secret-password"))
			h.authorization.mu.Lock()
			h.authorization.permissions["11111111-1111-4111-8111-111111111111"] = []core.Permission{core.PermissionRequestsCreate}
			h.authorization.mu.Unlock()
			recorder := h.request(t, testCase.method, testCase.path, `{}`, cookie)
			if recorder.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want 403: %s", recorder.Code, recorder.Body.String())
			}
			event := h.audit.last(t)
			if event.Permission != string(testCase.permission) || event.Result != "denied" {
				t.Fatalf("audit = %+v", event)
			}
		})
	}
}

func TestActivityQueryBounds(t *testing.T) {
	oversized := strings.Repeat("x", core.MaxExclusionIDBytes+1)
	tests := []struct{ name, query, field string }{
		{name: "limit low", query: "limit=0", field: "limit"},
		{name: "limit high", query: "limit=101", field: "limit"},
		{name: "duplicate limit", query: "limit=1&limit=2", field: "limit"},
		{name: "server", query: "media_server_id=invalid", field: "media_server_id"},
		{name: "user", query: "media_user_id=" + oversized, field: "media_user_id"},
		{name: "library", query: "library_id=" + oversized, field: "library_id"},
		{name: "item type", query: "item_type=" + oversized, field: "item_type"},
		{name: "client", query: "client=" + oversized, field: "client"},
		{name: "device", query: "device_id=" + oversized, field: "device_id"},
		{name: "query", query: "q=" + strings.Repeat("q", core.MaxActivitySearchBytes+1), field: "q"},
		{name: "play method", query: "play_method=copy", field: "play_method"},
		{name: "source", query: "source=manual", field: "source"},
		{name: "import source", query: "import_source=other", field: "import_source"},
		{name: "after", query: "started_after=yesterday", field: "started_after"},
		{name: "before", query: "started_before=tomorrow", field: "started_before"},
		{name: "window", query: "started_after=2026-01-02T00%3A00%3A00Z&started_before=2026-01-01T00%3A00%3A00Z", field: "started_before"},
		{name: "cursor", query: "cursor=invalid", field: "cursor"},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/api/v1/activity?"+testCase.query, nil)
			_, _, fields := parseActivityQuery(request)
			if !hasActivityField(fields, testCase.field) {
				t.Fatalf("fields = %+v, want %q", fields, testCase.field)
			}
		})
	}
}

func TestTimelineQueryBounds(t *testing.T) {
	tests := []struct{ name, query, user, field string }{
		{name: "limit low", query: "limit=0", user: "user", field: "limit"},
		{name: "limit high", query: "limit=101", user: "user", field: "limit"},
		{name: "gap low", query: "gap_seconds=0", user: "user", field: "gap_seconds"},
		{name: "gap high", query: "gap_seconds=604801", user: "user", field: "gap_seconds"},
		{name: "user empty", user: "", field: "media_user_id"},
		{name: "user high", user: strings.Repeat("u", core.MaxExclusionIDBytes+1), field: "media_user_id"},
		{name: "cursor", query: "cursor=invalid", user: "user", field: "cursor"},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/?"+testCase.query, nil)
			_, _, _, fields := parseTimelineQuery(request, testCase.user)
			if !hasActivityField(fields, testCase.field) {
				t.Fatalf("fields = %+v, want %q", fields, testCase.field)
			}
		})
	}
}

func TestActivityAndTimelineResponses(t *testing.T) {
	h := newAuthHarness(t, nil)
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	h.activity.watches = []core.PlaybackWatch{{
		ID: "00000000-0000-4000-8000-000000000001", MediaServerID: "00000000-0000-4000-8000-000000000002",
		MediaUserID: "user", ItemID: "item", ItemName: "Title", ItemType: "Movie",
		StartedAt: now, LastSeenAt: now, Source: core.WatchSourcePoll,
	}}
	cookie := sessionCookie(t, h.login(t, "alice", "secret-password"))
	activity := h.request(t, http.MethodGet, "/api/v1/activity?q=Title&limit=1", "", cookie)
	if activity.Code != http.StatusOK || h.activity.activityCalls != 1 || h.activity.activityQuery.Limit != 2 {
		t.Fatalf("activity = %d calls %d query %+v: %s", activity.Code, h.activity.activityCalls, h.activity.activityQuery, activity.Body.String())
	}
	serverID := "00000000-0000-4000-8000-000000000002"
	timeline := h.request(t, http.MethodGet, "/api/v1/media-servers/"+serverID+"/users/user/timeline?gap_seconds=3600", "", cookie)
	if timeline.Code != http.StatusOK || h.activity.timelineCalls != 1 || h.activity.timelineQuery.Limit != core.MaxTimelineWatchFetch {
		t.Fatalf("timeline = %d calls %d query %+v: %s", timeline.Code, h.activity.timelineCalls, h.activity.timelineQuery, timeline.Body.String())
	}
}

func TestReplaceExclusionsBoundsAndAudit(t *testing.T) {
	h := newAuthHarness(t, nil)
	cookie := sessionCookie(t, h.login(t, "alice", "secret-password"))
	serverID := "00000000-0000-4000-8000-000000000001"
	path := "/api/v1/media-servers/" + serverID + "/exclusions"
	tests := []struct{ name, body, field string }{
		{name: "missing arrays", body: `{}`, field: "body"},
		{name: "oversized id", body: `{"excluded_media_user_ids":["` + strings.Repeat("u", 129) + `"],"excluded_library_ids":[]}`, field: "excluded_media_user_ids.0"},
		{name: "duplicate", body: `{"excluded_media_user_ids":["u","u"],"excluded_library_ids":[]}`, field: "excluded_media_user_ids.1"},
		{name: "total", body: exclusionRequestBody(501), field: "body"},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			recorder := h.requestWithContentType(t, http.MethodPut, path, testCase.body, cookie, "application/json")
			if recorder.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d: %s", recorder.Code, recorder.Body.String())
			}
			if !strings.Contains(recorder.Body.String(), `"field":"`+testCase.field+`"`) {
				t.Fatalf("body = %s, want field %s", recorder.Body.String(), testCase.field)
			}
		})
	}
	body := `{"excluded_media_user_ids":["user"],"excluded_library_ids":["library"]}`
	recorder := h.requestWithContentType(t, http.MethodPut, path, body, cookie, "application/json")
	if recorder.Code != http.StatusOK || h.exclusions.replaceCalls != 1 {
		t.Fatalf("replace = %d calls %d: %s", recorder.Code, h.exclusions.replaceCalls, recorder.Body.String())
	}
	event := h.audit.last(t)
	if event.Action != "media_server.exclusions.replace" || event.Result != "success" {
		t.Fatalf("audit = %+v", event)
	}
}

func TestReplaceExclusionsRejectsUnsupportedContentType(t *testing.T) {
	h := newAuthHarness(t, nil)
	cookie := sessionCookie(t, h.login(t, "alice", "secret-password"))
	path := "/api/v1/media-servers/00000000-0000-4000-8000-000000000001/exclusions"
	body := `{"excluded_media_user_ids":[],"excluded_library_ids":[]}`
	recorder := h.requestWithContentType(t, http.MethodPut, path, body, cookie, "text/plain")
	if recorder.Code != http.StatusUnsupportedMediaType || h.exclusions.replaceCalls != 0 {
		t.Fatalf("response = %d, replace calls %d: %s", recorder.Code, h.exclusions.replaceCalls, recorder.Body.String())
	}
}

func TestReplaceExclusionsStopsAtCombinedLimit(t *testing.T) {
	h := newAuthHarness(t, nil)
	cookie := sessionCookie(t, h.login(t, "alice", "secret-password"))
	values := make([]string, core.MaxMediaServerExclusions+1)
	for index := range values {
		values[index] = ""
	}
	libraries := []string{}
	data, err := json.Marshal(exclusionsRequest{
		ExcludedMediaUserIDs: &values, ExcludedLibraryIDs: &libraries,
	})
	if err != nil {
		t.Fatalf("marshal exclusions: %v", err)
	}
	path := "/api/v1/media-servers/00000000-0000-4000-8000-000000000001/exclusions"
	recorder := h.requestWithContentType(t, http.MethodPut, path, string(data), cookie, "application/json")
	var response httputil.ErrorResponse
	if err = json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if recorder.Code != http.StatusUnprocessableEntity || len(response.Fields) != 1 ||
		response.Fields[0].Field != "body" || h.exclusions.replaceCalls != 0 {
		t.Fatalf("response = %d %+v, replace calls %d", recorder.Code, response, h.exclusions.replaceCalls)
	}
}

func hasActivityField(fields []httputil.FieldError, name string) bool {
	for _, field := range fields {
		if field.Field == name {
			return true
		}
	}
	return false
}

func exclusionRequestBody(count int) string {
	values := make([]string, count)
	for index := range values {
		values[index] = strings.Repeat("x", index/10+1) + string(rune('0'+index%10))
	}
	libraries := []string{}
	data, err := json.Marshal(exclusionsRequest{ExcludedMediaUserIDs: &values, ExcludedLibraryIDs: &libraries})
	if err != nil {
		panic(err)
	}
	return string(data)
}
