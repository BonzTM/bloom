package importer

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/BonzTM/bloom/internal/core"
)

const (
	defaultLeaseDuration = 30 * time.Second
	defaultStoreTimeout  = 5 * time.Second
)

// Metrics observes finite import worker outcomes.
type Metrics interface {
	ObserveImportJob(source, state, outcome string)
	AddImportedRecords(source string, count int64)
	SetRunningImports(int)
}

// LibraryResolver maps an imported item through its media server.
type LibraryResolver interface {
	ResolveLibrary(context.Context, string, string) (core.Library, bool, error)
}

// WorkerConfig bounds scheduling, leases, and duplicate matching.
type WorkerConfig struct {
	Interval, LeaseDuration, ResumeWindow, StoreTimeout time.Duration
}

// WorkerDependencies contains injected worker boundaries.
type WorkerDependencies struct {
	Store      core.ImportStore
	Reporting  PlaybackReportingService
	UserData   UserDataService
	Clock      core.Clock
	Metrics    Metrics
	Logger     *slog.Logger
	Staging    *Staging
	Exclusions core.ExclusionReader
	Libraries  core.ImportLibraryResolver
	Items      LibraryResolver
	Wait       func(context.Context, time.Duration) error
}

// Worker processes at most one import at a time in this process.
type Worker struct {
	config  WorkerConfig
	deps    WorkerDependencies
	sources sourceFactory
}

// NewWorker validates and constructs a supervised import worker.
func NewWorker(config WorkerConfig, deps WorkerDependencies) (*Worker, error) {
	if config.LeaseDuration == 0 {
		config.LeaseDuration = defaultLeaseDuration
	}
	if config.StoreTimeout == 0 {
		config.StoreTimeout = defaultStoreTimeout
	}
	if config.Interval < time.Second || config.Interval > time.Hour || config.LeaseDuration <= 0 ||
		config.ResumeWindow <= 0 || config.StoreTimeout <= 0 || config.StoreTimeout > 30*time.Second ||
		deps.Store == nil || deps.Reporting == nil || deps.Clock == nil ||
		deps.Metrics == nil || deps.Logger == nil || deps.Staging == nil {
		return nil, core.ErrInvalidArgument
	}
	if deps.Wait == nil {
		deps.Wait = waitContext
	}
	return &Worker{
		config: config, deps: deps,
		sources: sourceFactory{
			reporting: deps.Reporting, userData: deps.UserData, staging: deps.Staging,
			store: deps.Store, clock: deps.Clock,
			leaseDuration: config.LeaseDuration, storeTimeout: config.StoreTimeout, openWatch: openWatchUpload,
		},
	}, nil
}

// Run claims and drains jobs until cancellation.
func (w *Worker) Run(ctx context.Context) error {
	for ctx.Err() == nil {
		if err := w.runOnce(ctx); err != nil && ctx.Err() == nil {
			w.deps.Logger.WarnContext(ctx, "import worker pass failed", "error", err)
		}
		if ctx.Err() != nil {
			break
		}
		if err := w.deps.Wait(ctx, w.config.Interval); err != nil {
			if ctx.Err() != nil {
				break
			}
			return fmt.Errorf("wait for import worker: %w", err)
		}
	}
	return cleanWorkerShutdown()
}

func cleanWorkerShutdown() error { return nil }

func (w *Worker) runOnce(ctx context.Context) error {
	if err := w.sweep(ctx); err != nil {
		return fmt.Errorf("sweep import uploads: %w", err)
	}
	now := core.NormalizeTime(w.deps.Clock.Now())
	token, err := core.NewID()
	if err != nil {
		return fmt.Errorf("create import lease: %w", err)
	}
	storeCtx, cancel := w.storeContext(ctx)
	defer cancel()
	job, err := w.deps.Store.ClaimImport(storeCtx, core.ImportLease{
		Token: token, ExpiresAt: now.Add(w.config.LeaseDuration),
	}, now)
	if errors.Is(err, core.ErrNotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("claim import: %w", err)
	}
	w.deps.Metrics.SetRunningImports(1)
	defer w.deps.Metrics.SetRunningImports(0)
	return w.process(ctx, job)
}

func (w *Worker) process(ctx context.Context, job core.ImportJob) (result error) {
	reader, err := w.sources.reader(job.Source)
	if err != nil {
		return w.fail(ctx, job, err)
	}
	defer func() { result = errors.Join(result, reader.Close()) }()
	for {
		records, cursor, skipped, readErr := reader.ReadImportBatch(ctx, job)
		if readErr != nil {
			if errors.Is(readErr, core.ErrImportLeaseLost) {
				return w.handleLeaseLoss(ctx, job)
			}
			if errors.Is(readErr, core.ErrImportStore) {
				return fmt.Errorf("read import batch: %w", readErr)
			}
			if ctx.Err() != nil {
				return cleanWorkerShutdown()
			}
			return w.fail(ctx, job, readErr)
		}
		if len(records) == 0 && skipped == 0 {
			return w.complete(ctx, job)
		}
		records, exclusionSkipped, unresolved, exclusionErr := w.filterExcluded(ctx, job.MediaServerID, records)
		if exclusionErr != nil {
			return w.fail(ctx, job, exclusionErr)
		}
		skipped += exclusionSkipped
		result, commitErr := w.commit(ctx, job, records, cursor, skipped, unresolved)
		if commitErr != nil {
			if errors.Is(commitErr, core.ErrImportLeaseLost) {
				return w.handleLeaseLoss(ctx, job)
			}
			if errors.Is(commitErr, core.ErrImportStore) {
				return fmt.Errorf("commit import batch: %w", commitErr)
			}
			return w.fail(ctx, job, commitErr)
		}
		w.deps.Metrics.AddImportedRecords(string(job.Source), result.Imported-job.Imported)
		job.Cursor, job.Read, job.Imported = cursor, result.Read, result.Imported
		job.Skipped, job.Duplicate = result.Skipped, result.Duplicate
		job.UnresolvedLibrary = result.UnresolvedLibrary
		if job.Source != core.ImportSourceJellyfinUserData && len(records)+int(skipped) < core.ImportBatchSize {
			return w.complete(ctx, job)
		}
	}
}

func (w *Worker) filterExcluded(
	ctx context.Context, serverID string, records []core.ImportedWatch,
) ([]core.ImportedWatch, int64, int64, error) {
	if w.deps.Exclusions == nil || len(records) == 0 {
		return records, 0, 0, nil
	}
	exclusions, err := w.deps.Exclusions.GetExclusions(ctx, serverID)
	if err != nil {
		return nil, 0, 0, fmt.Errorf("load import exclusions: %w", err)
	}
	filtered := make([]core.ImportedWatch, 0, len(records))
	resolutions := make(map[string]importLibraryResolution, len(records))
	var skipped, unresolved int64
	for _, record := range records {
		if exclusions.ExcludesUser(record.MediaUserID) {
			skipped++
			continue
		}
		resolution, resolveErr := w.importLibrary(ctx, serverID, record, exclusions, resolutions)
		if resolveErr != nil {
			return nil, 0, 0, resolveErr
		}
		if exclusions.ExcludesLibrary(resolution.library.ID) {
			skipped++
			continue
		}
		if !resolution.found && record.LibraryID == "" && len(exclusions.LibraryIDs) > 0 {
			unresolved++
		}
		if resolution.found && record.LibraryID == "" {
			record.LibraryID, record.LibraryName = resolution.library.ID, resolution.library.Name
		}
		filtered = append(filtered, record)
	}
	return filtered, skipped, unresolved, nil
}

type importLibraryResolution struct {
	library core.Library
	found   bool
}

func (w *Worker) importLibrary(
	ctx context.Context, serverID string, record core.ImportedWatch,
	exclusions core.MediaServerExclusions, known map[string]importLibraryResolution,
) (importLibraryResolution, error) {
	if record.LibraryID != "" {
		return importLibraryResolution{library: core.Library{ID: record.LibraryID, Name: record.LibraryName}, found: true}, nil
	}
	if len(exclusions.LibraryIDs) == 0 {
		return importLibraryResolution{}, nil
	}
	if resolution, ok := known[record.ItemID]; ok {
		return resolution, nil
	}
	resolution, err := w.resolveImportLibrary(ctx, serverID, record.ItemID)
	if err != nil {
		return importLibraryResolution{}, err
	}
	known[record.ItemID] = resolution
	return resolution, nil
}

func (w *Worker) resolveImportLibrary(
	ctx context.Context, serverID, itemID string,
) (importLibraryResolution, error) {
	if w.deps.Libraries != nil {
		libraryID, found, err := w.deps.Libraries.ResolveImportLibrary(ctx, serverID, itemID)
		if err != nil {
			return importLibraryResolution{}, fmt.Errorf("resolve import catalog library: %w", err)
		}
		if found {
			return importLibraryResolution{library: core.Library{ID: libraryID}, found: true}, nil
		}
	}
	if w.deps.Items == nil {
		return importLibraryResolution{}, nil
	}
	library, found, err := w.deps.Items.ResolveLibrary(ctx, serverID, itemID)
	if err != nil {
		return importLibraryResolution{}, fmt.Errorf("resolve import media library: %w", err)
	}
	return importLibraryResolution{library: library, found: found}, nil
}

func (w *Worker) handleLeaseLoss(ctx context.Context, job core.ImportJob) error {
	storeCtx, cancel := w.storeContext(ctx)
	defer cancel()
	current, err := w.deps.Store.GetImport(storeCtx, job.ID)
	if err != nil {
		return err
	}
	if current.State != core.ImportCancelled {
		return nil
	}
	return nil
}

func (w *Worker) commit(
	ctx context.Context, job core.ImportJob, records []core.ImportedWatch, cursor string, skipped, unresolved int64,
) (core.ImportBatchResult, error) {
	now := core.NormalizeTime(w.deps.Clock.Now())
	lease := core.ImportLease{Token: job.LeaseToken, ExpiresAt: now.Add(w.config.LeaseDuration)}
	renewCtx, cancelRenew := w.storeContext(ctx)
	err := w.deps.Store.RenewImportLease(renewCtx, job.ID, lease, now)
	cancelRenew()
	if err != nil {
		return core.ImportBatchResult{}, err
	}
	commitCtx, cancelCommit := w.storeContext(ctx)
	defer cancelCommit()
	return w.deps.Store.CommitImportBatch(commitCtx, core.ImportBatch{
		JobID: job.ID, LeaseToken: job.LeaseToken, Cursor: cursor, Source: job.Source,
		MediaServerID: job.MediaServerID, Records: records, Skipped: skipped, UnresolvedLibrary: unresolved,
		ResumeWindow: w.config.ResumeWindow, Now: now, LeaseExpiresAt: lease.ExpiresAt,
	})
}

func (w *Worker) complete(ctx context.Context, job core.ImportJob) error {
	now := core.NormalizeTime(w.deps.Clock.Now())
	storeCtx, cancel := w.storeContext(ctx)
	defer cancel()
	if err := w.deps.Store.FinishImport(storeCtx, job.ID, job.LeaseToken, core.ImportCompleted, "", now); err != nil {
		return err
	}
	w.deps.Metrics.ObserveImportJob(string(job.Source), string(core.ImportCompleted), "success")
	return nil
}

func (w *Worker) fail(ctx context.Context, job core.ImportJob, cause error) error {
	now := core.NormalizeTime(w.deps.Clock.Now())
	reason := core.SafeImportError(cause)
	storeCtx, cancel := w.storeContext(ctx)
	defer cancel()
	if err := w.deps.Store.FinishImport(storeCtx, job.ID, job.LeaseToken, core.ImportFailed, reason, now); err != nil {
		return errors.Join(cause, err)
	}
	w.deps.Metrics.ObserveImportJob(string(job.Source), string(core.ImportFailed), "error")
	return fmt.Errorf("process import: %w", cause)
}

func (w *Worker) storeContext(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(parent, w.config.StoreTimeout)
}

func (w *Worker) sweep(ctx context.Context) error {
	storeCtx, cancel := w.storeContext(ctx)
	defer cancel()
	_, err := w.deps.Staging.sweep(storeCtx, w.deps.Clock.Now())
	return err
}

func waitContext(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
