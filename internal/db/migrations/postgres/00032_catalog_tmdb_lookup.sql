-- PostgreSQL migration 00032: find a catalog movie or series by its TMDB ID.
-- Owned-ratings lookups resolve a title to its media-server items through this
-- index. It is partial, so episode upserts never write to it.

-- +goose Up
-- +goose StatementBegin
CREATE INDEX library_items_tmdb_idx
    ON library_items (media_server_id, item_type, tmdb_id, item_id)
    WHERE tmdb_id IS NOT NULL AND archived = FALSE;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX library_items_tmdb_idx;
-- +goose StatementEnd
