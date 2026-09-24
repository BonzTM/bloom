-- PostgreSQL migration 00016: attach collection-folder identity to playback watches.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE watches
    ADD COLUMN library_id TEXT COLLATE "C" NOT NULL DEFAULT ''
        CHECK (octet_length(library_id) <= 128),
    ADD COLUMN library_name TEXT COLLATE "C" NOT NULL DEFAULT ''
        CHECK (octet_length(library_name) <= 500);
CREATE INDEX watches_server_library_started_idx
    ON watches (media_server_id, library_id, started_at DESC);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX watches_server_library_started_idx;
ALTER TABLE watches DROP COLUMN library_name, DROP COLUMN library_id;
-- +goose StatementEnd
