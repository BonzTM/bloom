-- Newest active movies and series across every media server, for the home
-- page's Recently Added row. Libraries an operator excluded stay hidden, the
-- same as every other catalog read.

-- name: ListRecentlyAddedTitles :many
SELECT li.media_server_id, li.item_id, li.library_id, li.parent_id, li.item_type, li.name,
       li.series_id, li.series_name, li.season_id, li.season_number, li.index_number,
       li.runtime_ms, li.premiere_date, li.production_year, li.community_rating, li.genres,
       li.primary_image_tag, li.date_created, li.archived, li.plays, li.watch_seconds,
       li.unique_users, li.first_played_at, li.last_played_at, li.first_seen_at,
       li.last_seen_at, li.updated_at, li.tmdb_id
FROM library_items li
WHERE li.archived = FALSE AND li.item_type IN ('Movie', 'Series') AND li.date_created IS NOT NULL
  AND NOT EXISTS (
      SELECT 1 FROM media_server_exclusions e
      WHERE e.media_server_id = li.media_server_id AND e.kind = 'library' AND e.external_id = li.library_id
  )
ORDER BY li.date_created DESC, li.media_server_id, li.item_id
LIMIT CAST(sqlc.arg(row_limit) AS BIGINT);
