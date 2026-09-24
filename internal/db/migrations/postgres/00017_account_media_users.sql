-- PostgreSQL migration 00017: Bloom accounts linked to media-server users.

-- +goose Up
CREATE TABLE account_media_users (
    account_id TEXT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    media_server_id TEXT NOT NULL REFERENCES media_servers(id) ON DELETE CASCADE,
    media_user_id TEXT NOT NULL CHECK (octet_length(media_user_id) BETWEEN 1 AND 128),
    username TEXT NOT NULL CHECK (octet_length(username) BETWEEN 1 AND 64),
    source TEXT NOT NULL CHECK (source IN ('invite', 'match', 'admin')),
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    suppressed_at TIMESTAMPTZ,
    PRIMARY KEY (account_id, media_server_id),
    UNIQUE (media_server_id, media_user_id)
);

-- +goose Down
DROP TABLE account_media_users;
