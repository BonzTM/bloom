package http

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/testutil"
)

const (
	catalogTestServerID  = "94000000-0000-4000-8000-000000000001"
	catalogTestLibraryID = "library-1"
	catalogTestItemID    = "item-1"
)

type fakeCatalogReader struct {
	now        time.Time
	requestErr error
}

func (f fakeCatalogReader) RequestSync(context.Context, string) (core.LibrarySync, error) {
	return core.LibrarySync{MediaServerID: catalogTestServerID, State: core.LibrarySyncPending}, f.requestErr
}

func (f fakeCatalogReader) ListLibraries(context.Context, string, core.CatalogWindow) ([]core.CatalogLibrarySummary, error) {
	return []core.CatalogLibrarySummary{{LibraryID: catalogTestLibraryID, Types: []core.CatalogTypeCount{{
		ItemType: "Movie", Items: 1, Plays: 2, WatchSeconds: 30,
	}}}}, nil
}

func (f fakeCatalogReader) ListItems(context.Context, core.CatalogItemQuery) ([]core.CatalogItemStats, error) {
	return []core.CatalogItemStats{f.item()}, nil
}

func (f fakeCatalogReader) Item(context.Context, string, string) (core.CatalogItemDetail, error) {
	return core.CatalogItemDetail{Item: f.item(), Children: []core.CatalogChildSummary{{
		ItemType: "Episode", Items: 3,
	}}}, nil
}

func (fakeCatalogReader) History(context.Context, core.CatalogHistoryQuery) ([]core.PlaybackWatch, error) {
	return []core.PlaybackWatch{}, nil
}

func (f fakeCatalogReader) Recent(context.Context, string, string, int) ([]core.CatalogItemStats, error) {
	return []core.CatalogItemStats{f.item()}, nil
}

func (fakeCatalogReader) Genres(context.Context, string, string, core.CatalogWindow) ([]core.CatalogGenreSummary, error) {
	return []core.CatalogGenreSummary{{Genre: "Drama", Items: 1, Plays: 2, WatchSeconds: 30}}, nil
}

func (f fakeCatalogReader) Stale(context.Context, core.CatalogStaleQuery) ([]core.CatalogItemStats, error) {
	return []core.CatalogItemStats{f.item()}, nil
}

func (f fakeCatalogReader) item() core.CatalogItemStats {
	return core.CatalogItemStats{Item: core.LibraryItem{
		MediaServerID: catalogTestServerID, ItemID: catalogTestItemID, LibraryID: catalogTestLibraryID,
		ItemType: "Movie", Name: "Film", Genres: []string{"Drama"},
		FirstSeenAt: f.now, LastSeenAt: f.now, UpdatedAt: f.now,
	}, Plays: 2, WatchSeconds: 30, UniqueUsers: 1}
}

func TestCatalogHandlersMatchOpenAPIContracts(t *testing.T) {
	now := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	server := &Server{
		catalog: fakeCatalogReader{now: now}, clock: testutil.NewFakeClock(now),
		logger: slog.New(slog.DiscardHandler),
	}
	document := loadOpenAPI(t)
	tests := []struct {
		name, method, target, schema string
		handler                      func(http.ResponseWriter, *http.Request)
	}{
		{"sync", http.MethodPost, "/api/v1/media-servers/x/catalog/sync", "#/components/schemas/CatalogSyncResponse", server.handleCatalogSync},
		{"libraries", http.MethodGet, "/api/v1/media-servers/x/libraries/catalog?days=30", "#/components/schemas/CatalogLibrariesResponse", server.handleCatalogLibraries},
		{"items", http.MethodGet, "/api/v1/media-servers/x/libraries/y/items?limit=10&sort=plays&order=desc", "#/components/schemas/CatalogItemsResponse", server.handleCatalogItems},
		{"item", http.MethodGet, "/api/v1/media-servers/x/items/z", "#/components/schemas/CatalogItemDetailResponse", server.handleCatalogItem},
		{"history", http.MethodGet, "/api/v1/media-servers/x/items/z/history", "#/components/schemas/CatalogHistoryResponse", server.handleCatalogHistory},
		{"recent", http.MethodGet, "/api/v1/media-servers/x/libraries/y/recent", "#/components/schemas/CatalogItemsResponse", server.handleCatalogRecent},
		{"genres", http.MethodGet, "/api/v1/media-servers/x/libraries/y/genres", "#/components/schemas/CatalogGenresResponse", server.handleCatalogGenres},
		{"stale", http.MethodGet, "/api/v1/media-servers/x/libraries/y/stale?days=45", "#/components/schemas/CatalogItemsResponse", server.handleCatalogStale},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(test.method, test.target, nil)
			request.SetPathValue("id", catalogTestServerID)
			request.SetPathValue("library_id", catalogTestLibraryID)
			request.SetPathValue("item_id", catalogTestItemID)
			recorder := httptest.NewRecorder()
			test.handler(recorder, request)
			wantStatus := http.StatusOK
			if test.name == "sync" {
				wantStatus = http.StatusAccepted
			}
			if recorder.Code != wantStatus {
				t.Fatalf("status = %d: %s", recorder.Code, recorder.Body.String())
			}
			assertJSONMatchesSchema(t, document, recorder.Body.Bytes(), test.schema)
		})
	}
}

func TestCatalogOpenAPIDocumentsEveryRoute(t *testing.T) {
	document := loadOpenAPI(t)
	paths := []string{
		"/api/v1/media-servers/{id}/catalog/sync",
		"/api/v1/media-servers/{id}/libraries/catalog",
		"/api/v1/media-servers/{id}/libraries/{library_id}/items",
		"/api/v1/media-servers/{id}/items/{item_id}",
		"/api/v1/media-servers/{id}/items/{item_id}/history",
		"/api/v1/media-servers/{id}/libraries/{library_id}/recent",
		"/api/v1/media-servers/{id}/libraries/{library_id}/genres",
		"/api/v1/media-servers/{id}/libraries/{library_id}/stale",
	}
	for _, path := range paths {
		if document.validator.Paths.Find(path) == nil {
			t.Errorf("OpenAPI path %q is missing", path)
		}
	}
}

func TestCatalogHandlersRejectDuplicateBoundsAndOverlappingSync(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	server := &Server{
		catalog: fakeCatalogReader{now: now}, clock: testutil.NewFakeClock(now),
		logger: slog.New(slog.DiscardHandler),
	}
	request := httptest.NewRequest(http.MethodGet, "/items?limit=10&limit=20", nil)
	request.SetPathValue("id", catalogTestServerID)
	request.SetPathValue("library_id", catalogTestLibraryID)
	recorder := httptest.NewRecorder()
	server.handleCatalogItems(recorder, request)
	if recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("duplicate limit status = %d, body = %s", recorder.Code, recorder.Body.String())
	}

	server.catalog = fakeCatalogReader{now: now, requestErr: core.ErrLibrarySyncInProgress}
	request = httptest.NewRequest(http.MethodPost, "/sync", nil)
	request.SetPathValue("id", catalogTestServerID)
	recorder = httptest.NewRecorder()
	server.handleCatalogSync(recorder, request)
	if envelope := decodeEnvelope(t, recorder); recorder.Code != http.StatusConflict ||
		envelope.Code != "library_sync_in_progress" {
		t.Fatalf("sync conflict = %d/%q", recorder.Code, envelope.Code)
	}

	server.catalog = fakeCatalogReader{now: now, requestErr: core.ErrNotFound}
	request = httptest.NewRequest(http.MethodPost, "/sync", nil)
	request.SetPathValue("id", catalogTestServerID)
	recorder = httptest.NewRecorder()
	server.handleCatalogSync(recorder, request)
	if envelope := decodeEnvelope(t, recorder); recorder.Code != http.StatusNotFound || envelope.Code != "not_found" {
		t.Fatalf("unknown sync = %d/%q", recorder.Code, envelope.Code)
	}
}

func TestCatalogCursorRejectsSortChangesAndDrivesNextPage(t *testing.T) {
	t.Parallel()
	items := []core.CatalogItemStats{
		{Item: core.LibraryItem{ItemID: "item-a", Name: "Alpha"}},
		{Item: core.LibraryItem{ItemID: "item-b", Name: "Beta"}},
	}
	archived := false
	query := core.CatalogItemQuery{
		MediaServerID: catalogTestServerID, LibraryID: catalogTestLibraryID,
		Sort: core.CatalogSortName, Order: core.SortAscending, Archived: &archived,
	}
	page, cursor := catalogItemPage(items, 1, query)
	if len(page) != 1 || cursor == "" {
		t.Fatalf("catalogItemPage = %d/%q", len(page), cursor)
	}
	request := httptest.NewRequest(http.MethodGet, "/items?cursor="+cursor, nil)
	after, fields := catalogItemCursor(request, &query)
	if len(fields) != 0 || after == nil || after.ItemID != "item-a" || after.TextValue != "Alpha" {
		t.Fatalf("catalogItemCursor = %+v/%+v", after, fields)
	}
	query.Sort = core.CatalogSortPlays
	if _, fields = catalogItemCursor(request, &query); len(fields) != 1 {
		t.Fatalf("changed-sort cursor fields = %+v", fields)
	}
}

func TestCatalogItemCursorBindsQueryAndFixedWindow(t *testing.T) {
	t.Parallel()
	firstNow := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	archived := false
	first := core.CatalogItemQuery{
		MediaServerID: catalogTestServerID, LibraryID: catalogTestLibraryID, ItemType: "Movie",
		Archived: &archived, Sort: core.CatalogSortName, Order: core.SortAscending,
		Window: core.CatalogWindow{Enabled: true, Start: firstNow.AddDate(0, 0, -30), End: firstNow},
	}
	items := []core.CatalogItemStats{
		{Item: core.LibraryItem{ItemID: "item-a", Name: "Alpha"}},
		{Item: core.LibraryItem{ItemID: "item-b", Name: "Beta"}},
	}
	_, cursor := catalogItemPage(items, 1, first)
	request := httptest.NewRequest(http.MethodGet, "/items?cursor="+cursor, nil)
	next := first
	next.Window = core.CatalogWindow{Enabled: true, Start: firstNow.Add(time.Minute).AddDate(0, 0, -30), End: firstNow.Add(time.Minute)}
	if after, fields := catalogItemCursor(request, &next); len(fields) != 0 || after == nil || next.Window != first.Window {
		t.Fatalf("fixed-window cursor = %+v/%+v/%+v", after, fields, next.Window)
	}
	for name, mutate := range catalogCursorMutations() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			changed := first
			mutate(&changed)
			if _, fields := catalogItemCursor(request, &changed); len(fields) != 1 {
				t.Fatalf("mismatched cursor fields = %+v", fields)
			}
		})
	}
}

func catalogCursorMutations() map[string]func(*core.CatalogItemQuery) {
	return map[string]func(*core.CatalogItemQuery){
		"server":    func(q *core.CatalogItemQuery) { q.MediaServerID = catalogCursorProbeID },
		"library":   func(q *core.CatalogItemQuery) { q.LibraryID = "another-library" },
		"item type": func(q *core.CatalogItemQuery) { q.ItemType = "Episode" },
		"archived":  func(q *core.CatalogItemQuery) { value := true; q.Archived = &value },
		"sort":      func(q *core.CatalogItemQuery) { q.Sort = core.CatalogSortPlays },
		"order":     func(q *core.CatalogItemQuery) { q.Order = core.SortDescending },
		"window": func(q *core.CatalogItemQuery) {
			q.Window.Start = q.Window.Start.AddDate(0, 0, -1)
		},
	}
}

func TestCatalogHistoryAndStaleCursorsBindRouteAndWindow(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	history := encodeCatalogCursor(catalogHistoryCursorPayload{
		MediaServerID: catalogTestServerID, ItemID: catalogTestItemID, StartedAt: now, ID: catalogCursorProbeID,
	})
	request := httptest.NewRequest(http.MethodGet, "/history?cursor="+history, nil)
	if _, fields := catalogHistoryCursor(request, catalogTestServerID, "other-item"); len(fields) != 1 {
		t.Fatalf("history mismatch fields = %+v", fields)
	}
	stale := encodeCatalogCursor(catalogStaleCursorPayload{
		MediaServerID: catalogTestServerID, LibraryID: catalogTestLibraryID, Days: 30,
		Before: now.AddDate(0, 0, -30), Name: "Alpha", ItemID: catalogTestItemID,
	})
	request = httptest.NewRequest(http.MethodGet, "/stale?cursor="+stale, nil)
	query := core.CatalogStaleQuery{
		MediaServerID: catalogTestServerID, LibraryID: catalogTestLibraryID,
		Before: now.Add(time.Minute).AddDate(0, 0, -30), Limit: 2,
	}
	if _, fields := catalogStaleCursor(request, &query, 30); len(fields) != 0 || !query.Before.Equal(now.AddDate(0, 0, -30)) {
		t.Fatalf("stale fixed-window cursor = %+v/%v", fields, query.Before)
	}
	query.LibraryID = "other-library"
	if _, fields := catalogStaleCursor(request, &query, 30); len(fields) != 1 {
		t.Fatalf("stale mismatch fields = %+v", fields)
	}
}

func TestCatalogHandlerRejectsMismatchedCursorWith422(t *testing.T) {
	t.Parallel()
	archived := false
	query := core.CatalogItemQuery{
		MediaServerID: catalogTestServerID, LibraryID: catalogTestLibraryID,
		Sort: core.CatalogSortName, Order: core.SortAscending, Archived: &archived,
	}
	items := []core.CatalogItemStats{
		{Item: core.LibraryItem{ItemID: "item-a", Name: "Alpha"}},
		{Item: core.LibraryItem{ItemID: "item-b", Name: "Beta"}},
	}
	_, cursor := catalogItemPage(items, 1, query)
	request := httptest.NewRequest(http.MethodGet, "/items?cursor="+cursor+"&item_type=Movie", nil)
	request.SetPathValue("id", catalogTestServerID)
	request.SetPathValue("library_id", catalogTestLibraryID)
	recorder := httptest.NewRecorder()
	server := &Server{
		catalog: fakeCatalogReader{}, clock: testutil.NewFakeClock(time.Now()),
		logger: slog.New(slog.DiscardHandler),
	}
	server.handleCatalogItems(recorder, request)
	if recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("mismatched cursor status = %d: %s", recorder.Code, recorder.Body.String())
	}
}
