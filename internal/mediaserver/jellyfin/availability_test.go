package jellyfin

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/BonzTM/bloom/internal/core"
)

func TestHasTitleMatchesTMDBAndRequestedSeriesSeasons(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/Items" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("parentId") != "" {
			_, _ = fmt.Fprint(w, `{"Items":[{"IndexNumber":1},{"IndexNumber":2}]}`)
			return
		}
		_, _ = fmt.Fprint(w, `{"Items":[{"Id":"11111111-1111-4111-8111-111111111111","ProviderIds":{"Tmdb":"200"}}]}`)
	}))
	defer server.Close()
	client := newTestClient(t, server, nil)
	available, seasons, err := client.HasTitle(
		t.Context(), core.MediaKindSeries, core.MetadataProviderTMDB, "200", []int{1, 2},
	)
	if err != nil || !available || len(seasons) != 2 {
		t.Fatalf("HasTitle = %t, %v, %v", available, seasons, err)
	}
}

func TestHasTitleDoesNotTreatDifferentTMDBTitleAsAvailable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"Items":[{"ProviderIds":{"Tmdb":"201"}}]}`)
	}))
	defer server.Close()
	client := newTestClient(t, server, nil)
	available, _, err := client.HasTitle(t.Context(), core.MediaKindMovie, core.MetadataProviderTMDB, "200", nil)
	if err != nil || available {
		t.Fatalf("HasTitle = %t, %v", available, err)
	}
}
