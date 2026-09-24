-- SQLite notification candidate selection. BEGIN IMMEDIATE already serializes
-- the write transaction; these names match the PostgreSQL row-lock queries.

-- name: LockNotificationChannelForClaim :one
SELECT channel.id
FROM notification_outbox AS candidate
JOIN notification_channels AS channel ON channel.id = candidate.channel_id
WHERE candidate.status = 'pending' AND candidate.next_attempt_at <= sqlc.arg(due_at)
  AND candidate.attempts < 8
  AND (candidate.lease_token = '' OR candidate.lease_expires_at <= sqlc.arg(due_at))
  AND channel.enabled = TRUE AND channel.deleted_at IS NULL
ORDER BY candidate.next_attempt_at, candidate.created_at, candidate.id
LIMIT 1;

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
LIMIT sqlc.arg(batch_size);
