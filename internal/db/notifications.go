package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/BonzTM/bloom/internal/config"
	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/db/postgres"
	"github.com/BonzTM/bloom/internal/db/sqlite"
)

const (
	maxNotificationPageSize        = 101
	maxNotificationMaintenanceSize = 1000
)

type notifications struct {
	pool     *sql.DB
	driver   config.Driver
	sqlite   *sqlite.Queries
	postgres *postgres.Queries
}

var (
	_ core.NotificationChannelReader    = (*notifications)(nil)
	_ core.NotificationChannelWriter    = (*notifications)(nil)
	_ core.NotificationEventStore       = (*notifications)(nil)
	_ core.NotificationDeliveryStore    = (*notifications)(nil)
	_ core.NotificationDeliveryReader   = (*notifications)(nil)
	_ core.NotificationMaintenanceStore = (*notifications)(nil)
)

// NewNotificationStores returns the notification persistence boundaries.
func NewNotificationStores(pool *sql.DB, driver config.Driver) (
	core.NotificationChannelReader, core.NotificationChannelWriter, core.NotificationEventStore,
	core.NotificationDeliveryStore, core.NotificationDeliveryReader, core.NotificationMaintenanceStore, error,
) {
	if pool == nil {
		return nil, nil, nil, nil, nil, nil, core.ErrInvalidArgument
	}
	store := &notifications{pool: pool, driver: driver}
	switch driver {
	case config.DriverSQLite:
		store.sqlite = sqlite.New(pool)
	case config.DriverPostgres:
		store.postgres = postgres.New(pool)
	default:
		return nil, nil, nil, nil, nil, nil, fmt.Errorf("unsupported database driver %q", driver)
	}
	return store, store, store, store, store, store, nil
}

func (s *notifications) CreateNotificationChannel(ctx context.Context, value core.NotificationRecord) (retErr error) {
	if err := validateNotificationRecord(value); err != nil {
		return err
	}
	settings, err := marshalNotificationSettings(value.Settings)
	if err != nil {
		return err
	}
	tx, err := s.pool.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin notification channel create: %w", err)
	}
	defer rollbackNotification(tx, &retErr)
	if err := s.createChannel(ctx, tx, value, settings); err != nil {
		return err
	}
	if err := s.replaceSubscriptions(ctx, tx, value.ID, value.Subscriptions, false); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit notification channel create: %w", err)
	}
	return nil
}

func validateNotificationRecord(value core.NotificationRecord) error {
	if err := core.ValidateNotificationRegistration(value.NotificationRegistration); err != nil {
		return err
	}
	if len(value.SecretCiphertext) == 0 || value.KeyID == "" || value.ConsecutiveFailures > math.MaxInt32 {
		return core.ErrInvalidArgument
	}
	return nil
}

func marshalNotificationSettings(value core.NotificationSettings) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("encode notification settings: %w", err)
	}
	if len(encoded) > core.MaxNotificationSettingsBytes {
		return "", core.ErrInvalidArgument
	}
	return string(encoded), nil
}

func (s *notifications) createChannel(ctx context.Context, tx *sql.Tx, value core.NotificationRecord, settings string) error {
	var err error
	if s.sqlite != nil {
		err = sqlite.New(tx).CreateNotificationChannel(ctx, sqlite.CreateNotificationChannelParams{
			ID: value.ID, Kind: string(value.Kind), Name: value.Name, NameKey: core.NotificationNameKey(value.Name),
			Target: value.Target, SettingsJson: settings, SecretCiphertext: value.SecretCiphertext,
			KeyID: value.KeyID, Enabled: boolToInt64(value.Enabled), DegradedAt: sqliteNullableTime(value.DegradedAt),
			ConsecutiveFailures: int64(value.ConsecutiveFailures), CreatedAt: formatSQLiteTime(value.CreatedAt),
			UpdatedAt: formatSQLiteTime(value.UpdatedAt),
		})
	} else {
		err = postgres.New(tx).CreateNotificationChannel(ctx, postgres.CreateNotificationChannelParams{
			ID: value.ID, Kind: string(value.Kind), Name: value.Name, NameKey: core.NotificationNameKey(value.Name),
			Target: value.Target, SettingsJson: settings, SecretCiphertext: value.SecretCiphertext,
			KeyID: value.KeyID, Enabled: value.Enabled, DegradedAt: postgresNullableTime(value.DegradedAt),
			ConsecutiveFailures: int32(value.ConsecutiveFailures), //nolint:gosec // validated against MaxInt32 above.
			CreatedAt:           core.NormalizeTime(value.CreatedAt),
			UpdatedAt:           core.NormalizeTime(value.UpdatedAt),
		})
	}
	if isUniqueError(err) {
		return fmt.Errorf("create notification channel: %w", core.ErrAlreadyExists)
	}
	if err != nil {
		return fmt.Errorf("create notification channel: %w", err)
	}
	return nil
}

func (s *notifications) replaceSubscriptions(
	ctx context.Context, tx *sql.Tx, id string, values []core.RequestEventType, remove bool,
) error {
	if s.sqlite != nil {
		queries := sqlite.New(tx)
		if remove {
			if err := queries.DeleteNotificationSubscriptions(ctx, id); err != nil {
				return fmt.Errorf("delete notification subscriptions: %w", err)
			}
		}
		for _, value := range core.SortedNotificationSubscriptions(values) {
			if err := queries.AddNotificationSubscription(ctx, sqlite.AddNotificationSubscriptionParams{ChannelID: id, EventType: string(value)}); err != nil {
				return fmt.Errorf("add notification subscription: %w", err)
			}
		}
		return nil
	}
	queries := postgres.New(tx)
	if remove {
		if err := queries.DeleteNotificationSubscriptions(ctx, id); err != nil {
			return fmt.Errorf("delete notification subscriptions: %w", err)
		}
	}
	for _, value := range core.SortedNotificationSubscriptions(values) {
		if err := queries.AddNotificationSubscription(ctx, postgres.AddNotificationSubscriptionParams{ChannelID: id, EventType: string(value)}); err != nil {
			return fmt.Errorf("add notification subscription: %w", err)
		}
	}
	return nil
}

func (s *notifications) GetNotificationChannel(ctx context.Context, id string) (core.NotificationRecord, error) {
	if !core.ValidID(id) {
		return core.NotificationRecord{}, core.ErrInvalidArgument
	}
	if s.sqlite != nil {
		row, err := s.sqlite.GetNotificationChannel(ctx, id)
		if err != nil {
			return core.NotificationRecord{}, mapNotFound("get notification channel", err)
		}
		return s.mapSQLiteChannel(ctx, row)
	}
	row, err := s.postgres.GetNotificationChannel(ctx, id)
	if err != nil {
		return core.NotificationRecord{}, mapNotFound("get notification channel", err)
	}
	return s.mapPostgresChannel(ctx, row)
}

func (s *notifications) ListNotificationChannels(ctx context.Context, after string, size int) ([]core.NotificationRegistration, error) {
	if size < 1 || size > maxNotificationPageSize {
		return nil, core.ErrInvalidArgument
	}
	if s.sqlite != nil {
		rows, err := s.sqlite.ListNotificationChannels(ctx, sqlite.ListNotificationChannelsParams{AfterNameKey: after, PageSize: int64(size)})
		if err != nil {
			return nil, fmt.Errorf("list notification channels: %w", err)
		}
		result := make([]core.NotificationRegistration, 0, len(rows))
		for _, row := range rows {
			value, mapErr := s.mapSQLiteListChannel(ctx, row)
			if mapErr != nil {
				return nil, mapErr
			}
			result = append(result, value)
		}
		return result, nil
	}
	rows, err := s.postgres.ListNotificationChannels(ctx, postgres.ListNotificationChannelsParams{AfterNameKey: after, PageSize: int32(size)})
	if err != nil {
		return nil, fmt.Errorf("list notification channels: %w", err)
	}
	result := make([]core.NotificationRegistration, 0, len(rows))
	for _, row := range rows {
		value, mapErr := s.mapPostgresListChannel(ctx, row)
		if mapErr != nil {
			return nil, mapErr
		}
		result = append(result, value)
	}
	return result, nil
}

func (s *notifications) UpdateNotificationChannel(ctx context.Context, value core.NotificationRecord) (retErr error) {
	if err := validateNotificationRecord(value); err != nil {
		return err
	}
	settings, err := marshalNotificationSettings(value.Settings)
	if err != nil {
		return err
	}
	tx, err := s.pool.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin notification channel update: %w", err)
	}
	defer rollbackNotification(tx, &retErr)
	rows, err := s.updateChannel(ctx, tx, value, settings)
	if isUniqueError(err) {
		return core.ErrAlreadyExists
	}
	if err != nil {
		return fmt.Errorf("update notification channel: %w", err)
	}
	if rows != 1 {
		return core.ErrNotFound
	}
	if err := s.replaceSubscriptions(ctx, tx, value.ID, value.Subscriptions, true); err != nil {
		return err
	}
	if !value.Enabled {
		if err := s.failPendingChannelDeliveries(ctx, tx, value.ID, value.UpdatedAt); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit notification channel update: %w", err)
	}
	return nil
}

func (s *notifications) failPendingChannelDeliveries(ctx context.Context, tx *sql.Tx, id string, at time.Time) error {
	var err error
	if s.sqlite != nil {
		_, err = sqlite.New(tx).FailPendingNotificationDeliveriesForChannel(ctx, sqlite.FailPendingNotificationDeliveriesForChannelParams{
			UpdatedAt: formatSQLiteTime(at), ChannelID: id,
		})
	} else {
		_, err = postgres.New(tx).FailPendingNotificationDeliveriesForChannel(ctx, postgres.FailPendingNotificationDeliveriesForChannelParams{
			UpdatedAt: core.NormalizeTime(at), ChannelID: id,
		})
	}
	if err != nil {
		return fmt.Errorf("fail disabled channel deliveries: %w", err)
	}
	return nil
}

func (s *notifications) updateChannel(ctx context.Context, tx *sql.Tx, value core.NotificationRecord, settings string) (int64, error) {
	if s.sqlite != nil {
		return sqlite.New(tx).UpdateNotificationChannel(ctx, sqlite.UpdateNotificationChannelParams{
			Kind: string(value.Kind), Name: value.Name, NameKey: core.NotificationNameKey(value.Name), Target: value.Target,
			SettingsJson: settings, SecretCiphertext: value.SecretCiphertext, KeyID: value.KeyID,
			Enabled: boolToInt64(value.Enabled), UpdatedAt: formatSQLiteTime(value.UpdatedAt), ID: value.ID,
		})
	}
	return postgres.New(tx).UpdateNotificationChannel(ctx, postgres.UpdateNotificationChannelParams{
		Kind: string(value.Kind), Name: value.Name, NameKey: core.NotificationNameKey(value.Name), Target: value.Target,
		SettingsJson: settings, SecretCiphertext: value.SecretCiphertext, KeyID: value.KeyID,
		Enabled: value.Enabled, UpdatedAt: core.NormalizeTime(value.UpdatedAt), ID: value.ID,
	})
}

func (s *notifications) DeleteNotificationChannel(ctx context.Context, id string, at time.Time) error {
	if !core.ValidID(id) || at.IsZero() {
		return core.ErrInvalidArgument
	}
	at = core.NormalizeTime(at)
	var rows int64
	var err error
	if s.sqlite != nil {
		rows, err = s.sqlite.DeleteNotificationChannel(ctx, sqlite.DeleteNotificationChannelParams{
			DeletedAt: sql.NullString{String: formatSQLiteTime(at), Valid: true}, ID: id,
		})
	} else {
		rows, err = s.postgres.DeleteNotificationChannel(ctx, postgres.DeleteNotificationChannelParams{
			DeletedAt: sql.NullTime{Time: at, Valid: true}, ID: id,
		})
	}
	if err != nil {
		return fmt.Errorf("delete notification channel: %w", err)
	}
	if rows != 1 {
		return core.ErrNotFound
	}
	return nil
}

func (s *notifications) GetUnfannedNotificationEvent(ctx context.Context) (core.NotificationEvent, error) {
	if s.sqlite != nil {
		row, err := s.sqlite.GetUnfannedNotificationEvent(ctx)
		if err != nil {
			return core.NotificationEvent{}, mapNotFound("get notification event", err)
		}
		occurred, err := parseSQLiteTime(row.OccurredAt)
		if err != nil {
			return core.NotificationEvent{}, err
		}
		return notificationEvent(row.ID, row.EventType, row.RequestID, row.RequesterID, row.ActorID,
			row.MediaKind, row.Title, row.RequestStatus, row.Reason, occurred), nil
	}
	row, err := s.postgres.GetUnfannedNotificationEvent(ctx)
	if err != nil {
		return core.NotificationEvent{}, mapNotFound("get notification event", err)
	}
	return notificationEvent(row.ID, row.EventType, row.RequestID, row.RequesterID, row.ActorID,
		row.MediaKind, row.Title, row.RequestStatus, row.Reason, core.NormalizeTime(row.OccurredAt)), nil
}

func notificationEvent(
	id, eventType, requestID, requesterID, actorID, kind, title, status, reason string, at time.Time,
) core.NotificationEvent {
	return core.NotificationEvent{ID: id, Event: core.RequestEvent{
		Type: core.RequestEventType(eventType), RequestID: requestID, RequesterID: requesterID,
		ActorID: actorID, Kind: core.MediaKind(kind), Title: title,
		Status: core.RequestStatus(status), Reason: reason, At: at,
	}}
}

func (s *notifications) FanOutNotificationEvent(
	ctx context.Context, eventID string, payload core.NotificationPayload, at time.Time,
) (count int, retErr error) {
	encoded, err := encodeNotificationPayload(eventID, payload, at)
	if err != nil {
		return 0, err
	}
	tx, err := s.pool.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin notification fan-out: %w", err)
	}
	defer rollbackNotification(tx, &retErr)
	if markErr := s.markEventFanned(ctx, tx, eventID, at); markErr != nil {
		return 0, markErr
	}
	count, err = s.enqueueForSubscribers(ctx, tx, eventID, payload.EventType, encoded, core.NormalizeTime(at))
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit notification fan-out: %w", err)
	}
	return count, nil
}

func encodeNotificationPayload(eventID string, payload core.NotificationPayload, at time.Time) (string, error) {
	if !core.ValidID(eventID) || core.ValidateNotificationPayload(payload) != nil || at.IsZero() {
		return "", core.ErrInvalidArgument
	}
	encoded, err := json.Marshal(payload)
	if err != nil || len(encoded) > core.MaxNotificationPayloadBytes {
		return "", core.ErrInvalidArgument
	}
	return string(encoded), nil
}

func rollbackNotification(tx *sql.Tx, result *error) {
	rollbackErr := tx.Rollback()
	if rollbackErr != nil && !errors.Is(rollbackErr, sql.ErrTxDone) {
		*result = errors.Join(*result, fmt.Errorf("roll back notification transaction: %w", rollbackErr))
	}
}

func (s *notifications) enqueueForSubscribers(
	ctx context.Context, tx *sql.Tx, eventID string, event core.RequestEventType, payload string, at time.Time,
) (int, error) {
	if s.sqlite != nil {
		queries := sqlite.New(tx)
		rows, err := queries.ListSubscribedNotificationChannels(ctx, sqlite.ListSubscribedNotificationChannelsParams{Enabled: 1, EventType: string(event)})
		if err != nil {
			return 0, fmt.Errorf("list notification subscribers: %w", err)
		}
		for _, row := range rows {
			if err := createSQLiteOutbox(ctx, queries, eventID, row.ID, row.Kind, string(event), payload, at); err != nil {
				return 0, err
			}
		}
		return len(rows), nil
	}
	queries := postgres.New(tx)
	rows, err := queries.ListSubscribedNotificationChannels(ctx, postgres.ListSubscribedNotificationChannelsParams{Enabled: true, EventType: string(event)})
	if err != nil {
		return 0, fmt.Errorf("list notification subscribers: %w", err)
	}
	for _, row := range rows {
		if err := createPostgresOutbox(ctx, queries, eventID, row.ID, row.Kind, string(event), payload, at); err != nil {
			return 0, err
		}
	}
	return len(rows), nil
}

func createSQLiteOutbox(
	ctx context.Context, queries *sqlite.Queries, eventID, channelID, kind, event, payload string, at time.Time,
) error {
	id, err := core.NewID()
	if err != nil {
		return fmt.Errorf("create notification delivery id: %w", err)
	}
	stamp := formatSQLiteTime(at)
	if err := queries.CreateNotificationOutbox(ctx, sqlite.CreateNotificationOutboxParams{
		ID: id, EventID: eventID, ChannelID: channelID, ChannelKind: kind, EventType: event, PayloadJson: payload,
		NextAttemptAt: stamp, CreatedAt: stamp, UpdatedAt: stamp,
	}); err != nil {
		return fmt.Errorf("create notification delivery: %w", err)
	}
	return nil
}

func createPostgresOutbox(
	ctx context.Context, queries *postgres.Queries, eventID, channelID, kind, event, payload string, at time.Time,
) error {
	id, err := core.NewID()
	if err != nil {
		return fmt.Errorf("create notification delivery id: %w", err)
	}
	if err := queries.CreateNotificationOutbox(ctx, postgres.CreateNotificationOutboxParams{
		ID: id, EventID: eventID, ChannelID: channelID, ChannelKind: kind, EventType: event, PayloadJson: payload,
		NextAttemptAt: at, CreatedAt: at, UpdatedAt: at,
	}); err != nil {
		return fmt.Errorf("create notification delivery: %w", err)
	}
	return nil
}

func (s *notifications) markEventFanned(ctx context.Context, tx *sql.Tx, eventID string, at time.Time) error {
	var rows int64
	var err error
	if s.sqlite != nil {
		rows, err = sqlite.New(tx).MarkNotificationEventFanned(ctx, sqlite.MarkNotificationEventFannedParams{
			FannedAt: sqliteNullableTime(&at), ID: eventID,
		})
	} else {
		rows, err = postgres.New(tx).MarkNotificationEventFanned(ctx, postgres.MarkNotificationEventFannedParams{
			FannedAt: postgresNullableTime(&at), ID: eventID,
		})
	}
	if err != nil {
		return fmt.Errorf("mark notification event fanned: %w", err)
	}
	if rows != 1 {
		return core.ErrNotFound
	}
	return nil
}

func (s *notifications) ClaimNotificationDelivery(ctx context.Context, lease core.NotificationLease, at time.Time) (core.NotificationDelivery, error) {
	if err := core.ValidateNotificationLease(lease, at); err != nil {
		return core.NotificationDelivery{}, err
	}
	if s.sqlite != nil {
		return s.claimSQLiteNotificationDelivery(ctx, lease, at)
	}
	return s.claimPostgresNotificationDelivery(ctx, lease, at)
}

func (s *notifications) claimSQLiteNotificationDelivery(
	ctx context.Context, lease core.NotificationLease, at time.Time,
) (core.NotificationDelivery, error) {
	var delivery core.NotificationDelivery
	err := withSQLiteWriteTransaction(ctx, s.pool, func(conn *sql.Conn) error {
		q := sqlite.New(conn)
		stamp := formatSQLiteTime(at)
		if _, failErr := q.FailExpiredExhaustedNotificationOutbox(ctx, stamp); failErr != nil {
			return fmt.Errorf("fail exhausted notification deliveries: %w", failErr)
		}
		channelID, lockErr := q.LockNotificationChannelForClaim(ctx, stamp)
		if lockErr != nil {
			return mapNotFound("lock notification channel for claim", lockErr)
		}
		row, claimErr := q.ClaimNotificationOutbox(ctx, sqlite.ClaimNotificationOutboxParams{
			LeaseToken: lease.Token, LeaseExpiresAt: sqliteNullableTime(&lease.ExpiresAt), UpdatedAt: stamp,
			ChannelID: channelID, DueAt: stamp,
		})
		if claimErr != nil {
			return mapNotFound("claim notification delivery", claimErr)
		}
		var mapErr error
		delivery, mapErr = mapSQLiteDelivery(row)
		return mapErr
	})
	return delivery, err
}

func (s *notifications) claimPostgresNotificationDelivery(
	ctx context.Context, lease core.NotificationLease, at time.Time,
) (core.NotificationDelivery, error) {
	var delivery core.NotificationDelivery
	stamp := core.NormalizeTime(at)
	if _, err := s.postgres.FailExpiredExhaustedNotificationOutbox(ctx, stamp); err != nil {
		return delivery, fmt.Errorf("fail exhausted notification deliveries: %w", err)
	}
	err := withTransaction(ctx, s.pool, func(tx *sql.Tx) error {
		q := postgres.New(tx)
		channelID, lockErr := q.LockNotificationChannelForClaim(ctx, stamp)
		if lockErr != nil {
			return mapNotFound("lock notification channel for claim", lockErr)
		}
		row, claimErr := q.ClaimNotificationOutbox(ctx, postgres.ClaimNotificationOutboxParams{
			LeaseToken: lease.Token, LeaseExpiresAt: postgresNullableTime(&lease.ExpiresAt), UpdatedAt: stamp,
			ChannelID: channelID, DueAt: stamp,
		})
		if claimErr != nil {
			return mapNotFound("claim notification delivery", claimErr)
		}
		var mapErr error
		delivery, mapErr = mapPostgresDelivery(row)
		return mapErr
	})
	return delivery, err
}

func (s *notifications) CompleteNotificationDelivery(ctx context.Context, id, token string, at time.Time) error {
	var rows int64
	var err error
	if s.sqlite != nil {
		rows, err = s.sqlite.CompleteNotificationOutbox(ctx, sqlite.CompleteNotificationOutboxParams{SentAt: sqliteNullableTime(&at), ID: id, LeaseToken: token})
	} else {
		rows, err = s.postgres.CompleteNotificationOutbox(ctx, postgres.CompleteNotificationOutboxParams{SentAt: postgresNullableTime(&at), ID: id, LeaseToken: token})
	}
	return notificationRows("complete notification delivery", rows, err)
}

func (s *notifications) RescheduleNotificationDelivery(
	ctx context.Context, id, token, reason string, at, next time.Time, terminal bool,
) error {
	if len(reason) > core.MaxNotificationErrorBytes || at.IsZero() || next.IsZero() {
		return core.ErrInvalidArgument
	}
	status := string(core.NotificationPending)
	if terminal {
		status = string(core.NotificationFailed)
	}
	var rows int64
	var err error
	if s.sqlite != nil {
		rows, err = s.sqlite.RescheduleNotificationOutbox(ctx, sqlite.RescheduleNotificationOutboxParams{
			Status: status, NextAttemptAt: formatSQLiteTime(next), LastError: reason,
			UpdatedAt: formatSQLiteTime(at), ID: id, LeaseToken: token,
		})
	} else {
		rows, err = s.postgres.RescheduleNotificationOutbox(ctx, postgres.RescheduleNotificationOutboxParams{
			Status: status, NextAttemptAt: core.NormalizeTime(next), LastError: reason,
			UpdatedAt: core.NormalizeTime(at), ID: id, LeaseToken: token,
		})
	}
	return notificationRows("reschedule notification delivery", rows, err)
}

func notificationRows(operation string, rows int64, err error) error {
	if err != nil {
		return fmt.Errorf("%s: %w", operation, err)
	}
	if rows != 1 {
		return core.ErrNotFound
	}
	return nil
}

func (s *notifications) ListNotificationDeliveries(
	ctx context.Context, channelID string, after *core.NotificationDeliveryCursor, size int,
) ([]core.NotificationDelivery, error) {
	if !core.ValidID(channelID) || size < 1 || size > maxNotificationPageSize {
		return nil, core.ErrInvalidArgument
	}
	when, id := time.Date(9999, 12, 31, 23, 59, 59, 0, time.UTC), "~"
	if after != nil {
		when, id = after.CreatedAt, after.ID
	}
	if s.sqlite != nil {
		rows, err := s.sqlite.ListNotificationDeliveries(ctx, sqlite.ListNotificationDeliveriesParams{
			ChannelID: channelID, AfterCreatedAt: formatSQLiteTime(when), AfterID: id, PageSize: int64(size),
		})
		if err != nil {
			return nil, fmt.Errorf("list notification deliveries: %w", err)
		}
		result := make([]core.NotificationDelivery, 0, len(rows))
		for _, row := range rows {
			value, mapErr := mapSQLiteDelivery(row)
			if mapErr != nil {
				return nil, mapErr
			}
			result = append(result, value)
		}
		return result, nil
	}
	rows, err := s.postgres.ListNotificationDeliveries(ctx, postgres.ListNotificationDeliveriesParams{
		ChannelID: channelID, AfterCreatedAt: core.NormalizeTime(when), AfterID: id, PageSize: int32(size),
	})
	if err != nil {
		return nil, fmt.Errorf("list notification deliveries: %w", err)
	}
	result := make([]core.NotificationDelivery, 0, len(rows))
	for _, row := range rows {
		value, mapErr := mapPostgresDelivery(row)
		if mapErr != nil {
			return nil, mapErr
		}
		result = append(result, value)
	}
	return result, nil
}

func (s *notifications) NotificationOutboxDepth(ctx context.Context) (int64, error) {
	if s.sqlite != nil {
		return s.sqlite.NotificationOutboxDepth(ctx)
	}
	return s.postgres.NotificationOutboxDepth(ctx)
}

func (s *notifications) RecordNotificationChannelResult(
	ctx context.Context, id string, success, terminal bool, degradedAfter int, at time.Time,
) error {
	if success {
		return s.recordChannelSuccess(ctx, id, at)
	}
	if !terminal {
		return nil
	}
	if degradedAfter < 1 || degradedAfter > math.MaxInt32 {
		return core.ErrInvalidArgument
	}
	var rows int64
	var err error
	if s.sqlite != nil {
		rows, err = s.sqlite.RecordNotificationChannelTerminalFailure(ctx, sqlite.RecordNotificationChannelTerminalFailureParams{
			DegradedAfter: int64(degradedAfter), UpdatedAt: formatSQLiteTime(at), ID: id,
		})
	} else {
		rows, err = s.postgres.RecordNotificationChannelTerminalFailure(ctx, postgres.RecordNotificationChannelTerminalFailureParams{
			DegradedAfter: int32(degradedAfter), UpdatedAt: core.NormalizeTime(at), ID: id,
		})
	}
	return notificationRows("record notification channel failure", rows, err)
}

func (s *notifications) recordChannelSuccess(ctx context.Context, id string, at time.Time) error {
	var rows int64
	var err error
	if s.sqlite != nil {
		rows, err = s.sqlite.RecordNotificationChannelSuccess(ctx, sqlite.RecordNotificationChannelSuccessParams{UpdatedAt: formatSQLiteTime(at), ID: id})
	} else {
		rows, err = s.postgres.RecordNotificationChannelSuccess(ctx, postgres.RecordNotificationChannelSuccessParams{UpdatedAt: core.NormalizeTime(at), ID: id})
	}
	return notificationRows("record notification channel success", rows, err)
}

func (s *notifications) PruneNotificationDeliveries(ctx context.Context, before time.Time, size int) (int64, error) {
	if before.IsZero() || size < 1 || size > maxNotificationMaintenanceSize {
		return 0, core.ErrInvalidArgument
	}
	if s.sqlite != nil {
		params := sqlite.PruneNotificationOutboxParams{BeforeAt: formatSQLiteTime(before), BatchSize: int64(size)}
		count, err := s.sqlite.PruneNotificationOutbox(ctx, params)
		if err != nil {
			return 0, err
		}
		_, err = s.sqlite.PruneNotificationEvents(ctx, sqlite.PruneNotificationEventsParams{
			BeforeAt: sql.NullString{String: params.BeforeAt, Valid: true}, BatchSize: params.BatchSize,
		})
		return count, err
	}
	params := postgres.PruneNotificationOutboxParams{BeforeAt: core.NormalizeTime(before), BatchSize: int32(size)}
	count, err := s.postgres.PruneNotificationOutbox(ctx, params)
	if err != nil {
		return 0, err
	}
	_, err = s.postgres.PruneNotificationEvents(ctx, postgres.PruneNotificationEventsParams{
		BeforeAt: sql.NullTime{Time: params.BeforeAt, Valid: true}, BatchSize: params.BatchSize,
	})
	return count, err
}

func (s *notifications) DeleteTombstonedNotificationChannels(
	ctx context.Context, at time.Time, size int,
) (int64, error) {
	if at.IsZero() || size < 1 || size > maxNotificationMaintenanceSize {
		return 0, core.ErrInvalidArgument
	}
	if s.sqlite != nil {
		return s.reapSQLiteNotificationChannels(ctx, at, size)
	}
	return s.reapPostgresNotificationChannels(ctx, at, size)
}

func (s *notifications) reapSQLiteNotificationChannels(ctx context.Context, at time.Time, size int) (int64, error) {
	var deleted int64
	err := withSQLiteWriteTransaction(ctx, s.pool, func(conn *sql.Conn) error {
		q := sqlite.New(conn)
		stamp := sql.NullString{String: formatSQLiteTime(at), Valid: true}
		ids, lockErr := q.LockTombstonedNotificationChannels(ctx, sqlite.LockTombstonedNotificationChannelsParams{
			At: stamp, BatchSize: int64(size),
		})
		if lockErr != nil {
			return fmt.Errorf("lock tombstoned notification channels: %w", lockErr)
		}
		for index := 0; index < len(ids) && index < maxNotificationMaintenanceSize; index++ {
			count, deleteErr := q.DeleteTombstonedNotificationChannel(ctx, sqlite.DeleteTombstonedNotificationChannelParams{
				ID: ids[index], At: stamp,
			})
			if deleteErr != nil {
				return fmt.Errorf("delete tombstoned notification channel: %w", deleteErr)
			}
			deleted += count
		}
		return nil
	})
	return deleted, err
}

func (s *notifications) reapPostgresNotificationChannels(ctx context.Context, at time.Time, size int) (int64, error) {
	var deleted int64
	err := withTransaction(ctx, s.pool, func(tx *sql.Tx) error {
		q := postgres.New(tx)
		stamp := sql.NullTime{Time: core.NormalizeTime(at), Valid: true}
		ids, lockErr := q.LockTombstonedNotificationChannels(ctx, postgres.LockTombstonedNotificationChannelsParams{
			At: stamp, BatchSize: int32(size), //nolint:gosec // validated as at most 1000 above.
		})
		if lockErr != nil {
			return fmt.Errorf("lock tombstoned notification channels: %w", lockErr)
		}
		for index := 0; index < len(ids) && index < maxNotificationMaintenanceSize; index++ {
			count, deleteErr := q.DeleteTombstonedNotificationChannel(ctx, postgres.DeleteTombstonedNotificationChannelParams{
				ID: ids[index], At: stamp,
			})
			if deleteErr != nil {
				return fmt.Errorf("delete tombstoned notification channel: %w", deleteErr)
			}
			deleted += count
		}
		return nil
	})
	return deleted, err
}

func (s *notifications) subscriptions(ctx context.Context, id string) ([]core.RequestEventType, error) {
	var rows []string
	var err error
	if s.sqlite != nil {
		rows, err = s.sqlite.GetNotificationSubscriptions(ctx, id)
	} else {
		rows, err = s.postgres.GetNotificationSubscriptions(ctx, id)
	}
	if err != nil {
		return nil, fmt.Errorf("get notification subscriptions: %w", err)
	}
	result := make([]core.RequestEventType, 0, len(rows))
	for _, row := range rows {
		result = append(result, core.RequestEventType(row))
	}
	return result, nil
}

func decodeNotificationSettings(value string) (core.NotificationSettings, error) {
	var settings core.NotificationSettings
	if err := json.Unmarshal([]byte(value), &settings); err != nil {
		return settings, fmt.Errorf("decode notification settings: %w", err)
	}
	return settings, nil
}

func (s *notifications) mapSQLiteChannel(ctx context.Context, row sqlite.GetNotificationChannelRow) (core.NotificationRecord, error) {
	registration, err := s.sqliteRegistration(ctx, row.ID, row.Kind, row.Name, row.Target, row.SettingsJson, row.Enabled, row.DegradedAt, row.ConsecutiveFailures, row.CreatedAt, row.UpdatedAt)
	if err != nil {
		return core.NotificationRecord{}, err
	}
	return core.NotificationRecord{NotificationRegistration: registration, SecretCiphertext: row.SecretCiphertext, KeyID: row.KeyID}, nil
}

func (s *notifications) mapPostgresChannel(ctx context.Context, row postgres.GetNotificationChannelRow) (core.NotificationRecord, error) {
	registration, err := s.postgresRegistration(ctx, row.ID, row.Kind, row.Name, row.Target, row.SettingsJson, row.Enabled, row.DegradedAt, row.ConsecutiveFailures, row.CreatedAt, row.UpdatedAt)
	if err != nil {
		return core.NotificationRecord{}, err
	}
	return core.NotificationRecord{NotificationRegistration: registration, SecretCiphertext: row.SecretCiphertext, KeyID: row.KeyID}, nil
}

func (s *notifications) mapSQLiteListChannel(ctx context.Context, row sqlite.ListNotificationChannelsRow) (core.NotificationRegistration, error) {
	return s.sqliteRegistration(ctx, row.ID, row.Kind, row.Name, row.Target, row.SettingsJson, row.Enabled, row.DegradedAt, row.ConsecutiveFailures, row.CreatedAt, row.UpdatedAt)
}

func (s *notifications) mapPostgresListChannel(ctx context.Context, row postgres.ListNotificationChannelsRow) (core.NotificationRegistration, error) {
	return s.postgresRegistration(ctx, row.ID, row.Kind, row.Name, row.Target, row.SettingsJson, row.Enabled, row.DegradedAt, row.ConsecutiveFailures, row.CreatedAt, row.UpdatedAt)
}

func (s *notifications) sqliteRegistration(
	ctx context.Context, id, kind, name, target, settingsJSON string, enabled int64,
	degraded sql.NullString, failures int64, createdRaw, updatedRaw string,
) (core.NotificationRegistration, error) {
	settings, err := decodeNotificationSettings(settingsJSON)
	if err != nil {
		return core.NotificationRegistration{}, err
	}
	created, err := parseSQLiteTime(createdRaw)
	if err != nil {
		return core.NotificationRegistration{}, err
	}
	updated, err := parseSQLiteTime(updatedRaw)
	if err != nil {
		return core.NotificationRegistration{}, err
	}
	degradedAt, err := parseNullableSQLiteTime(degraded)
	if err != nil {
		return core.NotificationRegistration{}, err
	}
	subscriptions, err := s.subscriptions(ctx, id)
	if err != nil {
		return core.NotificationRegistration{}, err
	}
	return core.NotificationRegistration{
		ID: id, Kind: core.NotificationKind(kind), Name: name, Target: target, Settings: settings,
		Subscriptions: subscriptions, Enabled: enabled != 0, DegradedAt: degradedAt,
		ConsecutiveFailures: int(failures), SecretSet: true, CreatedAt: created, UpdatedAt: updated,
	}, nil
}

func (s *notifications) postgresRegistration(
	ctx context.Context, id, kind, name, target, settingsJSON string, enabled bool,
	degraded sql.NullTime, failures int32, created, updated time.Time,
) (core.NotificationRegistration, error) {
	settings, err := decodeNotificationSettings(settingsJSON)
	if err != nil {
		return core.NotificationRegistration{}, err
	}
	subscriptions, err := s.subscriptions(ctx, id)
	if err != nil {
		return core.NotificationRegistration{}, err
	}
	var degradedAt *time.Time
	if degraded.Valid {
		value := core.NormalizeTime(degraded.Time)
		degradedAt = &value
	}
	return core.NotificationRegistration{
		ID: id, Kind: core.NotificationKind(kind), Name: name, Target: target, Settings: settings,
		Subscriptions: subscriptions, Enabled: enabled, DegradedAt: degradedAt,
		ConsecutiveFailures: int(failures), SecretSet: true,
		CreatedAt: core.NormalizeTime(created), UpdatedAt: core.NormalizeTime(updated),
	}, nil
}

func mapSQLiteDelivery(row sqlite.NotificationOutbox) (core.NotificationDelivery, error) {
	created, err := parseSQLiteTime(row.CreatedAt)
	if err != nil {
		return core.NotificationDelivery{}, err
	}
	updated, err := parseSQLiteTime(row.UpdatedAt)
	if err != nil {
		return core.NotificationDelivery{}, err
	}
	next, err := parseSQLiteTime(row.NextAttemptAt)
	if err != nil {
		return core.NotificationDelivery{}, err
	}
	lease, err := parseNullableSQLiteTime(row.LeaseExpiresAt)
	if err != nil {
		return core.NotificationDelivery{}, err
	}
	sent, err := parseNullableSQLiteTime(row.SentAt)
	if err != nil {
		return core.NotificationDelivery{}, err
	}
	return decodeDelivery(row.ID, row.ChannelID, row.ChannelKind, row.EventType, row.PayloadJson, row.Status,
		int(row.Attempts), next, row.LeaseToken, lease, row.LastError, sent, created, updated)
}

func mapPostgresDelivery(row postgres.NotificationOutbox) (core.NotificationDelivery, error) {
	var lease, sent *time.Time
	if row.LeaseExpiresAt.Valid {
		value := core.NormalizeTime(row.LeaseExpiresAt.Time)
		lease = &value
	}
	if row.SentAt.Valid {
		value := core.NormalizeTime(row.SentAt.Time)
		sent = &value
	}
	return decodeDelivery(row.ID, row.ChannelID, row.ChannelKind, row.EventType, row.PayloadJson, row.Status,
		int(row.Attempts), core.NormalizeTime(row.NextAttemptAt), row.LeaseToken, lease, row.LastError, sent,
		core.NormalizeTime(row.CreatedAt), core.NormalizeTime(row.UpdatedAt))
}

func decodeDelivery(
	id, channelID, kind, event, payloadJSON, status string, attempts int, next time.Time,
	leaseToken string, lease *time.Time, lastError string, sent *time.Time, created, updated time.Time,
) (core.NotificationDelivery, error) {
	var payload core.NotificationPayload
	if err := json.Unmarshal([]byte(payloadJSON), &payload); err != nil {
		return core.NotificationDelivery{}, fmt.Errorf("decode notification payload: %w", err)
	}
	return core.NotificationDelivery{
		ID: id, ChannelID: channelID, ChannelKind: core.NotificationKind(kind), EventType: core.RequestEventType(event),
		Payload: payload, Status: core.NotificationStatus(status), Attempts: attempts, NextAttemptAt: next,
		LeaseToken: leaseToken, LeaseExpiresAt: lease, LastError: lastError, SentAt: sent,
		CreatedAt: created, UpdatedAt: updated,
	}, nil
}
