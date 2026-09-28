-- PostgreSQL migration 00026: preserve upstream history-import provenance.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE watches
    ADD COLUMN import_origin_record_id TEXT COLLATE "C"
        CHECK (import_origin_record_id IS NULL OR octet_length(import_origin_record_id) <= 128);
-- The old Jellystat importer replaced each imported activity ID with this
-- marker. Its original Jellystat activity ID cannot be recovered, so retain
-- import_record_id and copy only encoded Playback Reporting row IDs of 1..128
-- bytes. Longer suffixes remain with NULL origin provenance.
UPDATE watches
SET import_origin_record_id = substring(import_record_id FROM 8)
WHERE import_source = 'jellystat'
  AND import_record_id LIKE 'plugin:%'
  AND octet_length(substring(import_record_id FROM 8)) BETWEEN 1 AND 128;
CREATE INDEX watches_import_origin_record_idx
    ON watches (media_server_id, import_origin_record_id)
    WHERE import_origin_record_id IS NOT NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX watches_import_origin_record_idx;
ALTER TABLE watches DROP COLUMN import_origin_record_id;
-- +goose StatementEnd
