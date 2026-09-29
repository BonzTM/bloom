package tmdb

import (
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/BonzTM/bloom/internal/core"
)

const maxSeriesSeasons = 100

type rawTitleList struct {
	Results *[]json.RawMessage `json:"results"`
}

type rawTitle struct {
	BackdropPath *string `json:"backdrop_path"`
	FirstAirDate *string `json:"first_air_date"`
	ID           *int    `json:"id"`
	MediaType    *string `json:"media_type"`
	Name         *string `json:"name"`
	Overview     *string `json:"overview"`
	PosterPath   *string `json:"poster_path"`
	ReleaseDate  *string `json:"release_date"`
	Title        *string `json:"title"`
}

type rawSeries struct {
	rawTitle
	Seasons *[]json.RawMessage `json:"seasons"`
}

type rawSeason struct {
	AirDate      *string `json:"air_date"`
	EpisodeCount *int    `json:"episode_count"`
	Name         *string `json:"name"`
	SeasonNumber *int    `json:"season_number"`
}

func decodeSearchResults(body []byte, filter *core.MediaKind) ([]core.MetadataTitle, int, error) {
	var response rawTitleList
	if err := json.Unmarshal(body, &response); err != nil || response.Results == nil {
		return nil, 0, errors.Join(core.ErrMetadataMalformed, err)
	}
	items := make([]core.MetadataTitle, 0, len(*response.Results))
	skipped := 0
	for _, encoded := range *response.Results {
		var result rawTitle
		if err := json.Unmarshal(encoded, &result); err != nil {
			skipped++
			continue
		}
		kind, ok := resultKind(value(result.MediaType))
		if !ok || filter != nil && *filter != kind {
			continue
		}
		if title, ok := mapSearchTitle(result, kind); ok {
			items = append(items, title)
		} else {
			skipped++
		}
	}
	return items, skipped, nil
}

func mapSearchTitle(result rawTitle, kind core.MediaKind) (core.MetadataTitle, bool) {
	if result.ID == nil {
		return core.MetadataTitle{}, false
	}
	title, date := value(result.Title), value(result.ReleaseDate)
	if kind == core.MediaKindSeries {
		title, date = value(result.Name), value(result.FirstAirDate)
	}
	item := mapTitle(kind, *result.ID, title, date, result.Overview, result.PosterPath, result.BackdropPath)
	return item, core.ValidateMetadataTitle(item) == nil
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

func decodeMovieResponse(body []byte, status int) (core.MetadataTitle, error) {
	var raw rawTitle
	if err := json.Unmarshal(body, &raw); err != nil || raw.ID == nil || raw.Title == nil {
		return core.MetadataTitle{}, malformedTitle("movie", status, err)
	}
	title := mapTitle(
		core.MediaKindMovie, *raw.ID, *raw.Title, value(raw.ReleaseDate), raw.Overview, raw.PosterPath, raw.BackdropPath,
	)
	if err := core.ValidateMetadataTitle(title); err != nil {
		return core.MetadataTitle{}, malformedTitle("movie", status, err)
	}
	return title, nil
}

func decodeSeriesResponse(body []byte, status int, includeSpecials bool) (core.MetadataSeries, int, error) {
	var raw rawSeries
	if err := json.Unmarshal(body, &raw); err != nil || raw.ID == nil || raw.Name == nil {
		return core.MetadataSeries{}, 0, malformedTitle("series", status, err)
	}
	title := mapTitle(
		core.MediaKindSeries, *raw.ID, *raw.Name, value(raw.FirstAirDate), raw.Overview, raw.PosterPath, raw.BackdropPath,
	)
	if err := core.ValidateMetadataTitle(title); err != nil {
		return core.MetadataSeries{}, 0, malformedTitle("series", status, err)
	}
	var encoded []json.RawMessage
	if raw.Seasons != nil {
		encoded = *raw.Seasons
	}
	seasons, skipped := mapSeasons(encoded, includeSpecials)
	return core.MetadataSeries{MetadataTitle: title, Seasons: seasons}, skipped, nil
}

func mapSeasons(encoded []json.RawMessage, includeSpecials bool) ([]core.MetadataSeason, int) {
	limit := min(len(encoded), maxSeriesSeasons)
	seasons := make([]core.MetadataSeason, 0, limit)
	seen := make(map[int]struct{}, limit)
	skipped := len(encoded) - limit
	for _, item := range encoded[:limit] {
		var raw rawSeason
		if err := json.Unmarshal(item, &raw); err != nil {
			skipped++
			continue
		}
		if raw.SeasonNumber != nil && !includeSpecials && *raw.SeasonNumber == 0 {
			continue
		}
		season, ok := mapSeason(raw, includeSpecials)
		if _, duplicate := seen[season.Number]; duplicate {
			ok = false
		}
		if !ok {
			skipped++
			continue
		}
		seen[season.Number] = struct{}{}
		seasons = append(seasons, season)
	}
	return seasons, skipped
}

func mapSeason(raw rawSeason, includeSpecials bool) (core.MetadataSeason, bool) {
	if raw.SeasonNumber == nil || raw.Name == nil || raw.EpisodeCount == nil {
		return core.MetadataSeason{}, false
	}
	season := core.MetadataSeason{
		Number: *raw.SeasonNumber, Name: *raw.Name, EpisodeCount: *raw.EpisodeCount,
		AirDate: parseDate(value(raw.AirDate)),
	}
	return season, core.ValidateMetadataSeasons([]core.MetadataSeason{season}, includeSpecials) == nil
}

func mapTitle(
	kind core.MediaKind,
	id int,
	title string,
	date string,
	overview *string,
	posterPath *string,
	backdropPath *string,
) core.MetadataTitle {
	return core.MetadataTitle{
		Kind: kind, Provider: core.MetadataProviderTMDB, ProviderID: strconv.Itoa(id), Title: title,
		Year: yearFromDate(date), Overview: value(overview), PosterPath: value(posterPath), BackdropPath: value(backdropPath),
	}
}

func malformedTitle(operation string, status int, err error) error {
	if err == nil {
		err = core.ErrInvalidArgument
	}
	return classifyError(operation, status, errors.Join(core.ErrMetadataMalformed, err))
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
