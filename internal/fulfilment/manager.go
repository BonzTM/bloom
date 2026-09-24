// Package fulfilment dispatches approved requests and polls availability.
package fulfilment

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/telemetry"
)

const (
	maxBatchSize        = 100
	maxDispatchAttempts = 3
	defaultScanInterval = 30 * time.Second
	maxFailureReason    = 1000
)

// Downloader dispatches titles and reads their live progress.
type Downloader interface {
	Add(ctx context.Context, managerName string, title core.DownloadTitle, options core.DownloadOptions) (string, error)
	Progress(ctx context.Context, managerName, managerItemID string) (core.DownloadProgress, error)
}

// Availability determines whether a processing request reached its configured authority.
type Availability interface {
	Available(ctx context.Context, request core.MediaRequest, profile core.RequestProfile) (bool, error)
}

// Audit records fulfilment lifecycle actions.
type Audit interface {
	Emit(ctx context.Context, event telemetry.AuditEvent) error
}

// RequestWriter performs fulfilment-owned state transitions.
type RequestWriter interface {
	TransitionRequest(ctx context.Context, id string, from, to core.RequestStatus, actorID, reason string, decidedAt time.Time) (core.MediaRequest, error)
}

// Config bounds dispatch batches, retries, and polling.
type Config struct {
	BatchSize            int
	AvailabilityInterval time.Duration
	ScanInterval         time.Duration
	RetryBase            time.Duration
}

// Dependencies contains the explicit fulfilment boundaries.
type Dependencies struct {
	Requests  core.RequestReader
	Writer    RequestWriter
	Dispatch  core.RequestDispatchWriter
	Profiles  core.RequestProfileReader
	Download  Downloader
	Available Availability
	Clock     core.Clock
	Events    core.RequestEventPublisher
	Audit     Audit
	Logger    *slog.Logger
	Wait      func(context.Context, time.Duration) error
}

// Manager supervises dispatch and availability polling.
type Manager struct {
	config Config
	deps   Dependencies
	wake   chan struct{}
}

// New validates config and dependencies and creates a stopped manager.
func New(config Config, deps Dependencies) (*Manager, error) {
	if config.BatchSize < 1 || config.BatchSize > maxBatchSize || config.AvailabilityInterval < time.Minute {
		return nil, core.ErrInvalidArgument
	}
	if config.ScanInterval <= 0 {
		config.ScanInterval = defaultScanInterval
	}
	if config.RetryBase <= 0 {
		config.RetryBase = time.Second
	}
	if deps.Requests == nil || deps.Writer == nil || deps.Dispatch == nil || deps.Profiles == nil || deps.Download == nil ||
		deps.Available == nil || deps.Clock == nil || deps.Events == nil || deps.Audit == nil || deps.Logger == nil {
		return nil, errors.New("fulfilment manager: all dependencies are required")
	}
	if deps.Wait == nil {
		deps.Wait = waitContext
	}
	return &Manager{config: config, deps: deps, wake: make(chan struct{}, 1)}, nil
}

// Enqueue wakes the manager after a request enters approved state.
func (m *Manager) Enqueue(string) {
	select {
	case m.wake <- struct{}{}:
	default:
	}
}

// Run processes bounded passes until ctx is cancelled.
func (m *Manager) Run(ctx context.Context) error {
	for {
		processing, err := m.runOnce(ctx)
		if ctx.Err() != nil {
			return nil //nolint:nilerr // root-context cancellation is a clean worker shutdown.
		}
		if err != nil {
			m.deps.Logger.WarnContext(ctx, "fulfilment pass failed", "error", err)
		}
		delay := m.config.ScanInterval
		if processing {
			delay = m.config.AvailabilityInterval
		}
		waitErr := m.wait(ctx, delay)
		if ctx.Err() != nil {
			return nil //nolint:nilerr // root-context cancellation is a clean worker shutdown.
		}
		if waitErr != nil {
			return fmt.Errorf("wait for fulfilment pass: %w", waitErr)
		}
	}
}

func (m *Manager) runOnce(ctx context.Context) (bool, error) {
	dispatchErr := m.dispatchBatch(ctx)
	processing, availableErr := m.pollAvailability(ctx)
	return processing, errors.Join(dispatchErr, availableErr)
}

func (m *Manager) dispatchBatch(ctx context.Context) error {
	status := core.RequestApproved
	values, err := m.deps.Requests.ListRequests(ctx, core.RequestListFilter{Status: &status, PageSize: m.config.BatchSize})
	if err != nil {
		return fmt.Errorf("list approved requests: %w", err)
	}
	var result error
	for _, request := range values {
		if ctx.Err() != nil {
			return errors.Join(result, ctx.Err())
		}
		if err := m.dispatch(ctx, request); err != nil {
			result = errors.Join(result, err)
		}
	}
	return result
}

func (m *Manager) dispatch(ctx context.Context, request core.MediaRequest) error {
	profile, err := m.deps.Profiles.GetRequestProfile(ctx, request.ProfileID)
	if err != nil {
		return m.fail(ctx, request, "request profile is unavailable", err)
	}
	itemID := request.DownloadManagerItemID
	if itemID == "" {
		itemID, err = m.addWithRetry(ctx, request, profile)
		if err != nil {
			return m.fail(ctx, request, "download manager dispatch failed", err)
		}
	}
	updated, err := m.deps.Dispatch.RecordRequestDispatch(ctx, request.ID, itemID, core.NormalizeTime(m.deps.Clock.Now()))
	if err != nil {
		return fmt.Errorf("record dispatched request %s: %w", request.ID, err)
	}
	m.publish(ctx, updated, core.RequestEventDispatched)
	m.audit(ctx, updated, "request.dispatch", telemetry.AuditSuccess, "")
	return nil
}

func (m *Manager) addWithRetry(
	ctx context.Context, request core.MediaRequest, profile core.RequestProfile,
) (string, error) {
	title := core.DownloadTitle{
		Kind: request.Kind, ProviderID: request.ProviderID, Title: request.Title,
		Year: request.Year, Seasons: seasonNumbers(request.Seasons),
	}
	options := core.DownloadOptions{
		QualityProfile: profile.QualityProfile, RootFolder: profile.RootFolder, Tags: profile.Tags,
	}
	var last error
	for attempt := range maxDispatchAttempts {
		itemID, err := m.deps.Download.Add(ctx, profile.DownloadManagerInstance, title, options)
		if err == nil {
			return itemID, nil
		}
		last = err
		if !retryable(err) || attempt == maxDispatchAttempts-1 {
			return "", last
		}
		if err := m.deps.Wait(ctx, m.config.RetryBase*time.Duration(1<<attempt)); err != nil {
			return "", errors.Join(last, err)
		}
	}
	return "", last
}

func (m *Manager) pollAvailability(ctx context.Context) (bool, error) {
	status := core.RequestProcessing
	values, err := m.deps.Requests.ListRequests(ctx, core.RequestListFilter{Status: &status, PageSize: m.config.BatchSize})
	if err != nil {
		return false, fmt.Errorf("list processing requests: %w", err)
	}
	var result error
	for _, request := range values {
		profile, profileErr := m.deps.Profiles.GetRequestProfile(ctx, request.ProfileID)
		if profileErr != nil {
			result = errors.Join(result, profileErr)
			continue
		}
		available, availabilityErr := m.deps.Available.Available(ctx, request, profile)
		if availabilityErr != nil {
			result = errors.Join(result, availabilityErr)
			continue
		}
		if available {
			if transitionErr := m.available(ctx, request); transitionErr != nil {
				result = errors.Join(result, transitionErr)
			}
		}
	}
	return len(values) != 0, result
}

func (m *Manager) available(ctx context.Context, request core.MediaRequest) error {
	updated, err := m.deps.Writer.TransitionRequest(
		ctx, request.ID, core.RequestProcessing, core.RequestAvailable, "", "", core.NormalizeTime(m.deps.Clock.Now()),
	)
	if err != nil {
		return fmt.Errorf("mark request available: %w", err)
	}
	m.publish(ctx, updated, core.RequestEventAvailable)
	m.audit(ctx, updated, "request.available", telemetry.AuditSuccess, "")
	return nil
}

func (m *Manager) fail(
	ctx context.Context, request core.MediaRequest, reason string, cause error,
) error {
	if len(reason) > maxFailureReason {
		reason = reason[:maxFailureReason]
	}
	updated, err := m.deps.Writer.TransitionRequest(
		ctx, request.ID, core.RequestApproved, core.RequestFailed, "", reason, core.NormalizeTime(m.deps.Clock.Now()),
	)
	if err != nil {
		return errors.Join(cause, fmt.Errorf("mark request failed: %w", err))
	}
	m.publish(ctx, updated, core.RequestEventFailed)
	m.audit(ctx, updated, "request.fail", telemetry.AuditFailure, "dispatch")
	return fmt.Errorf("dispatch request %s: %w", request.ID, cause)
}

func (m *Manager) publish(ctx context.Context, request core.MediaRequest, eventType core.RequestEventType) {
	m.deps.Events.PublishRequestEvent(ctx, core.RequestEvent{
		Type: eventType, RequestID: request.ID, ActorID: "system", Kind: request.Kind,
		Title: request.Title, At: request.UpdatedAt,
	})
}

func (m *Manager) audit(
	ctx context.Context, request core.MediaRequest, action string, result telemetry.AuditResult, reason string,
) {
	err := m.deps.Audit.Emit(ctx, telemetry.AuditEvent{
		Actor: "system", Action: action, Resource: "request:" + request.ID,
		Kind: string(request.Kind), Title: request.Title, Result: result, Reason: reason, Source: "worker",
	})
	if err != nil {
		m.deps.Logger.ErrorContext(ctx, "write fulfilment audit event", "error", err, "action", action)
	}
}

func (m *Manager) wait(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-m.wake:
		return nil
	case <-timer.C:
		return nil
	}
}

func retryable(err error) bool {
	var classified *core.DownloadManagerError
	return errors.As(err, &classified) && classified.Retryable
}

func seasonNumbers(values []core.RequestSeason) []int {
	result := make([]int, 0, len(values))
	for _, value := range values {
		result = append(result, value.Number)
	}
	return result
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

// DownloadAvailability uses a manager's file or completion signal.
type DownloadAvailability struct {
	Download Downloader
}

// Available reports the manager's authoritative completion state.
func (a DownloadAvailability) Available(
	ctx context.Context, request core.MediaRequest, profile core.RequestProfile,
) (bool, error) {
	progress, err := a.Download.Progress(ctx, profile.DownloadManagerInstance, request.DownloadManagerItemID)
	if err != nil {
		return false, err
	}
	return progress.HasFile || progress.Complete, nil
}

// MediaServerAvailability looks up provider identifiers on registered media servers.
type MediaServerAvailability interface {
	HasTitle(
		ctx context.Context, kind core.MediaKind, provider core.MetadataProviderKind,
		providerID string, seasons []int,
	) (bool, []int, error)
}

// MediaAvailability uses registered media servers as the authority.
type MediaAvailability struct {
	MediaServers MediaServerAvailability
}

// Available reports whether a media server contains the requested title.
func (a MediaAvailability) Available(
	ctx context.Context, request core.MediaRequest, _ core.RequestProfile,
) (bool, error) {
	available, _, err := a.MediaServers.HasTitle(
		ctx, request.Kind, request.Provider, request.ProviderID, seasonNumbers(request.Seasons),
	)
	return available, err
}
