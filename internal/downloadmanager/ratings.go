package downloadmanager

import (
	"context"
	"errors"
	"fmt"

	"github.com/BonzTM/bloom/internal/core"
)

// maxRatingManagers bounds how many registrations one ratings lookup asks.
const maxRatingManagers = 100

var _ core.OwnedRatingsSource = (*Service)(nil)

// OwnedTitleRatings returns the scores the first registered manager that
// holds the movie keeps for it. Series have no manager scores. A manager
// that fails is skipped; its error is returned only when no manager answered.
func (s *Service) OwnedTitleRatings(ctx context.Context, title core.RatedTitle) (core.TitleRatings, error) {
	if core.ValidateRatedTitle(title) != nil {
		return core.TitleRatings{}, core.ErrInvalidArgument
	}
	if title.Kind != core.MediaKindMovie {
		return core.TitleRatings{}, nil
	}
	managers, err := s.reader.ListDownloadManagers(ctx, "", maxRatingManagers)
	if err != nil {
		return core.TitleRatings{}, fmt.Errorf("list download managers for ratings: %w", err)
	}
	var failures []error
	for _, manager := range managers {
		if manager.Kind != core.DownloadManagerKindRadarr {
			continue
		}
		ratings, found, lookupErr := s.movieRatings(ctx, manager.ID, title.TMDBID)
		if lookupErr != nil {
			failures = append(failures, lookupErr)
			continue
		}
		if found {
			return ratings, nil
		}
	}
	return core.TitleRatings{}, errors.Join(failures...)
}

func (s *Service) movieRatings(ctx context.Context, managerID, tmdbID string) (core.TitleRatings, bool, error) {
	adapter, err := s.adapterByID(ctx, managerID)
	if err != nil {
		return core.TitleRatings{}, false, err
	}
	lookup, ok := adapter.(core.MovieRatingsLookup)
	if !ok {
		return core.TitleRatings{}, false, nil
	}
	ratings, found, err := lookup.MovieRatings(ctx, tmdbID)
	if err != nil {
		return core.TitleRatings{}, false, fmt.Errorf("read download manager movie ratings: %w", err)
	}
	return ratings, found, nil
}
