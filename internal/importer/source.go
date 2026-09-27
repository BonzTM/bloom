package importer

import (
	"context"
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
	default:
		return nil, core.ErrInvalidArgument
	}
}
