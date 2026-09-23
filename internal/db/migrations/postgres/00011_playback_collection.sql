-- PostgreSQL migration 00011: persisted playback watches, active segments, and position samples.

-- +goose Up
-- +goose StatementBegin
CREATE TABLE watches (
    id                TEXT PRIMARY KEY,
    media_server_id   TEXT NOT NULL REFERENCES media_servers(id) ON DELETE CASCADE,
    media_user_id     TEXT NOT NULL,
    username          TEXT NOT NULL,
    device_id         TEXT NOT NULL,
    device_name       TEXT NOT NULL,
    client            TEXT NOT NULL,
    server_session_id TEXT NOT NULL,
    item_id           TEXT NOT NULL,
    item_name         TEXT NOT NULL,
    item_type         TEXT NOT NULL,
    series_name       TEXT NOT NULL,
    season_number     INTEGER,
    episode_number    INTEGER,
    play_method       TEXT NOT NULL CHECK (play_method IN ('direct_play', 'direct_stream', 'transcode', 'unknown')),
    state             TEXT NOT NULL CHECK (state IN ('playing', 'paused', 'stopped')),
    started_at        TIMESTAMPTZ NOT NULL,
    last_seen_at      TIMESTAMPTZ NOT NULL,
    ended_at          TIMESTAMPTZ,
    active_seconds    BIGINT NOT NULL CHECK (active_seconds >= 0),
    last_position_ms  BIGINT NOT NULL CHECK (last_position_ms >= 0),
    source            TEXT NOT NULL CHECK (source IN ('poll', 'websocket', 'webhook', 'import')),
    created_at        TIMESTAMPTZ NOT NULL,
    updated_at        TIMESTAMPTZ NOT NULL,
    CHECK ((state = 'stopped' AND ended_at IS NOT NULL) OR (state <> 'stopped' AND ended_at IS NULL))
);
CREATE INDEX watches_server_state_idx ON watches (media_server_id, state);
CREATE INDEX watches_server_started_idx ON watches (media_server_id, started_at DESC, id DESC);
CREATE INDEX watches_key_idx ON watches (
    media_server_id, media_user_id, device_id, item_id, server_session_id
);

CREATE TABLE watch_segments (
    watch_id   TEXT NOT NULL REFERENCES watches(id) ON DELETE CASCADE,
    started_at TIMESTAMPTZ NOT NULL,
    ended_at   TIMESTAMPTZ,
    source     TEXT NOT NULL CHECK (source IN ('poll', 'websocket', 'webhook', 'import')),
    PRIMARY KEY (watch_id, started_at),
    CHECK (ended_at IS NULL OR ended_at >= started_at)
);
CREATE INDEX watch_segments_watch_idx ON watch_segments (watch_id, started_at);

CREATE TABLE watch_positions (
    watch_id    TEXT NOT NULL REFERENCES watches(id) ON DELETE CASCADE,
    observed_at TIMESTAMPTZ NOT NULL,
    position_ms BIGINT NOT NULL CHECK (position_ms >= 0),
    paused      BOOLEAN NOT NULL,
    play_method TEXT NOT NULL CHECK (play_method IN ('direct_play', 'direct_stream', 'transcode', 'unknown')),
    source      TEXT NOT NULL CHECK (source IN ('poll', 'websocket', 'webhook', 'import')),
    PRIMARY KEY (watch_id, observed_at)
);
CREATE INDEX watch_positions_watch_idx ON watch_positions (watch_id, observed_at DESC);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE watch_positions;
DROP TABLE watch_segments;
DROP TABLE watches;
-- +goose StatementEnd
