-- SQLite migration 00028: account notification routing and playback events.

-- +goose Up
DROP INDEX notification_subscriptions_event_idx;
ALTER TABLE notification_channel_subscriptions RENAME TO notification_channel_subscriptions_old;
CREATE TABLE notification_channel_subscriptions (
    channel_id TEXT NOT NULL REFERENCES notification_channels(id) ON DELETE CASCADE,
    event_type TEXT NOT NULL CHECK (event_type IN ('created', 'approved', 'declined', 'dispatched', 'available', 'failed', 'playback.session_started')),
    PRIMARY KEY (channel_id, event_type)
) STRICT;
INSERT INTO notification_channel_subscriptions (channel_id, event_type)
SELECT channel_id, event_type FROM notification_channel_subscriptions_old;
DROP TABLE notification_channel_subscriptions_old;
CREATE INDEX notification_subscriptions_event_idx ON notification_channel_subscriptions (event_type, channel_id);

DROP INDEX notification_outbox_event_channel_idx;
DROP INDEX notification_outbox_claim_idx;
DROP INDEX notification_outbox_channel_recent_idx;
ALTER TABLE notification_outbox RENAME TO notification_outbox_old;
DROP INDEX notification_events_fanout_idx;
ALTER TABLE notification_events RENAME TO notification_events_old;

CREATE TABLE notification_events (
    id TEXT PRIMARY KEY CHECK (length(id) = 36),
    event_type TEXT NOT NULL CHECK (event_type IN ('created', 'approved', 'declined', 'dispatched', 'available', 'failed', 'playback.session_started')),
    request_id TEXT NOT NULL CHECK (length(request_id) = 36),
    requester_id TEXT NOT NULL CHECK (length(requester_id) = 36),
    actor_id TEXT NOT NULL,
    media_kind TEXT NOT NULL CHECK (media_kind IN ('movie', 'series')),
    title TEXT NOT NULL CHECK (length(CAST(title AS BLOB)) <= 4096),
    request_status TEXT NOT NULL CHECK (request_status IN ('pending', 'approved', 'declined', 'processing', 'available', 'failed')),
    reason TEXT NOT NULL CHECK (length(CAST(reason AS BLOB)) <= 4096),
    source_payload_json TEXT NOT NULL DEFAULT '{}' CHECK (length(CAST(source_payload_json AS BLOB)) BETWEEN 2 AND 32768),
    event_sequence INTEGER NOT NULL CHECK (event_sequence BETWEEN 0 AND 1),
    occurred_at TEXT NOT NULL,
    fanned_at TEXT,
    created_at TEXT NOT NULL
) STRICT;
INSERT INTO notification_events (
    id, event_type, request_id, requester_id, actor_id, media_kind, title,
    request_status, reason, source_payload_json, event_sequence, occurred_at, fanned_at, created_at
) SELECT id, event_type, request_id, requester_id, actor_id, media_kind, title,
         request_status, reason, '{}', event_sequence, occurred_at, fanned_at, created_at
  FROM notification_events_old;
CREATE INDEX notification_events_fanout_idx ON notification_events (fanned_at, created_at, event_sequence, id);
CREATE UNIQUE INDEX notification_events_playback_watch_idx
    ON notification_events (event_type, request_id) WHERE event_type = 'playback.session_started';

CREATE TABLE notification_event_recipients (
    event_id TEXT NOT NULL REFERENCES notification_events(id) ON DELETE CASCADE,
    account_id TEXT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    PRIMARY KEY (event_id, account_id)
) STRICT;
INSERT INTO notification_event_recipients (event_id, account_id)
SELECT event.id, event.requester_id
FROM notification_events AS event
JOIN accounts ON accounts.id = event.requester_id;

CREATE TABLE notification_outbox (
    id TEXT PRIMARY KEY CHECK (length(id) = 36),
    event_id TEXT NOT NULL REFERENCES notification_events(id) ON DELETE CASCADE,
    channel_id TEXT NOT NULL REFERENCES notification_channels(id) ON DELETE CASCADE,
    recipient_account_id TEXT CHECK (length(recipient_account_id) = 36),
    channel_kind TEXT NOT NULL CHECK (channel_kind IN ('webhook', 'discord', 'email')),
    event_type TEXT NOT NULL CHECK (event_type IN ('created', 'approved', 'declined', 'dispatched', 'available', 'failed', 'playback.session_started')),
    payload_json TEXT NOT NULL CHECK (length(CAST(payload_json AS BLOB)) BETWEEN 2 AND 32768),
    status TEXT NOT NULL CHECK (status IN ('pending', 'sent', 'failed')),
    attempts INTEGER NOT NULL DEFAULT 0 CHECK (attempts BETWEEN 0 AND 8),
    next_attempt_at TEXT NOT NULL,
    lease_token TEXT NOT NULL DEFAULT '',
    lease_expires_at TEXT,
    last_error TEXT NOT NULL DEFAULT '' CHECK (length(CAST(last_error AS BLOB)) <= 512),
    sent_at TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    CHECK ((lease_token = '' AND lease_expires_at IS NULL) OR (length(lease_token) = 36 AND lease_expires_at IS NOT NULL))
) STRICT;
INSERT INTO notification_outbox (
    id, event_id, channel_id, recipient_account_id, channel_kind, event_type, payload_json,
    status, attempts, next_attempt_at, lease_token, lease_expires_at, last_error, sent_at, created_at, updated_at
) SELECT old.id, old.event_id, old.channel_id, event.requester_id, old.channel_kind, old.event_type,
         old.payload_json, old.status, old.attempts, old.next_attempt_at, old.lease_token,
         old.lease_expires_at, old.last_error, old.sent_at, old.created_at, old.updated_at
  FROM notification_outbox_old AS old
  JOIN notification_events AS event ON event.id = old.event_id;
CREATE UNIQUE INDEX notification_outbox_event_channel_idx
    ON notification_outbox (event_id, channel_id) WHERE recipient_account_id IS NULL;
CREATE UNIQUE INDEX notification_outbox_event_channel_recipient_idx
    ON notification_outbox (event_id, channel_id, recipient_account_id) WHERE recipient_account_id IS NOT NULL;
CREATE INDEX notification_outbox_claim_idx ON notification_outbox (status, next_attempt_at, lease_expires_at, created_at, id);
CREATE INDEX notification_outbox_channel_recent_idx ON notification_outbox (channel_id, created_at DESC, id DESC);

DROP TABLE notification_outbox_old;
DROP TABLE notification_events_old;

CREATE TABLE account_notification_preferences (
    account_id TEXT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    event_type TEXT NOT NULL CHECK (event_type IN ('created', 'approved', 'declined', 'dispatched', 'available', 'failed', 'playback.session_started')),
    enabled INTEGER NOT NULL CHECK (enabled IN (0, 1)),
    PRIMARY KEY (account_id, event_type)
) STRICT;

CREATE TABLE playback_notification_emissions (
    watch_id TEXT PRIMARY KEY REFERENCES watches(id) ON DELETE CASCADE,
    emitted_at TEXT NOT NULL
) STRICT;

CREATE TABLE title_availability_subscriptions (
    account_id TEXT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    provider TEXT NOT NULL CHECK (provider IN ('tmdb')),
    provider_id TEXT NOT NULL CHECK (length(CAST(provider_id AS BLOB)) BETWEEN 1 AND 20),
    created_at TEXT NOT NULL,
    PRIMARY KEY (account_id, provider, provider_id)
) STRICT;
CREATE INDEX title_availability_subscriptions_title_idx
    ON title_availability_subscriptions (provider, provider_id, account_id);

INSERT INTO role_permissions (role_id, permission) VALUES
    ('00000000-0000-4000-8000-000000000001', 'notifications.manage.own'),
    ('00000000-0000-4000-8000-000000000002', 'notifications.manage.own')
ON CONFLICT DO NOTHING;
INSERT INTO role_permissions (role_id, permission)
SELECT role_id, 'notifications.manage.own'
FROM role_permissions
WHERE permission = 'requests.read.own'
ON CONFLICT DO NOTHING;

-- +goose Down
DELETE FROM role_permissions WHERE permission = 'notifications.manage.own';
DROP TABLE title_availability_subscriptions;
DROP TABLE playback_notification_emissions;
DROP TABLE account_notification_preferences;
DROP TABLE notification_event_recipients;

DROP INDEX notification_outbox_event_channel_idx;
DROP INDEX notification_outbox_event_channel_recipient_idx;
DROP INDEX notification_outbox_claim_idx;
DROP INDEX notification_outbox_channel_recent_idx;
ALTER TABLE notification_outbox RENAME TO notification_outbox_new;
DROP INDEX notification_events_playback_watch_idx;
DROP INDEX notification_events_fanout_idx;
ALTER TABLE notification_events RENAME TO notification_events_new;

CREATE TABLE notification_events (
    id TEXT PRIMARY KEY CHECK (length(id) = 36),
    event_type TEXT NOT NULL CHECK (event_type IN ('created', 'approved', 'declined', 'dispatched', 'available', 'failed')),
    request_id TEXT NOT NULL CHECK (length(request_id) = 36), requester_id TEXT NOT NULL CHECK (length(requester_id) = 36),
    actor_id TEXT NOT NULL, media_kind TEXT NOT NULL CHECK (media_kind IN ('movie', 'series')),
    title TEXT NOT NULL CHECK (length(CAST(title AS BLOB)) <= 4096),
    request_status TEXT NOT NULL CHECK (request_status IN ('pending', 'approved', 'declined', 'processing', 'available', 'failed')),
    reason TEXT NOT NULL CHECK (length(CAST(reason AS BLOB)) <= 4096),
    event_sequence INTEGER NOT NULL CHECK (event_sequence BETWEEN 0 AND 1), occurred_at TEXT NOT NULL,
    fanned_at TEXT, created_at TEXT NOT NULL
) STRICT;
INSERT INTO notification_events (
    id, event_type, request_id, requester_id, actor_id, media_kind, title,
    request_status, reason, event_sequence, occurred_at, fanned_at, created_at
)
SELECT id, event_type, request_id, requester_id, actor_id, media_kind, title, request_status,
       reason, event_sequence, occurred_at, fanned_at, created_at
FROM notification_events_new WHERE event_type <> 'playback.session_started';
CREATE INDEX notification_events_fanout_idx ON notification_events (fanned_at, created_at, event_sequence, id);

CREATE TABLE notification_outbox (
    id TEXT PRIMARY KEY CHECK (length(id) = 36), event_id TEXT NOT NULL REFERENCES notification_events(id) ON DELETE CASCADE,
    channel_id TEXT NOT NULL REFERENCES notification_channels(id) ON DELETE CASCADE,
    channel_kind TEXT NOT NULL CHECK (channel_kind IN ('webhook', 'discord', 'email')),
    event_type TEXT NOT NULL CHECK (event_type IN ('created', 'approved', 'declined', 'dispatched', 'available', 'failed')),
    payload_json TEXT NOT NULL CHECK (length(CAST(payload_json AS BLOB)) BETWEEN 2 AND 32768),
    status TEXT NOT NULL CHECK (status IN ('pending', 'sent', 'failed')),
    attempts INTEGER NOT NULL DEFAULT 0 CHECK (attempts BETWEEN 0 AND 8), next_attempt_at TEXT NOT NULL,
    lease_token TEXT NOT NULL DEFAULT '', lease_expires_at TEXT,
    last_error TEXT NOT NULL DEFAULT '' CHECK (length(CAST(last_error AS BLOB)) <= 512),
    sent_at TEXT, created_at TEXT NOT NULL, updated_at TEXT NOT NULL,
    CHECK ((lease_token = '' AND lease_expires_at IS NULL) OR (length(lease_token) = 36 AND lease_expires_at IS NOT NULL))
) STRICT;
INSERT INTO notification_outbox (
    id, event_id, channel_id, channel_kind, event_type, payload_json, status, attempts,
    next_attempt_at, lease_token, lease_expires_at, last_error, sent_at, created_at, updated_at
)
SELECT candidate.id, candidate.event_id, candidate.channel_id, candidate.channel_kind,
       candidate.event_type, candidate.payload_json, candidate.status, candidate.attempts,
       candidate.next_attempt_at, candidate.lease_token, candidate.lease_expires_at,
       candidate.last_error, candidate.sent_at, candidate.created_at, candidate.updated_at
FROM notification_outbox_new AS candidate
WHERE candidate.event_type <> 'playback.session_started'
  AND NOT EXISTS (
      SELECT 1 FROM notification_outbox_new AS preferred
      WHERE preferred.event_id = candidate.event_id
        AND preferred.channel_id = candidate.channel_id
        AND preferred.event_type <> 'playback.session_started'
        AND (
          CASE WHEN preferred.recipient_account_id IS NULL THEN 1 ELSE 0 END
            < CASE WHEN candidate.recipient_account_id IS NULL THEN 1 ELSE 0 END
          OR (
            CASE WHEN preferred.recipient_account_id IS NULL THEN 1 ELSE 0 END
              = CASE WHEN candidate.recipient_account_id IS NULL THEN 1 ELSE 0 END
            AND (COALESCE(preferred.recipient_account_id, '') < COALESCE(candidate.recipient_account_id, '')
              OR (COALESCE(preferred.recipient_account_id, '') = COALESCE(candidate.recipient_account_id, '')
                AND preferred.id < candidate.id))
          )
        )
  );
CREATE UNIQUE INDEX notification_outbox_event_channel_idx ON notification_outbox (event_id, channel_id);
CREATE INDEX notification_outbox_claim_idx ON notification_outbox (status, next_attempt_at, lease_expires_at, created_at, id);
CREATE INDEX notification_outbox_channel_recent_idx ON notification_outbox (channel_id, created_at DESC, id DESC);
DROP TABLE notification_outbox_new;
DROP TABLE notification_events_new;

DROP INDEX notification_subscriptions_event_idx;
ALTER TABLE notification_channel_subscriptions RENAME TO notification_channel_subscriptions_new;
CREATE TABLE notification_channel_subscriptions (
    channel_id TEXT NOT NULL REFERENCES notification_channels(id) ON DELETE CASCADE,
    event_type TEXT NOT NULL CHECK (event_type IN ('created', 'approved', 'declined', 'dispatched', 'available', 'failed')),
    PRIMARY KEY (channel_id, event_type)
) STRICT;
INSERT INTO notification_channel_subscriptions (channel_id, event_type)
SELECT channel_id, event_type FROM notification_channel_subscriptions_new
WHERE event_type <> 'playback.session_started';
DROP TABLE notification_channel_subscriptions_new;
CREATE INDEX notification_subscriptions_event_idx ON notification_channel_subscriptions (event_type, channel_id);
