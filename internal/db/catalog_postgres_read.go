package db

import (
	"context"
	"database/sql"
	"errors"

	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/db/postgres"
)

func (s *postgresCatalogStore) ResolveImportLibrary(
	ctx context.Context, serverID, itemID string,
) (string, bool, error) {
	if !core.ValidID(serverID) || !core.ValidCatalogID(itemID) {
		return "", false, core.ErrInvalidArgument
	}
	libraryID, err := s.q.GetCatalogItemLibrary(ctx, postgres.GetCatalogItemLibraryParams{
		MediaServerID: serverID, ItemID: itemID,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, catalogStoreError("resolve import library", err)
	}
	return libraryID, true, nil
}

func (s *postgresCatalogStore) ListCatalogLibraries(
	ctx context.Context, serverID string, window core.CatalogWindow,
) ([]core.CatalogLibrarySummary, error) {
	if !core.ValidID(serverID) || !window.Valid() {
		return nil, core.ErrInvalidArgument
	}
	start, end := catalogWindowTimes(window)
	rows, err := s.q.CatalogLibraryTypeRows(ctx, postgres.CatalogLibraryTypeRowsParams{
		MediaServerID: serverID, WindowEnabled: boolToInt32(window.Enabled), WindowStart: start, WindowEnd: end,
	})
	if err != nil {
		return nil, catalogStoreError("list catalog libraries", err)
	}
	mapped := make([]catalogLibraryRow, 0, len(rows))
	for _, row := range rows {
		plays, playsErr := catalogInt64(row.Plays)
		seconds, secondsErr := catalogInt64(row.WatchSeconds)
		if playsErr != nil || secondsErr != nil {
			return nil, errors.Join(playsErr, secondsErr)
		}
		mapped = append(mapped, catalogLibraryRow{row.LibraryID, row.ItemType, row.ItemCount, plays, seconds})
	}
	return catalogLibraries(mapped)
}

func (s *postgresCatalogStore) ListCatalogItems(
	ctx context.Context, query core.CatalogItemQuery,
) ([]core.CatalogItemStats, error) {
	if !query.Valid() {
		return nil, core.ErrInvalidArgument
	}
	rows, err := s.listCatalogItemRows(ctx, query)
	if err != nil {
		return nil, catalogStoreError("list catalog items", err)
	}
	result := make([]core.CatalogItemStats, 0, len(rows))
	for _, row := range rows {
		stats, statsErr := postgresCatalogStats(row)
		if statsErr != nil {
			return nil, statsErr
		}
		stats.PagePosition = catalogPagePosition(query.Sort, stats)
		if query.Window.Enabled {
			stats, statsErr = s.postgresCatalogWindowStats(ctx, stats, query.Window)
			if statsErr != nil {
				return nil, statsErr
			}
		}
		result = append(result, stats)
	}
	return result, nil
}

func (s *postgresCatalogStore) postgresCatalogWindowStats(
	ctx context.Context, item core.CatalogItemStats, window core.CatalogWindow,
) (core.CatalogItemStats, error) {
	row, err := s.q.CatalogItemWindowSummary(ctx, postgres.CatalogItemWindowSummaryParams{
		MediaServerID: item.Item.MediaServerID, ItemID: item.Item.ItemID,
		WindowStart: core.NormalizeTime(window.Start), WindowEnd: core.NormalizeTime(window.End),
	})
	if err != nil {
		return core.CatalogItemStats{}, catalogStoreError("summarize catalog item window", err)
	}
	seconds, secondsErr := catalogInt64(row.WatchSeconds)
	windowed, statsErr := catalogItemStats(
		item.Item, row.Plays, seconds, row.UniqueUsers, row.FirstPlayedAt, row.LastPlayedAt,
	)
	if secondsErr != nil || statsErr != nil {
		return core.CatalogItemStats{}, errors.Join(secondsErr, statsErr)
	}
	windowed.PagePosition = item.PagePosition
	return windowed, nil
}

func (s *postgresCatalogStore) GetCatalogItem(
	ctx context.Context, serverID, itemID string,
) (core.CatalogItemDetail, error) {
	if !core.ValidID(serverID) || !core.ValidCatalogID(itemID) {
		return core.CatalogItemDetail{}, core.ErrInvalidArgument
	}
	row, err := s.q.GetCatalogItem(ctx, postgres.GetCatalogItemParams{MediaServerID: serverID, ItemID: itemID})
	if err != nil {
		return core.CatalogItemDetail{}, catalogStoreError("get catalog item", importNotFound(err))
	}
	item, err := postgresCatalogItem(row)
	if err != nil {
		return core.CatalogItemDetail{}, err
	}
	stats, err := s.postgresItemPlaySummary(ctx, item)
	if err != nil {
		return core.CatalogItemDetail{}, err
	}
	children, err := s.q.CatalogChildSummary(ctx, postgres.CatalogChildSummaryParams{
		MediaServerID: serverID, ItemID: itemID,
	})
	if err != nil {
		return core.CatalogItemDetail{}, catalogStoreError("summarize catalog children", err)
	}
	result := core.CatalogItemDetail{Item: stats, Children: make([]core.CatalogChildSummary, 0, len(children))}
	for _, child := range children {
		result.Children = append(result.Children, core.CatalogChildSummary{ItemType: child.ItemType, Items: child.ItemCount})
	}
	return result, nil
}

func (s *postgresCatalogStore) postgresItemPlaySummary(
	ctx context.Context, item core.LibraryItem,
) (core.CatalogItemStats, error) {
	row, err := s.q.CatalogItemPlaySummary(ctx, postgres.CatalogItemPlaySummaryParams{
		ServerKey: item.MediaServerID, CatalogKey: item.ItemID,
	})
	if err != nil {
		return core.CatalogItemStats{}, catalogStoreError("summarize catalog item", err)
	}
	seconds, err := catalogInt64(row.WatchSeconds)
	if err != nil {
		return core.CatalogItemStats{}, err
	}
	return catalogItemStats(item, row.Plays, seconds, row.UniqueUsers, row.FirstPlayedAt, row.LastPlayedAt)
}

func (s *postgresCatalogStore) ListCatalogHistory(
	ctx context.Context, query core.CatalogHistoryQuery,
) ([]core.PlaybackWatch, error) {
	if !query.Valid() {
		return nil, core.ErrInvalidArgument
	}
	rows, err := s.q.ListCatalogItemHistory(ctx, postgres.ListCatalogItemHistoryParams{
		ServerKey: query.MediaServerID, CatalogKey: query.ItemID,
		AfterStartedAt: postgresHistoryAfterTime(query.After),
		AfterID:        catalogHistoryAfterID(query.After), PageSize: int64(query.Limit),
	})
	if err != nil {
		return nil, catalogStoreError("list catalog history", err)
	}
	result := make([]core.PlaybackWatch, 0, len(rows))
	for _, row := range rows {
		watch, mapErr := postgresCatalogWatch(row)
		if mapErr != nil {
			return nil, catalogStoreError("map catalog history", mapErr)
		}
		result = append(result, watch)
	}
	return result, nil
}

func (s *postgresCatalogStore) ListRecentCatalogItems(
	ctx context.Context, serverID, libraryID string, limit int,
) ([]core.CatalogItemStats, error) {
	if !core.ValidID(serverID) || !core.ValidLibraryID(libraryID) || limit < 1 || limit > core.MaxCatalogReadPageSize {
		return nil, core.ErrInvalidArgument
	}
	rows, err := s.q.ListRecentCatalogItems(ctx, postgres.ListRecentCatalogItemsParams{
		MediaServerID: serverID, LibraryID: libraryID, RowLimit: int64(limit),
	})
	if err != nil {
		return nil, catalogStoreError("list recent catalog items", err)
	}
	result := make([]core.CatalogItemStats, 0, len(rows))
	for _, row := range rows {
		stats, mapErr := postgresCatalogStats(row)
		if mapErr != nil {
			return nil, mapErr
		}
		result = append(result, stats)
	}
	return result, nil
}

func (s *postgresCatalogStore) ListCatalogGenres(
	ctx context.Context, serverID, libraryID string, window core.CatalogWindow,
) ([]core.CatalogGenreSummary, error) {
	if !core.ValidID(serverID) || !core.ValidLibraryID(libraryID) || !window.Valid() {
		return nil, core.ErrInvalidArgument
	}
	start, end := catalogWindowTimes(window)
	rows, err := s.q.ListCatalogGenreRows(ctx, postgres.ListCatalogGenreRowsParams{
		MediaServerID: serverID, LibraryID: libraryID, WindowEnabled: boolToInt32(window.Enabled),
		WindowStart: start, WindowEnd: end,
	})
	if err != nil {
		return nil, catalogStoreError("list catalog genres", err)
	}
	if len(rows) > maxCatalogGenreSummaries {
		return nil, errors.New("catalog genre summary bound exceeded")
	}
	result := make([]core.CatalogGenreSummary, 0, len(rows))
	for _, row := range rows {
		seconds, mapErr := catalogInt64(row.WatchSeconds)
		if mapErr != nil {
			return nil, mapErr
		}
		result = append(result, core.CatalogGenreSummary{
			Genre: row.Genre, Items: row.ItemCount, Plays: row.Plays, WatchSeconds: seconds,
		})
	}
	return result, nil
}

func (s *postgresCatalogStore) ListStaleCatalogItems(
	ctx context.Context, query core.CatalogStaleQuery,
) ([]core.CatalogItemStats, error) {
	if !query.Valid() {
		return nil, core.ErrInvalidArgument
	}
	rows, err := s.q.ListStaleCatalogItems(ctx, postgres.ListStaleCatalogItemsParams{
		MediaServerID: query.MediaServerID, LibraryID: query.LibraryID, StaleBefore: nullableTime(&query.Before),
		AfterNull: boolToInt32(query.After == nil || query.After.LastPlayedAt == nil),
		AfterName: staleAfterName(query.After), AfterItemID: staleAfterItemID(query.After),
		AfterTime: nullableTime(queryStaleTime(query.After)), PageSize: int64(query.Limit),
	})
	if err != nil {
		return nil, catalogStoreError("list stale catalog items", err)
	}
	return postgresStaleItems(rows)
}

func (s *postgresCatalogStore) ListCatalogImportItems(
	ctx context.Context, serverID, afterID string, limit int,
) ([]core.CatalogImportItem, error) {
	if !core.ValidID(serverID) || (afterID != "" && !core.ValidCatalogID(afterID)) ||
		limit < 1 || limit > core.CatalogUserDataBatchSize {
		return nil, core.ErrInvalidArgument
	}
	rows, err := s.q.ListCatalogImportItems(ctx, postgres.ListCatalogImportItemsParams{
		MediaServerID: serverID, AfterItemID: afterID, RowLimit: int64(limit),
	})
	if err != nil {
		return nil, catalogStoreError("list catalog import items", err)
	}
	result := make([]core.CatalogImportItem, 0, len(rows))
	for _, row := range rows {
		item, mapErr := postgresCatalogImportItem(row)
		if mapErr != nil {
			return nil, mapErr
		}
		result = append(result, item)
	}
	return result, nil
}

func postgresCatalogItem(row postgres.LibraryItem) (core.LibraryItem, error) {
	runtime, err := catalogRuntime(row.RuntimeMs)
	if err != nil {
		return core.LibraryItem{}, err
	}
	return (storedCatalogItem{
		mediaServerID: row.MediaServerID, itemID: row.ItemID, libraryID: row.LibraryID,
		parentID: row.ParentID, itemType: row.ItemType, name: row.Name,
		seriesID: row.SeriesID, seriesName: row.SeriesName, seasonID: row.SeasonID,
		seasonNumber: int32FromNull(row.SeasonNumber), indexNumber: int32FromNull(row.IndexNumber),
		productionYear: int32FromNull(row.ProductionYear), runtime: runtime,
		premiereDate: timeFromNull(row.PremiereDate), dateCreated: timeFromNull(row.DateCreated),
		communityRating: catalogFloat(row.CommunityRating), genres: row.Genres, primaryImageTag: row.PrimaryImageTag,
		archived: row.Archived, firstSeenAt: core.NormalizeTime(row.FirstSeenAt),
		lastSeenAt: core.NormalizeTime(row.LastSeenAt), updatedAt: core.NormalizeTime(row.UpdatedAt),
	}).domain()
}

func postgresCatalogStats(row postgres.LibraryItem) (core.CatalogItemStats, error) {
	item, itemErr := postgresCatalogItem(row)
	stats, statsErr := catalogItemStats(
		item, row.Plays, row.WatchSeconds, row.UniqueUsers, row.FirstPlayedAt, row.LastPlayedAt,
	)
	return stats, errors.Join(itemErr, statsErr)
}

func postgresStaleItems(rows []postgres.LibraryItem) ([]core.CatalogItemStats, error) {
	result := make([]core.CatalogItemStats, 0, len(rows))
	for _, row := range rows {
		stats, err := postgresCatalogStats(row)
		if err != nil {
			return nil, err
		}
		result = append(result, stats)
	}
	return result, nil
}

func postgresCatalogImportItem(row postgres.ListCatalogImportItemsRow) (core.CatalogImportItem, error) {
	runtime, err := catalogRuntime(row.RuntimeMs)
	if err != nil {
		return core.CatalogImportItem{}, err
	}
	return core.CatalogImportItem{
		ItemID: row.ItemID, ItemName: row.Name, ItemType: row.ItemType,
		SeriesID: row.SeriesID, SeriesName: row.SeriesName, LibraryID: row.LibraryID,
		SeasonNumber: int32FromNull(row.SeasonNumber), IndexNumber: int32FromNull(row.IndexNumber), Runtime: runtime,
	}, nil
}

func postgresCatalogWatch(row postgres.ListCatalogItemHistoryRow) (core.PlaybackWatch, error) {
	return postgresStoredWatch(
		row.ID, row.MediaServerID, row.MediaServerName, row.MediaUserID, row.Username,
		row.DeviceID, row.DeviceName, row.Client, row.ServerSessionID,
		row.ItemID, row.ItemName, row.ItemType, row.SeriesID, row.SeriesName, row.LibraryID, row.LibraryName,
		row.SeasonNumber, row.EpisodeNumber, row.PlayMethod, row.State, row.StartedAt, row.LastSeenAt, row.EndedAt,
		row.ActiveSeconds, row.LastPositionMs, row.RuntimeMs, row.Source, row.CreatedAt, row.UpdatedAt,
		row.ImportSource, row.ImportRecordID, row.ImportOriginRecordID,
		postgresStream(row.StreamContainer, row.StreamVideoCodec, row.StreamAudioCodec, row.StreamBitrate,
			row.StreamWidth, row.StreamHeight, row.StreamFramerateHundredths, row.StreamAudioChannels,
			row.StreamIsVideoDirect, row.StreamIsAudioDirect, row.StreamTranscodeReasons),
	)
}

func boolToInt32(value bool) int32 {
	if value {
		return 1
	}
	return 0
}
