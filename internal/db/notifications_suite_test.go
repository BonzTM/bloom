package db_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/BonzTM/bloom/internal/config"
	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/db"
	"github.com/BonzTM/bloom/internal/db/postgres"
)

type notificationStores struct {
	reader      core.NotificationChannelReader
	writer      core.NotificationChannelWriter
	events      core.NotificationEventStore
	deliveries  core.NotificationDeliveryStore
	recent      core.NotificationDeliveryReader
	maintenance core.NotificationMaintenanceStore
}

func runNotificationEngineTests(t *testing.T, pool *sql.DB, driver config.Driver, accounts core.AccountStore) {
	t.Helper()
	reader, writer, events, deliveries, recent, maintenance, err := db.NewNotificationStores(pool, driver)
	if err != nil {
		t.Fatalf("NewNotificationStores: %v", err)
	}
	stores := notificationStores{reader, writer, events, deliveries, recent, maintenance}
	t.Run("channels subscriptions and durable outbox", func(t *testing.T) {
		testNotificationOutbox(t, pool, driver, accounts, stores)
	})
	t.Run("expired attempt limit advances queue", func(t *testing.T) {
		testNotificationAttemptLimit(t, pool, driver, accounts, stores)
	})
	t.Run("disabling channel fails pending deliveries", func(t *testing.T) {
		testDisableNotificationChannel(t, pool, driver, accounts, stores)
	})
	t.Run("tombstoned channel is hidden and safely reaped", func(t *testing.T) {
		testTombstonedNotificationChannel(t, pool, driver, accounts, stores)
	})
	if driver == config.DriverPostgres {
		t.Run("claim serializes with tombstone reap", func(t *testing.T) {
			testPostgresClaimReapSerialization(t, pool, accounts, stores)
		})
	}
	t.Run("request mutations commit durable events", func(t *testing.T) {
		testRequestMutationEvents(t, pool, driver, accounts, stores.events)
	})
}

func testRequestMutationEvents(
	t *testing.T, pool *sql.DB, driver config.Driver, accounts core.AccountStore, events core.NotificationEventStore,
) {
	t.Helper()
	at := core.NormalizeTime(time.Date(2026, 9, 24, 15, 0, 0, 0, time.UTC))
	drainNotificationEvents(t, events, at)
	writer, accountID, profileID := notificationRequestWriter(t, pool, driver, accounts, at)
	testAvailableRequestEvents(t, writer, events, accountID, profileID, at)
	testDeclinedRequestEvents(t, writer, events, accountID, profileID, at.Add(time.Minute))
	testFailedRequestEvents(t, writer, events, accountID, profileID, at.Add(2*time.Minute))
}

func notificationRequestWriter(
	t *testing.T, pool *sql.DB, driver config.Driver, accounts core.AccountStore, at time.Time,
) (core.RequestWriter, string, string) {
	t.Helper()
	account := requestTestAccount(t, accounts, at, "event-requester")
	_, profileWriter, err := db.NewRequestProfileStores(pool, driver)
	if err != nil {
		t.Fatalf("NewRequestProfileStores: %v", err)
	}
	profile := core.RequestProfile{
		ID: mustID(t), Name: "Event profile " + mustID(t), Kinds: []core.MediaKind{core.MediaKindMovie},
		DownloadManagerKind: "placeholder", DownloadManagerInstance: "future", QualityProfile: "Any",
		RootFolder: "/media", Tags: []string{}, CreatedAt: at, UpdatedAt: at,
	}
	if createErr := profileWriter.CreateRequestProfile(t.Context(), profile); createErr != nil {
		t.Fatalf("CreateRequestProfile: %v", createErr)
	}
	_, writer, _, _, _, err := db.NewRequestStores(pool, driver)
	if err != nil {
		t.Fatalf("NewRequestStores: %v", err)
	}
	return writer, account.ID, profile.ID
}

func testAvailableRequestEvents(
	t *testing.T, writer core.RequestWriter, events core.NotificationEventStore, accountID, profileID string, at time.Time,
) {
	t.Helper()
	request := requestFixture(t, accountID, profileID, "992", at)
	createAndAssertRequestEvent(t, writer, events, request, at, core.RequestEventCreated)
	transitionAndAssertRequestEvent(t, writer, events, request.ID, core.RequestPending, core.RequestApproved, accountID, "", at, core.RequestEventApproved)
	dispatch := requireRequestDispatchWriter(t, writer)
	lease := requestDispatchLease(mustID(t), at)
	if _, err := dispatch.ClaimRequestDispatch(t.Context(), request.ID, notificationDispatchSnapshot(), lease, at); err != nil {
		t.Fatalf("ClaimRequestDispatch: %v", err)
	}
	stamp := at.Add(time.Second)
	if _, err := dispatch.RecordRequestDispatch(t.Context(), request.ID, lease.Token, "992", stamp); err != nil {
		t.Fatalf("RecordRequestDispatch: %v", err)
	}
	assertNextRequestEvent(t, events, request.ID, core.RequestEventDispatched, stamp)
	transitionAndAssertRequestEvent(t, writer, events, request.ID, core.RequestProcessing, core.RequestAvailable, "", "", at.Add(2*time.Second), core.RequestEventAvailable)
}

func testDeclinedRequestEvents(
	t *testing.T, writer core.RequestWriter, events core.NotificationEventStore, accountID, profileID string, at time.Time,
) {
	t.Helper()
	request := requestFixture(t, accountID, profileID, "993", at)
	createAndAssertRequestEvent(t, writer, events, request, at, core.RequestEventCreated)
	transitionAndAssertRequestEvent(t, writer, events, request.ID, core.RequestPending, core.RequestDeclined, accountID, "no", at, core.RequestEventDeclined)
}

func testFailedRequestEvents(
	t *testing.T, writer core.RequestWriter, events core.NotificationEventStore, accountID, profileID string, at time.Time,
) {
	t.Helper()
	request := requestFixture(t, accountID, profileID, "994", at)
	createAndAssertRequestEvent(t, writer, events, request, at, core.RequestEventCreated)
	transitionAndAssertRequestEvent(t, writer, events, request.ID, core.RequestPending, core.RequestApproved, accountID, "", at, core.RequestEventApproved)
	dispatch := requireRequestDispatchWriter(t, writer)
	lease := requestDispatchLease(mustID(t), at)
	if _, err := dispatch.ClaimRequestDispatch(t.Context(), request.ID, notificationDispatchSnapshot(), lease, at); err != nil {
		t.Fatalf("ClaimRequestDispatch: %v", err)
	}
	stamp := at.Add(time.Second)
	if _, err := dispatch.FailRequestDispatch(t.Context(), request.ID, lease.Token, "failed", stamp); err != nil {
		t.Fatalf("FailRequestDispatch: %v", err)
	}
	assertNextRequestEvent(t, events, request.ID, core.RequestEventFailed, stamp)
}

func createAndAssertRequestEvent(
	t *testing.T, writer core.RequestWriter, events core.NotificationEventStore,
	request core.MediaRequest, at time.Time, eventType core.RequestEventType,
) {
	t.Helper()
	if err := writer.CreateRequest(t.Context(), request, at, true); err != nil {
		t.Fatalf("CreateRequest: %v", err)
	}
	assertNextRequestEvent(t, events, request.ID, eventType, at)
}

func transitionAndAssertRequestEvent(
	t *testing.T, writer core.RequestWriter, events core.NotificationEventStore, id string,
	from, to core.RequestStatus, actorID, reason string, at time.Time, eventType core.RequestEventType,
) {
	t.Helper()
	if _, err := writer.TransitionRequest(t.Context(), id, from, to, actorID, reason, at); err != nil {
		t.Fatalf("TransitionRequest: %v", err)
	}
	assertNextRequestEvent(t, events, id, eventType, at)
}

func assertNextRequestEvent(
	t *testing.T, store core.NotificationEventStore, requestID string, eventType core.RequestEventType, at time.Time,
) {
	t.Helper()
	event, err := store.GetUnfannedNotificationEvent(t.Context())
	if err != nil || event.Event.RequestID != requestID || event.Event.Type != eventType {
		t.Fatalf("GetUnfannedNotificationEvent = %+v, %v", event, err)
	}
	payload := notificationPayload(event.Event, event.Event.At)
	if _, err := store.FanOutNotificationEvent(t.Context(), event.ID, payload, at); err != nil {
		t.Fatalf("FanOutNotificationEvent: %v", err)
	}
}

func notificationDispatchSnapshot() core.RequestDispatchSnapshot {
	return core.RequestDispatchSnapshot{
		DownloadManagerID: "ffffffff-ffff-4fff-8fff-ffffffffffff",
		QualityProfile:    "1", RootFolder: "/media", Tags: []string{},
	}
}

func testNotificationOutbox(
	t *testing.T, pool *sql.DB, driver config.Driver, accounts core.AccountStore, stores notificationStores,
) {
	t.Helper()
	ctx := context.Background()
	at := core.NormalizeTime(time.Date(2026, 9, 24, 12, 0, 0, 123456789, time.UTC))
	drainNotificationEvents(t, stores.events, at)
	enabled := notificationRecord(t, "Enabled webhook", true, core.RequestEventCreated, at)
	disabled := notificationRecord(t, "Disabled webhook", false, core.RequestEventCreated, at)
	other := notificationRecord(t, "Other event", true, core.RequestEventApproved, at)
	for _, record := range []core.NotificationRecord{enabled, disabled, other} {
		if err := stores.writer.CreateNotificationChannel(ctx, record); err != nil {
			t.Fatalf("CreateNotificationChannel: %v", err)
		}
	}
	got, err := stores.reader.GetNotificationChannel(ctx, enabled.ID)
	if err != nil || string(got.SecretCiphertext) != "encrypted" || len(got.Subscriptions) != 1 {
		t.Fatalf("GetNotificationChannel = %+v, %v", got, err)
	}
	event := createNotificationRequestEvent(t, pool, driver, accounts, at)
	payload := notificationPayload(event.Event, at)
	count, err := stores.events.FanOutNotificationEvent(ctx, event.ID, payload, at)
	if err != nil || count != 1 {
		t.Fatalf("EnqueueNotificationEvent = %d, %v", count, err)
	}
	if _, err := stores.events.FanOutNotificationEvent(ctx, event.ID, payload, at); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("repeat FanOutNotificationEvent error = %v, want ErrNotFound", err)
	}
	delivery := claimNotificationRace(t, stores.deliveries, at)
	if delivery.ChannelID != enabled.ID || delivery.Attempts != 1 || delivery.Payload.Title != payload.Title {
		t.Fatalf("claimed delivery = %+v", delivery)
	}
	if err := stores.deliveries.RescheduleNotificationDelivery(
		ctx, delivery.ID, delivery.LeaseToken, "delivery rejected", at, at.Add(time.Minute), true,
	); err != nil {
		t.Fatalf("RescheduleNotificationDelivery: %v", err)
	}
	testNotificationHealth(t, stores, enabled.ID, at)
	testNotificationRetention(t, stores, enabled.ID, at)
	for _, record := range []core.NotificationRecord{enabled, disabled, other} {
		if err := stores.writer.DeleteNotificationChannel(ctx, record.ID, at.Add(time.Hour)); err != nil {
			t.Fatalf("DeleteNotificationChannel: %v", err)
		}
	}
}

func testNotificationAttemptLimit(
	t *testing.T, pool *sql.DB, driver config.Driver, accounts core.AccountStore, stores notificationStores,
) {
	t.Helper()
	at := core.NormalizeTime(time.Date(2026, 9, 24, 13, 0, 0, 0, time.UTC))
	drainNotificationEvents(t, stores.events, at)
	channel := notificationRecord(t, "Attempt limit", true, core.RequestEventCreated, at)
	if err := stores.writer.CreateNotificationChannel(t.Context(), channel); err != nil {
		t.Fatalf("CreateNotificationChannel: %v", err)
	}
	createAndFanOutNotificationEvent(t, pool, driver, accounts, stores.events, at)
	createAndFanOutNotificationEvent(t, pool, driver, accounts, stores.events, at.Add(time.Second))
	var exhausted core.NotificationDelivery
	for attempt := 1; attempt <= core.MaxNotificationAttempts; attempt++ {
		exhausted = claimNotification(t, stores.deliveries, at, attempt)
		if attempt < core.MaxNotificationAttempts {
			if err := stores.deliveries.RescheduleNotificationDelivery(
				t.Context(), exhausted.ID, exhausted.LeaseToken, "retry", at, at, false,
			); err != nil {
				t.Fatalf("RescheduleNotificationDelivery: %v", err)
			}
		}
	}
	next := claimNotification(t, stores.deliveries, at.Add(2*time.Minute), 1)
	if next.ID == exhausted.ID {
		t.Fatalf("next delivery = %s after exhausted %s", next.ID, exhausted.ID)
	}
	page, err := stores.recent.ListNotificationDeliveries(t.Context(), channel.ID, nil, 10)
	if err != nil {
		t.Fatalf("ListNotificationDeliveries: %v", err)
	}
	assertAttemptLimitFailure(t, page, exhausted.ID)
	if err := stores.writer.DeleteNotificationChannel(t.Context(), channel.ID, at.Add(time.Hour)); err != nil {
		t.Fatalf("DeleteNotificationChannel: %v", err)
	}
}

func claimNotification(
	t *testing.T, store core.NotificationDeliveryStore, at time.Time, wantAttempts int,
) core.NotificationDelivery {
	t.Helper()
	token := mustID(t)
	delivery, err := store.ClaimNotificationDelivery(t.Context(), core.NotificationLease{
		Token: token, ExpiresAt: at.Add(time.Minute),
	}, at)
	if err != nil || delivery.Attempts != wantAttempts {
		t.Fatalf("ClaimNotificationDelivery = %+v, %v; want attempts %d", delivery, err, wantAttempts)
	}
	return delivery
}

func assertAttemptLimitFailure(t *testing.T, deliveries []core.NotificationDelivery, id string) {
	t.Helper()
	for _, delivery := range deliveries {
		if delivery.ID == id {
			if delivery.Status != core.NotificationFailed || delivery.LastError != "attempt limit reached" {
				t.Fatalf("exhausted delivery = %+v", delivery)
			}
			return
		}
	}
	t.Fatalf("exhausted delivery %s not found", id)
}

func testDisableNotificationChannel(
	t *testing.T, pool *sql.DB, driver config.Driver, accounts core.AccountStore, stores notificationStores,
) {
	t.Helper()
	at := core.NormalizeTime(time.Date(2026, 9, 24, 14, 0, 0, 0, time.UTC))
	drainNotificationEvents(t, stores.events, at)
	channel := notificationRecord(t, "Disable pending", true, core.RequestEventCreated, at)
	if err := stores.writer.CreateNotificationChannel(t.Context(), channel); err != nil {
		t.Fatalf("CreateNotificationChannel: %v", err)
	}
	createAndFanOutNotificationEvent(t, pool, driver, accounts, stores.events, at)
	leased := claimNotification(t, stores.deliveries, at, 1)
	createAndFanOutNotificationEvent(t, pool, driver, accounts, stores.events, at.Add(time.Second))
	channel.Enabled, channel.UpdatedAt = false, at.Add(30*time.Second)
	if err := stores.writer.UpdateNotificationChannel(t.Context(), channel); err != nil {
		t.Fatalf("UpdateNotificationChannel: %v", err)
	}
	page, err := stores.recent.ListNotificationDeliveries(t.Context(), channel.ID, nil, 10)
	if err != nil || len(page) != 2 {
		t.Fatalf("disabled deliveries = %+v, %v", page, err)
	}
	assertDisabledDeliveryStates(t, page, leased.ID)
	completedAt := at.Add(40 * time.Second)
	if err := stores.deliveries.CompleteNotificationDelivery(t.Context(), leased.ID, leased.LeaseToken, completedAt); err != nil {
		t.Fatalf("CompleteNotificationDelivery: %v", err)
	}
	if err := stores.maintenance.RecordNotificationChannelResult(t.Context(), channel.ID, true, false, 3, completedAt); err != nil {
		t.Fatalf("RecordNotificationChannelResult: %v", err)
	}
}

func assertDisabledDeliveryStates(t *testing.T, deliveries []core.NotificationDelivery, leasedID string) {
	t.Helper()
	for _, delivery := range deliveries {
		if delivery.ID == leasedID {
			if delivery.Status != core.NotificationPending || delivery.LeaseToken == "" {
				t.Fatalf("leased delivery changed during disable: %+v", delivery)
			}
			continue
		}
		if delivery.Status != core.NotificationFailed || delivery.LastError != "channel disabled" {
			t.Fatalf("unleased delivery not failed during disable: %+v", delivery)
		}
	}
}

func testTombstonedNotificationChannel(
	t *testing.T, pool *sql.DB, driver config.Driver, accounts core.AccountStore, stores notificationStores,
) {
	t.Helper()
	at := core.NormalizeTime(time.Date(2026, 9, 24, 16, 0, 0, 0, time.UTC))
	drainNotificationEvents(t, stores.events, at)
	if _, err := stores.maintenance.DeleteTombstonedNotificationChannels(t.Context(), at, 1000); err != nil {
		t.Fatalf("clear prior tombstones: %v", err)
	}
	channel := notificationRecord(t, "Tombstoned channel", true, core.RequestEventCreated, at)
	if err := stores.writer.CreateNotificationChannel(t.Context(), channel); err != nil {
		t.Fatalf("CreateNotificationChannel: %v", err)
	}
	createAndFanOutNotificationEvent(t, pool, driver, accounts, stores.events, at)
	leased := claimNotification(t, stores.deliveries, at, 1)
	deletedAt := at.Add(10 * time.Second)
	if err := stores.writer.DeleteNotificationChannel(t.Context(), channel.ID, deletedAt); err != nil {
		t.Fatalf("DeleteNotificationChannel: %v", err)
	}
	assertTombstoneInvisible(t, stores, channel)
	event := createNotificationRequestEvent(t, pool, driver, accounts, at.Add(20*time.Second))
	count, err := stores.events.FanOutNotificationEvent(t.Context(), event.ID, notificationPayload(event.Event, event.Event.At), event.Event.At)
	if err != nil || count != 0 {
		t.Fatalf("fan-out after tombstone = %d, %v", count, err)
	}
	deleted, err := stores.maintenance.DeleteTombstonedNotificationChannels(t.Context(), at.Add(30*time.Second), 1)
	if err != nil || deleted != 0 || notificationChannelRowCount(t, pool, driver, channel.ID) != 1 {
		t.Fatalf("live-lease maintenance = %d, %v", deleted, err)
	}
	deleted, err = stores.maintenance.DeleteTombstonedNotificationChannels(t.Context(), leased.LeaseExpiresAt.Add(time.Second), 1)
	if err != nil || deleted != 1 || notificationChannelRowCount(t, pool, driver, channel.ID) != 0 {
		t.Fatalf("expired-lease maintenance = %d, %v", deleted, err)
	}
}

func testPostgresClaimReapSerialization(
	t *testing.T, pool *sql.DB, accounts core.AccountStore, stores notificationStores,
) {
	t.Helper()
	at := core.NormalizeTime(time.Date(2026, 9, 24, 17, 0, 0, 0, time.UTC))
	channel := notificationRecord(t, "Concurrent reap", true, core.RequestEventCreated, at)
	if err := stores.writer.CreateNotificationChannel(t.Context(), channel); err != nil {
		t.Fatalf("CreateNotificationChannel: %v", err)
	}
	createAndFanOutNotificationEvent(t, pool, config.DriverPostgres, accounts, stores.events, at)
	tx, deliveryID, lease := beginPostgresNotificationClaim(t, pool, channel.ID, at)
	assertPostgresChannelLocked(t, pool, channel.ID)
	started := make(chan struct{})
	done := make(chan reapResult, 1)
	go func() {
		close(started)
		deleteErr := stores.writer.DeleteNotificationChannel(context.Background(), channel.ID, at.Add(time.Second))
		deleted, reapErr := stores.maintenance.DeleteTombstonedNotificationChannels(context.Background(), at.Add(2*time.Second), 1)
		done <- reapResult{deleted: deleted, err: errors.Join(deleteErr, reapErr)}
	}()
	<-started
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit claim: %v", err)
	}
	result := <-done
	if result.err != nil || result.deleted != 0 || notificationOutboxRowCount(t, pool, deliveryID) != 1 {
		t.Fatalf("concurrent reap = %d, %v; delivery rows = %d", result.deleted, result.err, notificationOutboxRowCount(t, pool, deliveryID))
	}
	deleted, err := stores.maintenance.DeleteTombstonedNotificationChannels(t.Context(), lease.ExpiresAt.Add(time.Second), 1)
	if err != nil || deleted != 1 {
		t.Fatalf("expired lease reap = %d, %v", deleted, err)
	}
}

type reapResult struct {
	deleted int64
	err     error
}

func beginPostgresNotificationClaim(
	t *testing.T, pool *sql.DB, channelID string, at time.Time,
) (*sql.Tx, string, core.NotificationLease) {
	t.Helper()
	tx, err := pool.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatalf("begin claim: %v", err)
	}
	t.Cleanup(func() { rollbackPostgresTestTx(t, tx) })
	q := postgres.New(tx)
	lockedID, err := q.LockNotificationChannelForClaim(t.Context(), at)
	if err != nil || lockedID != channelID {
		t.Fatalf("LockNotificationChannelForClaim = %q, %v", lockedID, err)
	}
	lease := core.NotificationLease{Token: mustID(t), ExpiresAt: at.Add(time.Minute)}
	row, err := q.ClaimNotificationOutbox(t.Context(), postgres.ClaimNotificationOutboxParams{
		LeaseToken: lease.Token, LeaseExpiresAt: sql.NullTime{Time: lease.ExpiresAt, Valid: true},
		UpdatedAt: at, ChannelID: channelID, DueAt: at,
	})
	if err != nil {
		t.Fatalf("ClaimNotificationOutbox: %v", err)
	}
	return tx, row.ID, lease
}

func assertPostgresChannelLocked(t *testing.T, pool *sql.DB, channelID string) {
	t.Helper()
	tx, err := pool.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatalf("begin lock probe: %v", err)
	}
	defer rollbackPostgresTestTx(t, tx)
	_, err = tx.ExecContext(t.Context(), "SELECT id FROM notification_channels WHERE id = $1 FOR UPDATE NOWAIT", channelID)
	var postgresErr *pgconn.PgError
	if !errors.As(err, &postgresErr) || postgresErr.Code != "55P03" {
		t.Fatalf("channel lock probe error = %v, want lock_not_available", err)
	}
}

func rollbackPostgresTestTx(t *testing.T, tx *sql.Tx) {
	t.Helper()
	if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
		t.Errorf("roll back PostgreSQL test transaction: %v", err)
	}
}

func notificationOutboxRowCount(t *testing.T, pool *sql.DB, id string) int {
	t.Helper()
	var count int
	if err := pool.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM notification_outbox WHERE id = $1", id).Scan(&count); err != nil {
		t.Fatalf("count notification delivery: %v", err)
	}
	return count
}

func assertTombstoneInvisible(t *testing.T, stores notificationStores, channel core.NotificationRecord) {
	t.Helper()
	if _, err := stores.reader.GetNotificationChannel(t.Context(), channel.ID); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("GetNotificationChannel tombstone error = %v", err)
	}
	values, err := stores.reader.ListNotificationChannels(t.Context(), "", 101)
	if err != nil {
		t.Fatalf("ListNotificationChannels: %v", err)
	}
	for _, value := range values {
		if value.ID == channel.ID {
			t.Fatalf("tombstoned channel listed: %+v", value)
		}
	}
	channel.UpdatedAt = channel.UpdatedAt.Add(time.Minute)
	if err := stores.writer.UpdateNotificationChannel(t.Context(), channel); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("UpdateNotificationChannel tombstone error = %v", err)
	}
}

func notificationChannelRowCount(t *testing.T, pool *sql.DB, driver config.Driver, id string) int {
	t.Helper()
	query := "SELECT COUNT(*) FROM notification_channels WHERE id = ?"
	if driver == config.DriverPostgres {
		query = "SELECT COUNT(*) FROM notification_channels WHERE id = $1"
	}
	var count int
	if err := pool.QueryRowContext(t.Context(), query, id).Scan(&count); err != nil {
		t.Fatalf("count notification channel: %v", err)
	}
	return count
}

func createAndFanOutNotificationEvent(
	t *testing.T, pool *sql.DB, driver config.Driver, accounts core.AccountStore,
	store core.NotificationEventStore, at time.Time,
) {
	t.Helper()
	event := createNotificationRequestEvent(t, pool, driver, accounts, at)
	if _, err := store.FanOutNotificationEvent(t.Context(), event.ID, notificationPayload(event.Event, at), at); err != nil {
		t.Fatalf("FanOutNotificationEvent: %v", err)
	}
}

func drainNotificationEvents(t *testing.T, store core.NotificationEventStore, at time.Time) {
	t.Helper()
	for range 200 {
		event, err := store.GetUnfannedNotificationEvent(t.Context())
		if errors.Is(err, core.ErrNotFound) {
			return
		}
		if err != nil {
			t.Fatalf("GetUnfannedNotificationEvent: %v", err)
		}
		if _, err := store.FanOutNotificationEvent(t.Context(), event.ID, notificationPayload(event.Event, at), at); err != nil {
			t.Fatalf("FanOutNotificationEvent: %v", err)
		}
	}
	t.Fatal("notification event drain exceeded bound")
}

func createNotificationRequestEvent(
	t *testing.T, pool *sql.DB, driver config.Driver, accounts core.AccountStore, at time.Time,
) core.NotificationEvent {
	t.Helper()
	account := requestTestAccount(t, accounts, at, "notification-requester")
	_, profileWriter, err := db.NewRequestProfileStores(pool, driver)
	if err != nil {
		t.Fatalf("NewRequestProfileStores: %v", err)
	}
	profile := core.RequestProfile{
		ID: mustID(t), Name: "Notification " + mustID(t), Kinds: []core.MediaKind{core.MediaKindMovie},
		DownloadManagerKind: "placeholder", DownloadManagerInstance: "future", QualityProfile: "Any",
		RootFolder: "/media", Tags: []string{}, CreatedAt: at, UpdatedAt: at,
	}
	if createErr := profileWriter.CreateRequestProfile(t.Context(), profile); createErr != nil {
		t.Fatalf("CreateRequestProfile: %v", createErr)
	}
	_, requestWriter, _, _, _, err := db.NewRequestStores(pool, driver)
	if err != nil {
		t.Fatalf("NewRequestStores: %v", err)
	}
	request := requestFixture(t, account.ID, profile.ID, "991", at)
	if createErr := requestWriter.CreateRequest(t.Context(), request, at, true); createErr != nil {
		t.Fatalf("CreateRequest: %v", createErr)
	}
	event, err := notificationEventStore(t, pool, driver).GetUnfannedNotificationEvent(t.Context())
	if err != nil {
		t.Fatalf("GetUnfannedNotificationEvent: %v", err)
	}
	return event
}

func notificationEventStore(t *testing.T, pool *sql.DB, driver config.Driver) core.NotificationEventStore {
	t.Helper()
	_, _, events, _, _, _, err := db.NewNotificationStores(pool, driver)
	if err != nil {
		t.Fatalf("NewNotificationStores: %v", err)
	}
	return events
}

func claimNotificationRace(t *testing.T, store core.NotificationDeliveryStore, at time.Time) core.NotificationDelivery {
	t.Helper()
	start := make(chan struct{})
	results := make(chan core.NotificationDelivery, 2)
	errorsFound := make(chan error, 2)
	for range 2 {
		go func() {
			<-start
			token, err := core.NewID()
			if err != nil {
				errorsFound <- err
				return
			}
			value, err := store.ClaimNotificationDelivery(context.Background(), core.NotificationLease{
				Token: token, ExpiresAt: at.Add(time.Minute),
			}, at)
			if err != nil {
				errorsFound <- err
				return
			}
			results <- value
		}()
	}
	close(start)
	var winner core.NotificationDelivery
	wins, misses := 0, 0
	for range 2 {
		select {
		case winner = <-results:
			wins++
		case err := <-errorsFound:
			if !errors.Is(err, core.ErrNotFound) {
				t.Fatalf("ClaimNotificationDelivery: %v", err)
			}
			misses++
		}
	}
	if wins != 1 || misses != 1 {
		t.Fatalf("claim results: wins=%d misses=%d", wins, misses)
	}
	return winner
}

func testNotificationHealth(t *testing.T, stores notificationStores, channelID string, at time.Time) {
	t.Helper()
	for offset := range 3 {
		stamp := at.Add(time.Duration(offset+1) * time.Minute)
		if err := stores.maintenance.RecordNotificationChannelResult(t.Context(), channelID, false, true, 3, stamp); err != nil {
			t.Fatalf("RecordNotificationChannelResult failure: %v", err)
		}
	}
	degraded, err := stores.reader.GetNotificationChannel(t.Context(), channelID)
	if err != nil || degraded.DegradedAt == nil || degraded.ConsecutiveFailures != 3 {
		t.Fatalf("degraded channel = %+v, %v", degraded, err)
	}
	if recoveryErr := stores.maintenance.RecordNotificationChannelResult(t.Context(), channelID, true, false, 3, at.Add(5*time.Minute)); recoveryErr != nil {
		t.Fatalf("RecordNotificationChannelResult success: %v", recoveryErr)
	}
	recovered, err := stores.reader.GetNotificationChannel(t.Context(), channelID)
	if err != nil || recovered.DegradedAt != nil || recovered.ConsecutiveFailures != 0 {
		t.Fatalf("recovered channel = %+v, %v", recovered, err)
	}
}

func testNotificationRetention(t *testing.T, stores notificationStores, channelID string, at time.Time) {
	t.Helper()
	page, err := stores.recent.ListNotificationDeliveries(t.Context(), channelID, nil, 10)
	if err != nil || len(page) != 1 || page[0].Status != core.NotificationFailed {
		t.Fatalf("ListNotificationDeliveries = %+v, %v", page, err)
	}
	deleted, err := stores.maintenance.PruneNotificationDeliveries(t.Context(), at.Add(time.Second), 1)
	if err != nil || deleted != 1 {
		t.Fatalf("PruneNotificationDeliveries = %d, %v", deleted, err)
	}
	depth, err := stores.recent.NotificationOutboxDepth(t.Context())
	if err != nil || depth != 0 {
		t.Fatalf("NotificationOutboxDepth = %d, %v", depth, err)
	}
}

func notificationRecord(
	t *testing.T, name string, enabled bool, event core.RequestEventType, at time.Time,
) core.NotificationRecord {
	t.Helper()
	return core.NotificationRecord{
		NotificationRegistration: core.NotificationRegistration{
			ID: mustID(t), Kind: core.NotificationKindWebhook, Name: name,
			Subscriptions: []core.RequestEventType{event}, Enabled: enabled, SecretSet: true,
			CreatedAt: at, UpdatedAt: at,
		},
		SecretCiphertext: []byte("encrypted"), KeyID: "key-1",
	}
}

func notificationPayload(event core.RequestEvent, at time.Time) core.NotificationPayload {
	return core.NotificationPayload{
		EventType: event.Type, RequestID: event.RequestID,
		Title: event.Title, Kind: event.Kind, Status: event.Status,
		RequesterUsername: "requester", ActorUsername: "requester", OccurredAt: at,
	}
}
