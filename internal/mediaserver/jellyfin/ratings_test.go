package jellyfin

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/BonzTM/bloom/internal/core"
)

func criticServer(t *testing.T, body string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		if r.URL.Path != "/Items" || query.Get("searchTerm") != "The Matrix" ||
			query.Get("includeItemTypes") != "Movie" || query.Get("limit") != "20" {
			t.Errorf("request = %s", r.URL.String())
		}
		w.Header().Set("Content-Type", "application/json")
		if _, err := fmt.Fprint(w, body); err != nil {
			t.Errorf("write: %v", err)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func TestCriticRatingMatchesTheTMDBID(t *testing.T) {
	server := criticServer(t, `{"Items":[{"ProviderIds":{"Tmdb":"604"},"CriticRating":40},`+
		`{"ProviderIds":{"Tmdb":"603"},"CriticRating":83}]}`)
	score, found, err := newTestClient(t, server, nil).CriticRating(t.Context(), core.MediaKindMovie, "603", "The Matrix")
	if err != nil || !found || score != 83 {
		t.Fatalf("CriticRating = %v, %t, %v", score, found, err)
	}
}

func TestCriticRatingMissingScoreOrTitleIsNotFound(t *testing.T) {
	for _, body := range []string{
		`{"Items":[{"ProviderIds":{"Tmdb":"603"}}]}`,
		`{"Items":[{"ProviderIds":{"Tmdb":"603"},"CriticRating":0}]}`,
		`{"Items":[{"ProviderIds":{"Tmdb":"603"},"CriticRating":130}]}`,
		`{"Items":[{"ProviderIds":{"Tmdb":"604"},"CriticRating":83}]}`,
		`{"Items":[]}`,
	} {
		server := criticServer(t, body)
		score, found, err := newTestClient(t, server, nil).CriticRating(t.Context(), core.MediaKindMovie, "603", "The Matrix")
		if err != nil || found || score != 0 {
			t.Errorf("%s: CriticRating = %v, %t, %v", body, score, found, err)
		}
	}
}

func TestCriticRatingRejectsInvalidInput(t *testing.T) {
	client := newTestClient(t, criticServer(t, `{"Items":[]}`), nil)
	cases := []struct {
		kind         core.MediaKind
		tmdbID, name string
	}{{"music", "603", "x"}, {core.MediaKindMovie, "abc", "x"}, {core.MediaKindMovie, "603", ""}}
	for _, tc := range cases {
		if _, _, err := client.CriticRating(t.Context(), tc.kind, tc.tmdbID, tc.name); !errors.Is(err, core.ErrInvalidArgument) {
			t.Errorf("CriticRating(%q, %q, %q) error = %v", tc.kind, tc.tmdbID, tc.name, err)
		}
	}
}

func itemCriticServer(t *testing.T, body string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		if r.URL.Path != "/Items" || query.Get("ids") != "item-a,item-b" || query.Get("searchTerm") != "" ||
			query.Get("includeItemTypes") != "Series" || query.Get("limit") != "2" {
			t.Errorf("request = %s", r.URL.String())
		}
		w.Header().Set("Content-Type", "application/json")
		if _, err := fmt.Fprint(w, body); err != nil {
			t.Errorf("write: %v", err)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func TestItemCriticRatingReadsTheCatalogItems(t *testing.T) {
	server := itemCriticServer(t, `{"Items":[{"ProviderIds":{"Tmdb":"1399"}},`+
		`{"ProviderIds":{"Tmdb":"1399"},"CriticRating":89}]}`)
	score, found, err := newTestClient(t, server, nil).
		ItemCriticRating(t.Context(), core.MediaKindSeries, "1399", []string{"item-a", "item-b"})
	if err != nil || !found || score != 89 {
		t.Fatalf("ItemCriticRating = %v, %t, %v", score, found, err)
	}
}

func TestItemCriticRatingWithoutScoreIsNotFound(t *testing.T) {
	for _, body := range []string{
		`{"Items":[{"ProviderIds":{"Tmdb":"1399"}}]}`,
		`{"Items":[{"ProviderIds":{"Tmdb":"1400"},"CriticRating":89}]}`,
		`{"Items":[]}`,
	} {
		server := itemCriticServer(t, body)
		score, found, err := newTestClient(t, server, nil).
			ItemCriticRating(t.Context(), core.MediaKindSeries, "1399", []string{"item-a", "item-b"})
		if err != nil || found || score != 0 {
			t.Errorf("%s: ItemCriticRating = %v, %t, %v", body, score, found, err)
		}
	}
}

func TestItemCriticRatingRejectsInvalidInput(t *testing.T) {
	client := newTestClient(t, itemCriticServer(t, `{"Items":[]}`), nil)
	cases := []struct {
		kind    core.MediaKind
		tmdbID  string
		itemIDs []string
	}{
		{"music", "1399", []string{"item-a"}},
		{core.MediaKindSeries, "abc", []string{"item-a"}},
		{core.MediaKindSeries, "1399", nil},
		{core.MediaKindSeries, "1399", []string{"item-a", "item-a"}},
		{core.MediaKindSeries, "1399", []string{"a", "b", "c", "d", "e", "f"}},
	}
	for _, tc := range cases {
		_, _, err := client.ItemCriticRating(t.Context(), tc.kind, tc.tmdbID, tc.itemIDs)
		if !errors.Is(err, core.ErrInvalidArgument) {
			t.Errorf("ItemCriticRating(%q, %q, %v) error = %v", tc.kind, tc.tmdbID, tc.itemIDs, err)
		}
	}
}
