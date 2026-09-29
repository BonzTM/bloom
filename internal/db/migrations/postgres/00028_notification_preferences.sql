-- PostgreSQL migration 00028: account notification routing and playback events.

-- +goose Up
ALTER TABLE notification_channel_subscriptions DROP CONSTRAINT notification_channel_subscriptions_event_type_check;
ALTER TABLE notification_channel_subscriptions ADD CONSTRAINT notification_channel_subscriptions_event_type_check
    CHECK (event_type IN ('created', 'approved', 'declined', 'dispatched', 'available', 'failed', 'playback.session_started'));

ALTER TABLE notification_events DROP CONSTRAINT notification_events_event_type_check;
ALTER TABLE notification_events ADD CONSTRAINT notification_events_event_type_check
    CHECK (event_type IN ('created', 'approved', 'declined', 'dispatched', 'available', 'failed', 'playback.session_started'));
ALTER TABLE notification_events ADD COLUMN source_payload_json TEXT NOT NULL DEFAULT '{}'
    CHECK (octet_length(source_payload_json) BETWEEN 2 AND 32768);
CREATE UNIQUE INDEX notification_events_playback_watch_idx
    ON notification_events (event_type, request_id) WHERE event_type = 'playback.session_started';

CREATE TABLE notification_event_recipients (
    event_id TEXT NOT NULL REFERENCES notification_events(id) ON DELETE CASCADE,
    account_id TEXT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    PRIMARY KEY (event_id, account_id)
);
INSERT INTO notification_event_recipients (event_id, account_id)
SELECT event.id, event.requester_id
FROM notification_events AS event
JOIN accounts ON accounts.id = event.requester_id;

ALTER TABLE notification_outbox DROP CONSTRAINT notification_outbox_event_type_check;
ALTER TABLE notification_outbox ADD CONSTRAINT notification_outbox_event_type_check
    CHECK (event_type IN ('created', 'approved', 'declined', 'dispatched', 'available', 'failed', 'playback.session_started'));
ALTER TABLE notification_outbox ADD COLUMN recipient_account_id TEXT;
UPDATE notification_outbox AS delivery
SET recipient_account_id = event.requester_id
FROM notification_events AS event
WHERE event.id = delivery.event_id;
ALTER TABLE notification_outbox ALTER COLUMN recipient_account_id SET NOT NULL;
ALTER TABLE notification_outbox ADD CONSTRAINT notification_outbox_recipient_account_id_check
    CHECK (length(recipient_account_id) = 36);
DROP INDEX notification_outbox_event_channel_idx;
CREATE UNIQUE INDEX notification_outbox_event_channel_recipient_idx
    ON notification_outbox (event_id, channel_id, recipient_account_id);

CREATE TABLE account_notification_preferences (
    account_id TEXT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    event_type TEXT NOT NULL CHECK (event_type IN ('created', 'approved', 'declined', 'dispatched', 'available', 'failed', 'playback.session_started')),
    enabled BOOLEAN NOT NULL,
    PRIMARY KEY (account_id, event_type)
);

CREATE TABLE playback_notification_emissions (
    watch_id TEXT PRIMARY KEY REFERENCES watches(id) ON DELETE CASCADE,
    emitted_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE title_availability_subscriptions (
    account_id TEXT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    provider TEXT NOT NULL CHECK (provider IN ('tmdb')),
    provider_id TEXT NOT NULL CHECK (octet_length(provider_id) BETWEEN 1 AND 20),
    created_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (account_id, provider, provider_id)
);
CREATE INDEX title_availability_subscriptions_title_idx
    ON title_availability_subscriptions (provider, provider_id, account_id);

-- +goose Down
DROP TABLE title_availability_subscriptions;
DROP TABLE playback_notification_emissions;
DROP TABLE account_notification_preferences;
DROP TABLE notification_event_recipients;

DELETE FROM notification_outbox WHERE event_type = 'playback.session_started';
DELETE FROM notification_events WHERE event_type = 'playback.session_started';
DELETE FROM notification_channel_subscriptions WHERE event_type = 'playback.session_started';

DROP INDEX notification_outbox_event_channel_recipient_idx;
DELETE FROM notification_outbox AS candidate
USING notification_outbox AS preferred
WHERE preferred.event_id = candidate.event_id
  AND preferred.channel_id = candidate.channel_id
  AND (preferred.recipient_account_id < candidate.recipient_account_id
    OR (preferred.recipient_account_id = candidate.recipient_account_id
      AND preferred.id < candidate.id));
ALTER TABLE notification_outbox DROP CONSTRAINT notification_outbox_recipient_account_id_check;
ALTER TABLE notification_outbox DROP COLUMN recipient_account_id;
CREATE UNIQUE INDEX notification_outbox_event_channel_idx ON notification_outbox (event_id, channel_id);
ALTER TABLE notification_outbox DROP CONSTRAINT notification_outbox_event_type_check;
ALTER TABLE notification_outbox ADD CONSTRAINT notification_outbox_event_type_check
    CHECK (event_type IN ('created', 'approved', 'declined', 'dispatched', 'available', 'failed'));

DROP INDEX notification_events_playback_watch_idx;
ALTER TABLE notification_events DROP COLUMN source_payload_json;
ALTER TABLE notification_events DROP CONSTRAINT notification_events_event_type_check;
ALTER TABLE notification_events ADD CONSTRAINT notification_events_event_type_check
    CHECK (event_type IN ('created', 'approved', 'declined', 'dispatched', 'available', 'failed'));

ALTER TABLE notification_channel_subscriptions DROP CONSTRAINT notification_channel_subscriptions_event_type_check;
ALTER TABLE notification_channel_subscriptions ADD CONSTRAINT notification_channel_subscriptions_event_type_check
    CHECK (event_type IN ('created', 'approved', 'declined', 'dispatched', 'available', 'failed'));
