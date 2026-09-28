-- SQLite migration 00026: preserve upstream history-import provenance.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE watches ADD COLUMN import_origin_record_id TEXT
    CHECK (import_origin_record_id IS NULL OR length(CAST(import_origin_record_id AS BLOB)) <= 128);
-- The old Jellystat importer replaced each imported activity ID with this
-- marker. Its original Jellystat activity ID cannot be recovered, so retain
-- import_record_id and copy only encoded Playback Reporting row IDs of 1..128
-- bytes. Longer suffixes remain with NULL origin provenance.
UPDATE watches
SET import_origin_record_id = substr(import_record_id, 8)
WHERE import_source = 'jellystat'
  AND substr(import_record_id, 1, 7) = 'plugin:'
  AND length(CAST(substr(import_record_id, 8) AS BLOB)) BETWEEN 1 AND 128;
CREATE INDEX watches_import_origin_record_idx
    ON watches (media_server_id, import_origin_record_id)
    WHERE import_origin_record_id IS NOT NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX watches_import_origin_record_idx;
ALTER TABLE watches DROP COLUMN import_origin_record_id;
-- +goose StatementEnd
