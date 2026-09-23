-- PostgreSQL migration 00010: invite links, library access, and redemptions.

-- +goose Up
CREATE TABLE invites (
    id TEXT PRIMARY KEY CHECK (length(id) = 36),
    media_server_id TEXT NOT NULL REFERENCES media_servers(id) ON DELETE RESTRICT,
    created_by_account_id TEXT NOT NULL REFERENCES accounts(id) ON DELETE RESTRICT,
    code_hash BYTEA NOT NULL UNIQUE CHECK (octet_length(code_hash) = 32),
    label TEXT NOT NULL CHECK (octet_length(label) BETWEEN 1 AND 100),
    expires_at TIMESTAMPTZ,
    max_uses INTEGER CHECK (max_uses BETWEEN 1 AND 1000),
    use_count INTEGER NOT NULL DEFAULT 0 CHECK (use_count >= 0),
    revoked_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX invites_newest_idx ON invites(created_at DESC, id DESC);

CREATE TABLE invite_libraries (
    invite_id TEXT NOT NULL REFERENCES invites(id) ON DELETE CASCADE,
    library_id TEXT NOT NULL CHECK (octet_length(library_id) BETWEEN 1 AND 128),
    PRIMARY KEY (invite_id, library_id)
);

CREATE TABLE invite_redemptions (
    id TEXT PRIMARY KEY CHECK (length(id) = 36),
    invite_id TEXT NOT NULL REFERENCES invites(id) ON DELETE RESTRICT,
    media_server_id TEXT NOT NULL REFERENCES media_servers(id) ON DELETE RESTRICT,
    media_user_id TEXT NOT NULL CHECK (octet_length(media_user_id) BETWEEN 1 AND 128),
    username TEXT NOT NULL CHECK (octet_length(username) BETWEEN 1 AND 64),
    redeemed_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX invite_redemptions_invite_idx ON invite_redemptions(invite_id, redeemed_at);

CREATE TABLE invite_provisioning_failures (
    id TEXT PRIMARY KEY CHECK (length(id) = 36),
    invite_id TEXT NOT NULL REFERENCES invites(id) ON DELETE RESTRICT,
    media_server_id TEXT NOT NULL REFERENCES media_servers(id) ON DELETE RESTRICT,
    media_user_id TEXT CHECK (media_user_id IS NULL OR octet_length(media_user_id) BETWEEN 1 AND 128),
    username TEXT NOT NULL CHECK (octet_length(username) BETWEEN 1 AND 64),
    reason TEXT NOT NULL CHECK (reason IN ('cleanup_failed', 'ambiguous_create')),
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX invite_provisioning_failures_invite_idx
    ON invite_provisioning_failures(invite_id, created_at);

-- +goose Down
DROP TABLE invite_provisioning_failures;
DROP TABLE invite_redemptions;
DROP TABLE invite_libraries;
DROP TABLE invites;
