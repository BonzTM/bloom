package http

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/BonzTM/bloom/internal/core"
)

const playbackTestServerID = "33333333-3333-4333-8333-333333333333"

type fakePlaybackReader struct {
	mu              sync.Mutex
	watches         []core.PlaybackWatch
	positions       []core.PlaybackPosition
	err             error
	queries         []core.PlaybackQuery
	positionWatchID string
}

func (f *fakePlaybackReader) ListWatchPositions(
	_ context.Context, watchID string,
) ([]core.PlaybackPosition, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.positionWatchID = watchID
	if f.err != nil {
		return nil, f.err
	}
	return append([]core.PlaybackPosition(nil), f.positions...), nil
}

func (f *fakePlaybackReader) ListWatches(
	_ context.Context,
	query core.PlaybackQuery,
) ([]core.PlaybackWatch, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.queries = append(f.queries, query)
	if f.err != nil {
		return nil, f.err
	}
	watches := append([]core.PlaybackWatch(nil), f.watches...)
	if query.Mode != core.PlaybackQueryNow {
		return watches, nil
	}
	page := make([]core.PlaybackWatch, 0, query.PageSize)
	for _, watch := range watches {
		if !playbackWatchBeforeCursor(watch, query) {
			continue
		}
		page = append(page, watch)
		if len(page) == query.PageSize {
			break
		}
	}
	return page, nil
}

func playbackWatchBeforeCursor(watch core.PlaybackWatch, query core.PlaybackQuery) bool {
	if query.BeforeStartedAt.IsZero() {
		return true
	}
	return watch.StartedAt.Before(query.BeforeStartedAt) ||
		(watch.StartedAt.Equal(query.BeforeStartedAt) && watch.ID < query.BeforeID)
}

func TestPlaybackNowReturnsCurrentActiveTime(t *testing.T) {
	h := newAuthHarness(t, nil)
	now := h.clock.Now()
	h.playback.watches = []core.PlaybackWatch{playbackHTTPWatch(now, core.WatchPlaying)}
	cookie := sessionCookie(t, h.login(t, "alice", "secret-password"))
	auditCount := len(h.audit.events)
	recorder := h.request(t, http.MethodGet, "/api/v1/playback/now", "", cookie)
	if recorder.Code != http.StatusOK {
		t.Fatalf("GET playback now = %d: %s", recorder.Code, recorder.Body.String())
	}
	var response playbackNowResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode playback now: %v", err)
	}
	if len(response.Items) != 1 || response.Items[0].ActiveSeconds != 15 ||
		response.Items[0].MediaServerName != "Home" || response.Items[0].Source != core.WatchSourcePoll ||
		response.Items[0].Stream == nil || response.Items[0].Stream.VideoCodec != "h264" {
		t.Fatalf("playback now response = %+v", response)
	}
	if len(h.audit.events) != auditCount {
		t.Fatalf("successful playback read emitted an audit event")
	}
}

func TestPlaybackNowPaginatesMoreThanSnapshotLimitAcrossServers(t *testing.T) {
	h := newAuthHarness(t, nil)
	now := h.clock.Now()
	h.playback.watches = playbackNowWatches(now, core.MaxPlaybackSessions+2)
	cookie := sessionCookie(t, h.login(t, "alice", "secret-password"))
	cursor := ""
	seen := make(map[string]bool, len(h.playback.watches))
	for range 12 {
		path := "/api/v1/playback/now?page_size=100"
		if cursor != "" {
			path += "&cursor=" + cursor
		}
		recorder := h.request(t, http.MethodGet, path, "", cookie)
		if recorder.Code != http.StatusOK {
			t.Fatalf("GET playback now = %d: %s", recorder.Code, recorder.Body.String())
		}
		var response struct {
			Items      []playbackWatchResponse `json:"items"`
			NextCursor string                  `json:"next_cursor"`
		}
		if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
			t.Fatalf("decode playback now: %v", err)
		}
		for _, item := range response.Items {
			if seen[item.ID] {
				t.Fatalf("duplicate watch %s across pages", item.ID)
			}
			seen[item.ID] = true
		}
		cursor = response.NextCursor
		if cursor == "" {
			break
		}
	}
	if len(seen) != len(h.playback.watches) || cursor != "" {
		t.Fatalf("paginated watches = %d, cursor = %q; want %d and final empty cursor",
			len(seen), cursor, len(h.playback.watches))
	}
}

func playbackNowWatches(now time.Time, count int) []core.PlaybackWatch {
	const secondServerID = "44444444-4444-4444-8444-444444444444"
	watches := make([]core.PlaybackWatch, 0, count)
	for index := range count {
		watch := playbackHTTPWatch(now, core.WatchPlaying)
		watch.ID = fmt.Sprintf("00000000-0000-4000-8000-%012d", count-index)
		watch.StartedAt = now.Add(-time.Duration(index) * time.Microsecond)
		if index%2 == 1 {
			watch.MediaServerID = secondServerID
			watch.MediaServerName = "Away"
		}
		watches = append(watches, watch)
	}
	return watches
}

func TestPlaybackHistoryPaginatesAndFilters(t *testing.T) {
	h := newAuthHarness(t, nil)
	now := h.clock.Now()
	first := playbackHTTPWatch(now, core.WatchStopped)
	second := first
	second.ID = "44444444-4444-4444-8444-444444444444"
	second.StartedAt = first.StartedAt.Add(-time.Minute)
	h.playback.watches = []core.PlaybackWatch{first, second}
	cookie := sessionCookie(t, h.login(t, "alice", "secret-password"))
	path := "/api/v1/playback/history?page_size=1&media_server_id=" + playbackTestServerID
	recorder := h.request(t, http.MethodGet, path, "", cookie)
	if recorder.Code != http.StatusOK {
		t.Fatalf("GET playback history = %d: %s", recorder.Code, recorder.Body.String())
	}
	var response playbackHistoryResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode playback history: %v", err)
	}
	if len(response.Items) != 1 || response.NextCursor == "" {
		t.Fatalf("playback history response = %+v", response)
	}
	if response.Items[0].Source != core.WatchSourcePoll || response.Items[0].Stream == nil {
		t.Fatalf("playback history source = %q", response.Items[0].Source)
	}
	query := h.playback.queries[len(h.playback.queries)-1]
	if query.PageSize != 2 || query.MediaServerID != playbackTestServerID {
		t.Fatalf("playback history query = %+v", query)
	}
	secondPage := h.request(t, http.MethodGet, "/api/v1/playback/history?page_size=1&cursor="+response.NextCursor, "", cookie)
	if secondPage.Code != http.StatusOK {
		t.Fatalf("GET playback history page 2 = %d: %s", secondPage.Code, secondPage.Body.String())
	}
	query = h.playback.queries[len(h.playback.queries)-1]
	if query.BeforeID != first.ID || !query.BeforeStartedAt.Equal(first.StartedAt) {
		t.Fatalf("playback cursor query = %+v", query)
	}
}

func TestPlaybackPositionsReturnsNewestBoundedSeries(t *testing.T) {
	h := newAuthHarness(t, nil)
	now := h.clock.Now()
	h.playback.positions = []core.PlaybackPosition{{
		WatchID: "22222222-2222-4222-8222-222222222222", ObservedAt: now,
		Position: time.Minute, Paused: false, PlayMethod: core.PlayMethodTranscode,
		Stream: playbackHTTPStream(), Source: core.WatchSourcePoll,
	}}
	cookie := sessionCookie(t, h.login(t, "alice", "secret-password"))
	path := "/api/v1/playback/watches/22222222-2222-4222-8222-222222222222/positions"
	recorder := h.request(t, http.MethodGet, path, "", cookie)
	if recorder.Code != http.StatusOK {
		t.Fatalf("GET playback positions = %d: %s", recorder.Code, recorder.Body.String())
	}
	var response playbackPositionsResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if h.playback.positionWatchID == "" || len(response.Items) != 1 ||
		response.Items[0].Stream == nil || response.Items[0].Stream.VideoCodec != "h264" {
		t.Fatalf("positions response = %+v, watch id = %q", response, h.playback.positionWatchID)
	}
}

func TestPlaybackPositionsRejectsBadIDAndReturnsNotFound(t *testing.T) {
	h := newAuthHarness(t, nil)
	cookie := sessionCookie(t, h.login(t, "alice", "secret-password"))
	bad := h.request(t, http.MethodGet, "/api/v1/playback/watches/bad/positions", "", cookie)
	if bad.Code != http.StatusUnprocessableEntity || h.playback.positionWatchID != "" {
		t.Fatalf("bad id = %d %s", bad.Code, bad.Body.String())
	}
	h.playback.err = core.ErrNotFound
	missing := h.request(t, http.MethodGet,
		"/api/v1/playback/watches/22222222-2222-4222-8222-222222222222/positions", "", cookie)
	if missing.Code != http.StatusNotFound {
		t.Fatalf("missing watch = %d %s", missing.Code, missing.Body.String())
	}
}

func TestPlaybackRoutesRequireStatsPermission(t *testing.T) {
	document := loadOpenAPI(t)
	for _, path := range []string{
		"/api/v1/playback/now", "/api/v1/playback/history",
		"/api/v1/playback/watches/22222222-2222-4222-8222-222222222222/positions",
	} {
		t.Run(path+" missing session", func(t *testing.T) {
			h := newAuthHarness(t, nil)
			recorder := h.request(t, http.MethodGet, path, "", nil)
			assertPlaybackContractResponse(t, document, recorder, http.StatusUnauthorized, errorSchema)
		})
		t.Run(path+" missing permission", func(t *testing.T) {
			h := newAuthHarness(t, nil)
			cookie := sessionCookie(t, h.login(t, "alice", "secret-password"))
			h.authorization.mu.Lock()
			h.authorization.permissions[h.store.accounts["alice"].ID] = nil
			h.authorization.mu.Unlock()
			recorder := h.request(t, http.MethodGet, path, "", cookie)
			assertPlaybackContractResponse(t, document, recorder, http.StatusForbidden, errorSchema)
			event := h.audit.last(t)
			if event.Resource != auditResourcePlayback || event.Permission != string(core.PermissionStatsReadAll) {
				t.Fatalf("authorization audit = %+v", event)
			}
		})
	}
}

func TestPlaybackOpenAPIContract(t *testing.T) {
	document := loadOpenAPI(t)
	tests := []struct {
		path, schema string
		statuses     []int
	}{
		{
			path: "/api/v1/playback/now", schema: playbackNowSchema,
			statuses: []int{200, 401, 403, 422, 500, 405},
		},
		{
			path: "/api/v1/playback/history", schema: playbackHistorySchema,
			statuses: []int{200, 401, 403, 422, 500, 405},
		},
		{
			path:     "/api/v1/playback/watches/22222222-2222-4222-8222-222222222222/positions",
			schema:   playbackPositionsSchema,
			statuses: []int{200, 401, 403, 404, 422, 500, 405},
		},
	}
	for _, testCase := range tests {
		operationPath := testCase.path
		if strings.Contains(operationPath, "/positions") {
			operationPath = "/api/v1/playback/watches/{id}/positions"
		}
		observed := make(map[int]authContractCase)
		for _, status := range testCase.statuses {
			h := newAuthHarness(t, nil)
			recorder := playbackContractRequest(t, h, testCase.path, status)
			schema := errorSchema
			if status == http.StatusOK {
				schema = testCase.schema
			}
			headers := []string{"Cache-Control", "Set-Cookie", "Vary", "X-Request-ID"}
			if status == http.StatusMethodNotAllowed {
				headers = []string{"Allow", "Cache-Control", "X-Request-ID"}
			}
			expectation := authContractCase{
				path: operationPath, method: "get", status: status, schema: schema, headers: headers,
			}
			assertHandlerContract(t, document, recorder, expectation)
			observed[status] = expectation
		}
		assertOperationContract(t, document, "get "+operationPath, observed)
	}
}

func TestPlaybackHistoryOpenAPIMatchesPageSizeLimit(t *testing.T) {
	operation := loadOpenAPI(t).validator.Paths.Find("/api/v1/playback/history").Get
	for _, parameter := range operation.Parameters {
		if parameter.Value.Name == "page_size" {
			maximum := parameter.Value.Schema.Value.Max
			if maximum == nil || *maximum != maxPlaybackHistoryPageSize {
				t.Fatalf("page_size maximum = %v, want %d", maximum, maxPlaybackHistoryPageSize)
			}
			return
		}
	}
	t.Fatal("page_size parameter not found")
}

func TestPlaybackNowOpenAPIMatchesPaginationLimit(t *testing.T) {
	operation := loadOpenAPI(t).validator.Paths.Find("/api/v1/playback/now").Get
	for _, parameter := range operation.Parameters {
		if parameter.Value.Name == "page_size" {
			maximum := parameter.Value.Schema.Value.Max
			if maximum == nil || *maximum != maxPlaybackHistoryPageSize {
				t.Fatalf("page_size maximum = %v, want %d", maximum, maxPlaybackHistoryPageSize)
			}
			return
		}
	}
	t.Fatal("page_size parameter not found")
}

func TestPlaybackPositionsOpenAPIMatchesRetentionLimit(t *testing.T) {
	schema := loadOpenAPI(t).validator.Components.Schemas["PlaybackPositionsResponse"].Value
	items := schema.Properties["items"].Value
	if items.MaxItems == nil || *items.MaxItems != core.MaxWatchPositions {
		t.Fatalf("positions maxItems = %v, want %d", items.MaxItems, core.MaxWatchPositions)
	}
}

func playbackContractRequest(
	t *testing.T,
	h authHarness,
	path string,
	status int,
) *httptest.ResponseRecorder {
	t.Helper()
	if status == http.StatusUnauthorized {
		return h.request(t, http.MethodGet, path, "", &http.Cookie{Name: sessionCookieName, Value: "invalid"})
	}
	if status == http.StatusMethodNotAllowed {
		return h.request(t, http.MethodPatch, path, "", nil)
	}
	cookie := sessionCookie(t, h.login(t, "alice", "secret-password"))
	switch status {
	case http.StatusOK:
		if strings.Contains(path, "/positions") {
			h.playback.positions = []core.PlaybackPosition{{
				WatchID: "22222222-2222-4222-8222-222222222222", ObservedAt: h.clock.Now(),
				Position: time.Minute, PlayMethod: core.PlayMethodTranscode,
				Stream: playbackHTTPStream(), Source: core.WatchSourcePoll,
			}}
			break
		}
		state := core.WatchPlaying
		if path == "/api/v1/playback/history" {
			state = core.WatchStopped
		}
		h.playback.watches = []core.PlaybackWatch{playbackHTTPWatch(h.clock.Now(), state)}
	case http.StatusForbidden:
		h.authorization.mu.Lock()
		h.authorization.permissions[h.store.accounts["alice"].ID] = nil
		h.authorization.mu.Unlock()
	case http.StatusUnprocessableEntity:
		if strings.Contains(path, "/positions") {
			path = "/api/v1/playback/watches/bad/positions"
		} else {
			path += "?cursor=***"
		}
	case http.StatusNotFound:
		h.playback.err = core.ErrNotFound
	case http.StatusInternalServerError:
		h.playback.err = errors.New("store failed")
	}
	return h.request(t, http.MethodGet, path, "", cookie)
}

func TestPlaybackHistoryRejectsInvalidQuery(t *testing.T) {
	h := newAuthHarness(t, nil)
	cookie := sessionCookie(t, h.login(t, "alice", "secret-password"))
	for _, query := range []string{
		"cursor=***", "page_size=0", "page_size=101", "media_server_id=invalid", "cursor=%zz",
	} {
		recorder := h.request(t, http.MethodGet, "/api/v1/playback/history?"+query, "", cookie)
		if recorder.Code != http.StatusUnprocessableEntity {
			t.Errorf("query %q = %d, want 422: %s", query, recorder.Code, recorder.Body.String())
		}
	}
}

func TestPlaybackGETDoesNotUseCSRFAuditResourceTable(t *testing.T) {
	h := newAuthHarness(t, nil)
	request := httptest.NewRequest(http.MethodGet, "https://bloom.test/api/v1/playback/now", nil)
	request.Header.Set("Origin", "https://evil.example")
	recorder := httptest.NewRecorder()
	h.h.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("cross-origin GET = %d, want authentication response", recorder.Code)
	}
	if got := csrfAuditResource("/api/v1/playback/now"); got != auditResourceRouteUnmatched {
		t.Fatalf("playback GET CSRF resource = %q, want unmatched", got)
	}
}

func TestPlaybackStoreFailureIsOpaque(t *testing.T) {
	h := newAuthHarness(t, nil)
	h.playback.err = errors.New("database contains private detail")
	cookie := sessionCookie(t, h.login(t, "alice", "secret-password"))
	recorder := h.request(t, http.MethodGet, "/api/v1/playback/now", "", cookie)
	if recorder.Code != http.StatusInternalServerError || strings.Contains(recorder.Body.String(), "private detail") {
		t.Fatalf("store failure = %d %s", recorder.Code, recorder.Body.String())
	}
}

func assertPlaybackContractResponse(
	t *testing.T,
	document openAPIDocument,
	recorder *httptest.ResponseRecorder,
	status int,
	schema string,
) {
	t.Helper()
	if recorder.Code != status {
		t.Fatalf("playback response = %d, want %d: %s", recorder.Code, status, recorder.Body.String())
	}
	assertJSONMatchesSchema(t, document, recorder.Body.Bytes(), schema)
}

func playbackHTTPWatch(now time.Time, state core.WatchState) core.PlaybackWatch {
	endedAt := now.Add(-time.Second)
	watch := core.PlaybackWatch{
		ID: "22222222-2222-4222-8222-222222222222", MediaServerID: playbackTestServerID,
		MediaServerName: "Home", MediaUserID: "user-1", Username: "alice",
		DeviceID: "device-1", DeviceName: "TV", Client: "Jellyfin Web",
		ItemID: "item-1", ItemName: "Pilot", ItemType: "Episode", SeriesName: "Series",
		PlayMethod: core.PlayMethodDirectPlay, State: state, StartedAt: now.Add(-time.Minute),
		Stream:     playbackHTTPStream(),
		LastSeenAt: now.Add(-5 * time.Second), ActiveTime: 10 * time.Second,
		LastPosition: time.Minute, Source: core.WatchSourcePoll,
	}
	if state == core.WatchStopped {
		watch.EndedAt = &endedAt
	}
	return watch
}

func playbackHTTPStream() *core.StreamDetails {
	videoDirect, audioDirect := false, true
	return &core.StreamDetails{
		Container: "ts", VideoCodec: "h264", AudioCodec: "aac", Bitrate: 8_000_000,
		Width: 1920, Height: 1080, Framerate: 23.98, AudioChannels: 6,
		IsVideoDirect: &videoDirect, IsAudioDirect: &audioDirect,
		TranscodeReasons: []string{"VideoCodecNotSupported"},
	}
}
