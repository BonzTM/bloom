package notify

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/BonzTM/bloom/internal/core"
)

const (
	defaultWorkerBatch     = 50
	defaultDeliveryTimeout = 10 * time.Second
	defaultLeaseDuration   = 30 * time.Second
	defaultRetryBase       = 5 * time.Second
	maxRetryDelay          = 6 * time.Hour
	defaultDegradedAfter   = 3
	defaultRetentionBatch  = 500
)

// ChannelResolver resolves a channel for one claimed delivery.
type ChannelResolver interface {
	Channel(context.Context, string) (core.NotificationRegistration, core.NotificationChannel, error)
}

// WorkerMetrics observes bounded-cardinality outcomes and queue depth.
type WorkerMetrics interface {
	ObserveNotificationDelivery(kind, outcome string)
	SetNotificationOutboxDepth(int64)
}

// WorkerConfig bounds delivery, leasing, retry, and retention work.
type WorkerConfig struct {
	Interval       time.Duration
	Retention      time.Duration
	BatchSize      int
	SendTimeout    time.Duration
	LeaseDuration  time.Duration
	RetryBase      time.Duration
	DegradedAfter  int
	RetentionBatch int
}

// WorkerDependencies contains the worker's injected boundaries.
type WorkerDependencies struct {
	Events      core.NotificationEventStore
	Store       core.NotificationDeliveryStore
	Reader      core.NotificationDeliveryReader
	Maintenance core.NotificationMaintenanceStore
	Channels    ChannelResolver
	Accounts    AccountReader
	Clock       core.Clock
	Metrics     WorkerMetrics
	Logger      *slog.Logger
	Wait        func(context.Context, time.Duration) error
}

// Worker drains the durable notification outbox.
type Worker struct {
	config WorkerConfig
	deps   WorkerDependencies
	wake   chan struct{}
}

// NewWorker validates and constructs a delivery worker.
func NewWorker(config WorkerConfig, deps WorkerDependencies) (*Worker, error) {
	applyWorkerDefaults(&config)
	if config.Interval < time.Second || config.Retention <= 0 || config.BatchSize < 1 || config.BatchSize > 100 ||
		config.SendTimeout <= 0 || config.LeaseDuration <= 0 || config.RetryBase <= 0 || config.DegradedAfter < 1 ||
		config.RetentionBatch < 1 || config.RetentionBatch > 1000 {
		return nil, core.ErrInvalidArgument
	}
	if deps.Events == nil || deps.Store == nil || deps.Reader == nil || deps.Maintenance == nil || deps.Channels == nil ||
		deps.Accounts == nil || deps.Clock == nil || deps.Metrics == nil || deps.Logger == nil {
		return nil, errors.New("notification worker: all dependencies are required")
	}
	worker := &Worker{config: config, deps: deps, wake: make(chan struct{}, 1)}
	if worker.deps.Wait == nil {
		worker.deps.Wait = worker.wait
	}
	return worker, nil
}

func applyWorkerDefaults(config *WorkerConfig) {
	if config.BatchSize == 0 {
		config.BatchSize = defaultWorkerBatch
	}
	if config.SendTimeout == 0 {
		config.SendTimeout = defaultDeliveryTimeout
	}
	if config.LeaseDuration == 0 {
		config.LeaseDuration = defaultLeaseDuration
	}
	if config.RetryBase == 0 {
		config.RetryBase = defaultRetryBase
	}
	if config.DegradedAfter == 0 {
		config.DegradedAfter = defaultDegradedAfter
	}
	if config.RetentionBatch == 0 {
		config.RetentionBatch = defaultRetentionBatch
	}
}

// Run drains due deliveries until cancellation.
func (w *Worker) Run(ctx context.Context) error {
	for {
		if err := w.runOnce(ctx); err != nil && ctx.Err() == nil {
			w.deps.Logger.WarnContext(ctx, "notification worker pass failed", "error", err)
		}
		if ctx.Err() != nil {
			return nil //nolint:nilerr // cancellation is the clean shutdown signal.
		}
		if err := w.deps.Wait(ctx, w.config.Interval); err != nil {
			if ctx.Err() != nil {
				return nil //nolint:nilerr // cancellation is the clean shutdown signal.
			}
			return fmt.Errorf("wait for notification worker: %w", err)
		}
	}
}

// Wake hints that committed notification work is ready; polling remains authoritative.
func (w *Worker) Wake(context.Context, core.RequestEvent) {
	select {
	case w.wake <- struct{}{}:
	default:
	}
}

func (w *Worker) runOnce(ctx context.Context) error {
	result := w.fanOutEvents(ctx)
	for range w.config.BatchSize {
		if ctx.Err() != nil {
			return errors.Join(result, ctx.Err())
		}
		delivery, err := w.claim(ctx)
		if errors.Is(err, core.ErrNotFound) {
			break
		}
		if err != nil {
			result = errors.Join(result, err)
			break
		}
		if err := w.deliver(ctx, delivery); err != nil {
			result = errors.Join(result, err)
		}
	}
	w.observeDepth(ctx)
	now := core.NormalizeTime(w.deps.Clock.Now())
	_, deleteErr := w.deps.Maintenance.DeleteTombstonedNotificationChannels(
		ctx, now, w.config.RetentionBatch,
	)
	_, pruneErr := w.deps.Maintenance.PruneNotificationDeliveries(
		ctx, now.Add(-w.config.Retention), w.config.RetentionBatch,
	)
	return errors.Join(result, deleteErr, pruneErr)
}

func (w *Worker) fanOutEvents(ctx context.Context) error {
	var result error
	for range w.config.BatchSize {
		if ctx.Err() != nil {
			return errors.Join(result, ctx.Err())
		}
		event, err := w.deps.Events.GetUnfannedNotificationEvent(ctx)
		if errors.Is(err, core.ErrNotFound) {
			break
		}
		if err != nil {
			return errors.Join(result, fmt.Errorf("get notification event: %w", err))
		}
		payload := notificationPayload(ctx, w.deps.Accounts, event.Event)
		if _, err := w.deps.Events.FanOutNotificationEvent(ctx, event.ID, payload, event.Event.At); err != nil {
			return errors.Join(result, fmt.Errorf("fan out notification event: %w", err))
		}
	}
	return result
}

func (w *Worker) claim(ctx context.Context) (core.NotificationDelivery, error) {
	now := core.NormalizeTime(w.deps.Clock.Now())
	token, err := core.NewID()
	if err != nil {
		return core.NotificationDelivery{}, fmt.Errorf("create notification lease: %w", err)
	}
	delivery, err := w.deps.Store.ClaimNotificationDelivery(ctx, core.NotificationLease{
		Token: token, ExpiresAt: now.Add(w.config.LeaseDuration),
	}, now)
	if err != nil {
		return core.NotificationDelivery{}, err
	}
	return delivery, nil
}

func (w *Worker) deliver(ctx context.Context, delivery core.NotificationDelivery) error {
	registration, channel, err := w.deps.Channels.Channel(ctx, delivery.ChannelID)
	if err == nil {
		defer closeChannel(channel)
		var message core.RenderedNotification
		delivery.Payload.DeliveryID = delivery.ID
		message, err = RenderMessage(delivery.Payload, registration.Settings)
		if err == nil {
			sendCtx, cancel := context.WithTimeout(ctx, w.config.SendTimeout)
			err = channel.Send(sendCtx, message)
			cancel()
		}
	}
	if err != nil {
		return w.fail(ctx, delivery, err)
	}
	now := core.NormalizeTime(w.deps.Clock.Now())
	if err := w.deps.Store.CompleteNotificationDelivery(ctx, delivery.ID, delivery.LeaseToken, now); err != nil {
		return fmt.Errorf("complete notification delivery: %w", err)
	}
	if err := w.deps.Maintenance.RecordNotificationChannelResult(ctx, delivery.ChannelID, true, false, w.config.DegradedAfter, now); err != nil {
		return fmt.Errorf("clear notification channel degradation: %w", err)
	}
	w.deps.Metrics.ObserveNotificationDelivery(string(delivery.ChannelKind), "sent")
	return nil
}

func (w *Worker) fail(ctx context.Context, delivery core.NotificationDelivery, cause error) error {
	terminal := delivery.Attempts >= core.MaxNotificationAttempts || !notificationRetryable(cause)
	now := core.NormalizeTime(w.deps.Clock.Now())
	next := now.Add(w.retryDelay(delivery.Attempts, cause))
	reason := core.SafeNotificationReason(cause)
	if err := w.deps.Store.RescheduleNotificationDelivery(ctx, delivery.ID, delivery.LeaseToken, reason, now, next, terminal); err != nil {
		return errors.Join(cause, fmt.Errorf("record notification failure: %w", err))
	}
	if err := w.deps.Maintenance.RecordNotificationChannelResult(ctx, delivery.ChannelID, false, terminal, w.config.DegradedAfter, now); err != nil {
		return errors.Join(cause, fmt.Errorf("record notification channel degradation: %w", err))
	}
	outcome := "retry"
	if terminal {
		outcome = "failed"
	}
	w.deps.Metrics.ObserveNotificationDelivery(string(delivery.ChannelKind), outcome)
	return fmt.Errorf("deliver notification: %w", cause)
}

func (w *Worker) retryDelay(attempt int, err error) time.Duration {
	var classified *core.NotificationError
	if errors.As(err, &classified) && classified.RetryAfter > 0 {
		return min(classified.RetryAfter, maxRetryDelay)
	}
	exponent := max(0, min(attempt-1, core.MaxNotificationAttempts-1))
	return min(w.config.RetryBase*time.Duration(1<<exponent), maxRetryDelay)
}

func notificationRetryable(err error) bool {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var classified *core.NotificationError
	return errors.As(err, &classified) && classified.Retryable
}

func (w *Worker) observeDepth(ctx context.Context) {
	depth, err := w.deps.Reader.NotificationOutboxDepth(ctx)
	if err != nil {
		w.deps.Logger.WarnContext(ctx, "read notification outbox depth", "error", err)
		return
	}
	w.deps.Metrics.SetNotificationOutboxDepth(depth)
}

func (w *Worker) wait(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-w.wake:
		return nil
	case <-timer.C:
		return nil
	}
}
