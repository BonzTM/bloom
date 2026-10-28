-- SQLite migration 00030: TMDB IDs on catalog movies and series, and an index
-- for the home page's Recently Added row.
-- The column stays NULL until the next catalog sync reads the item's provider
-- IDs. The index is partial, so episode upserts never write to it.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE library_items ADD COLUMN tmdb_id INTEGER
    CHECK (tmdb_id IS NULL OR tmdb_id BETWEEN 1 AND 1000000000);
CREATE INDEX library_items_recently_added_idx
    ON library_items (date_created DESC, media_server_id, item_id)
    WHERE archived = FALSE AND item_type IN ('Movie', 'Series') AND date_created IS NOT NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX library_items_recently_added_idx;
ALTER TABLE library_items DROP COLUMN tmdb_id;
-- +goose StatementEnd
