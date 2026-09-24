-- PostgreSQL migration 00014: support unfiltered bounded statistics windows.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE watches
    ALTER COLUMN media_user_id TYPE TEXT COLLATE "C",
    ALTER COLUMN username TYPE TEXT COLLATE "C",
    ALTER COLUMN device_name TYPE TEXT COLLATE "C",
    ALTER COLUMN client TYPE TEXT COLLATE "C",
    ALTER COLUMN item_id TYPE TEXT COLLATE "C",
    ALTER COLUMN item_name TYPE TEXT COLLATE "C",
    ALTER COLUMN item_type TYPE TEXT COLLATE "C",
    ALTER COLUMN series_name TYPE TEXT COLLATE "C",
    ALTER COLUMN play_method TYPE TEXT COLLATE "C";
CREATE INDEX watches_started_idx ON watches (started_at DESC, id DESC);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX watches_started_idx;
ALTER TABLE watches
    ALTER COLUMN media_user_id TYPE TEXT COLLATE "default",
    ALTER COLUMN username TYPE TEXT COLLATE "default",
    ALTER COLUMN device_name TYPE TEXT COLLATE "default",
    ALTER COLUMN client TYPE TEXT COLLATE "default",
    ALTER COLUMN item_id TYPE TEXT COLLATE "default",
    ALTER COLUMN item_name TYPE TEXT COLLATE "default",
    ALTER COLUMN item_type TYPE TEXT COLLATE "default",
    ALTER COLUMN series_name TYPE TEXT COLLATE "default",
    ALTER COLUMN play_method TYPE TEXT COLLATE "default";
-- +goose StatementEnd
