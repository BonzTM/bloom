-- PostgreSQL migration 00013: download-manager registrations and request fulfilment state.

-- +goose Up
CREATE TABLE download_managers (
    id TEXT PRIMARY KEY CHECK (length(id) = 36),
    kind TEXT NOT NULL CHECK (kind IN ('radarr', 'sonarr')),
    name TEXT NOT NULL UNIQUE CHECK (octet_length(name) BETWEEN 1 AND 100),
    name_key TEXT NOT NULL UNIQUE CHECK (octet_length(name_key) BETWEEN 1 AND 300),
    base_url TEXT NOT NULL CHECK (octet_length(base_url) BETWEEN 1 AND 2048),
    allow_insecure BOOLEAN NOT NULL,
    credential_ciphertext BYTEA NOT NULL CHECK (octet_length(credential_ciphertext) > 0),
    key_id TEXT NOT NULL CHECK (octet_length(key_id) BETWEEN 1 AND 128),
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX download_managers_name_key_id_idx ON download_managers (name_key, id);

ALTER TABLE requests ADD COLUMN download_manager_item_id TEXT NOT NULL DEFAULT ''
    CHECK (octet_length(download_manager_item_id) <= 100);
ALTER TABLE requests ADD COLUMN failure_reason TEXT NOT NULL DEFAULT ''
    CHECK (octet_length(failure_reason) <= 1000);
CREATE INDEX requests_status_updated_idx ON requests(status, updated_at, id);

-- +goose Down
DROP INDEX requests_status_updated_idx;
ALTER TABLE requests DROP COLUMN failure_reason;
ALTER TABLE requests DROP COLUMN download_manager_item_id;
DROP TABLE download_managers;
