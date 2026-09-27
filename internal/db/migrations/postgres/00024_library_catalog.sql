-- PostgreSQL migration 00024: type-agnostic library catalog and Jellyfin user-data imports.

-- +goose Up
-- +goose StatementBegin
CREATE TABLE library_items (
    media_server_id   TEXT NOT NULL REFERENCES media_servers(id) ON DELETE CASCADE,
    item_id           TEXT COLLATE "C" NOT NULL CHECK (octet_length(item_id) BETWEEN 1 AND 128),
    library_id        TEXT COLLATE "C" NOT NULL CHECK (octet_length(library_id) BETWEEN 1 AND 128),
    parent_id         TEXT COLLATE "C" NOT NULL DEFAULT '' CHECK (octet_length(parent_id) <= 128),
    item_type         TEXT COLLATE "C" NOT NULL CHECK (octet_length(item_type) BETWEEN 1 AND 500),
    name              TEXT COLLATE "C" NOT NULL CHECK (octet_length(name) BETWEEN 1 AND 500),
    series_id         TEXT COLLATE "C" NOT NULL DEFAULT '' CHECK (octet_length(series_id) <= 128),
    series_name       TEXT COLLATE "C" NOT NULL DEFAULT '' CHECK (octet_length(series_name) <= 500),
    season_id         TEXT COLLATE "C" NOT NULL DEFAULT '' CHECK (octet_length(season_id) <= 128),
    season_number     INTEGER,
    index_number      INTEGER,
    runtime_ms        BIGINT CHECK (runtime_ms IS NULL OR runtime_ms BETWEEN 0 AND 9223372036854),
    premiere_date     TIMESTAMPTZ,
    production_year   INTEGER CHECK (production_year IS NULL OR production_year BETWEEN 0 AND 9999),
    community_rating  DOUBLE PRECISION CHECK (community_rating IS NULL OR community_rating BETWEEN 0 AND 100),
    genres            TEXT NOT NULL DEFAULT '[]' CHECK (octet_length(genres) <= 8192),
    primary_image_tag TEXT COLLATE "C" NOT NULL DEFAULT '' CHECK (octet_length(primary_image_tag) <= 256),
    date_created      TIMESTAMPTZ,
    archived          BOOLEAN NOT NULL DEFAULT FALSE,
    plays              BIGINT NOT NULL DEFAULT 0 CHECK (plays >= 0),
    watch_seconds      BIGINT NOT NULL DEFAULT 0 CHECK (watch_seconds >= 0),
    unique_users       BIGINT NOT NULL DEFAULT 0 CHECK (unique_users >= 0),
    first_played_at    TIMESTAMPTZ,
    last_played_at     TIMESTAMPTZ,
    first_seen_at     TIMESTAMPTZ NOT NULL,
    last_seen_at      TIMESTAMPTZ NOT NULL,
    updated_at        TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (media_server_id, item_id)
);
CREATE INDEX library_items_name_asc_idx
    ON library_items (media_server_id, library_id, archived, name, item_id);
CREATE INDEX library_items_name_desc_idx
    ON library_items (media_server_id, library_id, archived, name DESC, item_id);
CREATE INDEX library_items_date_asc_idx
    ON library_items (media_server_id, library_id, archived, (date_created IS NULL), date_created, item_id);
CREATE INDEX library_items_date_desc_idx
    ON library_items (media_server_id, library_id, archived, (date_created IS NULL), date_created DESC, item_id);
CREATE INDEX library_items_premiere_asc_idx
    ON library_items (media_server_id, library_id, archived, (premiere_date IS NULL), premiere_date, item_id);
CREATE INDEX library_items_premiere_desc_idx
    ON library_items (media_server_id, library_id, archived, (premiere_date IS NULL), premiere_date DESC, item_id);
CREATE INDEX library_items_plays_asc_idx ON library_items (media_server_id, library_id, archived, plays, item_id);
CREATE INDEX library_items_plays_desc_idx ON library_items (media_server_id, library_id, archived, plays DESC, item_id);
CREATE INDEX library_items_watch_asc_idx ON library_items (media_server_id, library_id, archived, watch_seconds, item_id);
CREATE INDEX library_items_watch_desc_idx ON library_items (media_server_id, library_id, archived, watch_seconds DESC, item_id);
CREATE INDEX library_items_last_played_asc_idx
    ON library_items (media_server_id, library_id, archived, (last_played_at IS NULL), last_played_at, item_id);
CREATE INDEX library_items_last_played_desc_idx
    ON library_items (media_server_id, library_id, archived, (last_played_at IS NULL), last_played_at DESC, item_id);
CREATE INDEX library_items_type_name_asc_idx ON library_items (media_server_id, library_id, archived, item_type, name, item_id);
CREATE INDEX library_items_type_name_desc_idx ON library_items (media_server_id, library_id, archived, item_type, name DESC, item_id);
CREATE INDEX library_items_type_date_asc_idx
    ON library_items (media_server_id, library_id, archived, item_type, (date_created IS NULL), date_created, item_id);
CREATE INDEX library_items_type_date_desc_idx
    ON library_items (media_server_id, library_id, archived, item_type, (date_created IS NULL), date_created DESC, item_id);
CREATE INDEX library_items_type_premiere_asc_idx
    ON library_items (media_server_id, library_id, archived, item_type, (premiere_date IS NULL), premiere_date, item_id);
CREATE INDEX library_items_type_premiere_desc_idx
    ON library_items (media_server_id, library_id, archived, item_type, (premiere_date IS NULL), premiere_date DESC, item_id);
CREATE INDEX library_items_type_plays_asc_idx ON library_items (media_server_id, library_id, archived, item_type, plays, item_id);
CREATE INDEX library_items_type_plays_desc_idx ON library_items (media_server_id, library_id, archived, item_type, plays DESC, item_id);
CREATE INDEX library_items_type_watch_asc_idx ON library_items (media_server_id, library_id, archived, item_type, watch_seconds, item_id);
CREATE INDEX library_items_type_watch_desc_idx ON library_items (media_server_id, library_id, archived, item_type, watch_seconds DESC, item_id);
CREATE INDEX library_items_type_last_played_asc_idx
    ON library_items (media_server_id, library_id, archived, item_type, (last_played_at IS NULL), last_played_at, item_id);
CREATE INDEX library_items_type_last_played_desc_idx
    ON library_items (media_server_id, library_id, archived, item_type, (last_played_at IS NULL), last_played_at DESC, item_id);
CREATE INDEX library_items_series_idx
    ON library_items (media_server_id, series_id, archived, season_number, index_number);
CREATE INDEX library_items_season_idx
    ON library_items (media_server_id, season_id, archived, index_number);
CREATE INDEX library_items_archived_idx
    ON library_items (media_server_id, archived, last_seen_at);
CREATE INDEX library_items_stale_idx
    ON library_items (media_server_id, library_id, archived, (last_played_at IS NOT NULL), last_played_at, name, item_id);
CREATE INDEX watches_server_item_started_idx
    ON watches (media_server_id, item_id, started_at DESC);
CREATE INDEX watches_catalog_aggregate_idx
    ON watches (media_server_id, item_id, started_at, active_seconds, media_user_id);

CREATE TABLE library_item_genres (
    media_server_id TEXT NOT NULL REFERENCES media_servers(id) ON DELETE CASCADE,
    item_id         TEXT COLLATE "C" NOT NULL,
    genre           TEXT COLLATE "C" NOT NULL CHECK (octet_length(genre) BETWEEN 1 AND 500),
    PRIMARY KEY (media_server_id, item_id, genre)
);
CREATE INDEX library_item_genres_summary_idx
    ON library_item_genres (media_server_id, genre, item_id);

CREATE TABLE library_syncs (
    media_server_id  TEXT PRIMARY KEY REFERENCES media_servers(id) ON DELETE CASCADE,
    state            TEXT COLLATE "C" NOT NULL CHECK (state IN ('pending', 'running', 'completed', 'failed')),
    cursor           TEXT COLLATE "C" NOT NULL DEFAULT '' CHECK (octet_length(cursor) <= 512),
    seen_count       BIGINT NOT NULL DEFAULT 0 CHECK (seen_count >= 0),
    upserted_count   BIGINT NOT NULL DEFAULT 0 CHECK (upserted_count >= 0),
    archived_count   BIGINT NOT NULL DEFAULT 0 CHECK (archived_count >= 0),
    last_error       TEXT COLLATE "C" NOT NULL DEFAULT '' CHECK (octet_length(last_error) <= 512),
    lease_token      TEXT COLLATE "C" NOT NULL DEFAULT '' CHECK (octet_length(lease_token) <= 128),
    lease_expires_at TIMESTAMPTZ,
    started_at       TIMESTAMPTZ,
    finished_at      TIMESTAMPTZ
);
CREATE INDEX library_syncs_claim_idx ON library_syncs (state, lease_expires_at, finished_at);

ALTER TABLE watches ADD COLUMN series_id TEXT COLLATE "C"
    CHECK (series_id IS NULL OR octet_length(series_id) <= 128);
ALTER TABLE watches DROP CONSTRAINT watches_import_source_check;
ALTER TABLE watches ADD CONSTRAINT watches_import_source_check
    CHECK (import_source IS NULL OR import_source IN ('playback_reporting', 'bloom_export', 'jellyfin_userdata'));
DROP INDEX watches_import_record_idx;
CREATE UNIQUE INDEX watches_import_record_idx
    ON watches (media_server_id, import_source, import_record_id)
    WHERE import_record_id IS NOT NULL AND import_source <> 'jellyfin_userdata';
CREATE UNIQUE INDEX watches_userdata_import_record_idx
    ON watches (media_server_id, import_source, media_user_id, import_record_id)
    WHERE import_record_id IS NOT NULL AND import_source = 'jellyfin_userdata';
ALTER TABLE imports DROP CONSTRAINT imports_source_check;
ALTER TABLE imports ADD CONSTRAINT imports_source_check
    CHECK (source IN ('playback_reporting', 'bloom_export', 'jellyfin_userdata'));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DELETE FROM watches WHERE import_source = 'jellyfin_userdata';
DELETE FROM imports WHERE source = 'jellyfin_userdata';
ALTER TABLE imports DROP CONSTRAINT imports_source_check;
ALTER TABLE imports ADD CONSTRAINT imports_source_check
    CHECK (source IN ('playback_reporting', 'bloom_export'));
DROP INDEX watches_userdata_import_record_idx;
DROP INDEX watches_import_record_idx;
CREATE UNIQUE INDEX watches_import_record_idx
    ON watches (media_server_id, import_source, import_record_id)
    WHERE import_record_id IS NOT NULL;
ALTER TABLE watches DROP CONSTRAINT watches_import_source_check;
ALTER TABLE watches ADD CONSTRAINT watches_import_source_check
    CHECK (import_source IS NULL OR import_source IN ('playback_reporting', 'bloom_export'));
ALTER TABLE watches DROP COLUMN series_id;
DROP TABLE library_syncs;
DROP TABLE library_item_genres;
DROP TABLE library_items;
DROP INDEX watches_catalog_aggregate_idx;
DROP INDEX watches_server_item_started_idx;
-- +goose StatementEnd
