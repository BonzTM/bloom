package importer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"

	"github.com/BonzTM/bloom/internal/core"
)

// MediaServerReader verifies the selected destination exists.
type MediaServerReader interface {
	Get(context.Context, string) (core.MediaServerConnection, error)
}

// Service creates, lists, reads, and cancels import jobs.
type Service struct {
	store   core.ImportStore
	servers MediaServerReader
	clock   core.Clock
	metrics Metrics
	staging *Staging
	logger  *slog.Logger
}

// NewService constructs the import application service.
func NewService(
	store core.ImportStore, servers MediaServerReader, clock core.Clock,
	staging *Staging, logger *slog.Logger, supplied ...Metrics,
) (*Service, error) {
	if store == nil || servers == nil || clock == nil || staging == nil || logger == nil {
		return nil, errors.New("import service: all dependencies are required")
	}
	metrics := Metrics(nopMetrics{})
	if len(supplied) > 0 && supplied[0] != nil {
		metrics = supplied[0]
	}
	return &Service{
		store: store, servers: servers, clock: clock, metrics: metrics,
		staging: staging, logger: logger,
	}, nil
}

// CreatePlaybackReporting creates a plugin-backed job.
func (s *Service) CreatePlaybackReporting(
	ctx context.Context, mediaServerID, requestedBy string,
) (core.ImportJob, error) {
	if err := s.validateTarget(ctx, mediaServerID, requestedBy); err != nil {
		return core.ImportJob{}, err
	}
	return s.create(ctx, mediaServerID, requestedBy, core.ImportSourcePlaybackReporting, "0")
}

// StageBloomExport streams one bounded upload into the private staging root.
func (s *Service) StageBloomExport(ctx context.Context, upload io.Reader) (string, error) {
	if upload == nil {
		return "", core.ErrInvalidArgument
	}
	if err := s.CheckBloomExportUpload(); err != nil {
		return "", err
	}
	select {
	case s.staging.slots <- struct{}{}:
		defer func() { <-s.staging.slots }()
	case <-ctx.Done():
		return "", ctx.Err()
	}
	return s.staging.stage(upload)
}

// CheckBloomExportUpload prepares the staging root without consuming an upload.
func (s *Service) CheckBloomExportUpload() error {
	return s.staging.ensureAvailable()
}

// CreateBloomExport creates a JSONL-backed job for a staged upload.
func (s *Service) CreateBloomExport(
	ctx context.Context, mediaServerID, requestedBy, stagingID string,
) (job core.ImportJob, result error) {
	defer func() {
		if result != nil {
			s.removeUnclaimedUpload(ctx, stagingID)
		}
	}()
	if err := s.validateTarget(ctx, mediaServerID, requestedBy); err != nil {
		return job, err
	}
	if !core.ValidID(stagingID) {
		return job, core.ErrInvalidArgument
	}
	cursor, err := encodeFileCursor(fileCursor{ID: stagingID})
	if err != nil {
		return job, err
	}
	return s.create(ctx, mediaServerID, requestedBy, core.ImportSourceBloomExport, cursor)
}

// DiscardBloomExport removes an unclaimed staged upload.
func (s *Service) DiscardBloomExport(stagingID string) error {
	return s.staging.remove(stagingID)
}

func (s *Service) removeUnclaimedUpload(ctx context.Context, stagingID string) {
	if err := s.staging.remove(stagingID); err != nil {
		s.logger.WarnContext(ctx, "remove unclaimed import staging file", "error", err)
	}
}

func (s *Service) create(
	ctx context.Context, mediaServerID, requestedBy string, source core.ImportSource, cursor string,
) (core.ImportJob, error) {
	if !source.Valid() {
		return core.ImportJob{}, core.ErrInvalidArgument
	}
	id, err := core.NewID()
	if err != nil {
		return core.ImportJob{}, fmt.Errorf("create import id: %w", err)
	}
	now := core.NormalizeTime(s.clock.Now())
	job := core.ImportJob{
		ID: id, MediaServerID: mediaServerID, RequestedBy: requestedBy,
		Source: source, State: core.ImportPending, Cursor: cursor, CreatedAt: now, UpdatedAt: now,
	}
	if err := s.store.CreateImport(ctx, job); err != nil {
		return core.ImportJob{}, err
	}
	return job, nil
}

func (s *Service) validateTarget(ctx context.Context, mediaServerID, requestedBy string) error {
	if !core.ValidID(mediaServerID) || !core.ValidID(requestedBy) {
		return core.ErrInvalidArgument
	}
	_, err := s.servers.Get(ctx, mediaServerID)
	return err
}

// List returns one bounded newest-first page.
func (s *Service) List(ctx context.Context, query core.ImportListQuery) ([]core.ImportJob, error) {
	return s.store.ListImports(ctx, query)
}

// Get returns one import job.
func (s *Service) Get(ctx context.Context, id string) (core.ImportJob, error) {
	return s.store.GetImport(ctx, id)
}

// Cancel transitions an active job and then removes its uploaded source file.
func (s *Service) Cancel(ctx context.Context, id string) (core.ImportJob, error) {
	job, err := s.store.CancelImport(ctx, id, core.NormalizeTime(s.clock.Now()))
	if err != nil {
		return core.ImportJob{}, err
	}
	s.metrics.ObserveImportJob(string(job.Source), string(core.ImportCancelled), "cancelled")
	s.cleanupUpload(ctx, job)
	return job, nil
}

func (s *Service) cleanupUpload(ctx context.Context, job core.ImportJob) {
	if job.Source != core.ImportSourceBloomExport {
		return
	}
	if err := cleanupJobUpload(s.staging, job); err != nil {
		s.logger.WarnContext(ctx, "remove import staging file", "error", err, "import_id", job.ID)
	}
}

type nopMetrics struct{}

func (nopMetrics) ObserveImportJob(string, string, string) {}
func (nopMetrics) AddImportedRecords(string, int64)        {}
func (nopMetrics) SetRunningImports(int)                   {}

func cleanupJobUpload(staging *Staging, job core.ImportJob) error {
	cursor, err := decodeFileCursor(job.Cursor)
	if err != nil {
		return err
	}
	return staging.remove(cursor.ID)
}
