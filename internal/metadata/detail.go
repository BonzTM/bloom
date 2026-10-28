package metadata

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/BonzTM/bloom/internal/core"
)

const detailResource = "detail"

// MovieDetail returns a cached or upstream movie with its rich detail, and
// the caller's request state on each related title.
func (s *Service) MovieDetail(ctx context.Context, accountID, providerID string) (core.MetadataMovieDetail, error) {
	if !core.ValidID(accountID) || core.ValidateProviderID(providerID) != nil {
		return core.MetadataMovieDetail{}, core.ErrInvalidArgument
	}
	key := cacheKey{provider: core.MetadataProviderTMDB, resource: detailResource, kind: core.MediaKindMovie, id: providerID}
	value, err := s.cachedLoad(key, cacheTTL, func() (cacheValue, error) {
		provider, providerErr := s.detailProvider(ctx)
		if providerErr != nil {
			return cacheValue{}, providerErr
		}
		movie, loadErr := provider.MovieDetail(ctx, providerID)
		if loadErr != nil {
			return cacheValue{}, s.providerError(ctx, "load movie detail", loadErr)
		}
		return cacheValue{movie: movie}, nil
	})
	if err != nil {
		return core.MetadataMovieDetail{}, err
	}
	movie := value.movie
	movie.Details, err = s.withRelatedStates(ctx, accountID, movie.Details)
	return movie, err
}

// SeriesDetail returns a cached or upstream series with its seasons, rich
// detail, and the caller's request state on each related title.
func (s *Service) SeriesDetail(
	ctx context.Context, accountID, providerID string, includeSpecials bool,
) (core.MetadataSeriesDetail, error) {
	if !core.ValidID(accountID) || core.ValidateProviderID(providerID) != nil {
		return core.MetadataSeriesDetail{}, core.ErrInvalidArgument
	}
	key := cacheKey{provider: core.MetadataProviderTMDB, resource: detailResource, kind: core.MediaKindSeries, id: providerID}
	value, err := s.cachedLoad(key, cacheTTL, func() (cacheValue, error) {
		provider, providerErr := s.detailProvider(ctx)
		if providerErr != nil {
			return cacheValue{}, providerErr
		}
		series, loadErr := provider.SeriesDetail(ctx, providerID, true)
		if loadErr != nil {
			return cacheValue{}, s.providerError(ctx, "load series detail", loadErr)
		}
		return cacheValue{seriesDetail: series}, nil
	})
	if err != nil {
		return core.MetadataSeriesDetail{}, err
	}
	series := value.seriesDetail
	series.MetadataSeries = filterSpecials(series.MetadataSeries, includeSpecials)
	series.Details, err = s.withRelatedStates(ctx, accountID, series.Details)
	return series, err
}

func (s *Service) detailProvider(ctx context.Context) (core.MetadataDetailProvider, error) {
	provider, err := s.loadProvider(ctx, core.MetadataProviderTMDB)
	if err != nil {
		return nil, err
	}
	detail, ok := provider.(core.MetadataDetailProvider)
	if !ok {
		return nil, errors.New("metadata provider does not support title detail")
	}
	return detail, nil
}

// withRelatedStates marks each recommended and similar title with the
// caller's latest request for it, one bounded lookup per row.
func (s *Service) withRelatedStates(
	ctx context.Context, accountID string, details core.MetadataDetails,
) (core.MetadataDetails, error) {
	var err error
	if details.Recommendations, err = s.relatedStates(ctx, accountID, details.Recommendations); err != nil {
		return core.MetadataDetails{}, err
	}
	if details.Similar, err = s.relatedStates(ctx, accountID, details.Similar); err != nil {
		return core.MetadataDetails{}, err
	}
	return details, nil
}

func (s *Service) relatedStates(
	ctx context.Context, accountID string, items []core.MetadataDiscoverItem,
) ([]core.MetadataDiscoverItem, error) {
	if len(items) == 0 {
		return items, nil
	}
	titles := make([]core.MetadataTitle, 0, len(items))
	for _, item := range items {
		titles = append(titles, item.MetadataTitle)
	}
	states, err := s.states.MetadataRequestStates(ctx, accountID, titles)
	if err != nil {
		return nil, fmt.Errorf("load related request states: %w", err)
	}
	page := discoverPageWithStates(core.MetadataPage{Items: titles}, states)
	return page.Items, nil
}

func cloneDetails(details core.MetadataDetails) core.MetadataDetails {
	details.Genres = slices.Clone(details.Genres)
	details.Keywords = slices.Clone(details.Keywords)
	details.Countries = slices.Clone(details.Countries)
	details.Studios = slices.Clone(details.Studios)
	details.ReleaseDates = slices.Clone(details.ReleaseDates)
	details.WatchProviders = slices.Clone(details.WatchProviders)
	details.Cast = slices.Clone(details.Cast)
	details.Crew = slices.Clone(details.Crew)
	details.Recommendations = slices.Clone(details.Recommendations)
	details.Similar = slices.Clone(details.Similar)
	details.Series.Creators = slices.Clone(details.Series.Creators)
	details.Series.Networks = slices.Clone(details.Series.Networks)
	return details
}
