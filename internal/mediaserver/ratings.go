package mediaserver

import (
	"context"
	"errors"
	"fmt"

	"github.com/BonzTM/bloom/internal/core"
)

// maxRatingServers bounds how many registrations one ratings lookup asks.
const maxRatingServers = 100

var _ core.OwnedRatingsSource = (*Service)(nil)

// OwnedTitleRatings returns the Rotten Tomatoes critics score the first
// registered server holding the title keeps for it. A server that fails is
// skipped; its error is returned only when no server answered.
func (s *Service) OwnedTitleRatings(ctx context.Context, title core.RatedTitle) (core.TitleRatings, error) {
	if core.ValidateRatedTitle(title) != nil {
		return core.TitleRatings{}, core.ErrInvalidArgument
	}
	if title.Title == "" {
		return core.TitleRatings{}, nil
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
	call, err := s.adapter(ctx, server.ID, "critic_rating")
	if err != nil {
		return 0, false, err
	}
	defer call.release()
	lookup, ok := call.entry.adapter.(core.CriticRatingLookup)
	if !ok {
		return 0, false, nil
	}
	score, found, err := lookup.CriticRating(ctx, title.Kind, title.TMDBID, title.Title)
	if err != nil {
		return 0, false, fmt.Errorf("read media server critic rating: %w", err)
	}
	return score, found, nil
}
