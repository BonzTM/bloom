package db

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/BonzTM/bloom/internal/config"
	"github.com/BonzTM/bloom/internal/core"
)

const (
	maxCatalogGenreSummaries = 1000
	maxCatalogLibraryRows    = 10000
)

// NewLibraryCatalogStore returns the catalog persistence seam for one engine.
func NewLibraryCatalogStore(pool *sql.DB, driver config.Driver) (core.LibraryCatalogStore, error) {
	if pool == nil {
		return nil, fmt.Errorf("library catalog store: %w", core.ErrInvalidArgument)
	}
	switch driver {
	case config.DriverSQLite:
		return newSQLiteCatalogStore(pool), nil
	case config.DriverPostgres:
		return newPostgresCatalogStore(pool), nil
	default:
		return nil, fmt.Errorf("unsupported database driver %q", driver)
	}
}

func validateCatalogClaim(lease core.LibrarySyncLease, now, dueBefore time.Time) error {
	if lease.Token == "" || now.IsZero() || dueBefore.IsZero() || !lease.ExpiresAt.After(now) {
		return core.ErrInvalidArgument
	}
	return nil
}

func validateCatalogCommit(sync core.LibrarySync, items []core.LibraryItem, cursor string, now time.Time) error {
	if !sync.Valid() || sync.State != core.LibrarySyncRunning || len(items) > core.CatalogPageSize ||
		len(cursor) > core.MaxCatalogCursorBytes || now.IsZero() {
		return core.ErrInvalidArgument
	}
	for _, item := range items {
		if !item.Valid() || item.MediaServerID != sync.MediaServerID {
			return core.ErrInvalidArgument
		}
	}
	return nil
}

func validateCatalogArchive(sync core.LibrarySync, itemIDs []string, now time.Time) error {
	if !sync.Valid() || sync.State != core.LibrarySyncRunning || sync.StartedAt == nil || now.IsZero() ||
		len(itemIDs) < 1 || len(itemIDs) > core.CatalogUserDataBatchSize {
		return core.ErrInvalidArgument
	}
	for _, itemID := range itemIDs {
		if !core.ValidCatalogID(itemID) {
			return core.ErrInvalidArgument
		}
	}
	return nil
}

func validateMissingCatalogIDs(sync core.LibrarySync, after string, limit int) error {
	if !sync.Valid() || sync.State != core.LibrarySyncRunning || sync.StartedAt == nil ||
		(after != "" && !core.ValidCatalogID(after)) || limit < 1 || limit > core.CatalogUserDataBatchSize {
		return core.ErrInvalidArgument
	}
	return nil
}

func catalogStoreError(operation string, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, core.ErrNotFound) || errors.Is(err, core.ErrLibrarySyncLeaseLost) ||
		errors.Is(err, core.ErrLibrarySyncInProgress) || errors.Is(err, core.ErrInvalidArgument) {
		return fmt.Errorf("%s: %w", operation, err)
	}
	return fmt.Errorf("%s: %w", operation, err)
}

func encodeCatalogGenres(genres []string) (string, error) {
	data, err := json.Marshal(genres)
	if err != nil || len(data) > core.MaxCatalogGenresJSONBytes {
		return "", fmt.Errorf("encode catalog genres: %w", errors.Join(err, core.ErrInvalidArgument))
	}
	return string(data), nil
}

func decodeCatalogGenres(value string) ([]string, error) {
	var genres []string
	if len(value) > core.MaxCatalogGenresJSONBytes || json.Unmarshal([]byte(value), &genres) != nil ||
		len(genres) > core.MaxCatalogGenres {
		return nil, errors.New("persisted catalog genres are invalid")
	}
	if genres == nil {
		genres = []string{}
	}
	return genres, nil
}

func catalogInt64(value any) (int64, error) {
	switch number := value.(type) {
	case int64:
		return number, nil
	case int32:
		return int64(number), nil
	case int:
		return int64(number), nil
	case float64:
		if number != math.Trunc(number) || number < math.MinInt64 || number > math.MaxInt64 {
			return 0, errors.New("catalog aggregate is out of range")
		}
		return int64(number), nil
	case []byte:
		return strconv.ParseInt(string(number), 10, 64)
	case string:
		return strconv.ParseInt(number, 10, 64)
	default:
		return 0, fmt.Errorf("catalog aggregate has type %T", value)
	}
}

func catalogOptionalTime(value any) (*time.Time, error) {
	switch typed := value.(type) {
	case nil:
		return nil, nil
	case time.Time:
		normalized := core.NormalizeTime(typed)
		return &normalized, nil
	case string:
		parsed, err := parseSQLiteTime(typed)
		return optionalParsedTime(parsed, err)
	case []byte:
		parsed, err := parseSQLiteTime(string(typed))
		return optionalParsedTime(parsed, err)
	case sql.NullString:
		if !typed.Valid {
			return nil, nil
		}
		parsed, err := parseSQLiteTime(typed.String)
		return optionalParsedTime(parsed, err)
	case sql.NullTime:
		if !typed.Valid {
			return nil, nil
		}
		normalized := core.NormalizeTime(typed.Time)
		return &normalized, nil
	default:
		return nil, fmt.Errorf("catalog time has type %T", value)
	}
}

func optionalParsedTime(value time.Time, err error) (*time.Time, error) {
	if err != nil {
		return nil, err
	}
	return &value, nil
}

func catalogWindowTimes(window core.CatalogWindow) (time.Time, time.Time) {
	if window.Enabled {
		return core.NormalizeTime(window.Start), core.NormalizeTime(window.End)
	}
	return time.Date(1, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(9999, 12, 31, 0, 0, 0, 0, time.UTC)
}

func catalogArchivedFilter(value *bool) int64 {
	if value != nil && *value {
		return 1
	}
	return 0
}

func nullableCatalogInt32(value *int32) sql.NullInt64 {
	if value == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: int64(*value), Valid: true}
}

func catalogInt32(value sql.NullInt64) (*int32, error) {
	if !value.Valid {
		return nil, nil
	}
	if value.Int64 < math.MinInt32 || value.Int64 > math.MaxInt32 {
		return nil, errors.New("catalog integer exceeds int32")
	}
	converted := int32(value.Int64)
	return &converted, nil
}

func nullableCatalogFloat(value *float64) sql.NullFloat64 {
	if value == nil {
		return sql.NullFloat64{}
	}
	return sql.NullFloat64{Float64: *value, Valid: true}
}

func catalogFloat(value sql.NullFloat64) *float64 {
	if !value.Valid {
		return nil
	}
	return &value.Float64
}

func catalogRuntime(value sql.NullInt64) (*time.Duration, error) {
	return durationFromNullMilliseconds(value)
}

func catalogImportItem(
	itemID, name, itemType, seriesID, seriesName, libraryID string,
	season, index, runtime sql.NullInt64,
) (core.CatalogImportItem, error) {
	seasonNumber, err := catalogInt32(season)
	if err != nil {
		return core.CatalogImportItem{}, err
	}
	indexNumber, err := catalogInt32(index)
	if err != nil {
		return core.CatalogImportItem{}, err
	}
	runtimeValue, err := catalogRuntime(runtime)
	if err != nil {
		return core.CatalogImportItem{}, err
	}
	return core.CatalogImportItem{
		ItemID: itemID, ItemName: name, ItemType: itemType, SeriesID: seriesID,
		SeriesName: seriesName, LibraryID: libraryID, SeasonNumber: seasonNumber,
		IndexNumber: indexNumber, Runtime: runtimeValue,
	}, nil
}

func catalogHistoryAfterID(value *core.CatalogHistoryPosition) string {
	if value == nil {
		return strings.Repeat("\U0010ffff", 32)
	}
	return value.ID
}

func sqliteHistoryAfterTime(value *core.CatalogHistoryPosition) string {
	if value == nil {
		return formatSQLiteTime(time.Date(9999, 12, 31, 23, 59, 59, 999999000, time.UTC))
	}
	return formatSQLiteTime(value.StartedAt)
}

func postgresHistoryAfterTime(value *core.CatalogHistoryPosition) time.Time {
	if value == nil {
		return time.Date(9999, 12, 31, 23, 59, 59, 999999000, time.UTC)
	}
	return core.NormalizeTime(value.StartedAt)
}

func staleAfterNull(value *core.CatalogStalePosition) int64 {
	return boolToInt64(value == nil || value.LastPlayedAt == nil)
}

func staleAfterName(value *core.CatalogStalePosition) string {
	if value == nil {
		return ""
	}
	return value.Name
}

func staleAfterItemID(value *core.CatalogStalePosition) string {
	if value == nil {
		return ""
	}
	return value.ItemID
}

func queryStaleTime(value *core.CatalogStalePosition) *time.Time {
	if value == nil || value.LastPlayedAt == nil {
		sentinel := time.Date(1, 1, 1, 0, 0, 0, 0, time.UTC)
		return &sentinel
	}
	return value.LastPlayedAt
}

type storedCatalogItem struct {
	mediaServerID, itemID, libraryID, parentID, itemType, name string
	seriesID, seriesName, seasonID, genres, primaryImageTag    string
	seasonNumber, indexNumber, productionYear                  *int32
	runtime                                                    *time.Duration
	premiereDate, dateCreated                                  *time.Time
	communityRating                                            *float64
	archived                                                   bool
	firstSeenAt, lastSeenAt, updatedAt                         time.Time
}

func (row storedCatalogItem) domain() (core.LibraryItem, error) {
	genres, err := decodeCatalogGenres(row.genres)
	if err != nil {
		return core.LibraryItem{}, err
	}
	item := core.LibraryItem{
		MediaServerID: row.mediaServerID, ItemID: row.itemID, LibraryID: row.libraryID,
		ParentID: row.parentID, ItemType: row.itemType, Name: row.name,
		SeriesID: row.seriesID, SeriesName: row.seriesName, SeasonID: row.seasonID,
		SeasonNumber: row.seasonNumber, IndexNumber: row.indexNumber, Runtime: row.runtime,
		PremiereDate: row.premiereDate, ProductionYear: row.productionYear,
		CommunityRating: row.communityRating, Genres: genres, PrimaryImageTag: row.primaryImageTag,
		DateCreated: row.dateCreated, Archived: row.archived,
		FirstSeenAt: row.firstSeenAt, LastSeenAt: row.lastSeenAt, UpdatedAt: row.updatedAt,
	}
	if !item.Valid() {
		return core.LibraryItem{}, errors.New("persisted catalog item is invalid")
	}
	return item, nil
}

func catalogItemStats(
	item core.LibraryItem, plays, watchSeconds, uniqueUsers int64, first, last any,
) (core.CatalogItemStats, error) {
	firstTime, firstErr := catalogOptionalTime(first)
	lastTime, lastErr := catalogOptionalTime(last)
	if firstErr != nil || lastErr != nil || plays < 0 || watchSeconds < 0 || uniqueUsers < 0 {
		return core.CatalogItemStats{}, errors.Join(firstErr, lastErr, errors.New("invalid catalog aggregates"))
	}
	return core.CatalogItemStats{
		Item: item, Plays: plays, WatchSeconds: watchSeconds, UniqueUsers: uniqueUsers,
		FirstPlayedAt: firstTime, LastPlayedAt: lastTime,
	}, nil
}

func catalogPagePosition(sort core.CatalogSort, item core.CatalogItemStats) core.CatalogItemPosition {
	position := core.CatalogItemPosition{ItemID: item.Item.ItemID}
	switch sort {
	case core.CatalogSortName:
		position.TextValue = item.Item.Name
	case core.CatalogSortDateAdded:
		position.TimeValue = item.Item.DateCreated
	case core.CatalogSortPremiereDate:
		position.TimeValue = item.Item.PremiereDate
	case core.CatalogSortPlays:
		position.NumberValue = item.Plays
	case core.CatalogSortWatchTime:
		position.NumberValue = item.WatchSeconds
	case core.CatalogSortLastPlayed:
		position.TimeValue = item.LastPlayedAt
	}
	return position
}

func catalogLibraries(rows []catalogLibraryRow) ([]core.CatalogLibrarySummary, error) {
	if len(rows) > maxCatalogLibraryRows {
		return nil, errors.New("catalog library aggregate bound exceeded")
	}
	result := make([]core.CatalogLibrarySummary, 0)
	for _, row := range rows {
		if row.libraryID == "" || row.itemType == "" || row.items < 0 || row.plays < 0 || row.watchSeconds < 0 {
			return nil, errors.New("invalid catalog library aggregate")
		}
		if len(result) == 0 || result[len(result)-1].LibraryID != row.libraryID {
			if len(result) >= core.MaxMediaServerLibraries {
				return nil, errors.New("catalog library count exceeds bound")
			}
			result = append(result, core.CatalogLibrarySummary{LibraryID: row.libraryID})
		}
		index := len(result) - 1
		result[index].Types = append(result[index].Types, core.CatalogTypeCount{
			ItemType: row.itemType, Items: row.items, Plays: row.plays, WatchSeconds: row.watchSeconds,
		})
	}
	return result, nil
}

type catalogLibraryRow struct {
	libraryID, itemType        string
	items, plays, watchSeconds int64
}

func rollbackCatalog(tx *sql.Tx, result *error) {
	if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
		*result = errors.Join(*result, err)
	}
}
