-- SQLite migration 00007: registered media servers and encrypted credentials.

-- +goose Up
-- +goose StatementBegin
CREATE TABLE media_servers (
    id                    TEXT PRIMARY KEY NOT NULL,
    kind                  TEXT NOT NULL CHECK (kind IN ('jellyfin')),
    name                  TEXT NOT NULL UNIQUE,
    name_key              TEXT NOT NULL COLLATE BINARY UNIQUE,
    base_url              TEXT NOT NULL,
    allow_insecure        INTEGER NOT NULL CHECK (allow_insecure IN (0, 1)),
    credential_ciphertext BLOB NOT NULL,
    created_at            TEXT NOT NULL,
    updated_at            TEXT NOT NULL
) STRICT;
CREATE INDEX media_servers_name_key_id_idx ON media_servers (name_key COLLATE BINARY, id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE media_servers;
-- +goose StatementEnd
