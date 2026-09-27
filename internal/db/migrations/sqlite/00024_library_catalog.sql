-- SQLite migration 00024: type-agnostic library catalog and Jellyfin user-data imports.

-- +goose Up
-- +goose StatementBegin
CREATE TABLE library_items (
    media_server_id   TEXT NOT NULL REFERENCES media_servers(id) ON DELETE CASCADE,
    item_id           TEXT NOT NULL CHECK (length(CAST(item_id AS BLOB)) BETWEEN 1 AND 128),
    library_id        TEXT NOT NULL CHECK (length(CAST(library_id AS BLOB)) BETWEEN 1 AND 128),
    parent_id         TEXT NOT NULL DEFAULT '' CHECK (length(CAST(parent_id AS BLOB)) <= 128),
    item_type         TEXT NOT NULL CHECK (length(CAST(item_type AS BLOB)) BETWEEN 1 AND 500),
    name              TEXT NOT NULL CHECK (length(CAST(name AS BLOB)) BETWEEN 1 AND 500),
    series_id         TEXT NOT NULL DEFAULT '' CHECK (length(CAST(series_id AS BLOB)) <= 128),
    series_name       TEXT NOT NULL DEFAULT '' CHECK (length(CAST(series_name AS BLOB)) <= 500),
    season_id         TEXT NOT NULL DEFAULT '' CHECK (length(CAST(season_id AS BLOB)) <= 128),
    season_number     INTEGER,
    index_number      INTEGER,
    runtime_ms        INTEGER CHECK (runtime_ms IS NULL OR runtime_ms BETWEEN 0 AND 9223372036854),
    premiere_date     TEXT,
    production_year   INTEGER CHECK (production_year IS NULL OR production_year BETWEEN 0 AND 9999),
    community_rating  REAL CHECK (community_rating IS NULL OR community_rating BETWEEN 0 AND 100),
    genres            TEXT NOT NULL DEFAULT '[]' CHECK (length(CAST(genres AS BLOB)) <= 8192),
    primary_image_tag TEXT NOT NULL DEFAULT '' CHECK (length(CAST(primary_image_tag AS BLOB)) <= 256),
    date_created      TEXT,
    archived          INTEGER NOT NULL DEFAULT 0 CHECK (archived IN (0, 1)),
    plays             INTEGER NOT NULL DEFAULT 0 CHECK (plays >= 0),
    watch_seconds     INTEGER NOT NULL DEFAULT 0 CHECK (watch_seconds >= 0),
    unique_users      INTEGER NOT NULL DEFAULT 0 CHECK (unique_users >= 0),
    first_played_at   TEXT,
    last_played_at    TEXT,
    first_seen_at     TEXT NOT NULL,
    last_seen_at      TEXT NOT NULL,
    updated_at        TEXT NOT NULL,
    PRIMARY KEY (media_server_id, item_id)
) STRICT;
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
CREATE INDEX library_items_plays_asc_idx
    ON library_items (media_server_id, library_id, archived, plays, item_id);
CREATE INDEX library_items_plays_desc_idx
    ON library_items (media_server_id, library_id, archived, plays DESC, item_id);
CREATE INDEX library_items_watch_asc_idx
    ON library_items (media_server_id, library_id, archived, watch_seconds, item_id);
CREATE INDEX library_items_watch_desc_idx
    ON library_items (media_server_id, library_id, archived, watch_seconds DESC, item_id);
CREATE INDEX library_items_last_played_asc_idx
    ON library_items (media_server_id, library_id, archived, (last_played_at IS NULL), last_played_at, item_id);
CREATE INDEX library_items_last_played_desc_idx
    ON library_items (media_server_id, library_id, archived, (last_played_at IS NULL), last_played_at DESC, item_id);
CREATE INDEX library_items_type_name_asc_idx
    ON library_items (media_server_id, library_id, archived, item_type, name, item_id);
CREATE INDEX library_items_type_name_desc_idx
    ON library_items (media_server_id, library_id, archived, item_type, name DESC, item_id);
CREATE INDEX library_items_type_date_asc_idx
    ON library_items (media_server_id, library_id, archived, item_type, (date_created IS NULL), date_created, item_id);
CREATE INDEX library_items_type_date_desc_idx
    ON library_items (media_server_id, library_id, archived, item_type, (date_created IS NULL), date_created DESC, item_id);
CREATE INDEX library_items_type_premiere_asc_idx
    ON library_items (media_server_id, library_id, archived, item_type, (premiere_date IS NULL), premiere_date, item_id);
CREATE INDEX library_items_type_premiere_desc_idx
    ON library_items (media_server_id, library_id, archived, item_type, (premiere_date IS NULL), premiere_date DESC, item_id);
CREATE INDEX library_items_type_plays_asc_idx
    ON library_items (media_server_id, library_id, archived, item_type, plays, item_id);
CREATE INDEX library_items_type_plays_desc_idx
    ON library_items (media_server_id, library_id, archived, item_type, plays DESC, item_id);
CREATE INDEX library_items_type_watch_asc_idx
    ON library_items (media_server_id, library_id, archived, item_type, watch_seconds, item_id);
CREATE INDEX library_items_type_watch_desc_idx
    ON library_items (media_server_id, library_id, archived, item_type, watch_seconds DESC, item_id);
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
    item_id         TEXT NOT NULL,
    genre           TEXT NOT NULL CHECK (length(CAST(genre AS BLOB)) BETWEEN 1 AND 500),
    PRIMARY KEY (media_server_id, item_id, genre)
) STRICT;
CREATE INDEX library_item_genres_summary_idx
    ON library_item_genres (media_server_id, genre, item_id);

CREATE TABLE library_syncs (
    media_server_id  TEXT PRIMARY KEY NOT NULL REFERENCES media_servers(id) ON DELETE CASCADE,
    state            TEXT NOT NULL CHECK (state IN ('pending', 'running', 'completed', 'failed')),
    cursor           TEXT NOT NULL DEFAULT '' CHECK (length(CAST(cursor AS BLOB)) <= 512),
    seen_count       INTEGER NOT NULL DEFAULT 0 CHECK (seen_count >= 0),
    upserted_count   INTEGER NOT NULL DEFAULT 0 CHECK (upserted_count >= 0),
    archived_count   INTEGER NOT NULL DEFAULT 0 CHECK (archived_count >= 0),
    last_error       TEXT NOT NULL DEFAULT '' CHECK (length(CAST(last_error AS BLOB)) <= 512),
    lease_token      TEXT NOT NULL DEFAULT '' CHECK (length(CAST(lease_token AS BLOB)) <= 128),
    lease_expires_at TEXT,
    started_at       TEXT,
    finished_at      TEXT
) STRICT;
CREATE INDEX library_syncs_claim_idx ON library_syncs (state, lease_expires_at, finished_at);

ALTER TABLE watches ADD COLUMN series_id TEXT
    CHECK (series_id IS NULL OR length(CAST(series_id AS BLOB)) <= 128);

DROP INDEX watches_import_record_idx;
ALTER TABLE watches DROP COLUMN import_provenance_guard;
ALTER TABLE watches ADD COLUMN import_source_v2 TEXT
    CHECK (import_source_v2 IS NULL OR import_source_v2 IN ('playback_reporting', 'bloom_export', 'jellyfin_userdata'));
UPDATE watches SET import_source_v2 = import_source;
ALTER TABLE watches DROP COLUMN import_source;
ALTER TABLE watches RENAME COLUMN import_source_v2 TO import_source;
ALTER TABLE watches ADD COLUMN import_provenance_guard INTEGER
    CHECK ((source = 'import') = (import_source IS NOT NULL AND import_record_id IS NOT NULL));
CREATE UNIQUE INDEX watches_import_record_idx
    ON watches (media_server_id, import_source, import_record_id)
    WHERE import_record_id IS NOT NULL AND import_source <> 'jellyfin_userdata';
CREATE UNIQUE INDEX watches_userdata_import_record_idx
    ON watches (media_server_id, import_source, media_user_id, import_record_id)
    WHERE import_record_id IS NOT NULL AND import_source = 'jellyfin_userdata';

DROP INDEX imports_one_active_idx;
ALTER TABLE imports ADD COLUMN source_v2 TEXT NOT NULL DEFAULT 'playback_reporting'
    CHECK (source_v2 IN ('playback_reporting', 'bloom_export', 'jellyfin_userdata'));
UPDATE imports SET source_v2 = source;
ALTER TABLE imports DROP COLUMN source;
ALTER TABLE imports RENAME COLUMN source_v2 TO source;
CREATE UNIQUE INDEX imports_one_active_idx ON imports (media_server_id, source)
    WHERE state IN ('pending', 'running');
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DELETE FROM watches WHERE import_source = 'jellyfin_userdata';
DELETE FROM imports WHERE source = 'jellyfin_userdata';
DROP INDEX imports_one_active_idx;
ALTER TABLE imports ADD COLUMN source_v1 TEXT NOT NULL DEFAULT 'playback_reporting'
    CHECK (source_v1 IN ('playback_reporting', 'bloom_export'));
UPDATE imports SET source_v1 = source;
ALTER TABLE imports DROP COLUMN source;
ALTER TABLE imports RENAME COLUMN source_v1 TO source;
CREATE UNIQUE INDEX imports_one_active_idx ON imports (media_server_id, source)
    WHERE state IN ('pending', 'running');

DROP INDEX watches_userdata_import_record_idx;
DROP INDEX watches_import_record_idx;
ALTER TABLE watches DROP COLUMN import_provenance_guard;
ALTER TABLE watches ADD COLUMN import_source_v1 TEXT
    CHECK (import_source_v1 IS NULL OR import_source_v1 IN ('playback_reporting', 'bloom_export'));
UPDATE watches SET import_source_v1 = import_source;
ALTER TABLE watches DROP COLUMN import_source;
ALTER TABLE watches RENAME COLUMN import_source_v1 TO import_source;
ALTER TABLE watches ADD COLUMN import_provenance_guard INTEGER
    CHECK ((source = 'import') = (import_source IS NOT NULL AND import_record_id IS NOT NULL));
CREATE UNIQUE INDEX watches_import_record_idx
    ON watches (media_server_id, import_source, import_record_id)
    WHERE import_record_id IS NOT NULL;
ALTER TABLE watches DROP COLUMN series_id;
DROP TABLE library_syncs;
DROP TABLE library_item_genres;
DROP TABLE library_items;
DROP INDEX watches_catalog_aggregate_idx;
DROP INDEX watches_server_item_started_idx;
-- +goose StatementEnd
