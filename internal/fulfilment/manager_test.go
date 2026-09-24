package fulfilment

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"testing"
	"time"

	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/telemetry"
	"github.com/BonzTM/bloom/internal/testutil"
)

type managerFixture struct {
	requests           []core.MediaRequest
	profile            core.RequestProfile
	addErrors          []error
	itemID             string
	addCalls           int
	addManagerIDs      []string
	addOptions         []core.DownloadOptions
	progress           core.DownloadProgress
	progressErr        error
	availCalls         int
	availabilityIDs    []string
	progressManagerIDs []string
	waits              []time.Duration
	events             []core.RequestEvent
	audits             []telemetry.AuditEvent
	eventCh            chan core.RequestEvent
	cancelAdd          context.CancelFunc
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
	_ context.Context, id string, from, to core.RequestStatus, _, reason string, at time.Time, _ ...core.RequestEvent,
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
	_ context.Context, id, leaseToken, itemID string, at time.Time, _ ...core.RequestEvent,
) (core.MediaRequest, error) {
	for index := range f.requests {
		if f.requests[index].ID == id && f.requests[index].Status == core.RequestApproved &&
			f.requests[index].DispatchLeaseToken == leaseToken {
			f.requests[index].Status = core.RequestProcessing
			f.requests[index].DownloadManagerItemID = itemID
			f.requests[index].DispatchLeaseToken = ""
			f.requests[index].DispatchLeaseExpiresAt = nil
			f.requests[index].UpdatedAt = at
			return f.requests[index], nil
		}
	}
	return core.MediaRequest{}, core.ErrInvalidTransition
}

func (f *managerFixture) ClaimRequestDispatch(
	_ context.Context, id string, snapshot core.RequestDispatchSnapshot, lease core.RequestDispatchLease, at time.Time,
) (core.MediaRequest, error) {
	for index := range f.requests {
		request := &f.requests[index]
		leaseExpired := request.DispatchLeaseExpiresAt != nil && !request.DispatchLeaseExpiresAt.After(at)
		if request.ID == id && request.Status == core.RequestApproved &&
			(request.DispatchLeaseToken == "" || leaseExpired) {
			request.DownloadManagerID = snapshot.DownloadManagerID
			request.DispatchQualityProfile = snapshot.QualityProfile
			request.DispatchRootFolder = snapshot.RootFolder
			request.DispatchTags = append([]string(nil), snapshot.Tags...)
			request.DispatchLeaseToken = lease.Token
			request.DispatchLeaseExpiresAt = new(lease.ExpiresAt)
			request.UpdatedAt = at
			return *request, nil
		}
	}
	return core.MediaRequest{}, core.ErrInvalidTransition
}

func (f *managerFixture) FailRequestDispatch(
	_ context.Context, id, leaseToken, reason string, at time.Time, _ ...core.RequestEvent,
) (core.MediaRequest, error) {
	for index := range f.requests {
		request := &f.requests[index]
		if request.ID == id && request.Status == core.RequestApproved &&
			request.DispatchLeaseToken == leaseToken && request.DispatchLeaseExpiresAt.After(at) {
			request.Status, request.FailureReason, request.UpdatedAt = core.RequestFailed, reason, at
			request.DispatchLeaseToken, request.DispatchLeaseExpiresAt = "", nil
			return *request, nil
		}
	}
	return core.MediaRequest{}, core.ErrInvalidTransition
}

func (f *managerFixture) ClaimRequestsForAvailability(
	_ context.Context, pageSize int, at time.Time,
) ([]core.MediaRequest, error) {
	indices := make([]int, 0, len(f.requests))
	for index := range f.requests {
		if f.requests[index].Status == core.RequestProcessing {
			indices = append(indices, index)
		}
	}
	slices.SortStableFunc(indices, func(left, right int) int {
		return compareAvailabilityOrder(f.requests[left], f.requests[right])
	})
	if len(indices) > pageSize {
		indices = indices[:pageSize]
	}
	result := make([]core.MediaRequest, 0, len(indices))
	for _, index := range indices {
		f.requests[index].LastAvailabilityCheckAt = new(at)
		result = append(result, f.requests[index])
	}
	return result, nil
}

func compareAvailabilityOrder(left, right core.MediaRequest) int {
	if left.LastAvailabilityCheckAt == nil && right.LastAvailabilityCheckAt != nil {
		return -1
	}
	if left.LastAvailabilityCheckAt != nil && right.LastAvailabilityCheckAt == nil {
		return 1
	}
	if left.LastAvailabilityCheckAt != nil && !left.LastAvailabilityCheckAt.Equal(*right.LastAvailabilityCheckAt) {
		return left.LastAvailabilityCheckAt.Compare(*right.LastAvailabilityCheckAt)
	}
	return left.CreatedAt.Compare(right.CreatedAt)
}

func (f *managerFixture) Resolve(_ context.Context, name string) (core.DownloadManager, error) {
	if name == "missing" {
		return core.DownloadManager{}, core.ErrNotFound
	}
	return core.DownloadManager{ID: testManagerID, Name: name, Kind: core.DownloadManagerKindRadarr}, nil
}

func (f *managerFixture) Add(_ context.Context, managerID string, _ core.DownloadTitle, options core.DownloadOptions) (string, error) {
	index := f.addCalls
	f.addCalls++
	f.addManagerIDs = append(f.addManagerIDs, managerID)
	f.addOptions = append(f.addOptions, options)
	if f.cancelAdd != nil {
		f.cancelAdd()
	}
	if index < len(f.addErrors) && f.addErrors[index] != nil {
		return "", f.addErrors[index]
	}
	return f.itemID, nil
}

func (f *managerFixture) Progress(_ context.Context, managerID, _ string, _ []int) (core.DownloadProgress, error) {
	f.progressManagerIDs = append(f.progressManagerIDs, managerID)
	return f.progress, f.progressErr
}

func (f *managerFixture) Available(_ context.Context, request core.MediaRequest) (bool, error) {
	f.availCalls++
	f.availabilityIDs = append(f.availabilityIDs, request.ID)
	return f.progress.HasFile, f.progressErr
}

func (f *managerFixture) Emit(_ context.Context, event telemetry.AuditEvent) error {
	f.audits = append(f.audits, event)
	return nil
}

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
	fixture.progress.HasFile = true
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

func TestCompletedQueueWithoutFileDoesNotMarkAvailable(t *testing.T) {
	fixture := newManagerFixture(t)
	fixture.progress = core.DownloadProgress{Complete: true, HasFile: false}
	availability := DownloadAvailability{Download: fixture}
	request := fixture.requests[0]
	request.DownloadManagerID = testManagerID
	request.DownloadManagerItemID = "82"
	available, err := availability.Available(t.Context(), request)
	if err != nil || available {
		t.Fatalf("Available = %t, %v; want false", available, err)
	}
}

func TestProfileEditDoesNotRedirectProcessingRequest(t *testing.T) {
	fixture := newManagerFixture(t)
	fixture.itemID = "82"
	manager := fixture.manager(t)
	manager.deps.Available = DownloadAvailability{Download: fixture}
	if err := manager.dispatchBatch(t.Context()); err != nil {
		t.Fatalf("dispatchBatch: %v", err)
	}
	fixture.profile.DownloadManagerInstance = "replacement"
	fixture.profile.QualityProfile = "9"
	fixture.progress.HasFile = true
	if _, err := manager.pollAvailability(t.Context()); err != nil {
		t.Fatalf("pollAvailability: %v", err)
	}
	request := fixture.requests[0]
	if request.Status != core.RequestAvailable || request.DownloadManagerID != testManagerID {
		t.Fatalf("request = %+v", request)
	}
	if !slices.Equal(fixture.addManagerIDs, []string{testManagerID}) ||
		!slices.Equal(fixture.progressManagerIDs, []string{testManagerID}) {
		t.Fatalf("manager ids = add %v progress %v", fixture.addManagerIDs, fixture.progressManagerIDs)
	}
	if fixture.addOptions[0].QualityProfile != "1" {
		t.Fatalf("dispatch options = %+v", fixture.addOptions[0])
	}
}

func TestAvailabilityClaimEventuallyChecksBeyondFirstBatch(t *testing.T) {
	fixture := newManagerFixture(t)
	fixture.requests = processingRequests(3)
	manager := fixture.managerWithBatchSize(t, 2)
	for range 3 {
		if _, err := manager.pollAvailability(t.Context()); err != nil {
			t.Fatalf("pollAvailability: %v", err)
		}
	}
	want := []string{fixture.requests[0].ID, fixture.requests[1].ID, fixture.requests[2].ID}
	for _, id := range want {
		if !slices.Contains(fixture.availabilityIDs, id) {
			t.Fatalf("availability calls %v do not include %s", fixture.availabilityIDs, id)
		}
	}
}

func TestTerminalAvailabilityFailureMarksRequestFailed(t *testing.T) {
	fixture := newManagerFixture(t)
	fixture.requests = processingRequests(1)
	fixture.progressErr = &core.DownloadManagerError{
		Kind: core.DownloadManagerUnauthorized, Operation: "movie", Retryable: false,
	}
	manager := fixture.manager(t)
	if _, err := manager.pollAvailability(t.Context()); err == nil {
		t.Fatal("pollAvailability succeeded")
	}
	request := fixture.requests[0]
	if request.Status != core.RequestFailed || request.FailureReason != "availability check failed: unauthorized" {
		t.Fatalf("request = %+v", request)
	}
	if fixture.events[len(fixture.events)-1].Type != core.RequestEventFailed ||
		fixture.audits[len(fixture.audits)-1].Action != "request.fail" {
		t.Fatalf("events = %+v audits = %+v", fixture.events, fixture.audits)
	}
}

func TestDeletedSnapshottedManagerMarksRequestFailed(t *testing.T) {
	fixture := newManagerFixture(t)
	fixture.requests = processingRequests(1)
	fixture.progressErr = core.ErrNotFound
	manager := fixture.manager(t)
	manager.deps.Available = DownloadAvailability{Download: fixture}
	if _, err := manager.pollAvailability(t.Context()); err == nil {
		t.Fatal("pollAvailability succeeded")
	}
	request := fixture.requests[0]
	if request.Status != core.RequestFailed ||
		request.FailureReason != "availability check failed: dependency not found" {
		t.Fatalf("request = %+v", request)
	}
}

func TestTransientAvailabilityFailureRemainsProcessing(t *testing.T) {
	fixture := newManagerFixture(t)
	fixture.requests = processingRequests(1)
	fixture.progressErr = &core.DownloadManagerError{
		Kind: core.DownloadManagerUnavailable, Operation: "movie", Retryable: true,
	}
	manager := fixture.manager(t)
	if _, err := manager.pollAvailability(t.Context()); err == nil {
		t.Fatal("pollAvailability succeeded")
	}
	if fixture.requests[0].Status != core.RequestProcessing || len(fixture.events) != 0 {
		t.Fatalf("request = %+v events = %+v", fixture.requests[0], fixture.events)
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
	return f.managerWithBatchSize(t, 10)
}

func (f *managerFixture) managerWithBatchSize(t *testing.T, batchSize int) *Manager {
	t.Helper()
	manager, err := New(Config{BatchSize: batchSize, AvailabilityInterval: time.Minute, RetryBase: time.Second}, Dependencies{
		Requests: f, Writer: f, Dispatch: f, AvailabilityRequests: f, Profiles: f, Download: f, Available: f,
		Clock:  testutil.NewFakeClock(time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)),
		Events: f, Audit: f, Logger: slog.New(slog.DiscardHandler), Wait: f.wait,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return manager
}

const testManagerID = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"

func processingRequests(count int) []core.MediaRequest {
	result := make([]core.MediaRequest, 0, count)
	for index := range count {
		id := "00000000-0000-4000-8000-00000000000" + string(rune('1'+index))
		result = append(result, core.MediaRequest{
			ID: id, Kind: core.MediaKindMovie, Provider: core.MetadataProviderTMDB,
			ProviderID: "10", Title: "Film", ProfileID: "profile", Status: core.RequestProcessing,
			DownloadManagerID: testManagerID, DownloadManagerItemID: "82",
			CreatedAt: time.Date(2026, 9, 23, 10+index, 0, 0, 0, time.UTC),
		})
	}
	return result
}
