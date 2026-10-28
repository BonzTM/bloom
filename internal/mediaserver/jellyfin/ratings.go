package jellyfin

import (
	"context"
	"math"
	"net/url"
	"strconv"

	"github.com/BonzTM/bloom/internal/core"
)

// ratingSearchLimit bounds the items one critic-score search reads.
const ratingSearchLimit = 20

var _ core.CriticRatingLookup = (*Client)(nil)

// ratedItem is the part of an item Bloom reads for its critic score.
// Jellyfin fills CriticRating on every item, without a fields request.
type ratedItem struct {
	ProviderIDs  map[string]*string `json:"ProviderIds"`
	CriticRating *float64           `json:"CriticRating"`
}

type ratedItems struct {
	Items []ratedItem `json:"Items"`
}

// CriticRating returns the critics score out of 100 Jellyfin keeps for the
// title with tmdbID. Jellyfin has no provider-ID filter, so the search
// narrows by name and the TMDB ID decides. A title stored under another name
// is reported as not found, and its score comes from elsewhere.
func (c *Client) CriticRating(
	ctx context.Context, kind core.MediaKind, tmdbID, title string,
) (float64, bool, error) {
	if !kind.Valid() || core.ValidateProviderID(tmdbID) != nil || title == "" ||
		len(title) > core.MaxMetadataTitleBytes {
		return 0, false, core.ErrInvalidArgument
	}
	itemType := "Movie"
	if kind == core.MediaKindSeries {
		itemType = "Series"
	}
	query := url.Values{
		"recursive": {"true"}, "hasTmdbId": {"true"}, "fields": {"ProviderIds"},
		"includeItemTypes": {itemType}, "searchTerm": {title}, "limit": {strconv.Itoa(ratingSearchLimit)},
	}
	var result ratedItems
	if _, err := c.getJSON(ctx, "critic_rating", "/Items?"+query.Encode(), &result); err != nil {
		return 0, false, err
	}
	return criticRatingFor(result.Items, tmdbID)
}

func criticRatingFor(items []ratedItem, tmdbID string) (float64, bool, error) {
	if len(items) > ratingSearchLimit {
		items = items[:ratingSearchLimit]
	}
	for _, item := range items {
		if !providerIDMatches(&item.ProviderIDs, "tmdb", tmdbID) {
			continue
		}
		score := item.CriticRating
		if score == nil || math.IsNaN(*score) || *score <= 0 || *score > 100 {
			return 0, false, nil
		}
		return *score, true, nil
	}
	return 0, false, nil
}
