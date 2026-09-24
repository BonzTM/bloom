package notify

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/testutil"
)

type workerStore struct {
	delivery   core.NotificationDelivery
	event      core.NotificationEvent
	payload    core.NotificationPayload
	eventRead  bool
	fanned     bool
	claimed    bool
	completed  bool
	terminal   bool
	reason     string
	next       time.Time
	healthOK   bool
	healthTerm bool
}

func (s *workerStore) GetUnfannedNotificationEvent(context.Context) (core.NotificationEvent, error) {
	if s.event.ID == "" || s.eventRead {
		return core.NotificationEvent{}, core.ErrNotFound
	}
	s.eventRead = true
	return s.event, nil
}

func (s *workerStore) FanOutNotificationEvent(
	_ context.Context, _ string, payload core.NotificationPayload, _ time.Time,
) (int, error) {
	s.payload, s.fanned = payload, true
	return 0, nil
}

func (s *workerStore) ClaimNotificationDelivery(context.Context, core.NotificationLease, time.Time) (core.NotificationDelivery, error) {
	if s.claimed {
		return core.NotificationDelivery{}, core.ErrNotFound
	}
	s.claimed = true
	return s.delivery, nil
}

func (s *workerStore) CompleteNotificationDelivery(context.Context, string, string, time.Time) error {
	s.completed = true
	return nil
}

func (s *workerStore) RescheduleNotificationDelivery(_ context.Context, _, _, reason string, _, next time.Time, terminal bool) error {
	s.reason, s.next, s.terminal = reason, next, terminal
	return nil
}

func (s *workerStore) ListNotificationDeliveries(context.Context, string, *core.NotificationDeliveryCursor, int) ([]core.NotificationDelivery, error) {
	return nil, nil
}
func (s *workerStore) NotificationOutboxDepth(context.Context) (int64, error) { return 0, nil }
func (s *workerStore) RecordNotificationChannelResult(_ context.Context, _ string, success, terminal bool, _ int, _ time.Time) error {
	s.healthOK, s.healthTerm = success, terminal
	return nil
}

func (s *workerStore) PruneNotificationDeliveries(context.Context, time.Time, int) (int64, error) {
	return 0, nil
}

func (s *workerStore) DeleteTombstonedNotificationChannels(context.Context, time.Time, int) (int64, error) {
	return 0, nil
}

type workerChannel struct {
	err     error
	sends   int
	message core.RenderedNotification
	onSend  func()
}

func (c *workerChannel) Send(_ context.Context, message core.RenderedNotification) error {
	c.sends++
	c.message = message
	if c.onSend != nil {
		c.onSend()
	}
	return c.err
}
func (c *workerChannel) Probe(context.Context) error { return c.err }
func (c *workerChannel) Kind() core.NotificationKind { return core.NotificationKindWebhook }

type workerResolver struct{ channel *workerChannel }

func (r workerResolver) Channel(context.Context, string) (core.NotificationRegistration, core.NotificationChannel, error) {
	return core.NotificationRegistration{Kind: core.NotificationKindWebhook}, r.channel, nil
}

type disabledWorkerResolver struct{ channel *workerChannel }

func (r disabledWorkerResolver) Channel(context.Context, string) (core.NotificationRegistration, core.NotificationChannel, error) {
	return core.NotificationRegistration{}, r.channel, &core.NotificationError{
		Kind: core.NotificationRejected, Operation: "resolve", Retryable: false,
	}
}

type workerMetrics struct{ outcome string }

func (m *workerMetrics) ObserveNotificationDelivery(_, outcome string) { m.outcome = outcome }
func (m *workerMetrics) SetNotificationOutboxDepth(int64)              {}

func TestWorkerRecordsSuccessRetryAndTerminalFailure(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, outcome string
		sendErr       error
		attempts      int
		terminal      bool
		delay         time.Duration
	}{
		{name: "success", outcome: "sent", attempts: 1},
		{name: "retry", outcome: "retry", attempts: 3, delay: 20 * time.Second, sendErr: &core.NotificationError{Kind: core.NotificationUnavailable, Operation: "send", Retryable: true}},
		{name: "terminal", outcome: "failed", attempts: 1, terminal: true, sendErr: &core.NotificationError{Kind: core.NotificationRejected, Operation: "send"}},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			at := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
			store := &workerStore{delivery: workerTestDelivery(at, testCase.attempts)}
			metrics := &workerMetrics{}
			channel := &workerChannel{err: testCase.sendErr}
			worker := newTestWorker(t, at, store, channel, metrics)
			err := worker.runOnce(t.Context())
			if testCase.sendErr == nil && err != nil {
				t.Fatalf("runOnce: %v", err)
			}
			if testCase.sendErr != nil && err == nil {
				t.Fatal("runOnce succeeded after send failure")
			}
			if metrics.outcome != testCase.outcome || store.terminal != testCase.terminal {
				t.Fatalf("outcome = %q, terminal = %v", metrics.outcome, store.terminal)
			}
			if testCase.delay > 0 && !store.next.Equal(at.Add(testCase.delay)) {
				t.Fatalf("next = %v, want %v", store.next, at.Add(testCase.delay))
			}
			if testCase.sendErr == nil && (!store.completed || !store.healthOK) {
				t.Fatalf("completed = %v, healthOK = %v", store.completed, store.healthOK)
			}
			if channel.sends > 0 && channel.message.Payload.DeliveryID != store.delivery.ID {
				t.Fatalf("delivery id = %q, want %q", channel.message.Payload.DeliveryID, store.delivery.ID)
			}
		})
	}
}

func TestWorkerCompletesSendWhenChannelIsDisabledInFlight(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	store := &workerStore{delivery: workerTestDelivery(at, 1)}
	disabled := false
	channel := &workerChannel{onSend: func() { disabled = true }}
	worker := newTestWorker(t, at, store, channel, &workerMetrics{})
	if err := worker.runOnce(t.Context()); err != nil {
		t.Fatalf("runOnce: %v", err)
	}
	if !disabled || !store.completed || !store.healthOK {
		t.Fatalf("disabled=%v completed=%v healthOK=%v", disabled, store.completed, store.healthOK)
	}
}

func TestWorkerTreatsShutdownAsRetryable(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	store := &workerStore{delivery: workerTestDelivery(at, 1)}
	worker := newTestWorker(t, at, store, &workerChannel{err: context.Canceled}, &workerMetrics{})
	err := worker.runOnce(t.Context())
	if err == nil || store.terminal || store.healthTerm {
		t.Fatalf("err = %v, terminal = %v, health terminal = %v", err, store.terminal, store.healthTerm)
	}
}

func TestWorkerNeverSendsThroughDisabledChannel(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	store := &workerStore{delivery: workerTestDelivery(at, 1)}
	channel := &workerChannel{}
	worker := newTestWorker(t, at, store, channel, &workerMetrics{})
	worker.deps.Channels = disabledWorkerResolver{channel: channel}
	if err := worker.runOnce(t.Context()); err == nil || !store.terminal || channel.sends != 0 {
		t.Fatalf("runOnce error=%v terminal=%v sends=%d", err, store.terminal, channel.sends)
	}
}

func TestWorkerFansOutWhenUsernameEnrichmentIsUnavailable(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	store := &workerStore{claimed: true, event: core.NotificationEvent{
		ID: "00000000-0000-4000-8000-000000000031",
		Event: core.RequestEvent{
			Type: core.RequestEventCreated, RequestID: "00000000-0000-4000-8000-000000000032",
			RequesterID: "00000000-0000-4000-8000-000000000033", ActorID: "00000000-0000-4000-8000-000000000033",
			Kind: core.MediaKindMovie, Title: "Example", Status: core.RequestPending, At: at,
		},
	}}
	worker := newTestWorker(t, at, store, &workerChannel{}, &workerMetrics{})
	if err := worker.runOnce(t.Context()); err != nil || !store.fanned ||
		store.payload.RequesterUsername != "" || store.payload.ActorUsername != "" {
		t.Fatalf("runOnce error=%v fanned=%v payload=%+v", err, store.fanned, store.payload)
	}
}

func TestWorkerRunStartsNonFatallyAndStopsWithoutSleeping(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	store := &workerStore{claimed: true}
	ctx, cancel := context.WithCancel(t.Context())
	waited := false
	worker, err := NewWorker(WorkerConfig{Interval: time.Second, Retention: time.Hour}, WorkerDependencies{
		Events: store, Store: store, Reader: store, Maintenance: store, Channels: workerResolver{channel: &workerChannel{}},
		Accounts: producerAccounts{},
		Clock:    testutil.NewFakeClock(at), Metrics: &workerMetrics{}, Logger: slog.New(slog.DiscardHandler),
		Wait: func(context.Context, time.Duration) error { waited = true; cancel(); return context.Canceled },
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := worker.Run(ctx); err != nil || !waited {
		t.Fatalf("Run = %v, waited = %v", err, waited)
	}
}

func newTestWorker(t *testing.T, at time.Time, store *workerStore, channel *workerChannel, metrics *workerMetrics) *Worker {
	t.Helper()
	worker, err := NewWorker(WorkerConfig{Interval: time.Second, Retention: time.Hour, RetryBase: 5 * time.Second}, WorkerDependencies{
		Events: store, Store: store, Reader: store, Maintenance: store, Channels: workerResolver{channel: channel},
		Accounts: producerAccounts{},
		Clock:    testutil.NewFakeClock(at), Metrics: metrics, Logger: slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatal(err)
	}
	return worker
}

func workerTestDelivery(at time.Time, attempts int) core.NotificationDelivery {
	return core.NotificationDelivery{
		ID: "00000000-0000-4000-8000-000000000010", ChannelID: "00000000-0000-4000-8000-000000000011",
		ChannelKind: core.NotificationKindWebhook, EventType: core.RequestEventCreated,
		Payload: notificationTestPayload(), Status: core.NotificationPending, Attempts: attempts,
		LeaseToken: "00000000-0000-4000-8000-000000000012", CreatedAt: at, UpdatedAt: at,
	}
}
