package catalog

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"testing"
	"time"

	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/testutil"
)

const catalogWorkerServerID = "91000000-0000-4000-8000-000000000001"

type fakeCatalogStore struct {
	sync         core.LibrarySync
	claimed      bool
	committed    []core.LibraryItem
	cursor       string
	finished     bool
	failedWith   string
	libraryCalls int
	genreCalls   int
	requested    bool
	libraries    []core.CatalogLibrarySummary
	genres       []core.CatalogGenreSummary
	missingIDs   []string
	archivedIDs  []string
}

func (f *fakeCatalogStore) ClaimLibrarySync(
	context.Context, core.LibrarySyncLease, time.Time, time.Time,
) (core.LibrarySync, error) {
	if f.claimed {
		return core.LibrarySync{}, core.ErrNotFound
	}
	f.claimed = true
	return f.sync, nil
}

func (f *fakeCatalogStore) CommitLibrarySyncPage(
	_ context.Context, sync core.LibrarySync, items []core.LibraryItem, cursor string, _ time.Time,
) (core.LibrarySync, error) {
	f.committed = append(f.committed, items...)
	f.cursor = cursor
	sync.Cursor = cursor
	sync.Seen += int64(len(items))
	sync.Upserted += int64(len(items))
	return sync, nil
}

func (f *fakeCatalogStore) ListLibrarySyncMissingIDs(
	_ context.Context, _ core.LibrarySync, after string, _ int,
) ([]string, error) {
	if after != "" {
		return nil, nil
	}
	return f.missingIDs, nil
}

func (f *fakeCatalogStore) CommitLibrarySyncArchives(
	_ context.Context, sync core.LibrarySync, itemIDs []string, _ time.Time,
) (core.LibrarySync, error) {
	f.archivedIDs = append(f.archivedIDs, itemIDs...)
	sync.Archived += int64(len(itemIDs))
	return sync, nil
}

type revalidationCatalogSource struct{ present []string }

func (revalidationCatalogSource) Libraries(context.Context, string) ([]core.Library, error) {
	return []core.Library{{ID: "library", Name: "Library"}}, nil
}

func (revalidationCatalogSource) CatalogItems(
	_ context.Context, _, libraryID string, start, _ int,
) (core.LibraryCatalogPage, error) {
	return core.LibraryCatalogPage{StartIndex: start, Total: 1, Items: []core.LibraryItem{{
		ItemID: "seen", LibraryID: libraryID, ItemType: "Movie", Name: "Seen", Genres: []string{},
	}}}, nil
}

func (s revalidationCatalogSource) CatalogItemIDs(
	_ context.Context, _ string, _ []string,
) ([]string, error) {
	return s.present, nil
}

func TestWorkerArchivesOnlyRevalidatedMissingItems(t *testing.T) {
	now := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	store := catalogWorkerStore(now)
	store.missingIDs = []string{"still-present", "deleted"}
	worker, err := NewWorker(WorkerConfig{Interval: time.Hour}, WorkerDependencies{
		Store: store, Source: revalidationCatalogSource{present: []string{"still-present"}},
		Clock: testutil.NewFakeClock(now), Metrics: &fakeCatalogMetrics{}, Logger: slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatalf("NewWorker: %v", err)
	}
	if err := worker.runOnce(t.Context()); err != nil {
		t.Fatalf("runOnce: %v", err)
	}
	if !store.finished || !slices.Equal(store.archivedIDs, []string{"deleted"}) {
		t.Fatalf("archive result = finished %t, ids %v", store.finished, store.archivedIDs)
	}
}

func (f *fakeCatalogStore) FinishLibrarySync(
	_ context.Context, sync core.LibrarySync, now time.Time,
) (core.LibrarySync, error) {
	f.finished = true
	sync.State = core.LibrarySyncCompleted
	sync.LeaseToken, sync.Cursor = "", ""
	sync.LeaseExpiresAt, sync.FinishedAt = nil, &now
	return sync, nil
}

func (f *fakeCatalogStore) FailLibrarySync(_ context.Context, _ core.LibrarySync, message string, _ time.Time) error {
	f.failedWith = message
	return nil
}

func (f *fakeCatalogStore) RequestLibrarySync(context.Context, string) (core.LibrarySync, error) {
	f.requested = true
	return core.LibrarySync{}, nil
}

func (f *fakeCatalogStore) ListCatalogLibraries(context.Context, string, core.CatalogWindow) ([]core.CatalogLibrarySummary, error) {
	f.libraryCalls++
	return f.libraries, nil
}

func (f *fakeCatalogStore) ListCatalogItems(context.Context, core.CatalogItemQuery) ([]core.CatalogItemStats, error) {
	return nil, nil
}

func (f *fakeCatalogStore) GetCatalogItem(context.Context, string, string) (core.CatalogItemDetail, error) {
	return core.CatalogItemDetail{}, nil
}

func (f *fakeCatalogStore) ListCatalogHistory(context.Context, core.CatalogHistoryQuery) ([]core.PlaybackWatch, error) {
	return nil, nil
}

func (f *fakeCatalogStore) ListRecentCatalogItems(context.Context, string, string, int) ([]core.CatalogItemStats, error) {
	return nil, nil
}

func (f *fakeCatalogStore) ListCatalogGenres(context.Context, string, string, core.CatalogWindow) ([]core.CatalogGenreSummary, error) {
	f.genreCalls++
	return f.genres, nil
}

func (f *fakeCatalogStore) ListStaleCatalogItems(context.Context, core.CatalogStaleQuery) ([]core.CatalogItemStats, error) {
	return nil, nil
}

func (f *fakeCatalogStore) ListCatalogImportItems(context.Context, string, string, int) ([]core.CatalogImportItem, error) {
	return nil, nil
}

type fakeCatalogSource struct{ err error }

func (fakeCatalogSource) CatalogItemIDs(_ context.Context, _ string, itemIDs []string) ([]string, error) {
	return itemIDs, nil
}

func (f fakeCatalogSource) Libraries(context.Context, string) ([]core.Library, error) {
	if f.err != nil {
		return nil, f.err
	}
	return []core.Library{{ID: "library", Name: "Library"}}, nil
}

type recordingCatalogSource struct{ starts []int }

func (*recordingCatalogSource) CatalogItemIDs(_ context.Context, _ string, itemIDs []string) ([]string, error) {
	return itemIDs, nil
}

func (*recordingCatalogSource) Libraries(context.Context, string) ([]core.Library, error) {
	return []core.Library{{ID: "library", Name: "Library"}}, nil
}

func (s *recordingCatalogSource) CatalogItems(
	_ context.Context, _, libraryID string, start, _ int,
) (core.LibraryCatalogPage, error) {
	s.starts = append(s.starts, start)
	return core.LibraryCatalogPage{StartIndex: start, Total: start + 1, Items: []core.LibraryItem{{
		ItemID: "resumed-item", LibraryID: libraryID, ItemType: "Episode", Name: "Resumed", Genres: []string{},
	}}}, nil
}

func (f fakeCatalogSource) CatalogItems(
	_ context.Context, _, libraryID string, start, _ int,
) (core.LibraryCatalogPage, error) {
	return core.LibraryCatalogPage{StartIndex: start, Total: 1, Items: []core.LibraryItem{{
		ItemID: "item", LibraryID: libraryID, ItemType: "FutureType", Name: "Name", Genres: []string{},
	}}}, nil
}

type fakeCatalogMetrics struct {
	items    int64
	outcomes []string
}

func (f *fakeCatalogMetrics) ObserveLibraryCatalogSync(outcome string, _ float64) {
	f.outcomes = append(f.outcomes, outcome)
}
func (f *fakeCatalogMetrics) AddLibraryCatalogItems(count int64) { f.items += count }
func (*fakeCatalogMetrics) AddLibraryCatalogArchived(int64)      {}
func (*fakeCatalogMetrics) SetLibraryCatalogRunning(int)         {}

func TestWorkerCompletesFullWalkAndFillsPersistenceFields(t *testing.T) {
	now := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	store := catalogWorkerStore(now)
	metrics := &fakeCatalogMetrics{}
	worker, err := NewWorker(WorkerConfig{Interval: time.Hour}, WorkerDependencies{
		Store: store, Source: fakeCatalogSource{}, Clock: testutil.NewFakeClock(now), Metrics: metrics,
		Logger: slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatalf("NewWorker: %v", err)
	}
	if err := worker.runOnce(t.Context()); err != nil {
		t.Fatalf("runOnce: %v", err)
	}
	if !store.finished || len(store.committed) != 1 || metrics.items != 1 {
		t.Fatalf("walk = finished %t, items %d, metric %d", store.finished, len(store.committed), metrics.items)
	}
	item := store.committed[0]
	if item.MediaServerID != catalogWorkerServerID || item.FirstSeenAt != now || !item.Valid() {
		t.Fatalf("committed item = %+v", item)
	}
}

func TestWorkerFailureDoesNotArchive(t *testing.T) {
	now := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	store := catalogWorkerStore(now)
	worker, err := NewWorker(WorkerConfig{Interval: time.Hour}, WorkerDependencies{
		Store: store, Source: fakeCatalogSource{err: errors.New("upstream unavailable")},
		Clock: testutil.NewFakeClock(now), Metrics: &fakeCatalogMetrics{},
		Logger: slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatalf("NewWorker: %v", err)
	}
	if err := worker.runOnce(t.Context()); err == nil || store.finished || store.failedWith == "" {
		t.Fatalf("failure = %v, finished %t, recorded %q", err, store.finished, store.failedWith)
	}
}

func TestWorkerResumesAtDurableCursor(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	store := catalogWorkerStore(now)
	store.sync.Cursor = `{"library_id":"library","start":200}`
	source := &recordingCatalogSource{}
	worker, err := NewWorker(WorkerConfig{Interval: time.Hour}, WorkerDependencies{
		Store: store, Source: source, Clock: testutil.NewFakeClock(now), Metrics: &fakeCatalogMetrics{},
		Logger: slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatalf("NewWorker: %v", err)
	}
	if err := worker.runOnce(t.Context()); err != nil {
		t.Fatalf("runOnce: %v", err)
	}
	if len(source.starts) != 1 || source.starts[0] != 0 || !store.finished {
		t.Fatalf("resumed starts = %v, finished = %t", source.starts, store.finished)
	}
}

type cancelAfterCheckpointSource struct {
	cancel context.CancelFunc
	starts []int
}

func (*cancelAfterCheckpointSource) CatalogItemIDs(_ context.Context, _ string, itemIDs []string) ([]string, error) {
	return itemIDs, nil
}

func (*cancelAfterCheckpointSource) Libraries(context.Context, string) ([]core.Library, error) {
	return []core.Library{{ID: "library", Name: "Library"}}, nil
}

func (s *cancelAfterCheckpointSource) CatalogItems(
	ctx context.Context, _, libraryID string, start, _ int,
) (core.LibraryCatalogPage, error) {
	s.starts = append(s.starts, start)
	if start > 0 {
		s.cancel()
		return core.LibraryCatalogPage{}, ctx.Err()
	}
	return core.LibraryCatalogPage{StartIndex: 0, Total: core.CatalogPageSize + 1, Items: []core.LibraryItem{{
		ItemID: "checkpointed", LibraryID: libraryID, ItemType: "Episode", Name: "Checkpointed", Genres: []string{},
	}}}, nil
}

func TestWorkerShutdownAfterCheckpointLeavesLeaseRecoverable(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	store := catalogWorkerStore(now)
	ctx, cancel := context.WithCancel(t.Context())
	source := &cancelAfterCheckpointSource{cancel: cancel}
	worker, err := NewWorker(WorkerConfig{Interval: time.Hour}, WorkerDependencies{
		Store: store, Source: source, Clock: testutil.NewFakeClock(now), Metrics: &fakeCatalogMetrics{},
		Logger: slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatalf("NewWorker: %v", err)
	}
	if err := worker.runOnce(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("runOnce = %v, want context cancellation", err)
	}
	if store.failedWith != "" || store.finished || store.cursor == "" || store.sync.StartedAt == nil {
		t.Fatalf("shutdown mutated recovery state: failed=%q finished=%t cursor=%q", store.failedWith, store.finished, store.cursor)
	}
}

func TestWorkerSortsAndDeduplicatesLibrariesBeforeResume(t *testing.T) {
	t.Parallel()
	libraries := []core.Library{{ID: "z"}, {ID: "a"}, {ID: "z"}, {ID: "m"}}
	got := orderedLibraries(libraries)
	if len(got) != 3 || got[0].ID != "a" || got[1].ID != "m" || got[2].ID != "z" {
		t.Fatalf("orderedLibraries = %+v", got)
	}
}

type reorderedRecoverySource struct{ starts []int }

func (*reorderedRecoverySource) CatalogItemIDs(_ context.Context, _ string, itemIDs []string) ([]string, error) {
	return itemIDs, nil
}

func (*reorderedRecoverySource) Libraries(context.Context, string) ([]core.Library, error) {
	return []core.Library{{ID: "library-z"}, {ID: "library-a"}, {ID: "library-z"}}, nil
}

func (s *reorderedRecoverySource) CatalogItems(
	_ context.Context, _, libraryID string, start, _ int,
) (core.LibraryCatalogPage, error) {
	s.starts = append(s.starts, start)
	items := []core.LibraryItem{
		{ItemID: "item-second", LibraryID: libraryID, ItemType: "Movie", Name: "Second", Genres: []string{}},
		{ItemID: "item-first", LibraryID: libraryID, ItemType: "Movie", Name: "First", Genres: []string{}},
	}
	return core.LibraryCatalogPage{StartIndex: start, Total: len(items), Items: items[start : start+1]}, nil
}

func TestWorkerRecoveryReplaysReorderedCurrentLibraryBeforeArchival(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	store := catalogWorkerStore(now)
	store.sync.Cursor = `{"library_id":"library-z","start":1}`
	source := &reorderedRecoverySource{}
	worker, err := NewWorker(WorkerConfig{Interval: time.Hour}, WorkerDependencies{
		Store: store, Source: source, Clock: testutil.NewFakeClock(now), Metrics: &fakeCatalogMetrics{},
		Logger: slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatalf("NewWorker: %v", err)
	}
	if err := worker.runOnce(t.Context()); err != nil {
		t.Fatalf("runOnce: %v", err)
	}
	if !store.finished || len(store.committed) != 2 || len(source.starts) != 2 || source.starts[0] != 0 {
		t.Fatalf("recovery = finished %t committed %d starts %v", store.finished, len(store.committed), source.starts)
	}
}

func catalogWorkerStore(now time.Time) *fakeCatalogStore {
	expires := now.Add(time.Minute)
	return &fakeCatalogStore{sync: core.LibrarySync{
		MediaServerID: catalogWorkerServerID, State: core.LibrarySyncRunning,
		LeaseToken: "lease", LeaseExpiresAt: &expires, StartedAt: &now,
	}}
}
