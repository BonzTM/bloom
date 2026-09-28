package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/BonzTM/bloom/internal/core"
)

const (
	defaultStoreTimeout = 5 * time.Second
	defaultLease        = 30 * time.Second
	maxServersPerPass   = 1000
)

// Source supplies libraries and bounded item pages through a registered adapter.
type Source interface {
	Libraries(context.Context, string) ([]core.Library, error)
	CatalogItems(context.Context, string, string, int, int) (core.LibraryCatalogPage, error)
	CatalogItemIDs(context.Context, string, []string) ([]string, error)
}

// Metrics observes finite catalog sync outcomes.
type Metrics interface {
	ObserveLibraryCatalogSync(outcome string, seconds float64)
	AddLibraryCatalogItems(count int64)
	AddLibraryCatalogArchived(count int64)
	SetLibraryCatalogRunning(count int)
}

// WorkerConfig bounds scheduling, persistence, and lease ownership.
type WorkerConfig struct {
	Interval, StoreTimeout, LeaseDuration time.Duration
}

// WorkerDependencies contains the worker's explicit boundaries.
type WorkerDependencies struct {
	Store      core.LibraryCatalogStore
	Source     Source
	Clock      core.Clock
	Metrics    Metrics
	Logger     *slog.Logger
	Wake       <-chan struct{}
	Wait       func(context.Context, time.Duration, <-chan struct{}) error
	Exclusions core.ExclusionReader
}

// Worker performs resumable full catalog walks.
type Worker struct {
	config WorkerConfig
	deps   WorkerDependencies
}

// NewWorker validates and constructs a catalog worker.
func NewWorker(config WorkerConfig, deps WorkerDependencies) (*Worker, error) {
	if config.StoreTimeout == 0 {
		config.StoreTimeout = defaultStoreTimeout
	}
	if config.LeaseDuration == 0 {
		config.LeaseDuration = defaultLease
	}
	if config.Interval < 5*time.Minute || config.Interval > 24*time.Hour || config.StoreTimeout <= 0 ||
		config.StoreTimeout > 30*time.Second || config.LeaseDuration <= config.StoreTimeout*2 || deps.Store == nil ||
		deps.Source == nil || deps.Clock == nil || deps.Metrics == nil || deps.Logger == nil {
		return nil, core.ErrInvalidArgument
	}
	if deps.Wait == nil {
		deps.Wait = waitWorker
	}
	return &Worker{config: config, deps: deps}, nil
}

// Run processes due servers until cancellation.
func (w *Worker) Run(ctx context.Context) error {
	for ctx.Err() == nil {
		if err := w.runOnce(ctx); err != nil && ctx.Err() == nil {
			w.deps.Logger.WarnContext(ctx, "library catalog sync pass failed", "error", err)
		}
		if ctx.Err() != nil {
			break
		}
		if err := w.deps.Wait(ctx, w.config.Interval, w.deps.Wake); err != nil {
			if ctx.Err() != nil {
				break
			}
			return fmt.Errorf("wait for library catalog sync: %w", err)
		}
	}
	return cleanWorkerShutdown()
}

func cleanWorkerShutdown() error { return nil }

func (w *Worker) runOnce(ctx context.Context) error {
	var result error
	for range maxServersPerPass {
		sync, err := w.claim(ctx)
		if errors.Is(err, core.ErrNotFound) {
			break
		}
		if err != nil {
			return errors.Join(result, err)
		}
		if err := w.walk(ctx, sync); err != nil {
			result = errors.Join(result, err)
		}
	}
	return result
}

func (w *Worker) claim(ctx context.Context) (core.LibrarySync, error) {
	now := core.NormalizeTime(w.deps.Clock.Now())
	token, err := core.NewID()
	if err != nil {
		return core.LibrarySync{}, fmt.Errorf("create library sync lease: %w", err)
	}
	storeCtx, cancel := context.WithTimeout(ctx, w.config.StoreTimeout)
	defer cancel()
	return w.deps.Store.ClaimLibrarySync(storeCtx, core.LibrarySyncLease{
		Token: token, ExpiresAt: now.Add(w.config.LeaseDuration),
	}, now, now.Add(-w.config.Interval))
}

func (w *Worker) walk(ctx context.Context, sync core.LibrarySync) error {
	started := time.Now()
	w.deps.Metrics.SetLibraryCatalogRunning(1)
	defer w.deps.Metrics.SetLibraryCatalogRunning(0)
	cursor, err := decodeCursor(sync.Cursor)
	if err == nil && cursor.Phase != syncPhaseRevalidation {
		var libraries []core.Library
		libraries, err = w.deps.Source.Libraries(ctx, sync.MediaServerID)
		if err == nil {
			libraries, err = w.filterExcludedLibraries(ctx, sync.MediaServerID, libraries)
		}
		if err == nil {
			libraries = orderedLibraries(libraries)
			err = w.walkLibraries(ctx, &sync, libraries)
		}
	}
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		w.deps.Metrics.ObserveLibraryCatalogSync("failed", time.Since(started).Seconds())
		return w.fail(ctx, sync, err)
	}
	finished, err := w.finish(ctx, sync)
	if err != nil {
		w.deps.Metrics.ObserveLibraryCatalogSync("failed", time.Since(started).Seconds())
		return err
	}
	w.deps.Metrics.AddLibraryCatalogArchived(finished.Archived)
	w.deps.Metrics.ObserveLibraryCatalogSync("completed", time.Since(started).Seconds())
	return nil
}

func (w *Worker) filterExcludedLibraries(
	ctx context.Context, serverID string, libraries []core.Library,
) ([]core.Library, error) {
	if w.deps.Exclusions == nil || len(libraries) == 0 {
		return libraries, nil
	}
	exclusions, err := w.deps.Exclusions.GetExclusions(ctx, serverID)
	if err != nil {
		return nil, fmt.Errorf("load catalog exclusions: %w", err)
	}
	filtered := make([]core.Library, 0, len(libraries))
	for _, library := range libraries {
		if !exclusions.ExcludesLibrary(library.ID) {
			filtered = append(filtered, library)
		}
	}
	return filtered, nil
}

func (w *Worker) walkLibraries(ctx context.Context, sync *core.LibrarySync, libraries []core.Library) error {
	if len(libraries) > core.MaxMediaServerLibraries {
		return errors.New("library count exceeds bound")
	}
	cursor, err := decodeCursor(sync.Cursor)
	if err != nil {
		return err
	}
	index, err := cursorLibraryIndex(cursor, libraries)
	if err != nil {
		return err
	}
	for ; index < len(libraries); index++ {
		if err := w.walkLibrary(ctx, sync, libraries[index], 0, libraries, index); err != nil {
			return err
		}
	}
	return nil
}

func orderedLibraries(libraries []core.Library) []core.Library {
	ordered := slices.Clone(libraries)
	slices.SortFunc(ordered, func(left, right core.Library) int { return strings.Compare(left.ID, right.ID) })
	return slices.CompactFunc(ordered, func(left, right core.Library) bool { return left.ID == right.ID })
}

func (w *Worker) walkLibrary(
	ctx context.Context, sync *core.LibrarySync, library core.Library, start int, libraries []core.Library, libraryIndex int,
) error {
	for pageNumber := start / core.CatalogPageSize; pageNumber < core.MaxCatalogPagesPerLibrary; pageNumber++ {
		page, err := w.deps.Source.CatalogItems(ctx, sync.MediaServerID, library.ID, start, core.CatalogPageSize)
		if err != nil {
			return err
		}
		if err := validatePage(page, start); err != nil {
			return err
		}
		next := start + len(page.Items)
		cursor := nextCursor(libraries, libraryIndex, next, page.Total)
		if err := w.commitPage(ctx, sync, library.ID, page.Items, cursor); err != nil {
			return err
		}
		if next >= page.Total {
			return nil
		}
		start = next
	}
	return errors.New("library page bound exceeded")
}

func (w *Worker) commitPage(
	ctx context.Context, sync *core.LibrarySync, libraryID string, items []core.LibraryItem, cursor syncCursor,
) error {
	now := core.NormalizeTime(w.deps.Clock.Now())
	for index := range items {
		items[index].MediaServerID = sync.MediaServerID
		items[index].LibraryID = libraryID
		items[index].Archived = false
		items[index].FirstSeenAt, items[index].LastSeenAt, items[index].UpdatedAt = *sync.StartedAt, now, now
	}
	encoded, err := encodeCursor(cursor)
	if err != nil {
		return err
	}
	expires := now.Add(w.config.LeaseDuration)
	sync.LeaseExpiresAt = &expires
	storeCtx, cancel := context.WithTimeout(ctx, w.config.StoreTimeout)
	updated, err := w.deps.Store.CommitLibrarySyncPage(storeCtx, *sync, items, encoded, now)
	cancel()
	if err != nil {
		return err
	}
	*sync = updated
	w.deps.Metrics.AddLibraryCatalogItems(int64(len(items)))
	return nil
}

func (w *Worker) finish(ctx context.Context, sync core.LibrarySync) (core.LibrarySync, error) {
	updated, err := w.revalidateMissing(ctx, sync)
	if err != nil {
		return core.LibrarySync{}, err
	}
	now := core.NormalizeTime(w.deps.Clock.Now())
	storeCtx, cancel := context.WithTimeout(ctx, w.config.StoreTimeout)
	defer cancel()
	return w.deps.Store.FinishLibrarySync(storeCtx, updated, now)
}

func (w *Worker) revalidateMissing(ctx context.Context, sync core.LibrarySync) (core.LibrarySync, error) {
	cursor, err := decodeCursor(sync.Cursor)
	if err != nil {
		return sync, err
	}
	if cursor.Phase != syncPhaseRevalidation {
		sync, err = w.checkpointRevalidation(ctx, sync, nil, "")
		if err != nil {
			return sync, err
		}
		cursor = syncCursor{Phase: syncPhaseRevalidation}
	}
	after := cursor.AfterItemID
	for range core.MaxCatalogPagesPerLibrary {
		ids, err := w.missingIDs(ctx, sync, after)
		if err != nil || len(ids) == 0 {
			return sync, err
		}
		present, err := w.deps.Source.CatalogItemIDs(ctx, sync.MediaServerID, ids)
		if err != nil {
			return sync, err
		}
		absent := absentCatalogIDs(ids, present)
		after = ids[len(ids)-1]
		sync, err = w.checkpointRevalidation(ctx, sync, absent, after)
		if err != nil {
			return sync, err
		}
	}
	return sync, errors.New("catalog archival revalidation bound exceeded")
}

func (w *Worker) missingIDs(ctx context.Context, sync core.LibrarySync, after string) ([]string, error) {
	storeCtx, cancel := context.WithTimeout(ctx, w.config.StoreTimeout)
	defer cancel()
	return w.deps.Store.ListLibrarySyncMissingIDs(storeCtx, sync, after, core.CatalogUserDataBatchSize)
}

func (w *Worker) checkpointRevalidation(
	ctx context.Context, sync core.LibrarySync, ids []string, after string,
) (core.LibrarySync, error) {
	now := core.NormalizeTime(w.deps.Clock.Now())
	expires := now.Add(w.config.LeaseDuration)
	sync.LeaseExpiresAt = &expires
	cursor, err := encodeCursor(syncCursor{Phase: syncPhaseRevalidation, AfterItemID: after})
	if err != nil {
		return core.LibrarySync{}, err
	}
	storeCtx, cancel := context.WithTimeout(ctx, w.config.StoreTimeout)
	defer cancel()
	return w.deps.Store.CommitLibrarySyncArchives(storeCtx, sync, ids, cursor, now)
}

func absentCatalogIDs(requested, present []string) []string {
	found := make(map[string]struct{}, len(present))
	for _, id := range present {
		found[id] = struct{}{}
	}
	absent := make([]string, 0, len(requested)-len(present))
	for _, id := range requested {
		if _, ok := found[id]; !ok {
			absent = append(absent, id)
		}
	}
	return absent
}

func (w *Worker) fail(ctx context.Context, sync core.LibrarySync, cause error) error {
	w.deps.Logger.ErrorContext(ctx, "library catalog sync failed", "media_server_id", sync.MediaServerID, "error", cause)
	now := core.NormalizeTime(w.deps.Clock.Now())
	storeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), w.config.StoreTimeout)
	defer cancel()
	recordErr := w.deps.Store.FailLibrarySync(storeCtx, sync, "catalog sync failed; see server logs", now)
	return errors.Join(cause, recordErr)
}

type syncCursor struct {
	Phase       string `json:"phase,omitempty"`
	LibraryID   string `json:"library_id,omitempty"`
	AfterItemID string `json:"after_item_id,omitempty"`
	Start       int    `json:"start,omitempty"`
}

const syncPhaseRevalidation = "revalidation"

func decodeCursor(value string) (syncCursor, error) {
	if value == "" {
		return syncCursor{}, nil
	}
	var cursor syncCursor
	if len(value) > core.MaxCatalogCursorBytes || json.Unmarshal([]byte(value), &cursor) != nil ||
		!validSyncCursor(cursor) || cursor.Start < 0 ||
		cursor.Start > core.MaxCatalogPagesPerLibrary*core.CatalogPageSize {
		return syncCursor{}, errors.New("library sync cursor is invalid")
	}
	return cursor, nil
}

func encodeCursor(cursor syncCursor) (string, error) {
	if cursor == (syncCursor{}) {
		return "", nil
	}
	encoded, err := json.Marshal(cursor)
	if err != nil || len(encoded) > core.MaxCatalogCursorBytes {
		return "", errors.New("library sync cursor exceeds bound")
	}
	return string(encoded), nil
}

func validSyncCursor(cursor syncCursor) bool {
	switch cursor.Phase {
	case "":
		return core.ValidLibraryID(cursor.LibraryID) && cursor.AfterItemID == ""
	case syncPhaseRevalidation:
		return cursor.LibraryID == "" && cursor.Start == 0 &&
			(cursor.AfterItemID == "" || core.ValidCatalogID(cursor.AfterItemID))
	default:
		return false
	}
}

func cursorLibraryIndex(cursor syncCursor, libraries []core.Library) (int, error) {
	if cursor.LibraryID == "" {
		return 0, nil
	}
	for index, library := range libraries {
		if library.ID == cursor.LibraryID {
			return index, nil
		}
	}
	return 0, errors.New("library sync cursor library is no longer available")
}

func nextCursor(libraries []core.Library, index, next, total int) syncCursor {
	if next < total {
		return syncCursor{LibraryID: libraries[index].ID, Start: next}
	}
	if index+1 < len(libraries) {
		return syncCursor{LibraryID: libraries[index+1].ID}
	}
	return syncCursor{Phase: syncPhaseRevalidation}
}

func validatePage(page core.LibraryCatalogPage, start int) error {
	if page.StartIndex != start || page.Total < 0 || page.Total > core.MaxCatalogPagesPerLibrary*core.CatalogPageSize ||
		len(page.Items) > core.CatalogPageSize || start+len(page.Items) > page.Total ||
		(len(page.Items) == 0 && start < page.Total) {
		return errors.New("catalog page metadata is invalid")
	}
	for _, item := range page.Items {
		if !item.ValidUpstream() {
			return errors.New("catalog page item is invalid")
		}
	}
	return nil
}

func waitWorker(ctx context.Context, duration time.Duration, wake <-chan struct{}) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	case <-wake:
		return nil
	}
}
