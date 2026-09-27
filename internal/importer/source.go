package importer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"time"

	"github.com/BonzTM/bloom/internal/core"
)

// PlaybackReportingService reads plugin pages for a registered server.
type PlaybackReportingService interface {
	PlaybackReporting(context.Context, string, int64, int) (core.PlaybackReportingPage, error)
}

// UserDataService joins bounded catalog pages with Jellyfin per-user state.
type UserDataService interface {
	ListCatalogImportItems(context.Context, string, string, int) ([]core.CatalogImportItem, error)
	NextCatalogUser(context.Context, string, string) (core.CatalogUser, error)
	CatalogUserData(context.Context, string, string, []string) ([]core.LibraryUserData, error)
}

type playbackReportingSource struct{ service PlaybackReportingService }

func (playbackReportingSource) Close() error { return nil }

func (s playbackReportingSource) ReadImportBatch(
	ctx context.Context, job core.ImportJob,
) ([]core.ImportedWatch, string, int64, error) {
	cursor, err := strconv.ParseInt(job.Cursor, 10, 64)
	if err != nil || cursor < 0 {
		return nil, "", 0, fmt.Errorf("parse playback reporting cursor: %w", core.ErrInvalidArgument)
	}
	page, err := s.service.PlaybackReporting(ctx, job.MediaServerID, cursor, core.ImportBatchSize)
	if err != nil || len(page.Records) == 0 && page.Skipped == 0 {
		return page.Records, job.Cursor, page.Skipped, err
	}
	if page.Cursor <= cursor {
		return nil, "", 0, fmt.Errorf("validate playback reporting cursor: %w", core.ErrInvalidArgument)
	}
	return page.Records, strconv.FormatInt(page.Cursor, 10), page.Skipped, nil
}

type sourceFactory struct {
	reporting    PlaybackReportingService
	userData     UserDataService
	staging      *Staging
	storeTimeout time.Duration
	openWatch    watchOpener
}

type jobSource interface {
	core.ImportSourceReader
	io.Closer
}

func (f sourceFactory) reader(source core.ImportSource) (jobSource, error) {
	switch source {
	case core.ImportSourcePlaybackReporting:
		return playbackReportingSource{service: f.reporting}, nil
	case core.ImportSourceBloomExport:
		return &jsonlReader{
			staging: f.staging, storeTimeout: f.storeTimeout, openWatch: f.openWatch,
		}, nil
	case core.ImportSourceJellyfinUserData:
		return userDataSource{service: f.userData}, nil
	default:
		return nil, core.ErrInvalidArgument
	}
}

type userDataSource struct{ service UserDataService }

func (userDataSource) Close() error { return nil }

type userDataCursor struct {
	UserID, Username, ItemID string
}

func (s userDataSource) ReadImportBatch(
	ctx context.Context, job core.ImportJob,
) ([]core.ImportedWatch, string, int64, error) {
	cursor, err := decodeUserDataCursor(job.Cursor)
	if err != nil || s.service == nil {
		return nil, "", 0, fmt.Errorf("prepare Jellyfin user-data import: %w", core.ErrInvalidArgument)
	}
	records := make([]core.ImportedWatch, 0, core.ImportBatchSize)
	var skipped int64
	for range core.ImportBatchSize / core.CatalogUserDataBatchSize {
		if cursor.UserID == "" {
			cursor, err = s.nextUser(ctx, job.MediaServerID, cursor.UserID)
			if errors.Is(err, core.ErrNotFound) {
				return finishUserDataBatch(records, cursor, skipped)
			}
			if err != nil {
				return nil, "", 0, err
			}
		}
		items, listErr := s.service.ListCatalogImportItems(
			ctx, job.MediaServerID, cursor.ItemID, core.CatalogUserDataBatchSize,
		)
		if listErr != nil {
			return nil, "", 0, listErr
		}
		if len(items) == 0 {
			previous := cursor.UserID
			next, nextErr := s.nextUser(ctx, job.MediaServerID, previous)
			err = nextErr
			if errors.Is(err, core.ErrNotFound) {
				return finishUserDataBatch(records, cursor, skipped)
			}
			if err != nil {
				return nil, "", 0, err
			}
			cursor = next
			continue
		}
		mapped, batchSkipped, readErr := s.readItems(ctx, job.MediaServerID, cursor, items)
		if readErr != nil {
			return nil, "", 0, readErr
		}
		records = append(records, mapped...)
		skipped += batchSkipped
		cursor.ItemID = items[len(items)-1].ItemID
	}
	encoded, err := encodeUserDataCursor(cursor)
	return records, encoded, skipped, err
}

func finishUserDataBatch(
	records []core.ImportedWatch, cursor userDataCursor, skipped int64,
) ([]core.ImportedWatch, string, int64, error) {
	encoded, err := encodeUserDataCursor(cursor)
	return records, encoded, skipped, err
}

func (s userDataSource) nextUser(
	ctx context.Context, serverID, afterUserID string,
) (userDataCursor, error) {
	user, err := s.service.NextCatalogUser(ctx, serverID, afterUserID)
	if err != nil {
		return userDataCursor{}, err
	}
	return userDataCursor{UserID: user.MediaUserID, Username: user.Username}, nil
}

func (s userDataSource) readItems(
	ctx context.Context, serverID string, cursor userDataCursor, items []core.CatalogImportItem,
) ([]core.ImportedWatch, int64, error) {
	ids := make([]string, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ItemID)
	}
	userData, err := s.service.CatalogUserData(ctx, serverID, cursor.UserID, ids)
	if err != nil {
		return nil, 0, err
	}
	byID := make(map[string]core.LibraryUserData, len(userData))
	for _, value := range userData {
		byID[value.ItemID] = value
	}
	result := make([]core.ImportedWatch, 0, len(items))
	var skipped int64
	for _, item := range items {
		state, found := byID[item.ItemID]
		if !found || state.PlayCount == 0 || state.LastPlayedAt == nil {
			skipped++
			continue
		}
		result = append(result, importedUserDataWatch(cursor, item, state))
	}
	return result, skipped, nil
}

func importedUserDataWatch(
	cursor userDataCursor, item core.CatalogImportItem, state core.LibraryUserData,
) core.ImportedWatch {
	ended := *state.LastPlayedAt
	return core.ImportedWatch{
		RecordID: item.ItemID, MediaUserID: cursor.UserID, Username: cursor.Username,
		ItemID: item.ItemID, ItemName: item.ItemName, ItemType: item.ItemType,
		SeriesID: item.SeriesID, SeriesName: item.SeriesName, LibraryID: item.LibraryID,
		SeasonNumber: item.SeasonNumber, EpisodeNumber: item.IndexNumber,
		PlayMethod: core.PlayMethodUnknown, StartedAt: *state.LastPlayedAt, EndedAt: &ended,
		Runtime: item.Runtime, LastPosition: state.PlaybackPosition,
	}
}

func decodeUserDataCursor(value string) (userDataCursor, error) {
	var cursor userDataCursor
	if len(value) > core.MaxImportCursorBytes || json.Unmarshal([]byte(value), &cursor) != nil {
		return userDataCursor{}, core.ErrInvalidArgument
	}
	if cursor.UserID != "" && !core.ValidAccountMediaUserID(cursor.UserID) ||
		cursor.ItemID != "" && !core.ValidCatalogID(cursor.ItemID) || len(cursor.Username) > core.MaxMediaUsernameBytes {
		return userDataCursor{}, core.ErrInvalidArgument
	}
	return cursor, nil
}

func encodeUserDataCursor(cursor userDataCursor) (string, error) {
	encoded, err := json.Marshal(cursor)
	if err != nil || len(encoded) > core.MaxImportCursorBytes {
		return "", core.ErrInvalidArgument
	}
	return string(encoded), nil
}
