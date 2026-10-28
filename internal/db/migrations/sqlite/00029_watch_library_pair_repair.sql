-- SQLite migration 00029: repair watches that hold a library ID without its name.
-- The catalog sync used to write library_id alone. Clearing it lets the
-- playback library backfill resolve the ID and name together.

-- +goose Up
UPDATE watches SET library_id = '' WHERE library_id <> '' AND library_name = '';

-- +goose Down
-- The repair only clears values that were invalid, so there is nothing to restore.
SELECT 1;
