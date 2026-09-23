-- PostgreSQL migration 00002: local credentials and SCS browser sessions.
-- Expiry is normalized to UTC microseconds by Bloom before storage.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE accounts ADD COLUMN password_hash TEXT CHECK (password_hash IS NULL OR char_length(password_hash) <= 128);
ALTER TABLE accounts ADD COLUMN disabled BOOLEAN NOT NULL DEFAULT FALSE;

CREATE TABLE sessions (
    token  TEXT PRIMARY KEY,
    data   BYTEA NOT NULL,
    expiry TIMESTAMPTZ NOT NULL
);
CREATE INDEX sessions_expiry_idx ON sessions (expiry);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE sessions;
ALTER TABLE accounts DROP COLUMN disabled;
ALTER TABLE accounts DROP COLUMN password_hash;
-- +goose StatementEnd
