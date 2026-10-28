package mediaserver

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/BonzTM/bloom/internal/core"
)

// maxRatingServers bounds how many registrations one ratings lookup asks.
const maxRatingServers = 100

var _ core.OwnedRatingsSource = (*Service)(nil)

// OwnedTitleRatings returns the Rotten Tomatoes critics score the first
// registered server holding the title keeps for it. The library catalog names
// the server's items for the title; a title the catalog has not seen yet is
// found by name. A server that fails is skipped; its error is returned only
// when no server answered.
func (s *Service) OwnedTitleRatings(ctx context.Context, title core.RatedTitle) (core.TitleRatings, error) {
	if core.ValidateRatedTitle(title) != nil {
		return core.TitleRatings{}, core.ErrInvalidArgument
	}
	servers, err := s.reader.ListMediaServers(ctx, "", maxRatingServers)
	if err != nil {
		return core.TitleRatings{}, fmt.Errorf("list media servers for ratings: %w", err)
	}
	var failures []error
	for _, server := range servers {
		score, found, lookupErr := s.criticRating(ctx, server, title)
		if lookupErr != nil {
			failures = append(failures, lookupErr)
			continue
		}
		if found {
			return core.TitleRatings{RottenTomatoes: &core.TitleRating{
				Score: score, Source: core.RatingSourceJellyfin,
			}}, nil
		}
	}
	return core.TitleRatings{}, errors.Join(failures...)
}

func (s *Service) criticRating(ctx context.Context, server core.MediaServer, title core.RatedTitle) (float64, bool, error) {
	capabilities, err := s.factory.Capabilities(server.Kind)
	if err != nil || !capabilities.ProviderIDLookup {
		return 0, false, err
	}
	itemIDs, err := s.catalogTitleItems(ctx, server.ID, title)
	if err != nil {
		return 0, false, err
	}
	if len(itemIDs) == 0 && title.Title == "" {
		return 0, false, nil
	}
	call, err := s.adapter(ctx, server.ID, "critic_rating")
	if err != nil {
		return 0, false, err
	}
	defer call.release()
	lookup, ok := call.entry.adapter.(core.CriticRatingLookup)
	if !ok {
		return 0, false, nil
	}
	score, found, err := askCriticRating(ctx, lookup, title, itemIDs)
	if err != nil {
		return 0, false, fmt.Errorf("read media server critic rating: %w", err)
	}
	return score, found, nil
}

// catalogTitleItems returns the catalog items that hold the title on the
// server. None means the catalog has not seen it, so a name search decides.
func (s *Service) catalogTitleItems(ctx context.Context, serverID string, title core.RatedTitle) ([]string, error) {
	if s.catalogTitles == nil {
		return nil, nil
	}
	tmdbID, err := strconv.ParseInt(title.TMDBID, 10, 64)
	if err != nil {
		return nil, core.ErrInvalidArgument
	}
	if tmdbID > core.MaxCatalogTMDBID {
		// The catalog stores no TMDB ID this large, so a name search decides.
		return nil, nil
	}
	itemIDs, err := s.catalogTitles.ListCatalogTitleItemIDs(ctx, core.CatalogTitleQuery{
		MediaServerID: serverID, Kind: title.Kind, TMDBID: tmdbID, Limit: core.MaxCatalogTitleItems,
	})
	if err != nil {
		return nil, fmt.Errorf("resolve catalog title items: %w", err)
	}
	return itemIDs, nil
}

func askCriticRating(
	ctx context.Context, lookup core.CriticRatingLookup, title core.RatedTitle, itemIDs []string,
) (float64, bool, error) {
	if len(itemIDs) > 0 {
		return lookup.ItemCriticRating(ctx, title.Kind, title.TMDBID, itemIDs)
	}
	return lookup.CriticRating(ctx, title.Kind, title.TMDBID, title.Title)
}
