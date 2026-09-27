package db

import (
	"context"
	"database/sql"
	"math"
	"strings"
	"time"

	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/db/postgres"
)

type postgresCatalogPageArgs struct {
	serverID, libraryID, itemType, afterItemID string
	afterName                                  string
	archived                                   int64
	afterNull                                  int32
	afterTime                                  sql.NullTime
	afterNumber, limit                         int64
}

func newPostgresCatalogPageArgs(query core.CatalogItemQuery) postgresCatalogPageArgs {
	args := postgresCatalogPageArgs{
		serverID: query.MediaServerID, libraryID: query.LibraryID, itemType: query.ItemType,
		archived: catalogArchivedFilter(query.Archived), limit: int64(query.Limit), afterNumber: -1,
	}
	sentinel := time.Date(1, 1, 1, 0, 0, 0, 0, time.UTC)
	if query.Order == core.SortDescending {
		args.afterName = strings.Repeat("\U0010ffff", 125)
		args.afterNumber = math.MaxInt64
		sentinel = time.Date(9999, 12, 31, 23, 59, 59, 999999000, time.UTC)
	}
	args.afterTime = nullableTime(&sentinel)
	if query.After != nil {
		args.afterItemID = query.After.ItemID
		args.afterName, args.afterNumber = query.After.TextValue, query.After.NumberValue
		args.afterNull = boolToInt32(query.After.TimeValue == nil)
		args.afterTime = nullableTime(query.After.TimeValue)
	}
	return args
}

func (s *postgresCatalogStore) listCatalogItemRows(
	ctx context.Context, query core.CatalogItemQuery,
) ([]postgres.LibraryItem, error) {
	switch query.Sort {
	case core.CatalogSortName:
		return s.listPostgresCatalogName(ctx, query)
	case core.CatalogSortDateAdded:
		return s.listPostgresCatalogDate(ctx, query, false)
	case core.CatalogSortPremiereDate:
		return s.listPostgresCatalogDate(ctx, query, true)
	case core.CatalogSortPlays:
		return s.listPostgresCatalogNumber(ctx, query, false)
	case core.CatalogSortWatchTime:
		return s.listPostgresCatalogNumber(ctx, query, true)
	case core.CatalogSortLastPlayed:
		return s.listPostgresCatalogLastPlayed(ctx, query)
	default:
		return nil, core.ErrInvalidArgument
	}
}

func (s *postgresCatalogStore) listPostgresCatalogName(
	ctx context.Context, query core.CatalogItemQuery,
) ([]postgres.LibraryItem, error) {
	a := newPostgresCatalogPageArgs(query)
	if query.Order == core.SortDescending {
		return s.listPostgresNameDesc(ctx, a)
	}
	if a.itemType != "" {
		rows, err := s.q.ListCatalogItemsNameAscFiltered(ctx, postgres.ListCatalogItemsNameAscFilteredParams{
			MediaServerID: a.serverID, LibraryID: a.libraryID, ArchivedFilter: a.archived,
			ItemTypeFilter: a.itemType, AfterName: a.afterName,
			AfterItemID: a.afterItemID, PageSize: a.limit,
		})
		return rows, err
	}
	return s.q.ListCatalogItemsNameAsc(ctx, postgres.ListCatalogItemsNameAscParams{
		MediaServerID: a.serverID, LibraryID: a.libraryID, ArchivedFilter: a.archived,
		AfterName: a.afterName, AfterItemID: a.afterItemID, PageSize: a.limit,
	})
}

func (s *postgresCatalogStore) listPostgresNameDesc(
	ctx context.Context, a postgresCatalogPageArgs,
) ([]postgres.LibraryItem, error) {
	if a.itemType != "" {
		rows, err := s.q.ListCatalogItemsNameDescFiltered(ctx, postgres.ListCatalogItemsNameDescFilteredParams{
			MediaServerID: a.serverID, LibraryID: a.libraryID, ArchivedFilter: a.archived,
			ItemTypeFilter: a.itemType, AfterName: a.afterName,
			AfterItemID: a.afterItemID, PageSize: a.limit,
		})
		return rows, err
	}
	rows, err := s.q.ListCatalogItemsNameDesc(ctx, postgres.ListCatalogItemsNameDescParams{
		MediaServerID: a.serverID, LibraryID: a.libraryID, ArchivedFilter: a.archived,
		AfterName: a.afterName, AfterItemID: a.afterItemID, PageSize: a.limit,
	})
	return rows, err
}

func (s *postgresCatalogStore) listPostgresCatalogDate(
	ctx context.Context, query core.CatalogItemQuery, premiere bool,
) ([]postgres.LibraryItem, error) {
	a := newPostgresCatalogPageArgs(query)
	if premiere {
		return s.listPostgresPremiere(ctx, query, a)
	}
	if query.Order == core.SortDescending {
		if a.itemType != "" {
			rows, err := s.q.ListCatalogItemsDateDescFiltered(ctx, postgres.ListCatalogItemsDateDescFilteredParams{
				MediaServerID: a.serverID, LibraryID: a.libraryID, ArchivedFilter: a.archived,
				ItemTypeFilter: a.itemType, AfterNull: a.afterNull,
				AfterTime: a.afterTime, AfterItemID: a.afterItemID, PageSize: a.limit,
			})
			return rows, err
		}
		rows, err := s.q.ListCatalogItemsDateDesc(ctx, postgres.ListCatalogItemsDateDescParams{
			MediaServerID: a.serverID, LibraryID: a.libraryID, ArchivedFilter: a.archived,
			AfterNull: a.afterNull, AfterTime: a.afterTime,
			AfterItemID: a.afterItemID, PageSize: a.limit,
		})
		return rows, err
	}
	if a.itemType != "" {
		rows, err := s.q.ListCatalogItemsDateAscFiltered(ctx, postgres.ListCatalogItemsDateAscFilteredParams{
			MediaServerID: a.serverID, LibraryID: a.libraryID, ArchivedFilter: a.archived,
			ItemTypeFilter: a.itemType, AfterNull: a.afterNull,
			AfterTime: a.afterTime, AfterItemID: a.afterItemID, PageSize: a.limit,
		})
		return rows, err
	}
	rows, err := s.q.ListCatalogItemsDateAsc(ctx, postgres.ListCatalogItemsDateAscParams{
		MediaServerID: a.serverID, LibraryID: a.libraryID, ArchivedFilter: a.archived,
		AfterNull: a.afterNull, AfterTime: a.afterTime,
		AfterItemID: a.afterItemID, PageSize: a.limit,
	})
	return rows, err
}

func (s *postgresCatalogStore) listPostgresPremiere(
	ctx context.Context, query core.CatalogItemQuery, a postgresCatalogPageArgs,
) ([]postgres.LibraryItem, error) {
	if query.Order == core.SortDescending {
		if a.itemType != "" {
			rows, err := s.q.ListCatalogItemsPremiereDescFiltered(ctx, postgres.ListCatalogItemsPremiereDescFilteredParams{
				MediaServerID: a.serverID, LibraryID: a.libraryID, ArchivedFilter: a.archived,
				ItemTypeFilter: a.itemType, AfterNull: a.afterNull,
				AfterTime: a.afterTime, AfterItemID: a.afterItemID, PageSize: a.limit,
			})
			return rows, err
		}
		rows, err := s.q.ListCatalogItemsPremiereDesc(ctx, postgres.ListCatalogItemsPremiereDescParams{
			MediaServerID: a.serverID, LibraryID: a.libraryID, ArchivedFilter: a.archived,
			AfterNull: a.afterNull, AfterTime: a.afterTime,
			AfterItemID: a.afterItemID, PageSize: a.limit,
		})
		return rows, err
	}
	if a.itemType != "" {
		rows, err := s.q.ListCatalogItemsPremiereAscFiltered(ctx, postgres.ListCatalogItemsPremiereAscFilteredParams{
			MediaServerID: a.serverID, LibraryID: a.libraryID, ArchivedFilter: a.archived,
			ItemTypeFilter: a.itemType, AfterNull: a.afterNull,
			AfterTime: a.afterTime, AfterItemID: a.afterItemID, PageSize: a.limit,
		})
		return rows, err
	}
	rows, err := s.q.ListCatalogItemsPremiereAsc(ctx, postgres.ListCatalogItemsPremiereAscParams{
		MediaServerID: a.serverID, LibraryID: a.libraryID, ArchivedFilter: a.archived,
		AfterNull: a.afterNull, AfterTime: a.afterTime,
		AfterItemID: a.afterItemID, PageSize: a.limit,
	})
	return rows, err
}

func (s *postgresCatalogStore) listPostgresCatalogNumber(
	ctx context.Context, query core.CatalogItemQuery, watchTime bool,
) ([]postgres.LibraryItem, error) {
	a := newPostgresCatalogPageArgs(query)
	if watchTime {
		return s.listPostgresWatchTime(ctx, query, a)
	}
	if query.Order == core.SortDescending {
		if a.itemType != "" {
			rows, err := s.q.ListCatalogItemsPlaysDescFiltered(ctx, postgres.ListCatalogItemsPlaysDescFilteredParams{
				MediaServerID: a.serverID, LibraryID: a.libraryID, ArchivedFilter: a.archived,
				ItemTypeFilter: a.itemType, AfterNumber: a.afterNumber,
				AfterItemID: a.afterItemID, PageSize: a.limit,
			})
			return rows, err
		}
		rows, err := s.q.ListCatalogItemsPlaysDesc(ctx, postgres.ListCatalogItemsPlaysDescParams{
			MediaServerID: a.serverID, LibraryID: a.libraryID, ArchivedFilter: a.archived,
			AfterNumber: a.afterNumber, AfterItemID: a.afterItemID, PageSize: a.limit,
		})
		return rows, err
	}
	if a.itemType != "" {
		rows, err := s.q.ListCatalogItemsPlaysAscFiltered(ctx, postgres.ListCatalogItemsPlaysAscFilteredParams{
			MediaServerID: a.serverID, LibraryID: a.libraryID, ArchivedFilter: a.archived,
			ItemTypeFilter: a.itemType, AfterNumber: a.afterNumber,
			AfterItemID: a.afterItemID, PageSize: a.limit,
		})
		return rows, err
	}
	rows, err := s.q.ListCatalogItemsPlaysAsc(ctx, postgres.ListCatalogItemsPlaysAscParams{
		MediaServerID: a.serverID, LibraryID: a.libraryID, ArchivedFilter: a.archived,
		AfterNumber: a.afterNumber, AfterItemID: a.afterItemID, PageSize: a.limit,
	})
	return rows, err
}

func (s *postgresCatalogStore) listPostgresWatchTime(
	ctx context.Context, query core.CatalogItemQuery, a postgresCatalogPageArgs,
) ([]postgres.LibraryItem, error) {
	if query.Order == core.SortDescending {
		if a.itemType != "" {
			rows, err := s.q.ListCatalogItemsWatchDescFiltered(ctx, postgres.ListCatalogItemsWatchDescFilteredParams{
				MediaServerID: a.serverID, LibraryID: a.libraryID, ArchivedFilter: a.archived,
				ItemTypeFilter: a.itemType, AfterNumber: a.afterNumber,
				AfterItemID: a.afterItemID, PageSize: a.limit,
			})
			return rows, err
		}
		rows, err := s.q.ListCatalogItemsWatchDesc(ctx, postgres.ListCatalogItemsWatchDescParams{
			MediaServerID: a.serverID, LibraryID: a.libraryID, ArchivedFilter: a.archived,
			AfterNumber: a.afterNumber, AfterItemID: a.afterItemID, PageSize: a.limit,
		})
		return rows, err
	}
	if a.itemType != "" {
		rows, err := s.q.ListCatalogItemsWatchAscFiltered(ctx, postgres.ListCatalogItemsWatchAscFilteredParams{
			MediaServerID: a.serverID, LibraryID: a.libraryID, ArchivedFilter: a.archived,
			ItemTypeFilter: a.itemType, AfterNumber: a.afterNumber,
			AfterItemID: a.afterItemID, PageSize: a.limit,
		})
		return rows, err
	}
	rows, err := s.q.ListCatalogItemsWatchAsc(ctx, postgres.ListCatalogItemsWatchAscParams{
		MediaServerID: a.serverID, LibraryID: a.libraryID, ArchivedFilter: a.archived,
		AfterNumber: a.afterNumber, AfterItemID: a.afterItemID, PageSize: a.limit,
	})
	return rows, err
}

func (s *postgresCatalogStore) listPostgresCatalogLastPlayed(
	ctx context.Context, query core.CatalogItemQuery,
) ([]postgres.LibraryItem, error) {
	a := newPostgresCatalogPageArgs(query)
	if query.Order == core.SortDescending {
		if a.itemType != "" {
			rows, err := s.q.ListCatalogItemsLastPlayedDescFiltered(ctx, postgres.ListCatalogItemsLastPlayedDescFilteredParams{
				MediaServerID: a.serverID, LibraryID: a.libraryID, ArchivedFilter: a.archived,
				ItemTypeFilter: a.itemType, AfterNull: a.afterNull,
				AfterTime: a.afterTime, AfterItemID: a.afterItemID, PageSize: a.limit,
			})
			return rows, err
		}
		rows, err := s.q.ListCatalogItemsLastPlayedDesc(ctx, postgres.ListCatalogItemsLastPlayedDescParams{
			MediaServerID: a.serverID, LibraryID: a.libraryID, ArchivedFilter: a.archived,
			AfterNull: a.afterNull, AfterTime: a.afterTime,
			AfterItemID: a.afterItemID, PageSize: a.limit,
		})
		return rows, err
	}
	if a.itemType != "" {
		rows, err := s.q.ListCatalogItemsLastPlayedAscFiltered(ctx, postgres.ListCatalogItemsLastPlayedAscFilteredParams{
			MediaServerID: a.serverID, LibraryID: a.libraryID, ArchivedFilter: a.archived,
			ItemTypeFilter: a.itemType, AfterNull: a.afterNull,
			AfterTime: a.afterTime, AfterItemID: a.afterItemID, PageSize: a.limit,
		})
		return rows, err
	}
	rows, err := s.q.ListCatalogItemsLastPlayedAsc(ctx, postgres.ListCatalogItemsLastPlayedAscParams{
		MediaServerID: a.serverID, LibraryID: a.libraryID, ArchivedFilter: a.archived,
		AfterNull: a.afterNull, AfterTime: a.afterTime,
		AfterItemID: a.afterItemID, PageSize: a.limit,
	})
	return rows, err
}
