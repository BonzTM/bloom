package tmdb

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/testutil"
)

// Trimmed from a real /3/movie/{id}?append_to_response=... answer, with one
// malformed item in each list to prove it is dropped and counted.
const movieDetailFixture = `{"id":1084242,"title":"The Dog Stars","release_date":"2026-08-28",
"overview":"After the pandemic.","poster_path":"/poster.jpg","backdrop_path":"/backdrop.jpg",
"tagline":"At the end of the world, no one survives alone. ","status":"Released","runtime":119,
"vote_average":6.6,"vote_count":120,"original_language":"en","imdb_id":"tt9850370",
"budget":110000000,"revenue":37416959,
"genres":[{"id":28,"name":"Action"},{"id":53,"name":"Thriller"},{"id":878,"name":""}],
"production_countries":[{"iso_3166_1":"US","name":"United States of America"},{"iso_3166_1":"usa","name":"Bad"}],
"production_companies":[{"id":25,"logo_path":"/fox.png","name":"20th Century Studios"},{"id":0,"name":"No ID"}],
"credits":{"cast":[
 {"id":1,"name":"Jacob Elordi","character":"Hig","profile_path":"/elordi.jpg","order":0},
 {"id":2,"name":"Josh Brolin","character":"Bangley","profile_path":null,"order":1},
 {"id":3,"name":"","character":"Nobody"}],
 "crew":[
 {"id":20,"name":"Mark L. Smith","job":"Producer"},
 {"id":10,"name":"Ridley Scott","job":"Director","profile_path":"/scott.jpg"},
 {"id":20,"name":"Mark L. Smith","job":"Screenplay"},
 {"id":10,"name":"Ridley Scott","job":"Director"},
 {"id":30,"name":"Grip","job":"Key Grip"}]},
"keywords":{"keywords":[{"id":1,"name":"dystopia"},{"id":2,"name":"pandemic"}]},
"release_dates":{"results":[
 {"iso_3166_1":"GB","release_dates":[{"certification":"15","release_date":"2026-08-27T00:00:00.000Z","type":3}]},
 {"iso_3166_1":"US","release_dates":[
  {"certification":"","release_date":"2026-12-01T00:00:00.000Z","type":5},
  {"certification":"R","release_date":"2026-08-28T00:00:00.000Z","type":3},
  {"certification":"R","release_date":"2026-10-27T00:00:00.000Z","type":4},
  {"certification":"R","release_date":"2026-10-30T00:00:00.000Z","type":4},
  {"certification":"","release_date":"not a date","type":1}]}]},
"recommendations":{"page":1,"results":[
 {"id":41,"media_type":"movie","title":"I Am Legend","release_date":"2007-12-14","poster_path":"/legend.jpg"},
 {"id":42,"media_type":"tv","name":"The Last of Us","first_air_date":"2023-01-15"},
 {"id":43,"media_type":"person","name":"Someone"}]},
"similar":{"page":1,"results":[{"id":51,"title":"The Road","release_date":"2009-11-25"},{"title":"No ID"}]},
"videos":{"results":[
 {"key":"teaser01234","site":"YouTube","type":"Teaser","official":true},
 {"key":"trailer0123","site":"YouTube","type":"Trailer","official":false},
 {"key":"official012","site":"YouTube","type":"Trailer","official":true},
 {"key":"vimeo","site":"Vimeo","type":"Trailer","official":true}]},
"watch/providers":{"results":{
 "US":{"link":"https://www.themoviedb.org/movie/1084242/watch","flatrate":[{"provider_id":337,"provider_name":"Disney Plus","logo_path":"/disney.jpg"}],
  "rent":[{"provider_id":2,"provider_name":"Apple TV","logo_path":"/apple.jpg"}],
  "ads":[{"provider_id":337,"provider_name":"Disney Plus","logo_path":"/disney.jpg"},{"provider_id":300,"provider_name":"Pluto TV","logo_path":"../x"}]},
 "GB":{"flatrate":[{"provider_id":8,"provider_name":"Netflix"}]}}},
"external_ids":{"imdb_id":"tt9850370"}}`

const seriesDetailFixture = `{"id":1399,"name":"Show","first_air_date":"2011-04-17","last_air_date":"2019-05-19",
"overview":"Plot","episode_run_time":[0,60],"status":"Ended","in_production":false,
"number_of_seasons":8,"number_of_episodes":73,"vote_average":8.4,"vote_count":20000,
"original_language":"en","tagline":"","imdb_id":null,
"created_by":[{"id":9813,"name":"David Benioff","profile_path":"/benioff.jpg"},{"id":0,"name":"Bad"}],
"networks":[{"id":49,"name":"HBO","logo_path":"/hbo.png"}],
"next_episode_to_air":{"season_number":9,"episode_number":1,"name":"Return","air_date":"2027-01-02"},
"seasons":[{"season_number":0,"name":"Specials","episode_count":2},{"season_number":1,"name":"Season 1","episode_count":10,"air_date":"2011-04-17"}],
"keywords":{"results":[{"id":1,"name":"dragon"}]},
"content_ratings":{"results":[{"iso_3166_1":"DE","rating":"16"},{"iso_3166_1":"US","rating":"TV-MA"}]},
"recommendations":{"results":[{"id":60,"media_type":"tv","name":"Other Show","first_air_date":"2020-01-01"}]},
"similar":{"results":[{"id":61,"name":"Similar Show","first_air_date":"2015-01-01"}]},
"external_ids":{"imdb_id":"tt0944947"}}`

func TestClientMovieDetailMapsAppendedResponse(t *testing.T) {
	t.Parallel()
	metrics := &metadataMetricsRecorder{}
	client := newDetailClient(t, "/3/movie/1084242", movieDetailAppend, movieDetailFixture, metrics)
	movie, err := client.MovieDetail(t.Context(), "1084242")
	if err != nil {
		t.Fatalf("MovieDetail: %v", err)
	}
	if movie.Title != "The Dog Stars" || movie.Year != 2026 || movie.Kind != core.MediaKindMovie {
		t.Fatalf("title = %+v", movie.MetadataTitle)
	}
	assertMovieFacts(t, movie.Details)
	assertMovieLists(t, movie.Details)
	assertMovieRelated(t, movie.Details)
	// genre, country, studio, cast member, recommendation, similar title, and provider logo path.
	if got := metrics.skipped["movie_detail"]; got != 6 {
		t.Fatalf("skipped = %d, want 6", got)
	}
}

func assertMovieFacts(t *testing.T, details core.MetadataDetails) {
	t.Helper()
	if details.Tagline != "At the end of the world, no one survives alone." || details.Status != "Released" ||
		details.RuntimeMinutes != 119 || details.VoteAverage != 6.6 || details.VoteCount != 120 ||
		details.OriginalLanguage != "en" || details.IMDbID != "tt9850370" || details.TrailerKey != "official012" ||
		details.Budget != 110000000 || details.Revenue != 37416959 || details.Certification != "R" {
		t.Fatalf("facts = %+v", details)
	}
	want := []core.MetadataReleaseDate{
		{Type: core.MetadataReleaseTheatrical, Date: time.Date(2026, 8, 28, 0, 0, 0, 0, time.UTC)},
		{Type: core.MetadataReleaseDigital, Date: time.Date(2026, 10, 27, 0, 0, 0, 0, time.UTC)},
		{Type: core.MetadataReleasePhysical, Date: time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)},
	}
	if fmt.Sprint(details.ReleaseDates) != fmt.Sprint(want) {
		t.Fatalf("release dates = %v, want %v", details.ReleaseDates, want)
	}
}

func assertMovieLists(t *testing.T, details core.MetadataDetails) {
	t.Helper()
	if fmt.Sprint(details.Genres) != "[Action Thriller]" || fmt.Sprint(details.Keywords) != "[dystopia pandemic]" {
		t.Fatalf("genres = %v, keywords = %v", details.Genres, details.Keywords)
	}
	if len(details.Countries) != 1 || details.Countries[0].Code != "US" || len(details.Studios) != 1 ||
		details.Studios[0].LogoPath != "/fox.png" {
		t.Fatalf("countries = %+v, studios = %+v", details.Countries, details.Studios)
	}
	if len(details.Cast) != 2 || details.Cast[0].Role != "Hig" || details.Cast[1].ProfilePath != "" {
		t.Fatalf("cast = %+v", details.Cast)
	}
	crew := fmt.Sprint(details.Crew)
	if crew != "[{10 Ridley Scott Director /scott.jpg} {20 Mark L. Smith Screenplay } {20 Mark L. Smith Producer }]" {
		t.Fatalf("crew = %s", crew)
	}
	if fmt.Sprint(details.WatchProviders) != "[{337 Disney Plus /disney.jpg} {300 Pluto TV }]" {
		t.Fatalf("providers = %+v", details.WatchProviders)
	}
}

func assertMovieRelated(t *testing.T, details core.MetadataDetails) {
	t.Helper()
	if len(details.Recommendations) != 2 || details.Recommendations[1].Kind != core.MediaKindSeries ||
		details.Recommendations[0].RequestState != core.MetadataRequestNone {
		t.Fatalf("recommendations = %+v", details.Recommendations)
	}
	if len(details.Similar) != 1 || details.Similar[0].Kind != core.MediaKindMovie || details.Similar[0].Year != 2009 {
		t.Fatalf("similar = %+v", details.Similar)
	}
	if len(details.Series.Creators) != 0 || details.Series.FirstAirDate != nil {
		t.Fatalf("movie series facts = %+v", details.Series)
	}
}

func TestClientSeriesDetailMapsSeriesFacts(t *testing.T) {
	t.Parallel()
	metrics := &metadataMetricsRecorder{}
	client := newDetailClient(t, "/3/tv/1399", seriesDetailAppend, seriesDetailFixture, metrics)
	series, err := client.SeriesDetail(t.Context(), "1399", false)
	if err != nil {
		t.Fatalf("SeriesDetail: %v", err)
	}
	details := series.Details
	if len(series.Seasons) != 1 || details.Certification != "TV-MA" || details.RuntimeMinutes != 60 ||
		details.IMDbID != "tt0944947" || fmt.Sprint(details.Keywords) != "[dragon]" || details.Budget != 0 ||
		len(details.ReleaseDates) != 0 || len(details.Similar) != 1 || details.Similar[0].Kind != core.MediaKindSeries {
		t.Fatalf("series = %+v", series)
	}
	facts := details.Series
	next := facts.NextEpisode
	if len(facts.Creators) != 1 || facts.Creators[0].Role != "Creator" || len(facts.Networks) != 1 ||
		facts.SeasonCount != 8 || facts.EpisodeCount != 73 || facts.LastAirDate == nil ||
		next == nil || next.Season != 9 || next.AirDate == nil || next.AirDate.Year() != 2027 {
		t.Fatalf("series facts = %+v", facts)
	}
	if got := metrics.skipped["series_detail"]; got != 1 {
		t.Fatalf("skipped = %d, want 1", got)
	}
}

func TestDecodeDetailsKeepsFieldsAroundATypeMismatch(t *testing.T) {
	t.Parallel()
	body := []byte(`{"id":1,"title":"T","tagline":"Kept","runtime":"long","genres":[{"id":1,"name":"Drama"}]}`)
	details, skipped := decodeDetails(body, core.MediaKindMovie)
	if details.Tagline != "Kept" || details.RuntimeMinutes != 0 || len(details.Genres) != 1 || skipped != 1 {
		t.Fatalf("decodeDetails = %+v, %d", details, skipped)
	}
}

func TestDecodeDetailsBoundsEveryList(t *testing.T) {
	t.Parallel()
	cast := make([]string, 0, core.MaxMetadataCast+5)
	for index := range core.MaxMetadataCast + 5 {
		cast = append(cast, fmt.Sprintf(`{"id":%d,"name":"Actor %d"}`, index+1, index))
	}
	crew := make([]string, 0, core.MaxMetadataKeyCrew+5)
	for index := range core.MaxMetadataKeyCrew + 5 {
		crew = append(crew, fmt.Sprintf(`{"id":%d,"name":"Producer %d","job":"Producer"}`, index+1, index))
	}
	body := []byte(`{"id":1,"title":"T","credits":{"cast":[` + strings.Join(cast, ",") +
		`],"crew":[` + strings.Join(crew, ",") + `]}}`)
	details, _ := decodeDetails(body, core.MediaKindMovie)
	if len(details.Cast) != core.MaxMetadataCast || len(details.Crew) != core.MaxMetadataKeyCrew {
		t.Fatalf("cast = %d, crew = %d", len(details.Cast), len(details.Crew))
	}
}

func TestClientMovieDetailClassifiesNotFound(t *testing.T) {
	t.Parallel()
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusNotFound, Header: http.Header{"Content-Type": []string{"application/json"}},
			Body: io.NopCloser(strings.NewReader(`{"success":false}`)), Request: request,
		}, nil
	})
	client := newTransportClient(t, transport, nil)
	if _, err := client.MovieDetail(t.Context(), "404"); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("MovieDetail(404) = %v, want ErrNotFound", err)
	}
	if _, err := client.SeriesDetail(t.Context(), "0", false); err == nil {
		t.Fatal("SeriesDetail(0) accepted an invalid id")
	}
}

func newDetailClient(t *testing.T, path, appended, body string, metrics Metrics) *Client {
	t.Helper()
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Path != path || request.URL.Query().Get("append_to_response") != appended {
			t.Errorf("request = %s, want %s?append_to_response=%s", request.URL, path, appended)
		}
		return &http.Response{
			StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}},
			Body: io.NopCloser(strings.NewReader(body)), Request: request,
		}, nil
	})
	return newTransportClient(t, transport, metrics)
}

func newTransportClient(t *testing.T, transport http.RoundTripper, metrics Metrics) *Client {
	t.Helper()
	clock := testutil.NewFakeClock(time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC))
	client, err := New(testReadAccessToken, Dependencies{
		BaseURL: "https://tmdb.test", BaseTransport: transport, Clock: clock, Metrics: metrics,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(client.CloseIdleConnections)
	return client
}
