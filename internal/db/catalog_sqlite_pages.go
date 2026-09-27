package db

import (
	"context"
	"database/sql"
	"math"
	"strings"
	"time"

	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/db/sqlite"
)

type sqliteCatalogPageArgs struct {
	serverID, libraryID, itemType, afterItemID string
	afterName                                  string
	archived, afterNull, limit                 int64
	afterTime                                  sql.NullString
	afterNumber                                int64
}

func newSQLiteCatalogPageArgs(query core.CatalogItemQuery) sqliteCatalogPageArgs {
	args := sqliteCatalogPageArgs{
		serverID: query.MediaServerID, libraryID: query.LibraryID, itemType: query.ItemType,
		archived: catalogArchivedFilter(query.Archived), limit: int64(query.Limit), afterNumber: -1,
	}
	sentinel := time.Date(1, 1, 1, 0, 0, 0, 0, time.UTC)
	if query.Order == core.SortDescending {
		args.afterName = strings.Repeat("\U0010ffff", 125)
		args.afterNumber = math.MaxInt64
		sentinel = time.Date(9999, 12, 31, 23, 59, 59, 999999000, time.UTC)
	}
	args.afterTime = sqliteNullableTime(&sentinel)
	if query.After != nil {
		args.afterItemID = query.After.ItemID
		args.afterName, args.afterNumber = query.After.TextValue, query.After.NumberValue
		args.afterNull = boolToInt64(query.After.TimeValue == nil)
		if query.After.TimeValue != nil {
			args.afterTime = sqliteNullableTime(query.After.TimeValue)
		}
	}
	return args
}

func (s *sqliteCatalogStore) listSQLiteCatalogName(
	ctx context.Context, query core.CatalogItemQuery,
) ([]sqlite.LibraryItem, error) {
	a := newSQLiteCatalogPageArgs(query)
	if query.Order == core.SortDescending {
		return s.listSQLiteNameDesc(ctx, a)
	}
	if a.itemType != "" {
		rows, err := s.q.ListCatalogItemsNameAscFiltered(ctx, sqlite.ListCatalogItemsNameAscFilteredParams{
			MediaServerID: a.serverID, LibraryID: a.libraryID, ArchivedFilter: a.archived,
			ItemTypeFilter: a.itemType, AfterName: a.afterName,
			AfterItemID: a.afterItemID, PageSize: a.limit,
		})
		return rows, err
	}
	return s.q.ListCatalogItemsNameAsc(ctx, sqlite.ListCatalogItemsNameAscParams{
		MediaServerID: a.serverID, LibraryID: a.libraryID,
		ArchivedFilter: a.archived, AfterName: a.afterName,
		AfterItemID: a.afterItemID, PageSize: a.limit,
	})
}

func (s *sqliteCatalogStore) listSQLiteNameDesc(
	ctx context.Context, a sqliteCatalogPageArgs,
) ([]sqlite.LibraryItem, error) {
	if a.itemType != "" {
		rows, err := s.q.ListCatalogItemsNameDescFiltered(ctx, sqlite.ListCatalogItemsNameDescFilteredParams{
			MediaServerID: a.serverID, LibraryID: a.libraryID, ArchivedFilter: a.archived,
			ItemTypeFilter: a.itemType, AfterName: a.afterName,
			AfterItemID: a.afterItemID, PageSize: a.limit,
		})
		return rows, err
	}
	rows, err := s.q.ListCatalogItemsNameDesc(ctx, sqlite.ListCatalogItemsNameDescParams{
		MediaServerID: a.serverID, LibraryID: a.libraryID, ArchivedFilter: a.archived,
		AfterName: a.afterName, AfterItemID: a.afterItemID, PageSize: a.limit,
	})
	return rows, err
}

func (s *sqliteCatalogStore) listSQLiteCatalogDate(
	ctx context.Context, query core.CatalogItemQuery, premiere bool,
) ([]sqlite.LibraryItem, error) {
	a := newSQLiteCatalogPageArgs(query)
	if premiere {
		return s.listSQLitePremiere(ctx, query, a)
	}
	if query.Order == core.SortDescending {
		if a.itemType != "" {
			rows, err := s.q.ListCatalogItemsDateDescFiltered(ctx, sqlite.ListCatalogItemsDateDescFilteredParams{
				MediaServerID: a.serverID, LibraryID: a.libraryID, ArchivedFilter: a.archived,
				ItemTypeFilter: a.itemType, AfterNull: a.afterNull,
				AfterTime: a.afterTime, AfterItemID: a.afterItemID, PageSize: a.limit,
			})
			return rows, err
		}
		rows, err := s.q.ListCatalogItemsDateDesc(ctx, sqlite.ListCatalogItemsDateDescParams{
			MediaServerID: a.serverID, LibraryID: a.libraryID, ArchivedFilter: a.archived,
			AfterNull: a.afterNull, AfterTime: a.afterTime,
			AfterItemID: a.afterItemID, PageSize: a.limit,
		})
		return rows, err
	}
	if a.itemType != "" {
		rows, err := s.q.ListCatalogItemsDateAscFiltered(ctx, sqlite.ListCatalogItemsDateAscFilteredParams{
			MediaServerID: a.serverID, LibraryID: a.libraryID, ArchivedFilter: a.archived,
			ItemTypeFilter: a.itemType, AfterNull: a.afterNull,
			AfterTime: a.afterTime, AfterItemID: a.afterItemID, PageSize: a.limit,
		})
		return rows, err
	}
	rows, err := s.q.ListCatalogItemsDateAsc(ctx, sqlite.ListCatalogItemsDateAscParams{
		MediaServerID: a.serverID, LibraryID: a.libraryID, ArchivedFilter: a.archived,
		AfterNull: a.afterNull, AfterTime: a.afterTime,
		AfterItemID: a.afterItemID, PageSize: a.limit,
	})
	return rows, err
}

func (s *sqliteCatalogStore) listSQLitePremiere(
	ctx context.Context, query core.CatalogItemQuery, a sqliteCatalogPageArgs,
) ([]sqlite.LibraryItem, error) {
	if query.Order == core.SortDescending {
		if a.itemType != "" {
			rows, err := s.q.ListCatalogItemsPremiereDescFiltered(ctx, sqlite.ListCatalogItemsPremiereDescFilteredParams{
				MediaServerID: a.serverID, LibraryID: a.libraryID, ArchivedFilter: a.archived,
				ItemTypeFilter: a.itemType, AfterNull: a.afterNull,
				AfterTime: a.afterTime, AfterItemID: a.afterItemID, PageSize: a.limit,
			})
			return rows, err
		}
		rows, err := s.q.ListCatalogItemsPremiereDesc(ctx, sqlite.ListCatalogItemsPremiereDescParams{
			MediaServerID: a.serverID, LibraryID: a.libraryID, ArchivedFilter: a.archived,
			AfterNull: a.afterNull, AfterTime: a.afterTime,
			AfterItemID: a.afterItemID, PageSize: a.limit,
		})
		return rows, err
	}
	if a.itemType != "" {
		rows, err := s.q.ListCatalogItemsPremiereAscFiltered(ctx, sqlite.ListCatalogItemsPremiereAscFilteredParams{
			MediaServerID: a.serverID, LibraryID: a.libraryID, ArchivedFilter: a.archived,
			ItemTypeFilter: a.itemType, AfterNull: a.afterNull,
			AfterTime: a.afterTime, AfterItemID: a.afterItemID, PageSize: a.limit,
		})
		return rows, err
	}
	rows, err := s.q.ListCatalogItemsPremiereAsc(ctx, sqlite.ListCatalogItemsPremiereAscParams{
		MediaServerID: a.serverID, LibraryID: a.libraryID, ArchivedFilter: a.archived,
		AfterNull: a.afterNull, AfterTime: a.afterTime,
		AfterItemID: a.afterItemID, PageSize: a.limit,
	})
	return rows, err
}

func (s *sqliteCatalogStore) listSQLiteCatalogNumber(
	ctx context.Context, query core.CatalogItemQuery, watchTime bool,
) ([]sqlite.LibraryItem, error) {
	a := newSQLiteCatalogPageArgs(query)
	if watchTime {
		return s.listSQLiteWatchTime(ctx, query, a)
	}
	if query.Order == core.SortDescending {
		if a.itemType != "" {
			rows, err := s.q.ListCatalogItemsPlaysDescFiltered(ctx, sqlite.ListCatalogItemsPlaysDescFilteredParams{
				MediaServerID: a.serverID, LibraryID: a.libraryID, ArchivedFilter: a.archived,
				ItemTypeFilter: a.itemType, AfterNumber: a.afterNumber,
				AfterItemID: a.afterItemID, PageSize: a.limit,
			})
			return rows, err
		}
		rows, err := s.q.ListCatalogItemsPlaysDesc(ctx, sqlite.ListCatalogItemsPlaysDescParams{
			MediaServerID: a.serverID, LibraryID: a.libraryID, ArchivedFilter: a.archived,
			AfterNumber: a.afterNumber, AfterItemID: a.afterItemID, PageSize: a.limit,
		})
		return rows, err
	}
	if a.itemType != "" {
		rows, err := s.q.ListCatalogItemsPlaysAscFiltered(ctx, sqlite.ListCatalogItemsPlaysAscFilteredParams{
			MediaServerID: a.serverID, LibraryID: a.libraryID, ArchivedFilter: a.archived,
			ItemTypeFilter: a.itemType, AfterNumber: a.afterNumber,
			AfterItemID: a.afterItemID, PageSize: a.limit,
		})
		return rows, err
	}
	rows, err := s.q.ListCatalogItemsPlaysAsc(ctx, sqlite.ListCatalogItemsPlaysAscParams{
		MediaServerID: a.serverID, LibraryID: a.libraryID, ArchivedFilter: a.archived,
		AfterNumber: a.afterNumber, AfterItemID: a.afterItemID, PageSize: a.limit,
	})
	return rows, err
}

func (s *sqliteCatalogStore) listSQLiteWatchTime(
	ctx context.Context, query core.CatalogItemQuery, a sqliteCatalogPageArgs,
) ([]sqlite.LibraryItem, error) {
	if query.Order == core.SortDescending {
		if a.itemType != "" {
			rows, err := s.q.ListCatalogItemsWatchDescFiltered(ctx, sqlite.ListCatalogItemsWatchDescFilteredParams{
				MediaServerID: a.serverID, LibraryID: a.libraryID, ArchivedFilter: a.archived,
				ItemTypeFilter: a.itemType, AfterNumber: a.afterNumber,
				AfterItemID: a.afterItemID, PageSize: a.limit,
			})
			return rows, err
		}
		rows, err := s.q.ListCatalogItemsWatchDesc(ctx, sqlite.ListCatalogItemsWatchDescParams{
			MediaServerID: a.serverID, LibraryID: a.libraryID, ArchivedFilter: a.archived,
			AfterNumber: a.afterNumber, AfterItemID: a.afterItemID, PageSize: a.limit,
		})
		return rows, err
	}
	if a.itemType != "" {
		rows, err := s.q.ListCatalogItemsWatchAscFiltered(ctx, sqlite.ListCatalogItemsWatchAscFilteredParams{
			MediaServerID: a.serverID, LibraryID: a.libraryID, ArchivedFilter: a.archived,
			ItemTypeFilter: a.itemType, AfterNumber: a.afterNumber,
			AfterItemID: a.afterItemID, PageSize: a.limit,
		})
		return rows, err
	}
	rows, err := s.q.ListCatalogItemsWatchAsc(ctx, sqlite.ListCatalogItemsWatchAscParams{
		MediaServerID: a.serverID, LibraryID: a.libraryID, ArchivedFilter: a.archived,
		AfterNumber: a.afterNumber, AfterItemID: a.afterItemID, PageSize: a.limit,
	})
	return rows, err
}

func (s *sqliteCatalogStore) listSQLiteCatalogLastPlayed(
	ctx context.Context, query core.CatalogItemQuery,
) ([]sqlite.LibraryItem, error) {
	a := newSQLiteCatalogPageArgs(query)
	if query.Order == core.SortDescending {
		if a.itemType != "" {
			rows, err := s.q.ListCatalogItemsLastPlayedDescFiltered(ctx, sqlite.ListCatalogItemsLastPlayedDescFilteredParams{
				MediaServerID: a.serverID, LibraryID: a.libraryID, ArchivedFilter: a.archived,
				ItemTypeFilter: a.itemType, AfterNull: a.afterNull,
				AfterTime: a.afterTime, AfterItemID: a.afterItemID, PageSize: a.limit,
			})
			return rows, err
		}
		rows, err := s.q.ListCatalogItemsLastPlayedDesc(ctx, sqlite.ListCatalogItemsLastPlayedDescParams{
			MediaServerID: a.serverID, LibraryID: a.libraryID, ArchivedFilter: a.archived,
			AfterNull: a.afterNull, AfterTime: a.afterTime,
			AfterItemID: a.afterItemID, PageSize: a.limit,
		})
		return rows, err
	}
	if a.itemType != "" {
		rows, err := s.q.ListCatalogItemsLastPlayedAscFiltered(ctx, sqlite.ListCatalogItemsLastPlayedAscFilteredParams{
			MediaServerID: a.serverID, LibraryID: a.libraryID, ArchivedFilter: a.archived,
			ItemTypeFilter: a.itemType, AfterNull: a.afterNull,
			AfterTime: a.afterTime, AfterItemID: a.afterItemID, PageSize: a.limit,
		})
		return rows, err
	}
	rows, err := s.q.ListCatalogItemsLastPlayedAsc(ctx, sqlite.ListCatalogItemsLastPlayedAscParams{
		MediaServerID: a.serverID, LibraryID: a.libraryID, ArchivedFilter: a.archived,
		AfterNull: a.afterNull, AfterTime: a.afterTime,
		AfterItemID: a.afterItemID, PageSize: a.limit,
	})
	return rows, err
}
