-- PostgreSQL migration 00005: contract canonical username storage.
-- Sequence: 00003 adds nullable storage and a rollback backup; the
-- provider-registered Go migration 00004 computes PRECIS UsernameCaseMapped
-- keys; 00005 makes every key required and exactly unique.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE accounts ALTER COLUMN username_key SET NOT NULL;
CREATE UNIQUE INDEX accounts_username_key_idx ON accounts (username_key);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX accounts_username_key_idx;
ALTER TABLE accounts ALTER COLUMN username_key DROP NOT NULL;
-- +goose StatementEnd
