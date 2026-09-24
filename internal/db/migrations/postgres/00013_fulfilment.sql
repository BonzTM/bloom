-- PostgreSQL migration 00013: download-manager registrations and request fulfilment state.

-- +goose Up
CREATE TABLE download_managers (
    id TEXT PRIMARY KEY CHECK (length(id) = 36),
    kind TEXT NOT NULL CHECK (kind IN ('radarr', 'sonarr')),
    name TEXT NOT NULL UNIQUE CHECK (octet_length(name) BETWEEN 1 AND 100),
    name_key TEXT COLLATE "C" NOT NULL UNIQUE CHECK (octet_length(name_key) BETWEEN 1 AND 300),
    base_url TEXT NOT NULL CHECK (octet_length(base_url) BETWEEN 1 AND 2048),
    allow_insecure BOOLEAN NOT NULL,
    credential_ciphertext BYTEA NOT NULL CHECK (octet_length(credential_ciphertext) > 0),
    key_id TEXT NOT NULL CHECK (octet_length(key_id) BETWEEN 1 AND 128),
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX download_managers_name_key_id_idx ON download_managers (name_key COLLATE "C", id);

ALTER TABLE requests ADD COLUMN download_manager_item_id TEXT NOT NULL DEFAULT ''
    CHECK (octet_length(download_manager_item_id) <= 100);
ALTER TABLE requests ADD COLUMN failure_reason TEXT NOT NULL DEFAULT ''
    CHECK (octet_length(failure_reason) <= 1000);
ALTER TABLE requests ADD COLUMN download_manager_id TEXT NOT NULL DEFAULT ''
    CHECK (download_manager_id = '' OR length(download_manager_id) = 36);
ALTER TABLE requests ADD COLUMN dispatch_quality_profile TEXT NOT NULL DEFAULT ''
    CHECK (octet_length(dispatch_quality_profile) <= 500);
ALTER TABLE requests ADD COLUMN dispatch_root_folder TEXT NOT NULL DEFAULT ''
    CHECK (octet_length(dispatch_root_folder) <= 500);
ALTER TABLE requests ADD COLUMN dispatch_tags TEXT NOT NULL DEFAULT '[]'
    CHECK (octet_length(dispatch_tags) <= 32768);
ALTER TABLE requests ADD COLUMN dispatch_lease_expires_at TIMESTAMPTZ;
ALTER TABLE requests ADD COLUMN dispatch_lease_token TEXT NOT NULL DEFAULT ''
    CHECK ((dispatch_lease_token = '' AND dispatch_lease_expires_at IS NULL)
        OR (length(dispatch_lease_token) = 36 AND dispatch_lease_expires_at IS NOT NULL));
ALTER TABLE requests ADD COLUMN last_availability_check_at TIMESTAMPTZ;
CREATE INDEX requests_availability_idx ON requests(status, last_availability_check_at, created_at, id);

-- +goose Down
DROP INDEX requests_availability_idx;
ALTER TABLE requests DROP COLUMN last_availability_check_at;
ALTER TABLE requests DROP COLUMN dispatch_lease_token;
ALTER TABLE requests DROP COLUMN dispatch_lease_expires_at;
ALTER TABLE requests DROP COLUMN dispatch_tags;
ALTER TABLE requests DROP COLUMN dispatch_root_folder;
ALTER TABLE requests DROP COLUMN dispatch_quality_profile;
ALTER TABLE requests DROP COLUMN download_manager_id;
ALTER TABLE requests DROP COLUMN failure_reason;
ALTER TABLE requests DROP COLUMN download_manager_item_id;
DROP TABLE download_managers;
