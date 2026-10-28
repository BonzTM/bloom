package http

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/httputil"
	"github.com/BonzTM/bloom/internal/telemetry"
)

type ratingsManagerStub struct {
	ratings  core.TitleRatings
	err      error
	titles   []core.RatedTitle
	key      string
	removed  bool
	present  bool
	setKeyFn func(string) error
}

func (s *ratingsManagerStub) SetKey(_ context.Context, key string) error {
	s.key = key
	if s.setKeyFn != nil {
		return s.setKeyFn(key)
	}
	return nil
}

func (s *ratingsManagerStub) HasKey(context.Context) (bool, error) { return s.present, nil }

func (s *ratingsManagerStub) RemoveKey(context.Context) error {
	s.removed = true
	return nil
}

func (s *ratingsManagerStub) Ratings(_ context.Context, title core.RatedTitle) (core.TitleRatings, error) {
	s.titles = append(s.titles, title)
	return s.ratings, s.err
}

func serveRatings(
	t *testing.T, reader *metadataReaderStub, manager *ratingsManagerStub, path, id string,
	handle func(*Server, http.ResponseWriter, *http.Request),
) *httptest.ResponseRecorder {
	t.Helper()
	server := &Server{logger: slog.New(slog.DiscardHandler), metadataReader: reader, ratings: manager}
	request := requestWithAccount(t, http.MethodGet, path, "", core.PermissionRequestsReadOwn)
	request.SetPathValue("id", id)
	recorder := httptest.NewRecorder()
	handle(server, recorder, request)
	return recorder
}

const movieRatingsWire = `{"imdb":{"score":8.7,"votes":2123456,"source":"omdb"},` +
	`"rotten_tomatoes":{"score":83,"votes":0,"source":"omdb"},"metacritic":{"score":73,"votes":0,"source":"omdb"}}`

func TestMovieRatingsReturnExactWireShape(t *testing.T) {
	reader := &metadataReaderStub{movie: core.MetadataMovieDetail{
		MetadataTitle: detailTestTitle(core.MediaKindMovie, "603", "The Matrix"),
		Details:       core.MetadataDetails{IMDbID: "tt0133093"},
	}}
	manager := &ratingsManagerStub{ratings: core.TitleRatings{
		IMDb:           &core.TitleRating{Score: 8.7, Votes: 2_123_456, Source: core.RatingSourceOMDb},
		RottenTomatoes: &core.TitleRating{Score: 83, Source: core.RatingSourceOMDb},
		Metacritic:     &core.TitleRating{Score: 73, Source: core.RatingSourceOMDb},
	}}
	recorder := serveRatings(t, reader, manager, "/api/v1/metadata/movies/603/ratings", "603", (*Server).handleMovieRatings)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", recorder.Code, recorder.Body.String())
	}
	assertCompactJSON(t, recorder.Body.Bytes(), movieRatingsWire)
	assertJSONMatchesSchema(t, loadOpenAPI(t), recorder.Body.Bytes(), "#/components/schemas/TitleRatings")
	want := core.RatedTitle{Kind: core.MediaKindMovie, TMDBID: "603", IMDbID: "tt0133093", Title: "The Matrix"}
	if len(manager.titles) != 1 || manager.titles[0] != want || reader.accountID != testRequestAccountID {
		t.Fatalf("ratings read for %+v by account %q", manager.titles, reader.accountID)
	}
}

func TestSeriesRatingsUseTheSeriesIMDbID(t *testing.T) {
	reader := &metadataReaderStub{series: core.MetadataSeriesDetail{
		MetadataSeries: core.MetadataSeries{MetadataTitle: detailTestTitle(core.MediaKindSeries, "1399", "Game of Thrones")},
		Details:        core.MetadataDetails{IMDbID: "tt0944947"},
	}}
	manager := &ratingsManagerStub{ratings: core.TitleRatings{
		IMDb: &core.TitleRating{Score: 9.2, Votes: 2_400_000, Source: core.RatingSourceOMDb},
	}}
	recorder := serveRatings(t, reader, manager, "/api/v1/metadata/series/1399/ratings", "1399", (*Server).handleSeriesRatings)
	assertCompactJSON(t, recorder.Body.Bytes(), `{"imdb":{"score":9.2,"votes":2400000,"source":"omdb"}}`)
	want := core.RatedTitle{Kind: core.MediaKindSeries, TMDBID: "1399", IMDbID: "tt0944947", Title: "Game of Thrones"}
	if len(manager.titles) != 1 || manager.titles[0] != want {
		t.Fatalf("ratings read for %+v", manager.titles)
	}
}

func TestRatingsDropAnInvalidIMDbIDAndStillAsk(t *testing.T) {
	reader := &metadataReaderStub{movie: core.MetadataMovieDetail{
		MetadataTitle: detailTestTitle(core.MediaKindMovie, "7", "Film"), Details: core.MetadataDetails{IMDbID: "nm0000206"},
	}}
	manager := &ratingsManagerStub{}
	recorder := serveRatings(t, reader, manager, "/api/v1/metadata/movies/7/ratings", "7", (*Server).handleMovieRatings)
	if recorder.Code != http.StatusOK || len(manager.titles) != 1 || manager.titles[0].IMDbID != "" {
		t.Fatalf("status = %d, ratings calls = %+v", recorder.Code, manager.titles)
	}
	assertCompactJSON(t, recorder.Body.Bytes(), `{}`)
	assertJSONMatchesSchema(t, loadOpenAPI(t), recorder.Body.Bytes(), "#/components/schemas/TitleRatings")
}

func TestRatingsMapFailures(t *testing.T) {
	reader := &metadataReaderStub{movie: core.MetadataMovieDetail{Details: core.MetadataDetails{IMDbID: "tt0133093"}}}
	cases := []struct {
		name   string
		id     string
		reader *metadataReaderStub
		err    error
		want   int
	}{
		{"bad id", "abc", reader, nil, http.StatusUnprocessableEntity},
		{"detail not found", "7", &metadataReaderStub{detailErr: core.ErrNotFound}, nil, http.StatusNotFound},
		{"omdb unavailable", "7", reader, core.ErrMetadataUnavailable, http.StatusServiceUnavailable},
		{"omdb rejected key", "7", reader, core.ErrMetadataUnauthorized, http.StatusBadGateway},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			manager := &ratingsManagerStub{err: tc.err}
			recorder := serveRatings(t, tc.reader, manager, "/api/v1/metadata/movies/"+tc.id+"/ratings", tc.id, (*Server).handleMovieRatings)
			if recorder.Code != tc.want {
				t.Fatalf("status = %d, want %d: %s", recorder.Code, tc.want, recorder.Body.String())
			}
		})
	}
}

func TestRatingsRequireAnAccount(t *testing.T) {
	server := &Server{logger: slog.New(slog.DiscardHandler), metadataReader: &metadataReaderStub{}, ratings: &ratingsManagerStub{}}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/metadata/movies/7/ratings", nil)
	request.SetPathValue("id", "7")
	recorder := httptest.NewRecorder()
	server.handleMovieRatings(recorder, request)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d", recorder.Code)
	}
}

func omdbKeyServer(manager *ratingsManagerStub, audit *recordingAudit) *Server {
	return &Server{
		logger: slog.New(slog.DiscardHandler), maxBodyBytes: 8192, ratings: manager,
		audit: audit, auditFailureMetrics: telemetry.NopMetrics{},
	}
}

func TestSetOMDbKeyStoresAValidKeyAndAudits(t *testing.T) {
	manager, audit := &ratingsManagerStub{}, &recordingAudit{}
	request := requestWithAccount(t, http.MethodPut, "/api/v1/metadata/providers/omdb/key", `{"api_key":"a1b2c3d4"}`)
	recorder := httptest.NewRecorder()
	omdbKeyServer(manager, audit).handleSetOMDbKey(recorder, request)
	if recorder.Code != http.StatusOK || manager.key != "a1b2c3d4" {
		t.Fatalf("status = %d, stored key %q: %s", recorder.Code, manager.key, recorder.Body.String())
	}
	assertCompactJSON(t, recorder.Body.Bytes(), `{"configured":true}`)
	events := audit.snapshot()
	if len(events) != 1 || events[0].Resource != "metadata_provider:omdb" || events[0].Result != telemetry.AuditSuccess {
		t.Fatalf("audit events = %+v", events)
	}
	if strings.Contains(events[0].Action+events[0].Resource, "a1b2c3d4") {
		t.Fatalf("audit event carries the key: %+v", events[0])
	}
}

func TestSetOMDbKeyRejectsMalformedKeys(t *testing.T) {
	for _, body := range []string{`{"api_key":"short"}`, `{"api_key":"has spaces!"}`, `{}`, `{"api_key":"a1b2c3d4","x":1}`} {
		manager := &ratingsManagerStub{}
		request := requestWithAccount(t, http.MethodPut, "/api/v1/metadata/providers/omdb/key", body)
		recorder := httptest.NewRecorder()
		omdbKeyServer(manager, &recordingAudit{}).handleSetOMDbKey(recorder, request)
		var response httputil.ErrorResponse
		if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
			t.Fatalf("decode %s: %v", body, err)
		}
		if recorder.Code != http.StatusUnprocessableEntity || len(response.Fields) != 1 ||
			response.Fields[0].Code != "omdb_key_invalid" || manager.key != "" {
			t.Fatalf("%s: status %d, fields %+v, stored %q", body, recorder.Code, response.Fields, manager.key)
		}
	}
}

func TestSetOMDbKeyAuditsARejectedKey(t *testing.T) {
	manager := &ratingsManagerStub{setKeyFn: func(string) error { return core.ErrMetadataUnauthorized }}
	audit := &recordingAudit{}
	request := requestWithAccount(t, http.MethodPut, "/api/v1/metadata/providers/omdb/key", `{"api_key":"a1b2c3d4"}`)
	recorder := httptest.NewRecorder()
	omdbKeyServer(manager, audit).handleSetOMDbKey(recorder, request)
	events := audit.snapshot()
	if recorder.Code != http.StatusBadGateway || len(events) != 1 || events[0].Result != telemetry.AuditFailure {
		t.Fatalf("status = %d, audit = %+v", recorder.Code, events)
	}
}

func TestOMDbKeyPresenceAndRemoval(t *testing.T) {
	manager, audit := &ratingsManagerStub{present: true}, &recordingAudit{}
	server := omdbKeyServer(manager, audit)
	recorder := httptest.NewRecorder()
	server.handleOMDbKeyPresence(recorder, requestWithAccount(t, http.MethodGet, "/api/v1/metadata/providers/omdb/key", ""))
	assertCompactJSON(t, recorder.Body.Bytes(), `{"configured":true}`)
	recorder = httptest.NewRecorder()
	server.handleDeleteOMDbKey(recorder, requestWithAccount(t, http.MethodDelete, "/api/v1/metadata/providers/omdb/key", ""))
	if recorder.Code != http.StatusNoContent || !manager.removed || len(audit.snapshot()) != 1 {
		t.Fatalf("delete status = %d, removed = %t, audit = %+v", recorder.Code, manager.removed, audit.snapshot())
	}
}
