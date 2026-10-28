-- Active catalog items holding one movie or series on one media server, for
-- owned-ratings lookups. Exclusions do not apply: the read returns item IDs
-- only, to ask the media server for its own score of the title.

-- name: ListCatalogTitleItemIDs :many
SELECT item_id
FROM library_items
WHERE media_server_id = sqlc.arg(media_server_id) AND item_type = sqlc.arg(item_type)
  AND tmdb_id = sqlc.arg(tmdb_id) AND archived = FALSE
ORDER BY item_id
LIMIT CAST(sqlc.arg(row_limit) AS BIGINT);
