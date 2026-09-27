-- SQLite migration 00025: add Jellystat as a history-import source.

-- +goose Up
-- +goose StatementBegin
DROP INDEX watches_userdata_import_record_idx;
DROP INDEX watches_import_record_idx;
ALTER TABLE watches DROP COLUMN import_provenance_guard;
ALTER TABLE watches ADD COLUMN import_source_v3 TEXT
    CHECK (import_source_v3 IS NULL OR import_source_v3 IN ('playback_reporting', 'bloom_export', 'jellyfin_userdata', 'jellystat'));
UPDATE watches SET import_source_v3 = import_source;
ALTER TABLE watches DROP COLUMN import_source;
ALTER TABLE watches RENAME COLUMN import_source_v3 TO import_source;
ALTER TABLE watches ADD COLUMN import_provenance_guard INTEGER
    CHECK ((source = 'import') = (import_source IS NOT NULL AND import_record_id IS NOT NULL));
CREATE UNIQUE INDEX watches_import_record_idx
    ON watches (media_server_id, import_source, import_record_id)
    WHERE import_record_id IS NOT NULL AND import_source <> 'jellyfin_userdata';
CREATE UNIQUE INDEX watches_userdata_import_record_idx
    ON watches (media_server_id, import_source, media_user_id, import_record_id)
    WHERE import_record_id IS NOT NULL AND import_source = 'jellyfin_userdata';

DROP INDEX imports_one_active_idx;
ALTER TABLE imports ADD COLUMN source_v3 TEXT NOT NULL DEFAULT 'playback_reporting'
    CHECK (source_v3 IN ('playback_reporting', 'bloom_export', 'jellyfin_userdata', 'jellystat'));
UPDATE imports SET source_v3 = source;
ALTER TABLE imports DROP COLUMN source;
ALTER TABLE imports RENAME COLUMN source_v3 TO source;
CREATE UNIQUE INDEX imports_one_active_idx ON imports (media_server_id, source)
    WHERE state IN ('pending', 'running');
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DELETE FROM watches WHERE import_source = 'jellystat';
DELETE FROM imports WHERE source = 'jellystat';
DROP INDEX imports_one_active_idx;
ALTER TABLE imports ADD COLUMN source_v2 TEXT NOT NULL DEFAULT 'playback_reporting'
    CHECK (source_v2 IN ('playback_reporting', 'bloom_export', 'jellyfin_userdata'));
UPDATE imports SET source_v2 = source;
ALTER TABLE imports DROP COLUMN source;
ALTER TABLE imports RENAME COLUMN source_v2 TO source;
CREATE UNIQUE INDEX imports_one_active_idx ON imports (media_server_id, source)
    WHERE state IN ('pending', 'running');

DROP INDEX watches_userdata_import_record_idx;
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
-- +goose StatementEnd
