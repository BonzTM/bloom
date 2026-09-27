package importer

import (
	"context"
	"testing"

	"github.com/BonzTM/bloom/internal/core"
)

type reportingPageStub struct{ page core.PlaybackReportingPage }

func (s reportingPageStub) PlaybackReporting(
	context.Context, string, int64, int,
) (core.PlaybackReportingPage, error) {
	return s.page, nil
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
