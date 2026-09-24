package jellyfin

import (
	"context"
	"errors"
	"net/url"
	"strconv"
	"strings"

	"github.com/BonzTM/bloom/internal/core"
	jellyfinapi "github.com/BonzTM/bloom/internal/mediaserver/jellyfin/api"
)

const (
	availabilityPageSize = 100
	maxAvailabilityPages = 100
)

// HasTitle reports TMDB title presence and, when available, requested series seasons.
func (c *Client) HasTitle(
	ctx context.Context, kind core.MediaKind, provider core.MetadataProviderKind,
	providerID string, seasons []int,
) (bool, []int, error) {
	if !kind.Valid() || provider != core.MetadataProviderTMDB || core.ValidateProviderID(providerID) != nil {
		return false, nil, core.ErrInvalidArgument
	}
	item, found, err := c.findProviderItem(ctx, kind, providerID)
	if err != nil || !found || kind == core.MediaKindMovie || len(seasons) == 0 {
		return found, nil, err
	}
	available, err := c.availableSeasons(ctx, item, seasons)
	return len(available) == len(seasons), available, err
}

func (c *Client) findProviderItem(
	ctx context.Context, kind core.MediaKind, providerID string,
) (jellyfinapi.BaseItemDto, bool, error) {
	itemType := "Movie"
	if kind == core.MediaKindSeries {
		itemType = "Series"
	}
	for page := range maxAvailabilityPages {
		path := "/Items?recursive=true&hasTmdbId=true&fields=ProviderIds&includeItemTypes=" + itemType +
			"&limit=" + strconv.Itoa(availabilityPageSize) + "&startIndex=" + strconv.Itoa(page*availabilityPageSize)
		var result jellyfinapi.BaseItemDtoQueryResult
		if _, err := c.getJSON(ctx, "provider_id_lookup", path, &result); err != nil {
			return jellyfinapi.BaseItemDto{}, false, err
		}
		if result.Items == nil {
			return jellyfinapi.BaseItemDto{}, false, nil
		}
		for _, item := range *result.Items {
			if providerIDMatches(item.ProviderIds, "tmdb", providerID) {
				return item, true, nil
			}
		}
		if len(*result.Items) < availabilityPageSize {
			return jellyfinapi.BaseItemDto{}, false, nil
		}
	}
	return jellyfinapi.BaseItemDto{}, false, mediaError(
		"provider_id_lookup", core.MediaServerMalformed, errors.New("provider-id result exceeds page bound"),
	)
}

func (c *Client) availableSeasons(
	ctx context.Context, series jellyfinapi.BaseItemDto, requested []int,
) ([]int, error) {
	if series.Id == nil {
		return nil, mediaError("provider_id_lookup", core.MediaServerMalformed, errors.New("series id is missing"))
	}
	path := "/Items?parentId=" + url.QueryEscape(series.Id.String()) +
		"&includeItemTypes=Season&limit=1000&recursive=false"
	var result jellyfinapi.BaseItemDtoQueryResult
	if _, err := c.getJSON(ctx, "season_availability", path, &result); err != nil {
		return nil, err
	}
	if result.Items == nil {
		return nil, nil
	}
	wanted := make(map[int]struct{}, len(requested))
	for _, season := range requested {
		wanted[season] = struct{}{}
	}
	available := make([]int, 0, len(requested))
	for _, item := range *result.Items {
		if item.IndexNumber == nil {
			continue
		}
		if _, ok := wanted[int(*item.IndexNumber)]; ok {
			available = append(available, int(*item.IndexNumber))
		}
	}
	return available, nil
}

func providerIDMatches(values *map[string]*string, key, expected string) bool {
	if values == nil {
		return false
	}
	for candidate, value := range *values {
		if strings.EqualFold(candidate, key) && value != nil && *value == expected {
			return true
		}
	}
	return false
}
