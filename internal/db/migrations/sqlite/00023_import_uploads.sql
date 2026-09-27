-- SQLite migration 00023: database-backed import upload chunks.

-- +goose Up
-- +goose StatementBegin
CREATE TABLE import_uploads (
    id          TEXT NOT NULL,
    import_id   TEXT REFERENCES imports(id) ON DELETE CASCADE,
    chunk_index INTEGER NOT NULL CHECK (chunk_index >= 0),
    bytes       BLOB NOT NULL CHECK (length(bytes) <= 1048576),
    created_at  TEXT NOT NULL,
    PRIMARY KEY (id, chunk_index)
) STRICT;
CREATE INDEX import_uploads_orphan_idx
    ON import_uploads (created_at, id) WHERE import_id IS NULL;
CREATE INDEX import_uploads_import_idx ON import_uploads (import_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE import_uploads;
-- +goose StatementEnd
