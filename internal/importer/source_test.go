package importer

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/BonzTM/bloom/internal/core"
)

type reportingPageStub struct{ page core.PlaybackReportingPage }

func (s reportingPageStub) PlaybackReporting(
	context.Context, string, int64, int,
) (core.PlaybackReportingPage, error) {
	return s.page, nil
}

type userDataSourceStub struct{ playedAt time.Time }

func (s userDataSourceStub) ListCatalogImportItems(
	_ context.Context, _, after string, _ int,
) ([]core.CatalogImportItem, error) {
	if after != "" {
		return nil, nil
	}
	runtime := 45 * time.Minute
	return []core.CatalogImportItem{{
		ItemID: "item-1", ItemName: "Episode", ItemType: "Episode", SeriesID: "series-1",
		SeriesName: "Series", LibraryID: "library-1", Runtime: &runtime,
	}}, nil
}

func (s userDataSourceStub) NextCatalogUser(
	_ context.Context, _, after string,
) (core.CatalogUser, error) {
	if after != "" {
		return core.CatalogUser{}, core.ErrNotFound
	}
	return core.CatalogUser{
		MediaUserID: "92000000-0000-4000-8000-000000000001", Username: "viewer",
	}, nil
}

func (s userDataSourceStub) CatalogUserData(
	context.Context, string, string, []string,
) ([]core.LibraryUserData, error) {
	return []core.LibraryUserData{{
		ItemID: "item-1", PlayCount: 3, LastPlayedAt: &s.playedAt, PlaybackPosition: time.Minute,
	}}, nil
}

func TestPlaybackReportingSourceAdvancesAcrossSkippedRows(t *testing.T) {
	source := playbackReportingSource{service: reportingPageStub{page: core.PlaybackReportingPage{
		Cursor: 42, Skipped: 1,
	}}}
	job := core.ImportJob{MediaServerID: "11111111-1111-4111-8111-111111111111", Cursor: "41"}
	records, cursor, skipped, err := source.ReadImportBatch(t.Context(), job)
	if err != nil || len(records) != 0 || cursor != "42" || skipped != 1 {
		t.Fatalf("ReadImportBatch = %+v, %q, %d, %v", records, cursor, skipped, err)
	}
}

func TestPlaybackReportingSourceRejectsNonAdvancingPage(t *testing.T) {
	source := playbackReportingSource{service: reportingPageStub{page: core.PlaybackReportingPage{
		Cursor: 41, Skipped: 1,
	}}}
	job := core.ImportJob{MediaServerID: "11111111-1111-4111-8111-111111111111", Cursor: "41"}
	if _, _, _, err := source.ReadImportBatch(t.Context(), job); err == nil {
		t.Fatal("ReadImportBatch accepted a non-advancing page")
	}
}

func TestUserDataSourceCreatesOneSyntheticWatchAndThenCompletes(t *testing.T) {
	playedAt := time.Date(2026, 9, 27, 10, 0, 0, 0, time.UTC)
	source := userDataSource{service: userDataSourceStub{playedAt: playedAt}}
	job := core.ImportJob{
		MediaServerID: "93000000-0000-4000-8000-000000000001", Cursor: "{}",
	}
	records, cursor, skipped, err := source.ReadImportBatch(t.Context(), job)
	if err != nil || len(records) != 1 || skipped != 0 || cursor == "{}" {
		t.Fatalf("first ReadImportBatch = %+v, %q, %d, %v", records, cursor, skipped, err)
	}
	watch := records[0]
	if !watch.Valid() || watch.RecordID != "item-1" || watch.StartedAt != playedAt || watch.Duration != 0 ||
		watch.MediaUserID != "92000000-0000-4000-8000-000000000001" || watch.Username != "viewer" {
		t.Fatalf("synthetic watch = %+v", watch)
	}
	job.Cursor = cursor
	records, _, skipped, err = source.ReadImportBatch(t.Context(), job)
	if err != nil || len(records) != 0 || skipped != 0 {
		t.Fatalf("terminal ReadImportBatch = %+v, %d, %v", records, skipped, err)
	}
}

func TestUserDataCursorRejectsMalformedState(t *testing.T) {
	_, err := decodeUserDataCursor(`{"UserID":"bad\nvalue"}`)
	if !errors.Is(err, core.ErrInvalidArgument) {
		t.Fatalf("decodeUserDataCursor error = %v", err)
	}
}
