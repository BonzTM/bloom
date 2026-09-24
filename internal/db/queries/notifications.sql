-- Notification channel and durable outbox queries shared by both engines.

-- name: CreateNotificationChannel :exec
INSERT INTO notification_channels (
    id, kind, name, name_key, target, settings_json, secret_ciphertext, key_id,
    enabled, degraded_at, consecutive_failures, created_at, updated_at
) VALUES (
    sqlc.arg(id), sqlc.arg(kind), sqlc.arg(name), sqlc.arg(name_key), sqlc.arg(target),
    sqlc.arg(settings_json), sqlc.arg(secret_ciphertext), sqlc.arg(key_id), sqlc.arg(enabled),
    sqlc.arg(degraded_at), sqlc.arg(consecutive_failures), sqlc.arg(created_at), sqlc.arg(updated_at)
);

-- name: AddNotificationSubscription :exec
INSERT INTO notification_channel_subscriptions (channel_id, event_type)
VALUES (sqlc.arg(channel_id), sqlc.arg(event_type));

-- name: DeleteNotificationSubscriptions :exec
DELETE FROM notification_channel_subscriptions WHERE channel_id = sqlc.arg(channel_id);

-- name: GetNotificationSubscriptions :many
SELECT event_type FROM notification_channel_subscriptions
WHERE channel_id = sqlc.arg(channel_id) ORDER BY event_type;

-- name: GetNotificationChannel :one
SELECT id, kind, name, target, settings_json, secret_ciphertext, key_id, enabled,
       degraded_at, consecutive_failures, created_at, updated_at
FROM notification_channels WHERE id = sqlc.arg(id) AND deleted_at IS NULL;

-- name: ListNotificationChannels :many
SELECT id, kind, name, target, settings_json, enabled, degraded_at,
       consecutive_failures, created_at, updated_at
FROM notification_channels
WHERE deleted_at IS NULL AND name_key > sqlc.arg(after_name_key)
ORDER BY name_key, id LIMIT sqlc.arg(page_size);

-- name: UpdateNotificationChannel :execrows
UPDATE notification_channels SET
    kind = sqlc.arg(kind), name = sqlc.arg(name), name_key = sqlc.arg(name_key),
    target = sqlc.arg(target), settings_json = sqlc.arg(settings_json),
    secret_ciphertext = sqlc.arg(secret_ciphertext), key_id = sqlc.arg(key_id),
    enabled = sqlc.arg(enabled), updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id) AND deleted_at IS NULL;

-- name: FailPendingNotificationDeliveriesForChannel :execrows
UPDATE notification_outbox SET status = 'failed', lease_token = '', lease_expires_at = NULL,
    last_error = 'channel disabled', updated_at = sqlc.arg(updated_at)
WHERE channel_id = sqlc.arg(channel_id) AND status = 'pending'
  AND (lease_token = '' OR lease_expires_at <= sqlc.arg(updated_at));

-- name: DeleteNotificationChannel :execrows
UPDATE notification_channels SET enabled = FALSE, deleted_at = sqlc.arg(deleted_at),
    updated_at = sqlc.arg(deleted_at)
WHERE id = sqlc.arg(id) AND deleted_at IS NULL;

-- name: ListSubscribedNotificationChannels :many
SELECT c.id, c.kind FROM notification_channels c
JOIN notification_channel_subscriptions s ON s.channel_id = c.id
WHERE c.enabled = sqlc.arg(enabled) AND c.deleted_at IS NULL
  AND s.event_type = sqlc.arg(event_type)
ORDER BY c.id;

-- name: CreateNotificationEvent :exec
INSERT INTO notification_events (
    id, event_type, request_id, requester_id, actor_id, media_kind, title,
    request_status, reason, event_sequence, occurred_at, fanned_at, created_at
) VALUES (
    sqlc.arg(id), sqlc.arg(event_type), sqlc.arg(request_id), sqlc.arg(requester_id),
    sqlc.arg(actor_id), sqlc.arg(media_kind), sqlc.arg(title), sqlc.arg(request_status),
    sqlc.arg(reason), sqlc.arg(event_sequence), sqlc.arg(occurred_at), NULL, sqlc.arg(created_at)
);

-- name: GetUnfannedNotificationEvent :one
SELECT id, event_type, request_id, requester_id, actor_id, media_kind, title,
       request_status, reason, event_sequence, occurred_at, fanned_at, created_at
FROM notification_events
WHERE fanned_at IS NULL
ORDER BY created_at, event_sequence, id LIMIT 1;

-- name: MarkNotificationEventFanned :execrows
UPDATE notification_events SET fanned_at = sqlc.arg(fanned_at)
WHERE id = sqlc.arg(id) AND fanned_at IS NULL;

-- name: CreateNotificationOutbox :exec
INSERT INTO notification_outbox (
    id, event_id, channel_id, channel_kind, event_type, payload_json, status, attempts,
    next_attempt_at, lease_token, lease_expires_at, last_error, sent_at, created_at, updated_at
) VALUES (
    sqlc.arg(id), sqlc.arg(event_id), sqlc.arg(channel_id), sqlc.arg(channel_kind), sqlc.arg(event_type),
    sqlc.arg(payload_json), 'pending', 0, sqlc.arg(next_attempt_at), '', NULL, '', NULL,
    sqlc.arg(created_at), sqlc.arg(updated_at)
);

-- name: FailExpiredExhaustedNotificationOutbox :execrows
UPDATE notification_outbox SET status = 'failed', lease_token = '', lease_expires_at = NULL,
    last_error = 'attempt limit reached', updated_at = sqlc.arg(updated_at)
WHERE status = 'pending' AND attempts >= 8
  AND (lease_token = '' OR lease_expires_at <= sqlc.arg(updated_at));

-- name: ClaimNotificationOutbox :one
UPDATE notification_outbox SET
    lease_token = sqlc.arg(lease_token), lease_expires_at = sqlc.arg(lease_expires_at),
    attempts = attempts + 1, updated_at = sqlc.arg(updated_at)
WHERE id = (
	SELECT candidate.id FROM notification_outbox AS candidate
	JOIN notification_channels AS channel ON channel.id = candidate.channel_id
	WHERE candidate.channel_id = sqlc.arg(channel_id)
	  AND candidate.status = 'pending' AND candidate.next_attempt_at <= sqlc.arg(due_at)
	  AND channel.enabled = TRUE AND channel.deleted_at IS NULL
	  AND candidate.attempts < 8
	  AND (candidate.lease_token = '' OR candidate.lease_expires_at <= sqlc.arg(due_at))
	ORDER BY candidate.next_attempt_at, candidate.created_at, candidate.id LIMIT 1
)
AND status = 'pending' AND next_attempt_at <= sqlc.arg(due_at)
AND channel_id = sqlc.arg(channel_id)
AND attempts < 8
AND (lease_token = '' OR lease_expires_at <= sqlc.arg(due_at))
AND EXISTS (
	SELECT 1 FROM notification_channels AS channel
	WHERE channel.id = sqlc.arg(channel_id) AND channel.enabled = TRUE AND channel.deleted_at IS NULL
)
RETURNING id, event_id, channel_id, channel_kind, event_type, payload_json, status, attempts,
          next_attempt_at, lease_token, lease_expires_at, last_error, sent_at, created_at, updated_at;

-- name: CompleteNotificationOutbox :execrows
UPDATE notification_outbox SET status = 'sent', sent_at = sqlc.arg(sent_at),
    lease_token = '', lease_expires_at = NULL, last_error = '', updated_at = sqlc.arg(sent_at)
WHERE id = sqlc.arg(id) AND status = 'pending' AND lease_token = sqlc.arg(lease_token);

-- name: RescheduleNotificationOutbox :execrows
UPDATE notification_outbox SET status = sqlc.arg(status), next_attempt_at = sqlc.arg(next_attempt_at),
    lease_token = '', lease_expires_at = NULL, last_error = sqlc.arg(last_error),
    updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id) AND status = 'pending' AND lease_token = sqlc.arg(lease_token);

-- name: ListNotificationDeliveries :many
SELECT id, event_id, channel_id, channel_kind, event_type, payload_json, status, attempts,
       next_attempt_at, lease_token, lease_expires_at, last_error, sent_at, created_at, updated_at
FROM notification_outbox
WHERE channel_id = sqlc.arg(channel_id)
  AND (created_at < sqlc.arg(after_created_at)
       OR (created_at = sqlc.arg(after_created_at) AND id < sqlc.arg(after_id)))
ORDER BY created_at DESC, id DESC LIMIT sqlc.arg(page_size);

-- name: NotificationOutboxDepth :one
SELECT COUNT(*) FROM notification_outbox WHERE status = 'pending';

-- name: RecordNotificationChannelSuccess :execrows
UPDATE notification_channels SET consecutive_failures = 0, degraded_at = NULL,
    updated_at = sqlc.arg(updated_at) WHERE id = sqlc.arg(id);

-- name: RecordNotificationChannelTerminalFailure :execrows
UPDATE notification_channels SET consecutive_failures = consecutive_failures + 1,
    degraded_at = CASE WHEN consecutive_failures + 1 >= sqlc.arg(degraded_after)
        THEN COALESCE(degraded_at, sqlc.arg(updated_at)) ELSE degraded_at END,
    updated_at = sqlc.arg(updated_at) WHERE id = sqlc.arg(id);

-- name: PruneNotificationOutbox :execrows
DELETE FROM notification_outbox WHERE id IN (
	SELECT candidate.id FROM notification_outbox AS candidate
	WHERE candidate.status IN ('sent', 'failed') AND candidate.updated_at < sqlc.arg(before_at)
	ORDER BY candidate.updated_at, candidate.id LIMIT sqlc.arg(batch_size)
);

-- name: PruneNotificationEvents :execrows
DELETE FROM notification_events WHERE id IN (
	SELECT candidate.id FROM notification_events AS candidate
	WHERE candidate.fanned_at IS NOT NULL AND candidate.fanned_at < sqlc.arg(before_at)
	  AND NOT EXISTS (SELECT 1 FROM notification_outbox AS delivery WHERE delivery.event_id = candidate.id)
	ORDER BY candidate.fanned_at, candidate.id LIMIT sqlc.arg(batch_size)
);

-- name: DeleteTombstonedNotificationChannel :execrows
DELETE FROM notification_channels
WHERE notification_channels.id = sqlc.arg(id) AND notification_channels.deleted_at IS NOT NULL
  AND NOT EXISTS (
	SELECT 1 FROM notification_outbox AS delivery
	WHERE delivery.channel_id = notification_channels.id AND delivery.status = 'pending'
	  AND delivery.lease_token <> '' AND delivery.lease_expires_at > sqlc.arg(at)
  );
