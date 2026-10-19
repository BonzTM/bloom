package sonarr

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/BonzTM/bloom/internal/core"
)

func TestAddAdoptsExistingSeriesWithoutReplacingOperatorFields(t *testing.T) {
	var puts, searches atomic.Int32
	var updated map[string]json.RawMessage
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v3/series/lookup":
			if r.URL.Query().Get("term") != "tmdb:200" {
				t.Errorf("lookup term = %q", r.URL.Query().Get("term"))
			}
			writeTestJSON(t, w, []map[string]any{{"tmdbId": 200, "tvdbId": 300, "seasons": []map[string]any{{"seasonNumber": 1}, {"seasonNumber": 2}}}})
		case r.URL.Path == "/api/v3/series" && r.Method == http.MethodGet:
			writeTestJSON(t, w, []map[string]any{{
				"id": 19, "tmdbId": 200, "tvdbId": 300, "monitored": false,
				"qualityProfileId": 7, "rootFolderPath": "/operator/series", "path": "/operator/series/show",
				"tags": []int{3, 2}, "addOptions": map[string]any{"monitor": "all"},
				"language":     map[string]any{"id": 1, "name": "English"},
				"folders":      []map[string]any{{"path": "/operator/series/show"}},
				"translations": []map[string]any{{"title": "Show"}},
				"seasons": []map[string]any{
					{"seasonNumber": 1, "monitored": true, "seasonType": "standard"},
					{"seasonNumber": 2, "monitored": false, "seasonType": "standard"},
				},
			}})
		case r.URL.Path == "/api/v3/series/19" && r.Method == http.MethodPut:
			puts.Add(1)
			if err := json.NewDecoder(r.Body).Decode(&updated); err != nil {
				t.Errorf("decode update: %v", err)
			}
			writeTestJSON(t, w, updated)
		case r.URL.Path == "/api/v3/command" && r.Method == http.MethodPost:
			searches.Add(1)
			// Sonarr searches monitored episodes only: the season must be
			// monitored before its search starts.
			if puts.Load() != 1 {
				t.Error("season search started before the season was monitored")
			}
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
	options := seriesOptions()
	options.Tags = []string{"2", "4"}
	id, err := client.Add(t.Context(), title, options)
	if err != nil || id != "19" || puts.Load() != 1 || searches.Load() != 1 {
		t.Fatalf("Add = %q, %v; puts %d searches %d", id, err, puts.Load(), searches.Load())
	}
	assertRawValue(t, updated, "qualityProfileId", `7`)
	assertRawValue(t, updated, "rootFolderPath", `"/operator/series"`)
	assertRawValue(t, updated, "path", `"/operator/series/show"`)
	assertRawValue(t, updated, "monitored", `true`)
	assertRawValue(t, updated, "tags", `[3,2,4]`)
	assertRawValue(t, updated, "language", `{"id":1,"name":"English"}`)
	assertRawValue(t, updated, "folders", `[{"path":"/operator/series/show"}]`)
	assertRawValue(t, updated, "translations", `[{"title":"Show"}]`)
	if _, exists := updated["addOptions"]; exists {
		t.Fatal("PUT body retains addOptions")
	}
	var seasons []map[string]json.RawMessage
	if err := json.Unmarshal(updated["seasons"], &seasons); err != nil {
		t.Fatalf("decode updated seasons: %v", err)
	}
	assertRawValue(t, seasons[0], "monitored", `true`)
	assertRawValue(t, seasons[0], "seasonType", `"standard"`)
	assertRawValue(t, seasons[1], "monitored", `true`)
	assertRawValue(t, seasons[1], "seasonType", `"standard"`)
}

func TestAddTreatsNullTagsAndNullResponseIDAsEmpty(t *testing.T) {
	var updated map[string]json.RawMessage
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v3/series/lookup":
			writeTestJSON(t, w, []map[string]any{{"tmdbId": 200, "tvdbId": 300}})
		case r.URL.Path == "/api/v3/series" && r.Method == http.MethodGet:
			writeTestJSON(t, w, []map[string]any{{
				"id": 19, "tmdbId": 200, "tvdbId": 300, "tags": nil,
				"seasons": []map[string]any{{
					"seasonNumber": 1, "monitored": false,
					"statistics": map[string]any{"episodeCount": 8, "episodeFileCount": 8},
				}},
			}})
		case r.URL.Path == "/api/v3/series/19" && r.Method == http.MethodPut:
			if err := json.NewDecoder(r.Body).Decode(&updated); err != nil {
				t.Errorf("decode update: %v", err)
			}
			writeTestJSON(t, w, map[string]any{"id": nil})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	id, err := newTestClient(t, server).Add(t.Context(), seriesTitle(), seriesOptions())
	if err != nil || id != "19" {
		t.Fatalf("Add = %q, %v", id, err)
	}
	assertRawValue(t, updated, "tags", `[2]`)
	if _, exists := updated["addOptions"]; exists {
		t.Fatal("PUT body retains addOptions")
	}
}

func TestAddRetriesSearchAfterSuccessfulMonitoringUpdate(t *testing.T) {
	var puts, searches atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v3/series/lookup":
			writeTestJSON(t, w, []map[string]any{{"tmdbId": 200, "tvdbId": 300}})
		case r.URL.Path == "/api/v3/series" && r.Method == http.MethodGet:
			monitored := puts.Load() > 0
			writeTestJSON(t, w, []map[string]any{{
				"id": 19, "tvdbId": 300, "tags": []int{},
				"seasons": []map[string]any{{"seasonNumber": 1, "monitored": monitored}},
			}})
		case r.URL.Path == "/api/v3/series/19" && r.Method == http.MethodPut:
			puts.Add(1)
			writeTestJSON(t, w, map[string]any{"id": 19})
		case r.URL.Path == "/api/v3/command" && r.Method == http.MethodPost:
			if searches.Add(1) == 1 {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			writeTestJSON(t, w, map[string]any{"id": 92})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client := newTestClient(t, server)
	if _, err := client.Add(t.Context(), seriesTitle(), seriesOptions()); err == nil {
		t.Fatal("first Add returned nil error")
	}
	id, err := client.Add(t.Context(), seriesTitle(), seriesOptions())
	if err != nil || id != "19" || puts.Load() != 2 || searches.Load() != 2 {
		t.Fatalf("retry Add = %q, %v; puts %d searches %d", id, err, puts.Load(), searches.Load())
	}
}

func TestAddDoesNotSearchFullyDownloadedSeason(t *testing.T) {
	var puts, searches atomic.Int32
	server := newExistingSeriesServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v3/series/19" && r.Method == http.MethodPut:
			puts.Add(1)
			writeTestJSON(t, w, map[string]any{"id": 19})
		case r.URL.Path == "/api/v3/command":
			searches.Add(1)
			writeTestJSON(t, w, map[string]any{"id": 92})
		default:
			http.NotFound(w, r)
		}
	}, `{"id":19,"tvdbId":300,"tags":[],"seasons":[{"seasonNumber":1,"monitored":false,"statistics":{"episodeCount":8,"episodeFileCount":8}}]}`)
	defer server.Close()
	id, err := newTestClient(t, server).Add(t.Context(), seriesTitle(), seriesOptions())
	if err != nil || id != "19" || puts.Load() != 1 || searches.Load() != 0 {
		t.Fatalf("Add = %q, %v; puts %d searches %d", id, err, puts.Load(), searches.Load())
	}
}

func TestAddFetchesSeriesDetailWhenListSeasonsAreNull(t *testing.T) {
	var detailGets, puts atomic.Int32
	server := newExistingSeriesServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v3/series/19" && r.Method == http.MethodGet:
			detailGets.Add(1)
			writeRawTestJSON(t, w, `{"id":19,"tags":[],"seasons":[{"seasonNumber":1,"statistics":{"episodeCount":8,"episodeFileCount":8}}]}`)
		case r.URL.Path == "/api/v3/series/19" && r.Method == http.MethodPut:
			puts.Add(1)
			writeTestJSON(t, w, map[string]any{"id": 19})
		default:
			http.NotFound(w, r)
		}
	}, `{"id":19,"tvdbId":300,"tags":[],"seasons":null}`)
	defer server.Close()
	id, err := newTestClient(t, server).Add(t.Context(), seriesTitle(), seriesOptions())
	if err != nil || id != "19" || detailGets.Load() != 1 || puts.Load() != 1 {
		t.Fatalf("Add = %q, %v; detail GETs %d puts %d", id, err, detailGets.Load(), puts.Load())
	}
}

func TestAddReturnsNotFoundWhenDetailLacksRequestedSeason(t *testing.T) {
	var puts atomic.Int32
	server := newExistingSeriesServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v3/series/19" && r.Method == http.MethodGet:
			writeRawTestJSON(t, w, `{"id":19,"tags":[],"seasons":[{"seasonNumber":1}]}`)
		case r.Method == http.MethodPut:
			puts.Add(1)
			http.NotFound(w, r)
		default:
			http.NotFound(w, r)
		}
	}, `{"id":19,"tvdbId":300,"tags":[]}`)
	defer server.Close()
	title := seriesTitle()
	title.Seasons = []int{2}
	_, err := newTestClient(t, server).Add(t.Context(), title, seriesOptions())
	var classified *core.DownloadManagerError
	if !errors.As(err, &classified) || classified.Kind != core.DownloadManagerNotFound ||
		classified.Err == nil || !strings.Contains(classified.Err.Error(), "2") {
		t.Fatalf("Add error = %T %v", err, err)
	}
	if puts.Load() != 0 {
		t.Fatalf("puts = %d, want 0", puts.Load())
	}
}

func TestAddRejectsMalformedExistingSeasonDataWithoutWrite(t *testing.T) {
	tests := []string{
		`{"id":19,"tvdbId":300,"tags":[],"seasons":[{"seasonNumber":1}],"x":1,"x":2}`,
		`{"id":19,"tvdbId":300,"tags":[],"seasons":[{"seasonNumber":1,"x":1,"x":2}]}`,
		`{"id":19,"tvdbId":300,"tags":[],"seasons":[{"monitored":false}]}`,
		`{"id":19,"tvdbId":300,"tags":[],"seasons":[{"seasonNumber":1},{"seasonNumber":1}]}`,
	}
	for _, raw := range tests {
		_, writes, err := addWithRawSeries(t, raw, seriesTitle())
		var classified *core.DownloadManagerError
		if !errors.As(err, &classified) || classified.Kind != core.DownloadManagerMalformed {
			t.Fatalf("Add(%s) error = %T %v", raw, err, err)
		}
		if writes != 0 {
			t.Fatalf("Add(%s) writes = %d, want 0", raw, writes)
		}
	}
}

func TestAddRejectsConflictingSeriesResponseID(t *testing.T) {
	var searches atomic.Int32
	server := newExistingSeriesServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v3/series/19" && r.Method == http.MethodPut:
			writeTestJSON(t, w, map[string]any{"id": 20})
		case r.URL.Path == "/api/v3/command":
			searches.Add(1)
			writeTestJSON(t, w, map[string]any{"id": 92})
		default:
			http.NotFound(w, r)
		}
	}, `{"id":19,"tvdbId":300,"tags":[],"seasons":[{"seasonNumber":1}]}`)
	defer server.Close()
	_, err := newTestClient(t, server).Add(t.Context(), seriesTitle(), seriesOptions())
	var classified *core.DownloadManagerError
	if !errors.As(err, &classified) || classified.Kind != core.DownloadManagerMalformed {
		t.Fatalf("Add error = %T %v", err, err)
	}
	if searches.Load() != 0 {
		t.Fatalf("searches = %d, want 0", searches.Load())
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

func TestQueueRejectsMissingRequestedSeasonData(t *testing.T) {
	tests := []struct {
		name    string
		series  map[string]any
		seasons []int
	}{
		{name: "missing seasons", series: map[string]any{
			"id": 20, "statistics": map[string]any{"episodeCount": 16, "episodeFileCount": 16},
		}, seasons: []int{1}},
		{name: "partial seasons", series: map[string]any{
			"id": 20, "seasons": []map[string]any{{
				"seasonNumber": 1, "statistics": map[string]any{"episodeCount": 8, "episodeFileCount": 8},
			}},
		}, seasons: []int{1, 2}},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			progress, err := queueWithSeries(t, testCase.series, testCase.seasons)
			var classified *core.DownloadManagerError
			if progress.HasFile || !errors.As(err, &classified) ||
				classified.Kind != core.DownloadManagerMalformed || classified.Retryable {
				t.Fatalf("Queue = %+v, %v", progress, err)
			}
		})
	}
}

func queueWithSeries(t *testing.T, series map[string]any, seasons []int) (core.DownloadProgress, error) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v3/queue":
			writeTestJSON(t, w, map[string]any{"records": []any{}})
		case "/api/v3/series/20":
			writeTestJSON(t, w, series)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	return newTestClient(t, server).Queue(t.Context(), "20", seasons)
}

func TestMapOptionsReturnsNonNilEmptySlices(t *testing.T) {
	options, err := mapOptions(nil, nil, nil)
	if err != nil || options.QualityProfiles == nil || options.RootFolders == nil || options.Tags == nil {
		t.Fatalf("mapOptions = %+v, %v", options, err)
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

func newExistingSeriesServer(
	t *testing.T, next http.HandlerFunc, rawSeries string,
) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v3/series/lookup":
			writeTestJSON(t, w, []map[string]any{{"tmdbId": 200, "tvdbId": 300}})
		case r.URL.Path == "/api/v3/series" && r.Method == http.MethodGet:
			writeRawTestJSON(t, w, "["+rawSeries+"]")
		default:
			next(w, r)
		}
	}))
}

func addWithRawSeries(t *testing.T, raw string, title core.DownloadTitle) (string, int32, error) {
	t.Helper()
	var writes atomic.Int32
	server := newExistingSeriesServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut || r.Method == http.MethodPost {
			writes.Add(1)
		}
		http.NotFound(w, r)
	}, raw)
	defer server.Close()
	id, err := newTestClient(t, server).Add(t.Context(), title, seriesOptions())
	return id, writes.Load(), err
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

func writeRawTestJSON(t *testing.T, w http.ResponseWriter, value string) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if _, err := io.WriteString(w, value); err != nil {
		t.Errorf("write response: %v", err)
	}
}
