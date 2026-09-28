-- SQLite migration 00027: activity keysets and per-server collection exclusions.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE imports ADD COLUMN unresolved_library_count INTEGER NOT NULL DEFAULT 0
    CHECK (unresolved_library_count >= 0 AND unresolved_library_count <= read_count);
CREATE TABLE media_server_exclusions (
    media_server_id TEXT NOT NULL REFERENCES media_servers(id) ON DELETE CASCADE,
    kind            TEXT NOT NULL CHECK (kind IN ('media_user', 'library')),
    external_id     TEXT NOT NULL CHECK (length(CAST(external_id AS BLOB)) BETWEEN 1 AND 128),
    PRIMARY KEY (media_server_id, kind, external_id)
) STRICT;
CREATE INDEX media_server_exclusions_lookup_idx
    ON media_server_exclusions (media_server_id, external_id, kind);
CREATE INDEX watches_server_user_started_idx
    ON watches (media_server_id, media_user_id, started_at DESC, id DESC);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX watches_server_user_started_idx;
UPDATE library_items
SET plays = (SELECT COUNT(*) FROM watches w
             WHERE w.media_server_id = library_items.media_server_id
               AND w.item_id = library_items.item_id),
    watch_seconds = COALESCE((SELECT SUM(w.active_seconds) FROM watches w
             WHERE w.media_server_id = library_items.media_server_id
               AND w.item_id = library_items.item_id), 0),
    unique_users = (SELECT COUNT(DISTINCT w.media_user_id) FROM watches w
             WHERE w.media_server_id = library_items.media_server_id
               AND w.item_id = library_items.item_id),
    first_played_at = (SELECT MIN(w.started_at) FROM watches w
             WHERE w.media_server_id = library_items.media_server_id
               AND w.item_id = library_items.item_id),
    last_played_at = (SELECT MAX(w.started_at) FROM watches w
             WHERE w.media_server_id = library_items.media_server_id
               AND w.item_id = library_items.item_id);
DROP TABLE media_server_exclusions;
ALTER TABLE imports DROP COLUMN unresolved_library_count;
-- +goose StatementEnd
