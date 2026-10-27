package http

import (
	"time"

	"github.com/BonzTM/bloom/internal/core"
)

// The rich detail a title page shows. Every field carries an explicit
// lowercase key: core structs never cross the wire.
type metadataDetailsResponse struct {
	Tagline          string                          `json:"tagline"`
	Status           string                          `json:"status"`
	Certification    string                          `json:"certification"`
	RuntimeMinutes   int                             `json:"runtime_minutes"`
	VoteAverage      float64                         `json:"vote_average"`
	VoteCount        int                             `json:"vote_count"`
	OriginalLanguage string                          `json:"original_language"`
	IMDbID           string                          `json:"imdb_id"`
	TrailerKey       string                          `json:"trailer_youtube_key"`
	Budget           int64                           `json:"budget"`
	Revenue          int64                           `json:"revenue"`
	Genres           []string                        `json:"genres"`
	Keywords         []string                        `json:"keywords"`
	Countries        []metadataCountryResponse       `json:"production_countries"`
	Studios          []metadataCompanyResponse       `json:"studios"`
	ReleaseDates     []metadataReleaseDateResponse   `json:"release_dates"`
	WatchProviders   []metadataWatchProviderResponse `json:"watch_providers"`
	Cast             []metadataCreditResponse        `json:"cast"`
	Crew             []metadataCreditResponse        `json:"crew"`
	Recommendations  []metadataDiscoverItemResponse  `json:"recommendations"`
	Similar          []metadataDiscoverItemResponse  `json:"similar"`
	Series           *metadataSeriesFactsResponse    `json:"series,omitempty"`
}

type metadataCreditResponse struct {
	PersonID    int    `json:"person_id"`
	Name        string `json:"name"`
	Role        string `json:"role"`
	ProfilePath string `json:"profile_path"`
}

type metadataCompanyResponse struct {
	ID       int    `json:"id"`
	Name     string `json:"name"`
	LogoPath string `json:"logo_path"`
}

type metadataCountryResponse struct {
	Code string `json:"code"`
	Name string `json:"name"`
}

type metadataReleaseDateResponse struct {
	Type core.MetadataReleaseType `json:"type"`
	Date time.Time                `json:"date"`
}

type metadataWatchProviderResponse struct {
	ID       int    `json:"id"`
	Name     string `json:"name"`
	LogoPath string `json:"logo_path"`
}

type metadataSeriesFactsResponse struct {
	Creators     []metadataCreditResponse  `json:"creators"`
	Networks     []metadataCompanyResponse `json:"networks"`
	FirstAirDate *time.Time                `json:"first_air_date,omitempty"`
	LastAirDate  *time.Time                `json:"last_air_date,omitempty"`
	NextEpisode  *metadataEpisodeResponse  `json:"next_episode,omitempty"`
	SeasonCount  int                       `json:"season_count"`
	EpisodeCount int                       `json:"episode_count"`
	InProduction bool                      `json:"in_production"`
}

type metadataEpisodeResponse struct {
	Season  int        `json:"season"`
	Episode int        `json:"episode"`
	Name    string     `json:"name"`
	AirDate *time.Time `json:"air_date,omitempty"`
}

func metadataDetailsDTO(details core.MetadataDetails, kind core.MediaKind) metadataDetailsResponse {
	response := metadataDetailsResponse{
		Tagline: details.Tagline, Status: details.Status, Certification: details.Certification,
		RuntimeMinutes: details.RuntimeMinutes, VoteAverage: details.VoteAverage, VoteCount: details.VoteCount,
		OriginalLanguage: details.OriginalLanguage, IMDbID: details.IMDbID, TrailerKey: details.TrailerKey,
		Budget: details.Budget, Revenue: details.Revenue,
		Genres: nonNilStrings(details.Genres), Keywords: nonNilStrings(details.Keywords),
		Countries: mapDTOs(details.Countries, metadataCountryDTO), Studios: mapDTOs(details.Studios, metadataCompanyDTO),
		ReleaseDates:    mapDTOs(details.ReleaseDates, metadataReleaseDateDTO),
		WatchProviders:  mapDTOs(details.WatchProviders, metadataWatchProviderDTO),
		Cast:            mapDTOs(details.Cast, metadataCreditDTO),
		Crew:            mapDTOs(details.Crew, metadataCreditDTO),
		Recommendations: mapDTOs(details.Recommendations, metadataDiscoverItemDTO),
		Similar:         mapDTOs(details.Similar, metadataDiscoverItemDTO),
	}
	if kind == core.MediaKindSeries {
		facts := metadataSeriesFactsDTO(details.Series)
		response.Series = &facts
	}
	return response
}

func metadataSeriesFactsDTO(facts core.MetadataSeriesFacts) metadataSeriesFactsResponse {
	response := metadataSeriesFactsResponse{
		Creators: mapDTOs(facts.Creators, metadataCreditDTO), Networks: mapDTOs(facts.Networks, metadataCompanyDTO),
		FirstAirDate: facts.FirstAirDate, LastAirDate: facts.LastAirDate,
		SeasonCount: facts.SeasonCount, EpisodeCount: facts.EpisodeCount, InProduction: facts.InProduction,
	}
	if next := facts.NextEpisode; next != nil {
		response.NextEpisode = &metadataEpisodeResponse{
			Season: next.Season, Episode: next.Episode, Name: next.Name, AirDate: next.AirDate,
		}
	}
	return response
}

// mapDTOs maps a list to its wire shape, always as an array, never null.
func mapDTOs[T, R any](items []T, toDTO func(T) R) []R {
	mapped := make([]R, 0, len(items))
	for _, item := range items {
		mapped = append(mapped, toDTO(item))
	}
	return mapped
}

func nonNilStrings(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

func metadataCreditDTO(credit core.MetadataCredit) metadataCreditResponse {
	return metadataCreditResponse{
		PersonID: credit.PersonID, Name: credit.Name, Role: credit.Role, ProfilePath: credit.ProfilePath,
	}
}

func metadataCompanyDTO(company core.MetadataCompany) metadataCompanyResponse {
	return metadataCompanyResponse{ID: company.ID, Name: company.Name, LogoPath: company.LogoPath}
}

func metadataCountryDTO(country core.MetadataCountry) metadataCountryResponse {
	return metadataCountryResponse{Code: country.Code, Name: country.Name}
}

func metadataReleaseDateDTO(release core.MetadataReleaseDate) metadataReleaseDateResponse {
	return metadataReleaseDateResponse{Type: release.Type, Date: release.Date}
}

func metadataWatchProviderDTO(provider core.MetadataWatchProvider) metadataWatchProviderResponse {
	return metadataWatchProviderResponse{ID: provider.ID, Name: provider.Name, LogoPath: provider.LogoPath}
}

func metadataDiscoverItemDTO(item core.MetadataDiscoverItem) metadataDiscoverItemResponse {
	return metadataDiscoverItemResponse{
		metadataTitleResponse: metadataTitleDTO(item.MetadataTitle), RequestState: item.RequestState,
	}
}
