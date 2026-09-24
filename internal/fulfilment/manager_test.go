package fulfilment

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/telemetry"
	"github.com/BonzTM/bloom/internal/testutil"
)

type managerFixture struct {
	requests    []core.MediaRequest
	profile     core.RequestProfile
	addErrors   []error
	itemID      string
	addCalls    int
	progress    core.DownloadProgress
	progressErr error
	availCalls  int
	waits       []time.Duration
	events      []core.RequestEvent
	eventCh     chan core.RequestEvent
	cancelAdd   context.CancelFunc
}

func (f *managerFixture) GetRequest(_ context.Context, id string) (core.MediaRequest, error) {
	for _, request := range f.requests {
		if request.ID == id {
			return request, nil
		}
	}
	return core.MediaRequest{}, core.ErrNotFound
}

func (f *managerFixture) ListRequests(_ context.Context, filter core.RequestListFilter) ([]core.MediaRequest, error) {
	result := make([]core.MediaRequest, 0, len(f.requests))
	for _, request := range f.requests {
		if filter.Status != nil && request.Status == *filter.Status {
			result = append(result, request)
		}
	}
	return result, nil
}

func (f *managerFixture) GetRequestProfile(context.Context, string) (core.RequestProfile, error) {
	return f.profile, nil
}

func (f *managerFixture) ListRequestProfiles(context.Context, string, int) ([]core.RequestProfile, error) {
	return []core.RequestProfile{f.profile}, nil
}

func (f *managerFixture) TransitionRequest(
	_ context.Context, id string, from, to core.RequestStatus, _, reason string, at time.Time,
) (core.MediaRequest, error) {
	for index := range f.requests {
		if f.requests[index].ID == id && f.requests[index].Status == from {
			f.requests[index].Status = to
			f.requests[index].UpdatedAt = at
			if to == core.RequestFailed {
				f.requests[index].FailureReason = reason
			}
			return f.requests[index], nil
		}
	}
	return core.MediaRequest{}, core.ErrInvalidTransition
}

func (f *managerFixture) RecordRequestDispatch(
	_ context.Context, id, itemID string, at time.Time,
) (core.MediaRequest, error) {
	for index := range f.requests {
		if f.requests[index].ID == id && f.requests[index].Status == core.RequestApproved {
			f.requests[index].Status = core.RequestProcessing
			f.requests[index].DownloadManagerItemID = itemID
			f.requests[index].UpdatedAt = at
			return f.requests[index], nil
		}
	}
	return core.MediaRequest{}, core.ErrInvalidTransition
}

func (f *managerFixture) Add(context.Context, string, core.DownloadTitle, core.DownloadOptions) (string, error) {
	index := f.addCalls
	f.addCalls++
	if f.cancelAdd != nil {
		f.cancelAdd()
	}
	if index < len(f.addErrors) && f.addErrors[index] != nil {
		return "", f.addErrors[index]
	}
	return f.itemID, nil
}

func (f *managerFixture) Progress(context.Context, string, string) (core.DownloadProgress, error) {
	return f.progress, f.progressErr
}

func (f *managerFixture) Available(context.Context, core.MediaRequest, core.RequestProfile) (bool, error) {
	f.availCalls++
	return f.progress.Complete, f.progressErr
}

func (f *managerFixture) Emit(context.Context, telemetry.AuditEvent) error { return nil }

func (f *managerFixture) PublishRequestEvent(_ context.Context, event core.RequestEvent) {
	f.events = append(f.events, event)
	if f.eventCh != nil {
		f.eventCh <- event
	}
}

func (f *managerFixture) wait(_ context.Context, delay time.Duration) error {
	f.waits = append(f.waits, delay)
	return nil
}

func TestDispatchRetriesThenRecordsProcessing(t *testing.T) {
	retryable := &core.DownloadManagerError{Kind: core.DownloadManagerUnavailable, Retryable: true}
	fixture := newManagerFixture(t)
	fixture.itemID = "81"
	fixture.addErrors = []error{retryable, retryable}
	manager := fixture.manager(t)
	if _, err := manager.runOnce(t.Context()); err != nil {
		t.Fatalf("runOnce: %v", err)
	}
	if fixture.addCalls != 3 || fixture.requests[0].Status != core.RequestProcessing || fixture.requests[0].DownloadManagerItemID != "81" {
		t.Fatalf("dispatch state = calls %d request %+v", fixture.addCalls, fixture.requests[0])
	}
	if len(fixture.waits) != 2 || fixture.waits[0] != time.Second || fixture.waits[1] != 2*time.Second {
		t.Fatalf("retry waits = %v", fixture.waits)
	}
	if len(fixture.events) != 1 || fixture.events[0].Type != core.RequestEventDispatched {
		t.Fatalf("events = %+v", fixture.events)
	}
}

func TestDispatchGivesUpAndFailedRequestCanBeObserved(t *testing.T) {
	fixture := newManagerFixture(t)
	fixture.addErrors = []error{errors.New("rejected")}
	manager := fixture.manager(t)
	if _, err := manager.runOnce(t.Context()); err == nil {
		t.Fatal("runOnce succeeded after terminal dispatch failure")
	}
	if fixture.addCalls != 1 || fixture.requests[0].Status != core.RequestFailed || fixture.requests[0].FailureReason == "" {
		t.Fatalf("failed request = calls %d request %+v", fixture.addCalls, fixture.requests[0])
	}
	if len(fixture.events) != 1 || fixture.events[0].Type != core.RequestEventFailed {
		t.Fatalf("events = %+v", fixture.events)
	}
}

func TestAvailabilityOnlyRunsForProcessingRequests(t *testing.T) {
	fixture := newManagerFixture(t)
	fixture.itemID = "82"
	manager := fixture.manager(t)
	if processing, err := manager.runOnce(t.Context()); err != nil || !processing {
		t.Fatalf("dispatch pass = %t, %v", processing, err)
	}
	if fixture.availCalls != 1 {
		t.Fatalf("availability calls = %d, want 1", fixture.availCalls)
	}
	fixture.progress.Complete = true
	if processing, err := manager.runOnce(t.Context()); err != nil || !processing {
		t.Fatalf("availability pass = %t, %v", processing, err)
	}
	if fixture.requests[0].Status != core.RequestAvailable || fixture.events[len(fixture.events)-1].Type != core.RequestEventAvailable {
		t.Fatalf("available request = %+v; events %+v", fixture.requests[0], fixture.events)
	}
	if processing, err := manager.runOnce(t.Context()); err != nil || processing {
		t.Fatalf("idle pass = %t, %v", processing, err)
	}
	if fixture.availCalls != 2 {
		t.Fatalf("idle pass polled availability: %d calls", fixture.availCalls)
	}
}

func TestDispatchStopsMidBatchOnCancellation(t *testing.T) {
	fixture := newManagerFixture(t)
	fixture.requests = append(fixture.requests, core.MediaRequest{
		ID: "22222222-2222-4222-8222-222222222222", Kind: core.MediaKindMovie,
		Provider: core.MetadataProviderTMDB, ProviderID: "11", Title: "Second", ProfileID: "profile", Status: core.RequestApproved,
	})
	fixture.itemID = "83"
	ctx, cancel := context.WithCancel(t.Context())
	fixture.cancelAdd = cancel
	manager := fixture.manager(t)
	err := manager.dispatchBatch(ctx)
	if !errors.Is(err, context.Canceled) || fixture.addCalls != 1 {
		t.Fatalf("dispatchBatch = %v; add calls %d", err, fixture.addCalls)
	}
}

func TestRunKeepsFirstManagerFailureNonFatal(t *testing.T) {
	retryable := &core.DownloadManagerError{Kind: core.DownloadManagerUnavailable, Retryable: true}
	fixture := newManagerFixture(t)
	fixture.addErrors = []error{retryable, retryable, retryable}
	fixture.eventCh = make(chan core.RequestEvent, 1)
	manager := fixture.manager(t)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- manager.Run(ctx) }()
	event := <-fixture.eventCh
	if event.Type != core.RequestEventFailed {
		t.Fatalf("first event = %+v", event)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run: %v", err)
	}
}

func newManagerFixture(t *testing.T) *managerFixture {
	t.Helper()
	return &managerFixture{
		requests: []core.MediaRequest{{
			ID: "11111111-1111-4111-8111-111111111111", Kind: core.MediaKindMovie,
			Provider: core.MetadataProviderTMDB, ProviderID: "10", Title: "Film", ProfileID: "profile", Status: core.RequestApproved,
		}},
		profile: core.RequestProfile{ID: "profile", DownloadManagerInstance: "main", QualityProfile: "1", RootFolder: "/movies"},
	}
}

func (f *managerFixture) manager(t *testing.T) *Manager {
	t.Helper()
	manager, err := New(Config{BatchSize: 10, AvailabilityInterval: time.Minute, RetryBase: time.Second}, Dependencies{
		Requests: f, Writer: f, Dispatch: f, Profiles: f, Download: f, Available: f,
		Clock:  testutil.NewFakeClock(time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)),
		Events: f, Audit: f, Logger: slog.New(slog.DiscardHandler), Wait: f.wait,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return manager
}
