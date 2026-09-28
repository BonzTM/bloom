-- PostgreSQL migration 00025: add Jellystat as a history-import source.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE imports DROP CONSTRAINT imports_source_check;
ALTER TABLE imports ADD CONSTRAINT imports_source_check
    CHECK (source IN ('playback_reporting', 'bloom_export', 'jellyfin_userdata', 'jellystat'));
ALTER TABLE watches DROP CONSTRAINT watches_import_source_check;
ALTER TABLE watches ADD CONSTRAINT watches_import_source_check
    CHECK (import_source IS NULL OR import_source IN ('playback_reporting', 'bloom_export', 'jellyfin_userdata', 'jellystat'));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DELETE FROM watches WHERE import_source = 'jellystat';
DELETE FROM imports WHERE source = 'jellystat';
ALTER TABLE imports DROP CONSTRAINT imports_source_check;
ALTER TABLE imports ADD CONSTRAINT imports_source_check
    CHECK (source IN ('playback_reporting', 'bloom_export', 'jellyfin_userdata'));
ALTER TABLE watches DROP CONSTRAINT watches_import_source_check;
ALTER TABLE watches ADD CONSTRAINT watches_import_source_check
    CHECK (import_source IS NULL OR import_source IN ('playback_reporting', 'bloom_export', 'jellyfin_userdata'));
-- +goose StatementEnd
