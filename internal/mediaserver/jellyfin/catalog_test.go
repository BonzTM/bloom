package jellyfin

import (
	"net/http"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/BonzTM/bloom/internal/core"
	jellyfinapi "github.com/BonzTM/bloom/internal/mediaserver/jellyfin/api"
)

func TestCatalogItemsRequestsDeterministicOrdering(t *testing.T) {
	t.Parallel()
	client := newTransportClient(t, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Query().Get("sortBy") != "SortName,DateCreated" || r.URL.Query().Get("sortOrder") != "Ascending" {
			t.Fatalf("catalog order query = %q/%q", r.URL.Query().Get("sortBy"), r.URL.Query().Get("sortOrder"))
		}
		for value := range strings.SplitSeq(r.URL.Query().Get("sortBy"), ",") {
			if !jellyfinapi.ItemSortBy(value).Valid() {
				t.Fatalf("unsupported ItemSortBy %q", value)
			}
		}
		return jsonResponse(http.StatusOK, `{"Items":[],"TotalRecordCount":0,"StartIndex":0}`), nil
	}))
	if _, err := client.CatalogItems(t.Context(), "library", 0, core.CatalogPageSize); err != nil {
		t.Fatalf("CatalogItems: %v", err)
	}
}

// Production: a movie library returned 1,302 movies and 255 box sets; asking
// Jellyfin not to collapse box sets returned 1,994 movies.
func TestCatalogItemsListsMoviesInsideBoxSets(t *testing.T) {
	t.Parallel()
	client := newTransportClient(t, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if got := r.URL.Query().Get("collapseBoxSetItems"); got != "false" {
			t.Fatalf("collapseBoxSetItems = %q, want false", got)
		}
		return jsonResponse(http.StatusOK, `{"Items":[],"TotalRecordCount":0,"StartIndex":0}`), nil
	}))
	if _, err := client.CatalogItems(t.Context(), "library", 0, core.CatalogPageSize); err != nil {
		t.Fatalf("CatalogItems: %v", err)
	}
}

func TestCatalogRevalidationSurvivesEqualNamePageChanges(t *testing.T) {
	t.Parallel()
	const firstID = "00000000-0000-0000-0000-000000000001"
	const secondID = "00000000-0000-0000-0000-000000000002"
	var pages atomic.Int32
	client := newTransportClient(t, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if ids := r.URL.Query().Get("ids"); ids != "" {
			if ids != firstID {
				t.Fatalf("revalidation ids = %q", ids)
			}
			return jsonResponse(http.StatusOK, `{"Items":[{"Id":"`+firstID+`"}],"TotalRecordCount":1}`), nil
		}
		for value := range strings.SplitSeq(r.URL.Query().Get("sortBy"), ",") {
			if !jellyfinapi.ItemSortBy(value).Valid() {
				t.Fatalf("unsupported ItemSortBy %q", value)
			}
		}
		if pages.Add(1) == 1 {
			return jsonResponse(http.StatusOK, `{"Items":[{"Id":"`+firstID+`","Type":"Movie","Name":"Same"}],"TotalRecordCount":2,"StartIndex":0}`), nil
		}
		return jsonResponse(http.StatusOK, `{"Items":[{"Id":"`+secondID+`","Type":"Movie","Name":"Same"}],"TotalRecordCount":2,"StartIndex":1}`), nil
	}))
	if _, err := client.CatalogItems(t.Context(), "library", 0, core.CatalogPageSize); err != nil {
		t.Fatalf("first CatalogItems: %v", err)
	}
	if _, err := client.CatalogItems(t.Context(), "library", 1, core.CatalogPageSize); err != nil {
		t.Fatalf("second CatalogItems: %v", err)
	}
	found, err := client.CatalogItemIDs(t.Context(), []string{firstID})
	if err != nil || len(found) != 1 || found[0] != firstID {
		t.Fatalf("CatalogItemIDs = %v, %v", found, err)
	}
}

func TestMapCatalogPagePreservesUnknownItemTypeAndTicks(t *testing.T) {
	itemID := openapi_types.UUID{1}
	itemType := jellyfinapi.BaseItemKind("FutureJellyfinType")
	name := "Future item"
	ticks := int64(25_000_000)
	genres := []string{"Drama"}
	items := []jellyfinapi.BaseItemDto{{
		Id: &itemID, Type: &itemType, Name: &name, RunTimeTicks: &ticks, Genres: &genres,
	}}
	total := int32(1)
	start := int32(0)
	page, err := mapCatalogPage(jellyfinapi.BaseItemDtoQueryResult{
		Items: &items, TotalRecordCount: &total, StartIndex: &start,
	}, "library", 0, core.CatalogPageSize)
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("mapCatalogPage = %+v, %v", page, err)
	}
	item := page.Items[0]
	if item.ItemType != "FutureJellyfinType" || item.Runtime == nil || *item.Runtime != 2500*time.Millisecond ||
		!item.ValidUpstream() {
		t.Fatalf("mapped item = %+v", item)
	}
}

// A real Jellyfin 12.4 title: the en dash was stored as the C1 control
// character U+0096. That one item is rejected with its reason; the rest of
// the page still maps.
func TestCatalogItemsRejectsOneBadItemAndKeepsThePage(t *testing.T) {
	t.Parallel()
	client := newTransportClient(t, roundTripFunc(func(*http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, `{"Items":[`+
			`{"Id":"00000000-0000-0000-0000-000000000001","Type":"Episode","Name":"Pilot","SeriesName":"Raising Hope"},`+
			`{"Id":"8afebf13-9c7f-e7c7-a124-5c73135996e8","Type":"Episode","Name":"Burtmizvah \u0096\u00a0The Musical","SeriesName":"Raising Hope"},`+
			`{"Id":"00000000-0000-0000-0000-000000000003","Type":"Episode","Name":"","SeriesName":"Raising Hope"},`+
			`{"Id":"00000000-0000-0000-0000-000000000004","Type":"Episode","Name":"Long","RunTimeTicks":-1}`+
			`],"TotalRecordCount":4,"StartIndex":0}`), nil
	}))
	page, err := client.CatalogItems(t.Context(), "library", 0, core.CatalogPageSize)
	if err != nil {
		t.Fatalf("CatalogItems: %v", err)
	}
	if len(page.Items) != 1 || page.Items[0].Name != "Pilot" || page.Consumed() != 4 {
		t.Fatalf("items = %+v, consumed = %d", page.Items, page.Consumed())
	}
	want := []core.LibraryItemRejection{
		{
			ItemID: "8afebf13-9c7f-e7c7-a124-5c73135996e8", ItemType: "Episode", Name: "Burtmizvah \u0096\u00a0The Musical",
			SeriesName: "Raising Hope", Reason: "name contains control character U+0096 at byte 11",
		},
		{ItemID: "00000000-0000-0000-0000-000000000003", ItemType: "Episode", SeriesName: "Raising Hope", Reason: "name is empty"},
		{ItemID: "00000000-0000-0000-0000-000000000004", ItemType: "Episode", Name: "Long", Reason: "runtime ticks are out of range"},
	}
	if !slices.Equal(page.Rejected, want) {
		t.Fatalf("rejected = %+v\nwant %+v", page.Rejected, want)
	}
}

func TestMapCatalogUserDataTreatsOmittedStateAsUnplayed(t *testing.T) {
	itemID := openapi_types.UUID{2}
	items := []jellyfinapi.BaseItemDto{{Id: &itemID}}
	requested := []string{itemID.String()}
	got, err := mapCatalogUserData(jellyfinapi.BaseItemDtoQueryResult{Items: &items}, requested)
	if err != nil || len(got) != 1 || got[0].PlayCount != 0 || got[0].ItemID != requested[0] {
		t.Fatalf("mapCatalogUserData = %+v, %v", got, err)
	}
}

func TestMapCatalogUserDataRejectsUnexpectedItem(t *testing.T) {
	itemID := openapi_types.UUID{3}
	items := []jellyfinapi.BaseItemDto{{Id: &itemID}}
	if _, err := mapCatalogUserData(jellyfinapi.BaseItemDtoQueryResult{Items: &items}, []string{"another"}); err == nil {
		t.Fatal("mapCatalogUserData accepted an unexpected item")
	}
}

func TestCatalogItemsReadsTMDBIDsForMoviesAndSeriesOnly(t *testing.T) {
	t.Parallel()
	client := newTransportClient(t, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if fields := r.URL.Query().Get("fields"); !strings.Contains(fields, "ProviderIds") {
			t.Fatalf("catalog fields = %q, want ProviderIds", fields)
		}
		return jsonResponse(http.StatusOK, `{"Items":[`+
			`{"Id":"00000000-0000-0000-0000-000000000001","Type":"Movie","Name":"Heat","ProviderIds":{"Tmdb":"949","Imdb":"tt0113277"}},`+
			`{"Id":"00000000-0000-0000-0000-000000000002","Type":"Series","Name":"Fringe","ProviderIds":{"tmdb":"1705"}},`+
			`{"Id":"00000000-0000-0000-0000-000000000003","Type":"Episode","Name":"Pilot","ProviderIds":{"Tmdb":"62085"}},`+
			`{"Id":"00000000-0000-0000-0000-000000000004","Type":"Movie","Name":"Typo","ProviderIds":{"Tmdb":"0949"}},`+
			`{"Id":"00000000-0000-0000-0000-000000000005","Type":"Movie","Name":"Huge","ProviderIds":{"Tmdb":"1000000001"}},`+
			`{"Id":"00000000-0000-0000-0000-000000000006","Type":"Movie","Name":"None"}`+
			`],"TotalRecordCount":6,"StartIndex":0}`), nil
	}))
	page, err := client.CatalogItems(t.Context(), "library", 0, core.CatalogPageSize)
	if err != nil {
		t.Fatalf("CatalogItems: %v", err)
	}
	want := []int64{949, 1705, 0, 0, 0, 0}
	if len(page.Items) != len(want) {
		t.Fatalf("items = %d, want %d", len(page.Items), len(want))
	}
	for index, item := range page.Items {
		if item.TMDBID != want[index] {
			t.Errorf("%s TMDBID = %d, want %d", item.Name, item.TMDBID, want[index])
		}
	}
}
