-- SQLite migration 00002: local credentials and SCS browser sessions.
-- Expiry is normalized UTC Unix microseconds for exact cross-engine parity.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE accounts ADD COLUMN password_hash TEXT CHECK (password_hash IS NULL OR length(password_hash) <= 128);
ALTER TABLE accounts ADD COLUMN disabled INTEGER NOT NULL DEFAULT 0 CHECK (disabled IN (0, 1));

CREATE TABLE sessions (
    token  TEXT PRIMARY KEY,
    data   BLOB NOT NULL,
    expiry INTEGER NOT NULL
) STRICT;
CREATE INDEX sessions_expiry_idx ON sessions (expiry);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE sessions;
ALTER TABLE accounts DROP COLUMN disabled;
ALTER TABLE accounts DROP COLUMN password_hash;
-- +goose StatementEnd
