-- SQLite migration 00013: download-manager registrations and request fulfilment state.

-- +goose Up
CREATE TABLE download_managers (
    id TEXT PRIMARY KEY CHECK (length(id) = 36),
    kind TEXT NOT NULL CHECK (kind IN ('radarr', 'sonarr')),
    name TEXT NOT NULL UNIQUE CHECK (length(CAST(name AS BLOB)) BETWEEN 1 AND 100),
    name_key TEXT NOT NULL COLLATE BINARY UNIQUE CHECK (length(CAST(name_key AS BLOB)) BETWEEN 1 AND 300),
    base_url TEXT NOT NULL CHECK (length(CAST(base_url AS BLOB)) BETWEEN 1 AND 2048),
    allow_insecure INTEGER NOT NULL CHECK (allow_insecure IN (0, 1)),
    credential_ciphertext BLOB NOT NULL CHECK (length(credential_ciphertext) > 0),
    key_id TEXT NOT NULL CHECK (length(CAST(key_id AS BLOB)) BETWEEN 1 AND 128),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
) STRICT;
CREATE INDEX download_managers_name_key_id_idx ON download_managers (name_key COLLATE BINARY, id);

ALTER TABLE requests ADD COLUMN download_manager_item_id TEXT NOT NULL DEFAULT ''
    CHECK (length(CAST(download_manager_item_id AS BLOB)) <= 100);
ALTER TABLE requests ADD COLUMN failure_reason TEXT NOT NULL DEFAULT ''
    CHECK (length(CAST(failure_reason AS BLOB)) <= 1000);
CREATE INDEX requests_status_updated_idx ON requests(status, updated_at, id);

-- +goose Down
DROP INDEX requests_status_updated_idx;
ALTER TABLE requests DROP COLUMN failure_reason;
ALTER TABLE requests DROP COLUMN download_manager_item_id;
DROP TABLE download_managers;
