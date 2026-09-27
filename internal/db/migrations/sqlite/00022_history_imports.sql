-- SQLite migration 00022: resumable history imports and watch provenance.

-- +goose Up
-- +goose StatementBegin
CREATE TABLE imports (
    id               TEXT PRIMARY KEY NOT NULL,
    media_server_id  TEXT NOT NULL REFERENCES media_servers(id) ON DELETE CASCADE,
    source           TEXT NOT NULL CHECK (source IN ('playback_reporting', 'bloom_export')),
    state            TEXT NOT NULL CHECK (state IN ('pending', 'running', 'completed', 'failed', 'cancelled')),
    cursor           TEXT NOT NULL DEFAULT '' CHECK (length(CAST(cursor AS BLOB)) <= 512),
    read_count       INTEGER NOT NULL DEFAULT 0 CHECK (read_count >= 0),
    imported_count   INTEGER NOT NULL DEFAULT 0 CHECK (imported_count >= 0),
    skipped_count    INTEGER NOT NULL DEFAULT 0 CHECK (skipped_count >= 0),
    duplicate_count  INTEGER NOT NULL DEFAULT 0 CHECK (duplicate_count >= 0),
    last_error       TEXT NOT NULL DEFAULT '' CHECK (length(CAST(last_error AS BLOB)) <= 512),
    lease_token      TEXT NOT NULL DEFAULT '' CHECK (length(CAST(lease_token AS BLOB)) <= 128),
    lease_expires_at TEXT,
    requested_by     TEXT NOT NULL REFERENCES accounts(id) ON DELETE RESTRICT,
    created_at       TEXT NOT NULL,
    started_at       TEXT,
    finished_at      TEXT,
    updated_at       TEXT NOT NULL
) STRICT;
CREATE INDEX imports_created_idx ON imports (created_at DESC, id DESC);
CREATE INDEX imports_claim_idx ON imports (state, lease_expires_at, created_at);
CREATE UNIQUE INDEX imports_one_active_idx ON imports (media_server_id, source)
    WHERE state IN ('pending', 'running');

ALTER TABLE watches ADD COLUMN import_source TEXT
    CHECK (import_source IS NULL OR import_source IN ('playback_reporting', 'bloom_export'));
ALTER TABLE watches ADD COLUMN import_record_id TEXT
    CHECK (import_record_id IS NULL OR length(CAST(import_record_id AS BLOB)) <= 256);
UPDATE watches
SET import_source = 'bloom_export', import_record_id = id
WHERE source = 'import';
-- SQLite cannot add a table constraint after the columns exist. This inert
-- column adds the cross-column check only after legacy rows are backfilled.
ALTER TABLE watches ADD COLUMN import_provenance_guard INTEGER
    CHECK ((source = 'import') = (import_source IS NOT NULL AND import_record_id IS NOT NULL));
CREATE UNIQUE INDEX watches_import_record_idx
    ON watches (media_server_id, import_source, import_record_id)
    WHERE import_record_id IS NOT NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX watches_import_record_idx;
ALTER TABLE watches DROP COLUMN import_provenance_guard;
ALTER TABLE watches DROP COLUMN import_record_id;
ALTER TABLE watches DROP COLUMN import_source;
DROP TABLE imports;
-- +goose StatementEnd
