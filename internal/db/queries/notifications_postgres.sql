-- PostgreSQL notification row locks. Each lock is followed by a second
-- statement in the same Read Committed transaction that re-checks eligibility.

-- name: LockNotificationChannelForClaim :one
SELECT channel.id
FROM notification_outbox AS candidate
JOIN notification_channels AS channel ON channel.id = candidate.channel_id
WHERE candidate.status = 'pending' AND candidate.next_attempt_at <= sqlc.arg(due_at)
  AND candidate.attempts < 8
  AND (candidate.lease_token = '' OR candidate.lease_expires_at <= sqlc.arg(due_at))
  AND channel.enabled = TRUE AND channel.deleted_at IS NULL
ORDER BY candidate.next_attempt_at, candidate.created_at, candidate.id
LIMIT 1
FOR UPDATE OF channel;

-- name: LockTombstonedNotificationChannels :many
SELECT candidate.id
FROM notification_channels AS candidate
WHERE candidate.deleted_at IS NOT NULL
  AND NOT EXISTS (
	SELECT 1 FROM notification_outbox AS delivery
	WHERE delivery.channel_id = candidate.id AND delivery.status = 'pending'
	  AND delivery.lease_token <> '' AND delivery.lease_expires_at > sqlc.arg(at)
  )
ORDER BY candidate.deleted_at, candidate.id
LIMIT sqlc.arg(batch_size)
FOR UPDATE OF candidate;

-- name: LockNotificationPreferenceAccount :one
SELECT id FROM accounts WHERE id = sqlc.arg(account_id) FOR UPDATE;

-- name: ListAddressedNotificationChannels :many
SELECT channel.id, channel.kind, recipient.account_id
FROM notification_event_recipients AS recipient
JOIN notification_channel_subscriptions AS subscription
  ON subscription.event_type = sqlc.arg(event_type)
JOIN notification_channels AS channel ON channel.id = subscription.channel_id
LEFT JOIN account_notification_preferences AS preference
  ON preference.account_id = recipient.account_id
 AND preference.event_type = subscription.event_type
 AND preference.channel_id = channel.id
WHERE recipient.event_id = sqlc.arg(event_id)
  AND channel.enabled = TRUE AND channel.deleted_at IS NULL
  AND (preference.enabled = TRUE OR (preference.account_id IS NULL AND subscription.event_type <> 'playback.session_started'))
ORDER BY recipient.account_id, channel.id;
