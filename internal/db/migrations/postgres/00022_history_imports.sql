-- PostgreSQL migration 00022: resumable history imports and watch provenance.

-- +goose Up
-- +goose StatementBegin
CREATE TABLE imports (
    id               TEXT PRIMARY KEY,
    media_server_id  TEXT NOT NULL REFERENCES media_servers(id) ON DELETE CASCADE,
    source           TEXT COLLATE "C" NOT NULL CHECK (source IN ('playback_reporting', 'bloom_export')),
    state            TEXT COLLATE "C" NOT NULL CHECK (state IN ('pending', 'running', 'completed', 'failed', 'cancelled')),
    cursor           TEXT COLLATE "C" NOT NULL DEFAULT '' CHECK (octet_length(cursor) <= 512),
    read_count       BIGINT NOT NULL DEFAULT 0 CHECK (read_count >= 0),
    imported_count   BIGINT NOT NULL DEFAULT 0 CHECK (imported_count >= 0),
    skipped_count    BIGINT NOT NULL DEFAULT 0 CHECK (skipped_count >= 0),
    duplicate_count  BIGINT NOT NULL DEFAULT 0 CHECK (duplicate_count >= 0),
    last_error       TEXT COLLATE "C" NOT NULL DEFAULT '' CHECK (octet_length(last_error) <= 512),
    lease_token      TEXT COLLATE "C" NOT NULL DEFAULT '' CHECK (octet_length(lease_token) <= 128),
    lease_expires_at TIMESTAMPTZ,
    requested_by     TEXT NOT NULL REFERENCES accounts(id) ON DELETE RESTRICT,
    created_at       TIMESTAMPTZ NOT NULL,
    started_at       TIMESTAMPTZ,
    finished_at      TIMESTAMPTZ,
    updated_at       TIMESTAMPTZ NOT NULL
);
CREATE INDEX imports_created_idx ON imports (created_at DESC, id DESC);
CREATE INDEX imports_claim_idx ON imports (state, lease_expires_at, created_at);
CREATE UNIQUE INDEX imports_one_active_idx ON imports (media_server_id, source)
    WHERE state IN ('pending', 'running');

ALTER TABLE watches
    ADD COLUMN import_source TEXT COLLATE "C"
        CHECK (import_source IS NULL OR import_source IN ('playback_reporting', 'bloom_export')),
    ADD COLUMN import_record_id TEXT COLLATE "C"
        CHECK (import_record_id IS NULL OR octet_length(import_record_id) <= 256);
UPDATE watches
SET import_source = 'bloom_export', import_record_id = id
WHERE source = 'import';
-- Keep the physical columns identical to SQLite's guarded migration.
ALTER TABLE watches ADD COLUMN import_provenance_guard INTEGER,
    ADD CONSTRAINT watches_import_provenance_check
    CHECK ((source = 'import') = (import_source IS NOT NULL AND import_record_id IS NOT NULL));
CREATE UNIQUE INDEX watches_import_record_idx
    ON watches (media_server_id, import_source, import_record_id)
    WHERE import_record_id IS NOT NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX watches_import_record_idx;
ALTER TABLE watches DROP CONSTRAINT watches_import_provenance_check;
ALTER TABLE watches DROP COLUMN import_provenance_guard,
    DROP COLUMN import_record_id, DROP COLUMN import_source;
DROP TABLE imports;
-- +goose StatementEnd
