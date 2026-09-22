-- SQLite migration 00003: expand canonical username storage.
-- Sequence: 00003 adds nullable storage and a rollback backup; the
-- provider-registered Go migration 00004 computes PRECIS UsernameCaseMapped
-- keys; 00005 makes every key required and exactly unique.

-- +goose Up
-- +goose StatementBegin
CREATE TABLE account_username_migration_backup (
    account_id TEXT PRIMARY KEY,
    original_username TEXT NOT NULL
) STRICT;
ALTER TABLE accounts ADD COLUMN username_key TEXT;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE accounts DROP COLUMN username_key;
DROP TABLE account_username_migration_backup;
-- +goose StatementEnd
