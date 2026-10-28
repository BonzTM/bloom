package core

import "context"

// MaxCatalogTitleItems bounds the catalog items one title lookup reads: the
// copies of one movie or series across a media server's libraries.
const MaxCatalogTitleItems = 5

// CatalogTitleQuery names one movie or series on one media server by its
// TMDB ID.
type CatalogTitleQuery struct {
	MediaServerID string
	Kind          MediaKind
	TMDBID        int64
	Limit         int
}

// Valid reports whether the query fits the catalog's stored shape and bound.
func (q CatalogTitleQuery) Valid() bool {
	return ValidID(q.MediaServerID) && q.Kind.Valid() && q.TMDBID >= 1 && q.TMDBID <= MaxCatalogTMDBID &&
		q.Limit >= 1 && q.Limit <= MaxCatalogTitleItems
}

// ItemType returns the catalog item type that holds the query's kind.
func (q CatalogTitleQuery) ItemType() string {
	if q.Kind == MediaKindSeries {
		return "Series"
	}
	return "Movie"
}

// CatalogTitleItems resolves the active catalog items that hold a title.
// A title the catalog does not hold has no items and no error.
type CatalogTitleItems interface {
	ListCatalogTitleItemIDs(ctx context.Context, query CatalogTitleQuery) ([]string, error)
}
