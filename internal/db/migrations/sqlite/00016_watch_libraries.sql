-- SQLite migration 00016: attach collection-folder identity to playback watches.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE watches ADD COLUMN library_id TEXT NOT NULL DEFAULT ''
    CHECK (length(CAST(library_id AS BLOB)) <= 128);
ALTER TABLE watches ADD COLUMN library_name TEXT NOT NULL DEFAULT ''
    CHECK (length(CAST(library_name AS BLOB)) <= 500);
CREATE INDEX watches_server_library_started_idx
    ON watches (media_server_id, library_id, started_at DESC);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX watches_server_library_started_idx;
ALTER TABLE watches DROP COLUMN library_name;
ALTER TABLE watches DROP COLUMN library_id;
-- +goose StatementEnd
