package jellyfin

import (
	"context"
	"math"
	"net/url"
	"strconv"
	"strings"

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

// ItemCriticRating returns the critics score out of 100 Jellyfin keeps for
// the title with tmdbID, read from the catalog items known to hold it. An item
// whose TMDB ID changed since the catalog sync no longer counts.
func (c *Client) ItemCriticRating(
	ctx context.Context, kind core.MediaKind, tmdbID string, itemIDs []string,
) (float64, bool, error) {
	if !kind.Valid() || core.ValidateProviderID(tmdbID) != nil || len(itemIDs) == 0 ||
		len(itemIDs) > core.MaxCatalogTitleItems || validateCatalogItemIDs(itemIDs) != nil {
		return 0, false, core.ErrInvalidArgument
	}
	query := url.Values{
		"ids": {strings.Join(itemIDs, ",")}, "fields": {"ProviderIds"},
		"includeItemTypes": {jellyfinItemType(kind)}, "limit": {strconv.Itoa(len(itemIDs))},
	}
	var result ratedItems
	if _, err := c.getJSON(ctx, "item_critic_rating", "/Items?"+query.Encode(), &result); err != nil {
		return 0, false, err
	}
	return criticRatingFor(result.Items, tmdbID)
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
	query := url.Values{
		"recursive": {"true"}, "hasTmdbId": {"true"}, "fields": {"ProviderIds"},
		"includeItemTypes": {jellyfinItemType(kind)}, "searchTerm": {title}, "limit": {strconv.Itoa(ratingSearchLimit)},
	}
	var result ratedItems
	if _, err := c.getJSON(ctx, "critic_rating", "/Items?"+query.Encode(), &result); err != nil {
		return 0, false, err
	}
	return criticRatingFor(result.Items, tmdbID)
}

func jellyfinItemType(kind core.MediaKind) string {
	if kind == core.MediaKindSeries {
		return "Series"
	}
	return "Movie"
}

// criticRatingFor returns the first usable score among the items that carry
// tmdbID.
func criticRatingFor(items []ratedItem, tmdbID string) (float64, bool, error) {
	if len(items) > ratingSearchLimit {
		items = items[:ratingSearchLimit]
	}
	for _, item := range items {
		if !providerIDMatches(&item.ProviderIDs, "tmdb", tmdbID) {
			continue
		}
		// A copy of the title without a score leaves the next copy to decide.
		score := item.CriticRating
		if score == nil || math.IsNaN(*score) || *score <= 0 || *score > 100 {
			continue
		}
		return *score, true, nil
	}
	return 0, false, nil
}
