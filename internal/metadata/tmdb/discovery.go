package tmdb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/BonzTM/bloom/internal/core"
	tmdbapi "github.com/BonzTM/bloom/internal/metadata/tmdb/api"
)

type providerResponse interface {
	GetBody() []byte
	StatusCode() int
}

type rawDiscoverPage struct {
	Page       *int               `json:"page"`
	Results    *[]json.RawMessage `json:"results"`
	TotalPages *int               `json:"total_pages"`
}

type rawDiscoverTitle struct {
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

type rawGenreList struct {
	Genres *[]rawGenre `json:"genres"`
}

type rawGenre struct {
	ID   *int    `json:"id"`
	Name *string `json:"name"`
}

// Discover returns one of Bloom's bounded TMDB discovery rows.
func (c *Client) Discover(ctx context.Context, input core.MetadataDiscover) (core.MetadataPage, error) {
	if err := core.ValidateMetadataDiscover(input); err != nil {
		return core.MetadataPage{}, err
	}
	ctx, cancel := c.operationContext(ctx)
	defer cancel()
	operation := "discover_" + string(input.List)
	started := c.clock.Now()
	httpResponse, callErr := c.callDiscover(ctx, input) //nolint:bodyclose // readProviderResponse closes every returned body.
	response, err := readProviderResponse(httpResponse, callErr)
	status := responseStatus(response, err)
	if err != nil {
		c.observe(operation, started, status, err)
		return core.MetadataPage{}, classifyCallError(operation, err)
	}
	if statusErr := validateDiscoveryStatus(operation, status); statusErr != nil {
		c.observe(operation, started, status, statusErr)
		return core.MetadataPage{}, statusErr
	}
	page, skipped, err := decodeDiscoverPage(response.GetBody(), input)
	c.observe(operation, started, status, err)
	c.observeSkipped(operation, skipped)
	return page, err
}

func (c *Client) callDiscover(ctx context.Context, input core.MetadataDiscover) (*http.Response, error) {
	page := int32(input.Page) //nolint:gosec // ValidateMetadataDiscover bounds this value to 1..20.
	switch input.List {
	case core.MetadataTrending:
		return c.api.TrendingAll(ctx, tmdbapi.Week, nil, withPage(page))
	case core.MetadataMoviesPopular:
		return c.api.MoviePopularList(ctx, &tmdbapi.MoviePopularListParams{Page: &page})
	case core.MetadataSeriesPopular:
		return c.api.TvSeriesPopularList(ctx, &tmdbapi.TvSeriesPopularListParams{Page: &page})
	case core.MetadataMoviesUpcoming:
		return c.api.MovieUpcomingList(ctx, &tmdbapi.MovieUpcomingListParams{Page: &page})
	case core.MetadataSeriesUpcoming:
		return c.api.TvSeriesOnTheAirList(ctx, &tmdbapi.TvSeriesOnTheAirListParams{Page: &page})
	default:
		return nil, core.ErrInvalidArgument
	}
}

func withPage(page int32) tmdbapi.RequestEditorFn {
	return func(_ context.Context, request *http.Request) error {
		query := request.URL.Query()
		query.Set("page", strconv.FormatInt(int64(page), 10))
		request.URL.RawQuery = query.Encode()
		return nil
	}
}

func decodeDiscoverPage(body []byte, input core.MetadataDiscover) (core.MetadataPage, int, error) {
	var raw rawDiscoverPage
	if err := json.Unmarshal(body, &raw); err != nil {
		return core.MetadataPage{}, 0, malformedDiscover(input.List, err)
	}
	if raw.Page == nil || raw.Results == nil || raw.TotalPages == nil || *raw.Page != input.Page ||
		*raw.TotalPages < 0 || len(*raw.Results) > core.MetadataPageSize {
		return core.MetadataPage{}, 0, malformedDiscover(input.List, core.ErrInvalidArgument)
	}
	items := make([]core.MetadataTitle, 0, len(*raw.Results))
	skipped := 0
	for _, encoded := range *raw.Results {
		var result rawDiscoverTitle
		if err := json.Unmarshal(encoded, &result); err != nil {
			skipped++
			continue
		}
		if input.List == core.MetadataTrending {
			if _, ok := resultKind(value(result.MediaType)); !ok {
				continue
			}
		}
		if title, ok := mapDiscoverTitle(result, input.List); ok {
			items = append(items, title)
		} else {
			skipped++
		}
	}
	page := core.MetadataPage{Items: items, Page: input.Page, TotalPages: min(*raw.TotalPages, core.MaxMetadataPage)}
	return page, skipped, nil
}

func mapDiscoverTitle(raw rawDiscoverTitle, list core.MetadataDiscoverList) (core.MetadataTitle, bool) {
	kind, ok := discoverKind(list, value(raw.MediaType))
	if !ok || raw.ID == nil {
		return core.MetadataTitle{}, false
	}
	title, date := value(raw.Title), value(raw.ReleaseDate)
	if kind == core.MediaKindSeries {
		title, date = value(raw.Name), value(raw.FirstAirDate)
	}
	item := core.MetadataTitle{
		Kind: kind, Provider: core.MetadataProviderTMDB, ProviderID: strconv.Itoa(*raw.ID), Title: title,
		Year: yearFromDate(date), Overview: value(raw.Overview), PosterPath: value(raw.PosterPath),
		BackdropPath: value(raw.BackdropPath),
	}
	return item, core.ValidateMetadataTitle(item) == nil
}

func discoverKind(list core.MetadataDiscoverList, mediaType string) (core.MediaKind, bool) {
	switch list {
	case core.MetadataMoviesPopular, core.MetadataMoviesUpcoming:
		return core.MediaKindMovie, true
	case core.MetadataSeriesPopular, core.MetadataSeriesUpcoming:
		return core.MediaKindSeries, true
	case core.MetadataTrending:
		return resultKind(mediaType)
	default:
		return "", false
	}
}

// Genres returns the TMDB genre list for one media kind.
func (c *Client) Genres(ctx context.Context, kind core.MediaKind) ([]core.MetadataGenre, error) {
	if !kind.Valid() {
		return nil, core.ErrInvalidArgument
	}
	ctx, cancel := c.operationContext(ctx)
	defer cancel()
	operation := "genres_" + string(kind)
	started := c.clock.Now()
	response, err := c.callGenres(ctx, kind)
	status := responseStatus(response, err)
	c.observe(operation, started, status, err)
	if err != nil {
		return nil, classifyCallError(operation, err)
	}
	if err := validateDiscoveryStatus(operation, status); err != nil {
		return nil, err
	}
	return decodeGenres(response.GetBody(), operation)
}

func (c *Client) callGenres(ctx context.Context, kind core.MediaKind) (providerResponse, error) {
	if kind == core.MediaKindMovie {
		return c.api.GenreMovieListWithResponse(ctx, nil)
	}
	return c.api.GenreTvListWithResponse(ctx, nil)
}

func decodeGenres(body []byte, operation string) ([]core.MetadataGenre, error) {
	var raw rawGenreList
	if err := json.Unmarshal(body, &raw); err != nil || raw.Genres == nil {
		return nil, malformedGenres(operation, err)
	}
	genres := make([]core.MetadataGenre, 0, len(*raw.Genres))
	for _, item := range *raw.Genres {
		if item.ID == nil || item.Name == nil {
			return nil, malformedGenres(operation, core.ErrInvalidArgument)
		}
		genres = append(genres, core.MetadataGenre{ID: *item.ID, Name: *item.Name})
	}
	if err := core.ValidateMetadataGenres(genres); err != nil {
		return nil, malformedGenres(operation, err)
	}
	return genres, nil
}

func responseStatus(response providerResponse, err error) int {
	if responseErr, ok := errors.AsType[*responseReceivedError](err); ok {
		return responseErr.status
	}
	if err != nil || response == nil {
		return 0
	}
	return response.StatusCode()
}

func validateDiscoveryStatus(operation string, status int) error {
	if status == http.StatusNotFound {
		return classifyError(operation, status, core.ErrMetadataUnavailable)
	}
	return validateStatus(operation, status)
}

func malformedDiscover(list core.MetadataDiscoverList, err error) error {
	return classifyError("discover_"+string(list), http.StatusOK, errors.Join(core.ErrMetadataMalformed, err))
}

func malformedGenres(operation string, err error) error {
	if err == nil {
		err = core.ErrInvalidArgument
	}
	return fmt.Errorf("tmdb %s response: %w", operation, errors.Join(core.ErrMetadataMalformed, err))
}
