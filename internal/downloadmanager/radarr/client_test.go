package radarr

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"

	"github.com/BonzTM/bloom/internal/core"
)

func TestAddAdoptsExistingMovieWithoutPost(t *testing.T) {
	var posts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Api-Key") != "secret" {
			t.Error("missing API key")
		}
		switch r.URL.Path {
		case "/api/v3/movie":
			if r.Method == http.MethodPost {
				posts.Add(1)
			}
			writeTestJSON(t, w, []map[string]any{{"id": 17, "tmdbId": 100}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client := newTestClient(t, server)
	id, err := client.Add(t.Context(), movieTitle(), movieOptions())
	if err != nil || id != "17" || posts.Load() != 0 {
		t.Fatalf("Add = %q, %v; posts %d", id, err, posts.Load())
	}
}

func TestAddMissLooksUpAndPostsOnce(t *testing.T) {
	var posts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v3/movie" && r.Method == http.MethodGet:
			writeTestJSON(t, w, []any{})
		case r.URL.Path == "/api/v3/movie/lookup/tmdb":
			writeTestJSON(t, w, map[string]any{"tmdbId": 100, "title": "Film"})
		case r.URL.Path == "/api/v3/movie" && r.Method == http.MethodPost:
			posts.Add(1)
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode add: %v", err)
			}
			writeTestJSON(t, w, map[string]any{"id": 18, "tmdbId": 100})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client := newTestClient(t, server)
	id, err := client.Add(t.Context(), movieTitle(), movieOptions())
	if err != nil || id != "18" || posts.Load() != 1 {
		t.Fatalf("Add = %q, %v; posts %d", id, err, posts.Load())
	}
}

func TestProbeOptionsAndQueueProgress(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v3/system/status":
			writeTestJSON(t, w, map[string]any{"instanceName": "Main Radarr", "version": "5.0"})
		case "/api/v3/qualityprofile":
			writeTestJSON(t, w, []map[string]any{{"id": 1, "name": "HD"}})
		case "/api/v3/rootfolder":
			writeTestJSON(t, w, []map[string]any{{"path": "/movies"}})
		case "/api/v3/tag":
			writeTestJSON(t, w, []map[string]any{{"id": 2, "label": "requested"}})
		case "/api/v3/queue":
			writeTestJSON(t, w, map[string]any{"records": []map[string]any{{
				"movieId": 18, "status": "downloading", "size": 1000, "sizeleft": 250,
			}}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client := newTestClient(t, server)
	info, err := client.Probe(t.Context())
	if err != nil || info.Name != "Main Radarr" || len(info.Options.QualityProfiles) != 1 || len(info.Options.RootFolders) != 1 || len(info.Options.Tags) != 1 {
		t.Fatalf("Probe = %+v, %v", info, err)
	}
	progress, err := client.Queue(t.Context(), "18")
	if err != nil || progress.Status != "downloading" || progress.Size != 1000 || progress.SizeLeft != 250 {
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
		BaseURL: "https://radarr.example.test", APIKey: "secret",
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

func movieTitle() core.DownloadTitle {
	return core.DownloadTitle{Kind: core.MediaKindMovie, ProviderID: "100", Title: "Film", Year: 2026}
}

func movieOptions() core.DownloadOptions {
	return core.DownloadOptions{QualityProfile: "1", RootFolder: "/movies", Tags: []string{"2"}}
}

func writeTestJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Errorf("encode response: %v", err)
	}
}
