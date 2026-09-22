-- SQLite migration 00005: contract canonical username storage.
-- Sequence: 00003 adds nullable storage and a rollback backup; the
-- provider-registered Go migration 00004 computes PRECIS UsernameCaseMapped
-- keys; 00005 makes every key required and exactly unique.

-- +goose Up
-- +goose StatementBegin
CREATE TABLE accounts_canonical_new (
    id            TEXT PRIMARY KEY NOT NULL,
    username      TEXT NOT NULL UNIQUE,
    created_at    TEXT NOT NULL,
    password_hash TEXT CHECK (password_hash IS NULL OR length(password_hash) <= 128),
    disabled      INTEGER NOT NULL DEFAULT 0 CHECK (disabled IN (0, 1)),
    username_key  TEXT NOT NULL
) STRICT;
INSERT INTO accounts_canonical_new (id, username, created_at, password_hash, disabled, username_key)
SELECT id, username, created_at, password_hash, disabled, username_key FROM accounts;
DROP TABLE accounts;
ALTER TABLE accounts_canonical_new RENAME TO accounts;
CREATE UNIQUE INDEX accounts_username_key_idx ON accounts (username_key);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX accounts_username_key_idx;
CREATE TABLE accounts_canonical_old (
    id            TEXT PRIMARY KEY NOT NULL,
    username      TEXT NOT NULL UNIQUE,
    created_at    TEXT NOT NULL,
    password_hash TEXT CHECK (password_hash IS NULL OR length(password_hash) <= 128),
    disabled      INTEGER NOT NULL DEFAULT 0 CHECK (disabled IN (0, 1)),
    username_key  TEXT
) STRICT;
INSERT INTO accounts_canonical_old (id, username, created_at, password_hash, disabled, username_key)
SELECT id, username, created_at, password_hash, disabled, username_key FROM accounts;
DROP TABLE accounts;
ALTER TABLE accounts_canonical_old RENAME TO accounts;
-- +goose StatementEnd
