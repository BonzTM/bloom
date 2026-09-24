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
ALTER TABLE requests ADD COLUMN download_manager_id TEXT NOT NULL DEFAULT ''
    CHECK (download_manager_id = '' OR length(download_manager_id) = 36);
ALTER TABLE requests ADD COLUMN dispatch_quality_profile TEXT NOT NULL DEFAULT ''
    CHECK (length(CAST(dispatch_quality_profile AS BLOB)) <= 500);
ALTER TABLE requests ADD COLUMN dispatch_root_folder TEXT NOT NULL DEFAULT ''
    CHECK (length(CAST(dispatch_root_folder AS BLOB)) <= 500);
ALTER TABLE requests ADD COLUMN dispatch_tags TEXT NOT NULL DEFAULT '[]'
    CHECK (length(CAST(dispatch_tags AS BLOB)) <= 32768);
ALTER TABLE requests ADD COLUMN last_availability_check_at TEXT;
CREATE INDEX requests_availability_idx ON requests(status, last_availability_check_at, created_at, id);

-- +goose Down
DROP INDEX requests_availability_idx;
ALTER TABLE requests DROP COLUMN last_availability_check_at;
ALTER TABLE requests DROP COLUMN dispatch_tags;
ALTER TABLE requests DROP COLUMN dispatch_root_folder;
ALTER TABLE requests DROP COLUMN dispatch_quality_profile;
ALTER TABLE requests DROP COLUMN download_manager_id;
ALTER TABLE requests DROP COLUMN failure_reason;
ALTER TABLE requests DROP COLUMN download_manager_item_id;
DROP TABLE download_managers;
