package sonarr

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"

	"github.com/BonzTM/bloom/internal/core"
)

func TestAddAdoptsExistingSeriesAndUpdatesSeasons(t *testing.T) {
	var puts, searches atomic.Int32
	var updated map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v3/series/lookup":
			if r.URL.Query().Get("term") != "tmdb:200" {
				t.Errorf("lookup term = %q", r.URL.Query().Get("term"))
			}
			writeTestJSON(t, w, []map[string]any{{"tmdbId": 200, "tvdbId": 300, "seasons": []map[string]any{{"seasonNumber": 1}, {"seasonNumber": 2}}}})
		case r.URL.Path == "/api/v3/series" && r.Method == http.MethodGet:
			writeTestJSON(t, w, []map[string]any{{"id": 19, "tmdbId": 200, "tvdbId": 300, "seasons": []map[string]any{{"seasonNumber": 1, "monitored": true}, {"seasonNumber": 2, "monitored": false}}}})
		case r.URL.Path == "/api/v3/series/19" && r.Method == http.MethodPut:
			puts.Add(1)
			if err := json.NewDecoder(r.Body).Decode(&updated); err != nil {
				t.Errorf("decode update: %v", err)
			}
			writeTestJSON(t, w, updated)
		case r.URL.Path == "/api/v3/command" && r.Method == http.MethodPost:
			searches.Add(1)
			var command map[string]any
			if err := json.NewDecoder(r.Body).Decode(&command); err != nil {
				t.Errorf("decode command: %v", err)
			}
			if command["name"] != "SeasonSearch" || command["seasonNumber"] != float64(2) {
				t.Errorf("command = %+v", command)
			}
			writeTestJSON(t, w, map[string]any{"id": 92})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client := newTestClient(t, server)
	title := seriesTitle()
	title.Seasons = []int{2}
	id, err := client.Add(t.Context(), title, seriesOptions())
	if err != nil || id != "19" || puts.Load() != 1 || searches.Load() != 1 {
		t.Fatalf("Add = %q, %v; puts %d searches %d", id, err, puts.Load(), searches.Load())
	}
	seasons, ok := updated["seasons"].([]any)
	if !ok || len(seasons) != 2 {
		t.Fatalf("updated series = %+v", updated)
	}
	first, firstOK := seasons[0].(map[string]any)
	second, secondOK := seasons[1].(map[string]any)
	if !firstOK || !secondOK || first["monitored"] != true || second["monitored"] != true {
		t.Fatalf("updated seasons = %+v", seasons)
	}
}

func TestAddMissCreatesSeriesOnce(t *testing.T) {
	var posts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v3/series/lookup":
			writeTestJSON(t, w, []map[string]any{{"tmdbId": 200, "tvdbId": 300, "seasons": []map[string]any{{"seasonNumber": 1}}}})
		case r.URL.Path == "/api/v3/series" && r.Method == http.MethodGet:
			writeTestJSON(t, w, []any{})
		case r.URL.Path == "/api/v3/series" && r.Method == http.MethodPost:
			posts.Add(1)
			writeTestJSON(t, w, map[string]any{"id": 20, "tvdbId": 300})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client := newTestClient(t, server)
	id, err := client.Add(t.Context(), seriesTitle(), seriesOptions())
	if err != nil || id != "20" || posts.Load() != 1 {
		t.Fatalf("Add = %q, %v; posts %d", id, err, posts.Load())
	}
}

func TestProbeOptionsAndQueueFallback(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v3/system/status":
			writeTestJSON(t, w, map[string]any{"instanceName": "Main Sonarr", "version": "4.0"})
		case "/api/v3/qualityprofile":
			writeTestJSON(t, w, []map[string]any{{"id": 1, "name": "HD"}})
		case "/api/v3/rootfolder":
			writeTestJSON(t, w, []map[string]any{{"path": "/series"}})
		case "/api/v3/tag":
			writeTestJSON(t, w, []map[string]any{{"id": 2, "label": "requested"}})
		case "/api/v3/queue":
			writeTestJSON(t, w, map[string]any{"records": []any{}})
		case "/api/v3/series/20":
			writeTestJSON(t, w, map[string]any{"id": 20, "seasons": []map[string]any{{
				"seasonNumber": 1, "monitored": true, "statistics": map[string]any{"episodeCount": 8, "episodeFileCount": 8},
			}}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client := newTestClient(t, server)
	info, err := client.Probe(t.Context())
	if err != nil || info.Name != "Main Sonarr" || len(info.Options.QualityProfiles) != 1 || len(info.Options.RootFolders) != 1 || len(info.Options.Tags) != 1 {
		t.Fatalf("Probe = %+v, %v", info, err)
	}
	progress, err := client.Queue(t.Context(), "20", []int{1})
	if err != nil || !progress.HasFile || !progress.Complete || progress.Status != "not_queued" {
		t.Fatalf("Queue = %+v, %v", progress, err)
	}
}

func TestCompletedQueueWithoutRequestedSeasonFilesIsNotAvailable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v3/queue":
			writeTestJSON(t, w, map[string]any{"records": []map[string]any{{
				"seriesId": 20, "status": "completed", "size": 1000, "sizeleft": 0,
			}}})
		case "/api/v3/series/20":
			writeTestJSON(t, w, map[string]any{"id": 20, "seasons": []map[string]any{{
				"seasonNumber": 1, "monitored": true,
				"statistics": map[string]any{"episodeCount": 8, "episodeFileCount": 0},
			}}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	progress, err := newTestClient(t, server).Queue(t.Context(), "20", []int{1})
	if err != nil || !progress.Complete || progress.HasFile {
		t.Fatalf("Queue = %+v, %v", progress, err)
	}
}

func newTestClient(t *testing.T, server *httptest.Server) *Client {
	t.Helper()
	target, err := url.Parse(server.URL)
	if err != nil {
		t.Fatalf("parse test URL: %v", err)
	}
	client, err := New(Config{
		BaseURL: "https://sonarr.example.test", APIKey: "secret",
		HTTPClient: &http.Client{Transport: rewriteTransport{target: target, base: server.Client().Transport}},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return client
}

type rewriteTransport struct {
	target *url.URL
	base   http.RoundTripper
}

func (t rewriteTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	clone := request.Clone(request.Context())
	clone.URL.Scheme = t.target.Scheme
	clone.URL.Host = t.target.Host
	return t.base.RoundTrip(clone)
}

func seriesTitle() core.DownloadTitle {
	return core.DownloadTitle{Kind: core.MediaKindSeries, ProviderID: "200", Title: "Show", Year: 2026, Seasons: []int{1}}
}

func seriesOptions() core.DownloadOptions {
	return core.DownloadOptions{QualityProfile: "1", RootFolder: "/series", Tags: []string{"2"}}
}

func writeTestJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Errorf("encode response: %v", err)
	}
}
