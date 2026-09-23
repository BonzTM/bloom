package tmdb

import (
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/BonzTM/bloom/internal/core"
	tmdbapi "github.com/BonzTM/bloom/internal/metadata/tmdb/api"
)

//nolint:revive // Field spellings must exactly match oapi-codegen's anonymous OpenAPI response type.
func mapSearchResults(results []struct {
	Adult            *bool    `json:"adult,omitempty"`
	BackdropPath     *string  `json:"backdrop_path,omitempty"`
	GenreIds         *[]int   `json:"genre_ids,omitempty"`
	Id               *int     `json:"id,omitempty"`
	MediaType        *string  `json:"media_type,omitempty"`
	Name             *string  `json:"name,omitempty"`
	OriginalLanguage *string  `json:"original_language,omitempty"`
	OriginalName     *string  `json:"original_name,omitempty"`
	OriginalTitle    *string  `json:"original_title,omitempty"`
	Overview         *string  `json:"overview,omitempty"`
	Popularity       *float32 `json:"popularity,omitempty"`
	PosterPath       *string  `json:"poster_path,omitempty"`
	ReleaseDate      *string  `json:"release_date,omitempty"`
	Title            *string  `json:"title,omitempty"`
	Video            *bool    `json:"video,omitempty"`
	VoteAverage      *float32 `json:"vote_average,omitempty"`
	VoteCount        *int     `json:"vote_count,omitempty"`
}, filter *core.MediaKind,
) []core.MetadataTitle {
	items := make([]core.MetadataTitle, 0, len(results))
	for _, result := range results {
		kind, ok := resultKind(value(result.MediaType))
		if !ok || filter != nil && *filter != kind || result.Id == nil {
			continue
		}
		title := value(result.Title)
		date := value(result.ReleaseDate)
		if kind == core.MediaKindSeries {
			title = value(result.Name)
		}
		item := core.MetadataTitle{
			Kind: kind, Provider: core.MetadataProviderTMDB,
			ProviderID: strconv.Itoa(*result.Id), Title: title, Year: yearFromDate(date),
			Overview: value(result.Overview), PosterPath: value(result.PosterPath),
		}
		if core.ValidateMetadataTitle(item) == nil {
			items = append(items, item)
		}
	}
	return items
}

func resultKind(mediaType string) (core.MediaKind, bool) {
	switch mediaType {
	case "movie":
		return core.MediaKindMovie, true
	case "tv":
		return core.MediaKindSeries, true
	default:
		return "", false
	}
}

func mapSeriesResponse(response *tmdbapi.TvSeriesDetailsResponse, includeSpecials bool) (core.MetadataSeries, error) {
	data := response.JSON200
	if data == nil || data.Id == nil || data.Name == nil || data.Seasons == nil {
		return core.MetadataSeries{}, classifyError("series", response.StatusCode(), core.ErrMetadataMalformed)
	}
	title := core.MetadataTitle{
		Kind: core.MediaKindSeries, Provider: core.MetadataProviderTMDB,
		ProviderID: strconv.Itoa(*data.Id), Title: *data.Name, Year: yearFromDate(value(data.FirstAirDate)),
		Overview: value(data.Overview), PosterPath: value(data.PosterPath),
	}
	if err := core.ValidateMetadataTitle(title); err != nil {
		return core.MetadataSeries{}, classifyError("series", response.StatusCode(), errors.Join(core.ErrMetadataMalformed, err))
	}
	seasons := make([]core.MetadataSeason, 0, len(*data.Seasons))
	for _, raw := range *data.Seasons {
		if raw.SeasonNumber == nil || raw.Name == nil || raw.EpisodeCount == nil || !includeSpecials && *raw.SeasonNumber == 0 {
			continue
		}
		season := core.MetadataSeason{Number: *raw.SeasonNumber, Name: *raw.Name, EpisodeCount: *raw.EpisodeCount}
		season.AirDate = parseDate(value(raw.AirDate))
		seasons = append(seasons, season)
	}
	if err := core.ValidateMetadataSeasons(seasons, includeSpecials); err != nil {
		return core.MetadataSeries{}, fmt.Errorf("tmdb series seasons: %w", errors.Join(core.ErrMetadataMalformed, err))
	}
	return core.MetadataSeries{MetadataTitle: title, Seasons: seasons}, nil
}

func parseDate(value string) *time.Time {
	if value == "" {
		return nil
	}
	parsed, err := time.Parse(time.DateOnly, value)
	if err != nil {
		return nil
	}
	return &parsed
}
