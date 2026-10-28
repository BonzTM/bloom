-- SQLite migration 00031: let metadata_providers hold an OMDb ratings key.
-- SQLite cannot alter a CHECK, so the table is rebuilt with its rows.
-- Older binaries read only the 'tmdb' row, so the wider check is safe to roll out.

-- +goose Up
ALTER TABLE metadata_providers RENAME TO metadata_providers_old;
CREATE TABLE metadata_providers (
    kind TEXT PRIMARY KEY CHECK (kind IN ('tmdb', 'omdb')),
    credential_ciphertext BLOB NOT NULL CHECK (length(credential_ciphertext) > 0),
    key_id TEXT NOT NULL CHECK (length(CAST(key_id AS BLOB)) BETWEEN 1 AND 128),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
INSERT INTO metadata_providers (kind, credential_ciphertext, key_id, created_at, updated_at)
SELECT kind, credential_ciphertext, key_id, created_at, updated_at FROM metadata_providers_old;
DROP TABLE metadata_providers_old;

-- +goose Down
ALTER TABLE metadata_providers RENAME TO metadata_providers_old;
CREATE TABLE metadata_providers (
    kind TEXT PRIMARY KEY CHECK (kind IN ('tmdb')),
    credential_ciphertext BLOB NOT NULL CHECK (length(credential_ciphertext) > 0),
    key_id TEXT NOT NULL CHECK (length(CAST(key_id AS BLOB)) BETWEEN 1 AND 128),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
INSERT INTO metadata_providers (kind, credential_ciphertext, key_id, created_at, updated_at)
SELECT kind, credential_ciphertext, key_id, created_at, updated_at FROM metadata_providers_old
WHERE kind = 'tmdb';
DROP TABLE metadata_providers_old;
