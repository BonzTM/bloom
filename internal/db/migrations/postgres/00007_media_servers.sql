-- PostgreSQL migration 00007: registered media servers and encrypted credentials.

-- +goose Up
-- +goose StatementBegin
CREATE TABLE media_servers (
    id                    TEXT PRIMARY KEY,
    kind                  TEXT NOT NULL CHECK (kind IN ('jellyfin')),
    name                  TEXT NOT NULL UNIQUE,
    name_key              TEXT COLLATE "C" NOT NULL UNIQUE,
    base_url              TEXT NOT NULL,
    allow_insecure        BOOLEAN NOT NULL,
    credential_ciphertext BYTEA NOT NULL,
    created_at            TIMESTAMPTZ NOT NULL,
    updated_at            TIMESTAMPTZ NOT NULL
);
CREATE INDEX media_servers_name_key_id_idx ON media_servers (name_key COLLATE "C", id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE media_servers;
-- +goose StatementEnd
