package radarr

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/downloadmanager/arr"
	radarrapi "github.com/BonzTM/bloom/internal/downloadmanager/radarr/api"
)

var _ core.MovieRatingsLookup = (*Client)(nil)

// MovieRatings returns the IMDb, Rotten Tomatoes, and Metacritic scores
// Radarr keeps for a movie in its library. It reads the library only, never
// Radarr's metadata lookup, so it reports found false for a movie Radarr
// does not manage.
func (c *Client) MovieRatings(ctx context.Context, tmdbID string) (core.TitleRatings, bool, error) {
	id, err := strconv.ParseInt(tmdbID, 10, 32)
	if err != nil || id <= 0 {
		return core.TitleRatings{}, false, core.ErrInvalidArgument
	}
	raw, err := c.http.GetRawJSON(ctx, "ratings", "/api/v3/movie?tmdbId="+strconv.FormatInt(id, 10))
	if err != nil {
		return core.TitleRatings{}, false, err
	}
	ratings, found, err := movieRatings(raw, int32(id))
	if err != nil {
		return core.TitleRatings{}, false, malformedError("ratings", err)
	}
	return ratings, found, nil
}

func movieRatings(raw json.RawMessage, tmdbID int32) (core.TitleRatings, bool, error) {
	values, err := arr.DecodeArray(raw, maxMovieResults, "movies")
	if err != nil {
		return core.TitleRatings{}, false, err
	}
	for _, value := range values {
		object, decodeErr := arr.DecodeObject(value)
		if decodeErr != nil {
			return core.TitleRatings{}, false, decodeErr
		}
		id, _, decodeErr := arr.DecodeOptionalInt32(object, "tmdbId")
		if decodeErr != nil {
			return core.TitleRatings{}, false, decodeErr
		}
		if id != tmdbID {
			continue
		}
		ratings, decodeErr := decodeRatings(object["ratings"])
		return ratings, true, decodeErr
	}
	return core.TitleRatings{}, false, nil
}

func decodeRatings(raw json.RawMessage) (core.TitleRatings, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return core.TitleRatings{}, nil
	}
	var value radarrapi.Ratings
	if err := json.Unmarshal(raw, &value); err != nil {
		return core.TitleRatings{}, fmt.Errorf("decode movie ratings: %w", err)
	}
	return core.TitleRatings{
		IMDb:           ratingFrom(value.Imdb, 10, true),
		RottenTomatoes: ratingFrom(value.RottenTomatoes, 100, false),
		Metacritic:     ratingFrom(value.Metacritic, 100, false),
	}, nil
}

// ratingFrom keeps a score Radarr actually holds. Radarr writes a zero value
// for a score its metadata source does not have, so zero counts as missing.
func ratingFrom(child *radarrapi.RatingChild, maxScore float64, withVotes bool) *core.TitleRating {
	if child == nil || child.Value == nil || *child.Value <= 0 {
		return nil
	}
	rating := core.TitleRating{Score: *child.Value, Source: core.RatingSourceRadarr}
	if withVotes && child.Votes != nil {
		rating.Votes = int(*child.Votes)
	}
	if !core.ValidTitleRating(rating, maxScore) {
		return nil
	}
	return &rating
}
