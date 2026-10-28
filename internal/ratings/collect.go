package ratings

import (
	"context"
	"errors"
	"fmt"

	"github.com/BonzTM/bloom/internal/core"
)

// collected is one lookup's scores and whether every source answered, which
// decides whether the scores may be cached.
type collected struct {
	ratings  core.TitleRatings
	complete bool
}

// Ratings returns the scores of a title: first what Radarr or Jellyfin keep
// for a title the operator holds, then OMDb for any score still missing. A
// failing source is skipped; an error returns only when no score is known.
func (s *Service) Ratings(ctx context.Context, title core.RatedTitle) (core.TitleRatings, error) {
	if core.ValidateRatedTitle(title) != nil {
		return core.TitleRatings{}, core.ErrInvalidArgument
	}
	key := string(title.Kind) + ":" + title.TMDBID
	if ratings, ok := s.cache.get(key); ok {
		return ratings, nil
	}
	// A key change during the load clears the cache; the generation keeps
	// this load's answer from landing after that.
	generation := s.cache.generation()
	value, err, _ := s.loads.Do(key, func() (any, error) { return s.collect(ctx, title) })
	if err != nil {
		return core.TitleRatings{}, err
	}
	result, ok := value.(collected)
	if !ok {
		return core.TitleRatings{}, fmt.Errorf("load title ratings: %w", core.ErrMetadataMalformed)
	}
	if result.complete {
		s.cache.put(key, result.ratings, generation)
	}
	return cloneRatings(result.ratings), nil
}

func (s *Service) collect(ctx context.Context, title core.RatedTitle) (collected, error) {
	result := collected{complete: true}
	for _, source := range s.owned {
		owned, err := source.OwnedTitleRatings(ctx, title)
		if err != nil {
			s.logger.WarnContext(ctx, "owned title ratings failed", "kind", title.Kind, "error", err)
			result.complete = false
			continue
		}
		result.ratings = result.ratings.FillMissing(owned)
	}
	if result.ratings.Complete() || title.IMDbID == "" {
		return result, nil
	}
	fallback, err := s.omdbRatings(ctx, title.IMDbID)
	if err != nil && result.ratings.Empty() {
		return collected{}, err
	}
	if err != nil {
		s.logger.WarnContext(ctx, "OMDb ratings failed; showing owned scores", "error", err)
		result.complete = false
		return result, nil
	}
	result.ratings = result.ratings.FillMissing(fallback)
	return result, nil
}

// omdbRatings reads OMDb's scores, or none when no key is stored.
func (s *Service) omdbRatings(ctx context.Context, imdbID string) (core.TitleRatings, error) {
	client, err := s.loadClient(ctx)
	if errors.Is(err, core.ErrMetadataNotConfigured) {
		return core.TitleRatings{}, nil
	}
	if err != nil {
		return core.TitleRatings{}, err
	}
	ratings, err := client.TitleRatings(ctx, imdbID)
	if err != nil {
		return core.TitleRatings{}, fmt.Errorf("load OMDb ratings: %w", err)
	}
	return ratings, nil
}
