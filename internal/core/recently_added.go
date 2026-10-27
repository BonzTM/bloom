package core

import (
	"context"
	"time"
)

const (
	// DefaultRecentlyAddedLimit is the number of titles a home row shows.
	DefaultRecentlyAddedLimit = 20
	// MaxRecentlyAddedLimit bounds one recently added read.
	MaxRecentlyAddedLimit = 40
)

// RecentlyAddedTitle is one movie or series the media server added, newest first.
type RecentlyAddedTitle struct {
	MediaServerID string
	ItemID        string
	Kind          MediaKind
	Title         string
	Year          int
	AddedAt       time.Time
}

// RecentlyAddedStore lists the newest active catalog movies and series
// outside excluded libraries.
type RecentlyAddedStore interface {
	ListRecentlyAddedTitles(ctx context.Context, limit int) ([]CatalogItemStats, error)
}

// ValidRecentlyAddedLimit reports whether limit is inside the read bound.
func ValidRecentlyAddedLimit(limit int) bool {
	return limit >= 1 && limit <= MaxRecentlyAddedLimit
}

// RecentlyAddedFromCatalog maps a catalog movie or series row. It reports
// false for any other item type or a row without a creation time.
func RecentlyAddedFromCatalog(item LibraryItem) (RecentlyAddedTitle, bool) {
	var kind MediaKind
	switch item.ItemType {
	case "Movie":
		kind = MediaKindMovie
	case "Series":
		kind = MediaKindSeries
	default:
		return RecentlyAddedTitle{}, false
	}
	if item.DateCreated == nil {
		return RecentlyAddedTitle{}, false
	}
	year := 0
	if item.ProductionYear != nil {
		year = int(*item.ProductionYear)
	}
	return RecentlyAddedTitle{
		MediaServerID: item.MediaServerID, ItemID: item.ItemID, Kind: kind, Title: item.Name, Year: year,
		AddedAt: *item.DateCreated,
	}, true
}
