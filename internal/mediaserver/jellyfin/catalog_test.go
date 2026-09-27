package jellyfin

import (
	"net/http"
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
