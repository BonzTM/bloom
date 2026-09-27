package jellyfin

import (
	"context"
	"errors"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/BonzTM/bloom/internal/core"
	jellyfinapi "github.com/BonzTM/bloom/internal/mediaserver/jellyfin/api"
)

const catalogItemFields = "ParentId,Genres,DateCreated"

// CatalogItems returns one fixed-size recursive page without filtering item types.
func (c *Client) CatalogItems(
	ctx context.Context, libraryID string, startIndex, limit int,
) (core.LibraryCatalogPage, error) {
	if !core.ValidLibraryID(libraryID) || startIndex < 0 ||
		startIndex > core.MaxCatalogPagesPerLibrary*core.CatalogPageSize || limit != core.CatalogPageSize {
		return core.LibraryCatalogPage{}, core.ErrInvalidArgument
	}
	values := url.Values{
		"parentId": {libraryID}, "recursive": {"true"}, "fields": {catalogItemFields},
		"startIndex": {strconv.Itoa(startIndex)}, "limit": {strconv.Itoa(limit)},
		"sortBy": {"SortName,DateCreated"}, "sortOrder": {"Ascending"},
	}
	var dto jellyfinapi.BaseItemDtoQueryResult
	started, err := c.getJSON(ctx, "catalog_items", "/Items?"+values.Encode(), &dto)
	if err != nil {
		return core.LibraryCatalogPage{}, err
	}
	page, err := mapCatalogPage(dto, libraryID, startIndex, limit)
	if err != nil {
		c.observe("catalog_items", "malformed", started)
		return core.LibraryCatalogPage{}, mediaError("catalog_items", core.MediaServerMalformed, err)
	}
	c.observe("catalog_items", "success", started)
	return page, nil
}

// CatalogItemIDs returns the bounded subset of requested IDs that still exists.
func (c *Client) CatalogItemIDs(ctx context.Context, itemIDs []string) ([]string, error) {
	if len(itemIDs) < 1 || len(itemIDs) > core.CatalogUserDataBatchSize {
		return nil, core.ErrInvalidArgument
	}
	if err := validateCatalogItemIDs(itemIDs); err != nil {
		return nil, err
	}
	values := url.Values{"ids": {strings.Join(itemIDs, ",")}, "limit": {strconv.Itoa(len(itemIDs))}}
	var dto jellyfinapi.BaseItemDtoQueryResult
	started, err := c.getJSON(ctx, "catalog_item_ids", "/Items?"+values.Encode(), &dto)
	if err != nil {
		return nil, err
	}
	ids, err := mapCatalogItemIDs(dto, itemIDs)
	if err != nil {
		c.observe("catalog_item_ids", "malformed", started)
		return nil, mediaError("catalog_item_ids", core.MediaServerMalformed, err)
	}
	c.observe("catalog_item_ids", "success", started)
	return ids, nil
}

func mapCatalogItemIDs(dto jellyfinapi.BaseItemDtoQueryResult, requested []string) ([]string, error) {
	if dto.Items == nil || len(*dto.Items) > len(requested) {
		return nil, errors.New("catalog item id response count is invalid")
	}
	allowed := make(map[string]struct{}, len(requested))
	for _, id := range requested {
		allowed[id] = struct{}{}
	}
	ids := make([]string, 0, len(*dto.Items))
	seen := make(map[string]struct{}, len(*dto.Items))
	for _, item := range *dto.Items {
		id := uuidString(item.Id)
		_, requestedID := allowed[id]
		_, duplicate := seen[id]
		if !requestedID || duplicate {
			return nil, errors.New("catalog item id response is invalid")
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	return ids, nil
}

func mapCatalogPage(
	dto jellyfinapi.BaseItemDtoQueryResult, libraryID string, startIndex, limit int,
) (core.LibraryCatalogPage, error) {
	if dto.Items == nil || dto.TotalRecordCount == nil || *dto.TotalRecordCount < 0 ||
		len(*dto.Items) > limit || int(*dto.TotalRecordCount) > core.MaxCatalogPagesPerLibrary*core.CatalogPageSize {
		return core.LibraryCatalogPage{}, errors.New("catalog page metadata is invalid")
	}
	if dto.StartIndex != nil && int(*dto.StartIndex) != startIndex {
		return core.LibraryCatalogPage{}, errors.New("catalog page start index changed")
	}
	items := make([]core.LibraryItem, 0, len(*dto.Items))
	for _, value := range *dto.Items {
		item, err := mapCatalogItem(value, libraryID)
		if err != nil {
			return core.LibraryCatalogPage{}, err
		}
		items = append(items, item)
	}
	return core.LibraryCatalogPage{Items: items, StartIndex: startIndex, Total: int(*dto.TotalRecordCount)}, nil
}

func mapCatalogItem(dto jellyfinapi.BaseItemDto, libraryID string) (core.LibraryItem, error) {
	runtime, err := optionalDurationFromTicks(dto.RunTimeTicks)
	if err != nil {
		return core.LibraryItem{}, err
	}
	genres := []string{}
	if dto.Genres != nil {
		genres = append(genres, (*dto.Genres)...)
	}
	item := core.LibraryItem{
		ItemID: uuidString(dto.Id), LibraryID: libraryID, ParentID: uuidString(dto.ParentId),
		ItemType: enumString(dto.Type), Name: stringValue(dto.Name), SeriesID: uuidString(dto.SeriesId),
		SeriesName: stringValue(dto.SeriesName), SeasonID: uuidString(dto.SeasonId),
		SeasonNumber: dto.ParentIndexNumber, IndexNumber: dto.IndexNumber, Runtime: runtime,
		PremiereDate: normalizedTime(dto.PremiereDate), ProductionYear: dto.ProductionYear,
		CommunityRating: float64Pointer(dto.CommunityRating), Genres: genres,
		PrimaryImageTag: primaryImageTag(dto.ImageTags), DateCreated: normalizedTime(dto.DateCreated),
	}
	if !item.ValidUpstream() {
		return core.LibraryItem{}, errors.New("catalog item fields exceed bounds")
	}
	return item, nil
}

// CatalogUserData returns bounded play-state records for one Jellyfin user.
func (c *Client) CatalogUserData(
	ctx context.Context, userID string, itemIDs []string,
) ([]core.LibraryUserData, error) {
	if !core.ValidAccountMediaUserID(userID) || len(itemIDs) < 1 || len(itemIDs) > core.CatalogUserDataBatchSize {
		return nil, core.ErrInvalidArgument
	}
	if err := validateCatalogItemIDs(itemIDs); err != nil {
		return nil, err
	}
	values := url.Values{
		"userId": {userID}, "enableUserData": {"true"}, "ids": {strings.Join(itemIDs, ",")},
		"limit": {strconv.Itoa(len(itemIDs))},
	}
	var dto jellyfinapi.BaseItemDtoQueryResult
	started, err := c.getJSON(ctx, "catalog_user_data", "/Items?"+values.Encode(), &dto)
	if err != nil {
		return nil, err
	}
	result, err := mapCatalogUserData(dto, itemIDs)
	if err != nil {
		c.observe("catalog_user_data", "malformed", started)
		return nil, mediaError("catalog_user_data", core.MediaServerMalformed, err)
	}
	c.observe("catalog_user_data", "success", started)
	return result, nil
}

func validateCatalogItemIDs(itemIDs []string) error {
	seen := make(map[string]struct{}, len(itemIDs))
	for _, itemID := range itemIDs {
		if !core.ValidCatalogID(itemID) {
			return core.ErrInvalidArgument
		}
		if _, duplicate := seen[itemID]; duplicate {
			return core.ErrInvalidArgument
		}
		seen[itemID] = struct{}{}
	}
	return nil
}

func mapCatalogUserData(dto jellyfinapi.BaseItemDtoQueryResult, requested []string) ([]core.LibraryUserData, error) {
	if dto.Items == nil || len(*dto.Items) > len(requested) {
		return nil, errors.New("user data response count is invalid")
	}
	allowed := make(map[string]struct{}, len(requested))
	for _, id := range requested {
		allowed[id] = struct{}{}
	}
	result := make([]core.LibraryUserData, 0, len(*dto.Items))
	seen := make(map[string]struct{}, len(*dto.Items))
	for _, item := range *dto.Items {
		mapped, err := mapOneCatalogUserData(item)
		_, requestedItem := allowed[mapped.ItemID]
		_, duplicate := seen[mapped.ItemID]
		if err != nil || !requestedItem || duplicate {
			return nil, errors.New("user data item is invalid")
		}
		seen[mapped.ItemID] = struct{}{}
		result = append(result, mapped)
	}
	return result, nil
}

func mapOneCatalogUserData(item jellyfinapi.BaseItemDto) (core.LibraryUserData, error) {
	result := core.LibraryUserData{ItemID: uuidString(item.Id)}
	if item.UserData == nil {
		return result, nil
	}
	if item.UserData.PlayCount != nil {
		result.PlayCount = *item.UserData.PlayCount
	}
	if result.PlayCount < 0 {
		return core.LibraryUserData{}, errors.New("user data play count is negative")
	}
	position, err := positionFromTicks(item.UserData.PlaybackPositionTicks)
	if err != nil {
		return core.LibraryUserData{}, err
	}
	result.LastPlayedAt = normalizedTime(item.UserData.LastPlayedDate)
	result.PlaybackPosition = position
	return result, nil
}

func enumString[T ~string](value *T) string {
	if value == nil {
		return ""
	}
	return string(*value)
}

func normalizedTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	normalized := core.NormalizeTime(*value)
	return &normalized
}

func float64Pointer(value *float32) *float64 {
	if value == nil {
		return nil
	}
	converted := float64(*value)
	return &converted
}

func primaryImageTag(tags *map[string]*string) string {
	if tags == nil || (*tags)["Primary"] == nil {
		return ""
	}
	return *(*tags)["Primary"]
}

var _ core.LibraryCatalogSyncer = (*Client)(nil)
