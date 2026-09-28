package exclusion

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/BonzTM/bloom/internal/core"
	statsapp "github.com/BonzTM/bloom/internal/stats"
	"github.com/BonzTM/bloom/internal/testutil"
)

const exclusionServerID = "00000000-0000-4000-8000-000000000001"

type exclusionStoreFake struct {
	mu       sync.Mutex
	value    core.MediaServerExclusions
	gets     int
	replaces int
	started  chan<- struct{}
	release  <-chan struct{}
}

func (s *exclusionStoreFake) GetExclusions(ctx context.Context, _ string) (core.MediaServerExclusions, error) {
	s.mu.Lock()
	s.gets++
	value := s.value.Clone()
	started, release := s.started, s.release
	s.mu.Unlock()
	if started != nil {
		started <- struct{}{}
	}
	if release != nil {
		select {
		case <-release:
		case <-ctx.Done():
			return core.MediaServerExclusions{}, ctx.Err()
		}
	}
	return value, nil
}

func (s *exclusionStoreFake) ReplaceExclusions(_ context.Context, value core.MediaServerExclusions) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.replaces++
	s.value = value.Clone()
	return nil
}

func (s *exclusionStoreFake) counts() (int, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.gets, s.replaces
}

type exclusionServerFake struct{}

func (exclusionServerFake) Get(context.Context, string) (core.MediaServerConnection, error) {
	return core.MediaServerConnection{Server: core.MediaServer{ID: exclusionServerID}}, nil
}

func TestServiceCachesLookupsForLease(t *testing.T) {
	store := &exclusionStoreFake{value: core.MediaServerExclusions{
		MediaServerID: exclusionServerID, MediaUserIDs: []string{"user"},
	}}
	clock := testutil.NewFakeClock(time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC))
	service, err := NewService(store, exclusionServerFake{}, clock, time.Minute)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	first, err := service.GetExclusions(t.Context(), exclusionServerID)
	if err != nil {
		t.Fatalf("first GetExclusions: %v", err)
	}
	first.MediaUserIDs[0] = "changed"
	second, err := service.GetExclusions(t.Context(), exclusionServerID)
	gets, _ := store.counts()
	if err != nil || gets != 1 || second.MediaUserIDs[0] != "user" {
		t.Fatalf("cached = %+v, gets %d, %v", second, gets, err)
	}
	clock.Advance(time.Minute)
	if _, err = service.GetExclusions(t.Context(), exclusionServerID); err != nil {
		t.Fatalf("refreshed GetExclusions: %v", err)
	}
	gets, _ = store.counts()
	if gets != 2 {
		t.Fatalf("refreshed gets = %d, want 2", gets)
	}
}

func TestReplaceRefreshesCacheAndInvalidatesReaders(t *testing.T) {
	store := &exclusionStoreFake{value: core.MediaServerExclusions{MediaServerID: exclusionServerID}}
	clock := testutil.NewFakeClock(time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC))
	service, err := NewService(store, exclusionServerFake{}, clock, time.Minute)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	invalidations := 0
	service.AddInvalidator(func(string) { invalidations++ })
	want := core.MediaServerExclusions{MediaServerID: exclusionServerID, LibraryIDs: []string{"library"}}
	got, err := service.Replace(t.Context(), want)
	_, replaces := store.counts()
	if err != nil || replaces != 1 || invalidations != 1 || !got.ExcludesLibrary("library") {
		t.Fatalf("Replace = %+v, replaces %d invalidations %d, %v", got, replaces, invalidations, err)
	}
	if _, err = service.GetExclusions(t.Context(), exclusionServerID); err != nil {
		t.Fatalf("cached replacement: %v", err)
	}
	gets, _ := store.counts()
	if gets != 0 {
		t.Fatalf("cached replacement gets = %d, want 0", gets)
	}
}

func TestReadStartedBeforeReplaceCannotOverwriteCache(t *testing.T) {
	started, release := make(chan struct{}, 1), make(chan struct{})
	store := &exclusionStoreFake{
		value:   core.MediaServerExclusions{MediaServerID: exclusionServerID, LibraryIDs: []string{"old"}},
		started: started, release: release,
	}
	service, err := NewService(store, exclusionServerFake{}, testutil.NewFakeClock(time.Now()), time.Minute)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	readDone := make(chan error, 1)
	go func() {
		_, readErr := service.GetExclusions(t.Context(), exclusionServerID)
		readDone <- readErr
	}()
	<-started
	want := core.MediaServerExclusions{MediaServerID: exclusionServerID, LibraryIDs: []string{"new"}}
	if _, err = service.Replace(t.Context(), want); err != nil {
		t.Fatalf("Replace: %v", err)
	}
	close(release)
	if err = <-readDone; err != nil {
		t.Fatalf("old read: %v", err)
	}
	got, err := service.GetExclusions(t.Context(), exclusionServerID)
	if err != nil || !got.ExcludesLibrary("new") || got.ExcludesLibrary("old") {
		t.Fatalf("cached exclusions = %+v, %v", got, err)
	}
}

type blockingStatsReader struct {
	mu      sync.Mutex
	calls   int
	started chan<- struct{}
	release <-chan struct{}
}

func (r *blockingStatsReader) ReadStats(ctx context.Context, query core.StatsQuery) (core.StatsResult, error) {
	r.mu.Lock()
	r.calls++
	calls := r.calls
	started, release := r.started, r.release
	r.mu.Unlock()
	if calls == 1 {
		started <- struct{}{}
		select {
		case <-release:
		case <-ctx.Done():
			return core.StatsResult{}, ctx.Err()
		}
	}
	return core.StatsResult{Window: query.Window, Totals: core.StatsTotals{Plays: int64(calls)}}, nil
}

func TestReplacePreventsOldStatsReadFromRepopulatingCache(t *testing.T) {
	clock := testutil.NewFakeClock(time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC))
	store := &exclusionStoreFake{value: core.MediaServerExclusions{MediaServerID: exclusionServerID}}
	exclusions, err := NewService(store, exclusionServerFake{}, clock, time.Minute)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	started, release := make(chan struct{}, 1), make(chan struct{})
	reader := &blockingStatsReader{started: started, release: release}
	stats, err := statsapp.NewService(reader, clock, time.Minute, statsObserverFake{})
	if err != nil {
		t.Fatalf("NewService stats: %v", err)
	}
	exclusions.AddInvalidator(stats.Invalidate)
	window, err := core.NewStatsWindow(1, exclusionServerID, "UTC", clock.Now())
	if err != nil {
		t.Fatalf("NewStatsWindow: %v", err)
	}
	query := core.StatsQuery{Window: window, Report: core.StatsReportOverview}
	readDone := make(chan error, 1)
	go func() {
		_, readErr := stats.ReadStats(t.Context(), query)
		readDone <- readErr
	}()
	<-started
	if _, err = exclusions.Replace(t.Context(), core.MediaServerExclusions{
		MediaServerID: exclusionServerID, LibraryIDs: []string{"new"},
	}); err != nil {
		t.Fatalf("Replace: %v", err)
	}
	close(release)
	if err = <-readDone; err != nil {
		t.Fatalf("old stats read: %v", err)
	}
	result, err := stats.ReadStats(t.Context(), query)
	if err != nil || result.Totals.Plays != 2 {
		t.Fatalf("stats after replacement = %+v, %v", result, err)
	}
}

type statsObserverFake struct{}

func (statsObserverFake) ObserveStatsQuery(string, string, float64) {}
