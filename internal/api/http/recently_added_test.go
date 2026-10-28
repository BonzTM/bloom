package http

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/BonzTM/bloom/internal/core"
)

type recentlyAddedStub struct {
	titles []core.RecentlyAddedTitle
	err    error
	limit  int
}

func (s *recentlyAddedStub) List(_ context.Context, limit int) ([]core.RecentlyAddedTitle, error) {
	s.limit = limit
	return s.titles, s.err
}

func TestRecentlyAddedReturnsExactWireShape(t *testing.T) {
	added := time.Date(2026, 10, 20, 8, 30, 0, 0, time.FixedZone("EDT", -4*60*60))
	stub := &recentlyAddedStub{titles: []core.RecentlyAddedTitle{{
		MediaServerID: "11111111-1111-4111-8111-111111111111", ItemID: "abc", Kind: core.MediaKindSeries,
		Title: "Show", Year: 2025, AddedAt: added,
	}}}
	server := &Server{logger: slog.New(slog.DiscardHandler), recentlyAdded: stub}
	request := requestWithAccount(t, http.MethodGet, "/api/v1/library/recently-added?limit=12", "", core.PermissionRequestsCreate)
	recorder := httptest.NewRecorder()
	server.handleRecentlyAdded(recorder, request)
	want := `{"items":[{"media_server_id":"11111111-1111-4111-8111-111111111111","item_id":"abc","kind":"series",` +
		`"title":"Show","year":2025,"added_at":"2026-10-20T12:30:00Z"}]}` + "\n"
	if recorder.Code != http.StatusOK || recorder.Body.String() != want {
		t.Fatalf("response = %d %s, want %s", recorder.Code, recorder.Body.String(), want)
	}
	assertJSONMatchesSchema(t, loadOpenAPI(t), recorder.Body.Bytes(), "#/components/schemas/RecentlyAddedResponse")
	if stub.limit != 12 {
		t.Fatalf("limit = %d, want 12", stub.limit)
	}
}

func TestRecentlyAddedDefaultsLimitAndReturnsEmptyList(t *testing.T) {
	stub := &recentlyAddedStub{}
	server := &Server{logger: slog.New(slog.DiscardHandler), recentlyAdded: stub}
	request := requestWithAccount(t, http.MethodGet, "/api/v1/library/recently-added", "", core.PermissionRequestsReadOwn)
	recorder := httptest.NewRecorder()
	server.handleRecentlyAdded(recorder, request)
	if recorder.Code != http.StatusOK || recorder.Body.String() != `{"items":[]}`+"\n" || stub.limit != core.DefaultRecentlyAddedLimit {
		t.Fatalf("response = %d %s limit %d", recorder.Code, recorder.Body.String(), stub.limit)
	}
}

func TestRecentlyAddedRejectsInvalidLimit(t *testing.T) {
	for _, query := range []string{"limit=0", "limit=41", "limit=01", "limit=x", "limit=1&limit=2", "limit=", "limit=-1"} {
		stub := &recentlyAddedStub{}
		server := &Server{logger: slog.New(slog.DiscardHandler), recentlyAdded: stub}
		request := requestWithAccount(t, http.MethodGet, "/api/v1/library/recently-added?"+query, "", core.PermissionRequestsCreate)
		recorder := httptest.NewRecorder()
		server.handleRecentlyAdded(recorder, request)
		if recorder.Code != http.StatusUnprocessableEntity || !strings.Contains(recorder.Body.String(), `"field":"limit"`) || stub.limit != 0 {
			t.Fatalf("%s response = %d %s", query, recorder.Code, recorder.Body.String())
		}
	}
}

func TestRecentlyAddedHidesStoreFailure(t *testing.T) {
	server := &Server{
		logger: slog.New(slog.DiscardHandler), recentlyAdded: &recentlyAddedStub{err: errors.New("database password=secret")},
	}
	request := requestWithAccount(t, http.MethodGet, "/api/v1/library/recently-added", "", core.PermissionRequestsCreate)
	recorder := httptest.NewRecorder()
	server.handleRecentlyAdded(recorder, request)
	if recorder.Code != http.StatusInternalServerError || strings.Contains(recorder.Body.String(), "secret") {
		t.Fatalf("response = %d %s", recorder.Code, recorder.Body.String())
	}
	assertJSONMatchesSchema(t, loadOpenAPI(t), recorder.Body.Bytes(), "#/components/schemas/ErrorResponse")
}
