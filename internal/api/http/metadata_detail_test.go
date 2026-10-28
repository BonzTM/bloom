package http

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/BonzTM/bloom/internal/core"
)

func detailTestTitle(kind core.MediaKind, id, title string) core.MetadataTitle {
	return core.MetadataTitle{
		Kind: kind, Provider: core.MetadataProviderTMDB, ProviderID: id, Title: title, Year: 2026,
		Overview: "Plot", PosterPath: "/poster.jpg", BackdropPath: "/backdrop.jpg",
	}
}

func detailTestDetails() core.MetadataDetails {
	return core.MetadataDetails{
		Tagline: "Alone", Status: "Released", Certification: "R", RuntimeMinutes: 119, VoteAverage: 6.6,
		VoteCount: 120, OriginalLanguage: "en", IMDbID: "tt9850370", TrailerKey: "official012",
		Budget: 110000000, Revenue: 37416959, Genres: []string{"Action"}, Keywords: []string{"dystopia"},
		Countries: []core.MetadataCountry{{Code: "US", Name: "United States of America"}},
		Studios:   []core.MetadataCompany{{ID: 25, Name: "20th Century Studios", LogoPath: "/fox.png"}},
		ReleaseDates: []core.MetadataReleaseDate{
			{Type: core.MetadataReleaseTheatrical, Date: time.Date(2026, 8, 28, 0, 0, 0, 0, time.UTC)},
		},
		WatchProviders: []core.MetadataWatchProvider{{ID: 337, Name: "Disney Plus", LogoPath: "/disney.jpg"}},
		Cast:           []core.MetadataCredit{{PersonID: 1, Name: "Jacob Elordi", Role: "Hig", ProfilePath: "/elordi.jpg"}},
		Crew:           []core.MetadataCredit{{PersonID: 10, Name: "Ridley Scott", Role: "Director"}},
		Recommendations: []core.MetadataDiscoverItem{{
			MetadataTitle: detailTestTitle(core.MediaKindMovie, "41", "I Am Legend"), RequestState: core.MetadataRequestAvailable,
		}},
	}
}

const movieDetailWire = `{"kind":"movie","provider":"tmdb","provider_id":"11","title":"The Dog Stars","year":2026,` +
	`"overview":"Plot","poster_path":"/poster.jpg","backdrop_path":"/backdrop.jpg","subscribed":false,` +
	`"details":{"tagline":"Alone","status":"Released","certification":"R","runtime_minutes":119,` +
	`"vote_average":6.6,"vote_count":120,"original_language":"en","imdb_id":"tt9850370",` +
	`"trailer_youtube_key":"official012","budget":110000000,"revenue":37416959,"genres":["Action"],` +
	`"keywords":["dystopia"],"production_countries":[{"code":"US","name":"United States of America"}],` +
	`"studios":[{"id":25,"name":"20th Century Studios","logo_path":"/fox.png"}],` +
	`"release_dates":[{"type":"theatrical","date":"2026-08-28T00:00:00Z"}],` +
	`"watch_providers":[{"id":337,"name":"Disney Plus","logo_path":"/disney.jpg"}],` +
	`"cast":[{"person_id":1,"name":"Jacob Elordi","role":"Hig","profile_path":"/elordi.jpg"}],` +
	`"crew":[{"person_id":10,"name":"Ridley Scott","role":"Director","profile_path":""}],` +
	`"recommendations":[{"kind":"movie","provider":"tmdb","provider_id":"41","title":"I Am Legend","year":2026,` +
	`"overview":"Plot","poster_path":"/poster.jpg","backdrop_path":"/backdrop.jpg","request_state":"available"}],` +
	`"similar":[]}}`

func TestMetadataMovieReturnsExactDetailWireShape(t *testing.T) {
	reader := &metadataReaderStub{movie: core.MetadataMovieDetail{
		MetadataTitle: detailTestTitle(core.MediaKindMovie, "11", "The Dog Stars"), Details: detailTestDetails(),
	}}
	recorder := serveMetadataTitle(t, reader, "/api/v1/metadata/movies/11", "11", (*Server).handleMetadataMovie)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", recorder.Code, recorder.Body.String())
	}
	assertCompactJSON(t, recorder.Body.Bytes(), movieDetailWire)
	assertJSONMatchesSchema(t, loadOpenAPI(t), recorder.Body.Bytes(), "#/components/schemas/MetadataTitleDetail")
	if reader.accountID != testRequestAccountID {
		t.Fatalf("detail read for account %q, want the caller", reader.accountID)
	}
}

const seriesFactsWire = `"series":{"creators":[{"person_id":9813,"name":"David Benioff","role":"Creator","profile_path":""}],` +
	`"networks":[{"id":49,"name":"HBO","logo_path":"/hbo.png"}],"first_air_date":"2011-04-17T00:00:00Z",` +
	`"next_episode":{"season":9,"episode":1,"name":"Return","air_date":"2027-01-02T00:00:00Z"},` +
	`"season_count":8,"episode_count":73,"in_production":true}}}`

func TestMetadataSeriesReturnsExactSeriesFactsWireShape(t *testing.T) {
	first := time.Date(2011, 4, 17, 0, 0, 0, 0, time.UTC)
	next := time.Date(2027, 1, 2, 0, 0, 0, 0, time.UTC)
	reader := &metadataReaderStub{series: core.MetadataSeriesDetail{
		MetadataSeries: core.MetadataSeries{
			MetadataTitle: detailTestTitle(core.MediaKindSeries, "1399", "Show"),
			Seasons:       []core.MetadataSeason{{Number: 1, Name: "Season 1", EpisodeCount: 10}},
		},
		Details: core.MetadataDetails{Series: core.MetadataSeriesFacts{
			Creators:     []core.MetadataCredit{{PersonID: 9813, Name: "David Benioff", Role: "Creator"}},
			Networks:     []core.MetadataCompany{{ID: 49, Name: "HBO", LogoPath: "/hbo.png"}},
			FirstAirDate: &first, NextEpisode: &core.MetadataEpisode{Season: 9, Episode: 1, Name: "Return", AirDate: &next},
			SeasonCount: 8, EpisodeCount: 73, InProduction: true,
		}},
	}}
	path := "/api/v1/metadata/series/1399?include_specials=true"
	recorder := serveMetadataTitle(t, reader, path, "1399", (*Server).handleMetadataSeries)
	if recorder.Code != http.StatusOK || !reader.includeSpecials {
		t.Fatalf("status = %d, specials = %t: %s", recorder.Code, reader.includeSpecials, recorder.Body.String())
	}
	want := `{"kind":"series","provider":"tmdb","provider_id":"1399","title":"Show","year":2026,"overview":"Plot",` +
		`"poster_path":"/poster.jpg","backdrop_path":"/backdrop.jpg","subscribed":false,` +
		`"seasons":[{"number":1,"name":"Season 1","episode_count":10}],` +
		`"details":{"tagline":"","status":"","certification":"","runtime_minutes":0,"vote_average":0,"vote_count":0,` +
		`"original_language":"","imdb_id":"","trailer_youtube_key":"","budget":0,"revenue":0,"genres":[],"keywords":[],` +
		`"production_countries":[],"studios":[],"release_dates":[],"watch_providers":[],"cast":[],"crew":[],` +
		`"recommendations":[],"similar":[],` + seriesFactsWire
	assertCompactJSON(t, recorder.Body.Bytes(), want)
	assertJSONMatchesSchema(t, loadOpenAPI(t), recorder.Body.Bytes(), "#/components/schemas/MetadataSeries")
}

func TestMetadataTitleDetailRejectsBadInputAndMapsFailures(t *testing.T) {
	tests := []struct {
		name   string
		path   string
		id     string
		err    error
		handle func(*Server, http.ResponseWriter, *http.Request)
		want   int
	}{
		{name: "bad specials", path: "/api/v1/metadata/series/1?include_specials=maybe", id: "1", handle: (*Server).handleMetadataSeries, want: http.StatusUnprocessableEntity},
		{name: "bad id", path: "/api/v1/metadata/movies/0", id: "0", handle: (*Server).handleMetadataMovie, want: http.StatusUnprocessableEntity},
		{name: "not found", path: "/api/v1/metadata/movies/9", id: "9", err: core.ErrNotFound, handle: (*Server).handleMetadataMovie, want: http.StatusNotFound},
		{name: "provider down", path: "/api/v1/metadata/series/9", id: "9", err: core.ErrMetadataMalformed, handle: (*Server).handleMetadataSeries, want: http.StatusBadGateway},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			reader := &metadataReaderStub{detailErr: test.err}
			recorder := serveMetadataTitle(t, reader, test.path, test.id, test.handle)
			if recorder.Code != test.want {
				t.Fatalf("status = %d, want %d: %s", recorder.Code, test.want, recorder.Body.String())
			}
		})
	}
}

func TestMetadataTitleDetailRequiresAnAccount(t *testing.T) {
	server := &Server{logger: slog.New(slog.DiscardHandler), metadataReader: &metadataReaderStub{detailErr: errors.New("unreached")}}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/metadata/movies/11", nil)
	request.SetPathValue("id", "11")
	recorder := httptest.NewRecorder()
	server.handleMetadataMovie(recorder, request)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", recorder.Code)
	}
}

func serveMetadataTitle(
	t *testing.T, reader *metadataReaderStub, path, id string, handle func(*Server, http.ResponseWriter, *http.Request),
) *httptest.ResponseRecorder {
	t.Helper()
	server := &Server{logger: slog.New(slog.DiscardHandler), metadataReader: reader}
	request := requestWithAccount(t, http.MethodGet, path, "", core.PermissionRequestsReadOwn)
	request.SetPathValue("id", id)
	recorder := httptest.NewRecorder()
	handle(server, recorder, request)
	return recorder
}

func assertCompactJSON(t *testing.T, got []byte, want string) {
	t.Helper()
	var compact bytes.Buffer
	if err := json.Compact(&compact, got); err != nil {
		t.Fatalf("compact response: %v", err)
	}
	if compact.String() != want {
		t.Fatalf("wire shape mismatch\n got: %s\nwant: %s", compact.String(), want)
	}
}
