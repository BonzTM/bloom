-- PostgreSQL migration 00015: notification channels and durable delivery outbox.

-- +goose Up
CREATE TABLE notification_channels (
    id TEXT PRIMARY KEY CHECK (length(id) = 36),
    kind TEXT NOT NULL CHECK (kind IN ('webhook', 'discord', 'email')),
    name TEXT NOT NULL CHECK (octet_length(name) BETWEEN 1 AND 100),
    name_key TEXT COLLATE "C" NOT NULL CHECK (octet_length(name_key) BETWEEN 1 AND 300),
    target TEXT NOT NULL CHECK (octet_length(target) <= 2048),
    settings_json TEXT NOT NULL CHECK (octet_length(settings_json) BETWEEN 2 AND 16384),
    secret_ciphertext BYTEA NOT NULL CHECK (octet_length(secret_ciphertext) > 0),
    key_id TEXT NOT NULL CHECK (octet_length(key_id) BETWEEN 1 AND 128),
    enabled BOOLEAN NOT NULL,
    degraded_at TIMESTAMPTZ,
    consecutive_failures INTEGER NOT NULL DEFAULT 0 CHECK (consecutive_failures >= 0),
    deleted_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);
CREATE UNIQUE INDEX notification_channels_name_active_idx ON notification_channels (name) WHERE deleted_at IS NULL;
CREATE UNIQUE INDEX notification_channels_name_key_active_idx ON notification_channels (name_key COLLATE "C") WHERE deleted_at IS NULL;
CREATE INDEX notification_channels_name_key_id_idx ON notification_channels (name_key COLLATE "C", id) WHERE deleted_at IS NULL;

CREATE TABLE notification_channel_subscriptions (
    channel_id TEXT NOT NULL REFERENCES notification_channels(id) ON DELETE CASCADE,
    event_type TEXT NOT NULL CHECK (event_type IN ('created', 'approved', 'declined', 'dispatched', 'available', 'failed')),
    PRIMARY KEY (channel_id, event_type)
);
CREATE INDEX notification_subscriptions_event_idx ON notification_channel_subscriptions (event_type, channel_id);

CREATE TABLE notification_events (
    id TEXT PRIMARY KEY CHECK (length(id) = 36),
    event_type TEXT NOT NULL CHECK (event_type IN ('created', 'approved', 'declined', 'dispatched', 'available', 'failed')),
    request_id TEXT NOT NULL CHECK (length(request_id) = 36),
    requester_id TEXT NOT NULL CHECK (length(requester_id) = 36),
    actor_id TEXT NOT NULL,
    media_kind TEXT NOT NULL CHECK (media_kind IN ('movie', 'series')),
    title TEXT NOT NULL CHECK (octet_length(title) <= 4096),
    request_status TEXT NOT NULL CHECK (request_status IN ('pending', 'approved', 'declined', 'processing', 'available', 'failed')),
    reason TEXT NOT NULL CHECK (octet_length(reason) <= 4096),
    event_sequence INTEGER NOT NULL CHECK (event_sequence BETWEEN 0 AND 1),
    occurred_at TIMESTAMPTZ NOT NULL,
    fanned_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX notification_events_fanout_idx ON notification_events (fanned_at, created_at, event_sequence, id);

CREATE TABLE notification_outbox (
    id TEXT PRIMARY KEY CHECK (length(id) = 36),
    event_id TEXT NOT NULL REFERENCES notification_events(id) ON DELETE CASCADE,
    channel_id TEXT NOT NULL REFERENCES notification_channels(id) ON DELETE CASCADE,
    channel_kind TEXT NOT NULL CHECK (channel_kind IN ('webhook', 'discord', 'email')),
    event_type TEXT NOT NULL CHECK (event_type IN ('created', 'approved', 'declined', 'dispatched', 'available', 'failed')),
    payload_json TEXT NOT NULL CHECK (octet_length(payload_json) BETWEEN 2 AND 32768),
    status TEXT NOT NULL CHECK (status IN ('pending', 'sent', 'failed')),
    attempts INTEGER NOT NULL DEFAULT 0 CHECK (attempts BETWEEN 0 AND 8),
    next_attempt_at TIMESTAMPTZ NOT NULL,
    lease_token TEXT NOT NULL DEFAULT '',
    lease_expires_at TIMESTAMPTZ,
    last_error TEXT NOT NULL DEFAULT '' CHECK (octet_length(last_error) <= 512),
    sent_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    CHECK ((lease_token = '' AND lease_expires_at IS NULL) OR (length(lease_token) = 36 AND lease_expires_at IS NOT NULL))
);
CREATE UNIQUE INDEX notification_outbox_event_channel_idx ON notification_outbox (event_id, channel_id);
CREATE INDEX notification_outbox_claim_idx ON notification_outbox (status, next_attempt_at, lease_expires_at, created_at, id);
CREATE INDEX notification_outbox_channel_recent_idx ON notification_outbox (channel_id, created_at DESC, id DESC);

-- +goose Down
DROP TABLE notification_outbox;
DROP TABLE notification_events;
DROP TABLE notification_channel_subscriptions;
DROP TABLE notification_channels;
