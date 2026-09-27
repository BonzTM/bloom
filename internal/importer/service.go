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

// CreateJellyfinUserData creates a coarse catalog-backed play-state job.
func (s *Service) CreateJellyfinUserData(
	ctx context.Context, mediaServerID, requestedBy string,
) (core.ImportJob, error) {
	if err := s.validateTarget(ctx, mediaServerID, requestedBy); err != nil {
		return core.ImportJob{}, err
	}
	return s.create(ctx, mediaServerID, requestedBy, core.ImportSourceJellyfinUserData, "{}")
}

// StageBloomExport streams one bounded upload into database chunks.
func (s *Service) StageBloomExport(ctx context.Context, upload io.Reader) (string, error) {
	if upload == nil {
		return "", core.ErrInvalidArgument
	}
	select {
	case s.staging.slots <- struct{}{}:
		defer func() { <-s.staging.slots }()
	case <-ctx.Done():
		return "", ctx.Err()
	}
	return s.staging.stage(ctx, upload, s.clock)
}

// CreateBloomExport creates a job for a staged Bloom export upload.
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
	job, err = s.newJob(mediaServerID, requestedBy, core.ImportSourceBloomExport, cursor)
	if err != nil {
		return core.ImportJob{}, err
	}
	if err := s.store.CreateUploadedImport(ctx, job, stagingID); err != nil {
		return core.ImportJob{}, err
	}
	return job, nil
}

// DiscardBloomExport removes an unclaimed staged upload.
func (s *Service) DiscardBloomExport(ctx context.Context, stagingID string) error {
	return s.staging.remove(ctx, stagingID)
}

func (s *Service) removeUnclaimedUpload(ctx context.Context, stagingID string) {
	if err := s.staging.remove(ctx, stagingID); err != nil {
		s.logger.WarnContext(ctx, "remove unclaimed import upload", "error", err)
	}
}

func (s *Service) create(
	ctx context.Context, mediaServerID, requestedBy string, source core.ImportSource, cursor string,
) (core.ImportJob, error) {
	job, err := s.newJob(mediaServerID, requestedBy, source, cursor)
	if err != nil {
		return core.ImportJob{}, err
	}
	if err := s.store.CreateImport(ctx, job); err != nil {
		return core.ImportJob{}, err
	}
	return job, nil
}

func (s *Service) newJob(
	mediaServerID, requestedBy string, source core.ImportSource, cursor string,
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

// Cancel transitions an active job and atomically removes its uploaded chunks.
func (s *Service) Cancel(ctx context.Context, id string) (core.ImportJob, error) {
	job, err := s.store.CancelImport(ctx, id, core.NormalizeTime(s.clock.Now()))
	if err != nil {
		return core.ImportJob{}, err
	}
	s.metrics.ObserveImportJob(string(job.Source), string(core.ImportCancelled), "cancelled")
	return job, nil
}

type nopMetrics struct{}

func (nopMetrics) ObserveImportJob(string, string, string) {}
func (nopMetrics) AddImportedRecords(string, int64)        {}
func (nopMetrics) SetRunningImports(int)                   {}
