package tmdb

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/BonzTM/bloom/internal/core"
	tmdbapi "github.com/BonzTM/bloom/internal/metadata/tmdb/api"
)

// The title routes reach the client through the metadata service's detail seam.
var _ core.MetadataDetailProvider = (*Client)(nil)

// One detail call appends everything a title page shows, so the page costs
// one bounded TMDB request instead of nine.
var (
	movieDetailAppend  = "credits,keywords,release_dates,recommendations,similar,videos,watch/providers,external_ids"
	seriesDetailAppend = "credits,keywords,content_ratings,recommendations,similar,videos,watch/providers,external_ids"
)

// MovieDetail returns a TMDB movie with the rich detail its title page shows.
func (c *Client) MovieDetail(ctx context.Context, providerID string) (core.MetadataMovieDetail, error) {
	id, err := parseProviderID(providerID)
	if err != nil {
		return core.MetadataMovieDetail{}, err
	}
	params := &tmdbapi.MovieDetailsParams{AppendToResponse: &movieDetailAppend}
	fetched, err := c.fetchTitle(ctx, "movie_detail", func(ctx context.Context) (*http.Response, error) {
		return c.api.MovieDetails(ctx, id, params)
	})
	if err != nil {
		return core.MetadataMovieDetail{}, err
	}
	title, err := decodeMovieResponse(fetched.body, fetched.status)
	c.observe("movie_detail", fetched.started, fetched.status, err)
	if err != nil {
		return core.MetadataMovieDetail{}, err
	}
	details, skipped := decodeDetails(fetched.body, core.MediaKindMovie)
	c.observeSkipped("movie_detail", skipped)
	return core.MetadataMovieDetail{MetadataTitle: title, Details: details}, nil
}

// SeriesDetail returns a TMDB series with its seasons and the rich detail its title page shows.
func (c *Client) SeriesDetail(
	ctx context.Context, providerID string, includeSpecials bool,
) (core.MetadataSeriesDetail, error) {
	id, err := parseProviderID(providerID)
	if err != nil {
		return core.MetadataSeriesDetail{}, err
	}
	params := &tmdbapi.TvSeriesDetailsParams{AppendToResponse: &seriesDetailAppend}
	fetched, err := c.fetchTitle(ctx, "series_detail", func(ctx context.Context) (*http.Response, error) {
		return c.api.TvSeriesDetails(ctx, id, params)
	})
	if err != nil {
		return core.MetadataSeriesDetail{}, err
	}
	series, skipped, err := decodeSeriesResponse(fetched.body, fetched.status, includeSpecials)
	c.observe("series_detail", fetched.started, fetched.status, err)
	if err != nil {
		return core.MetadataSeriesDetail{}, err
	}
	details, detailSkipped := decodeDetails(fetched.body, core.MediaKindSeries)
	c.observeSkipped("series_detail", skipped+detailSkipped)
	return core.MetadataSeriesDetail{MetadataSeries: series, Details: details}, nil
}

// decodeDetails maps the appended detail leniently: the title already
// decoded, so a malformed field costs that field, never the page. It
// returns how many fields or items were dropped.
func decodeDetails(body []byte, kind core.MediaKind) (core.MetadataDetails, int) {
	var raw rawDetail
	skipped := 0
	if err := json.Unmarshal(body, &raw); err != nil {
		// A type mismatch leaves the rest decoded; anything else leaves nothing.
		if _, mismatch := errors.AsType[*json.UnmarshalTypeError](err); !mismatch {
			return core.MetadataDetails{}, 1
		}
		skipped++
	}
	details, dropped := mapDetails(raw, kind)
	return details, skipped + dropped
}
