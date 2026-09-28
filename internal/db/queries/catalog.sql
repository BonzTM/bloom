-- Type-agnostic library catalog queries shared by SQLite and PostgreSQL.

-- name: EnsureLibrarySyncRows :exec
INSERT INTO library_syncs (media_server_id, state)
SELECT id, 'pending' FROM media_servers
WHERE TRUE
ON CONFLICT (media_server_id) DO NOTHING;

-- name: RequestLibrarySync :execrows
INSERT INTO library_syncs (media_server_id, state)
VALUES (sqlc.arg(media_server_id), 'pending')
ON CONFLICT (media_server_id) DO UPDATE SET
    state = 'pending', cursor = '', seen_count = 0, upserted_count = 0,
    archived_count = 0, last_error = '', lease_token = '', lease_expires_at = NULL,
    started_at = NULL, finished_at = NULL
WHERE library_syncs.state NOT IN ('pending', 'running');

-- name: SelectClaimableLibrarySync :one
SELECT media_server_id, state, cursor, seen_count, upserted_count, archived_count,
       last_error, lease_token, lease_expires_at, started_at, finished_at
FROM library_syncs
WHERE state = 'pending'
   OR (state = 'running' AND lease_expires_at <= sqlc.arg(now))
   OR (state IN ('completed', 'failed') AND finished_at <= sqlc.arg(due_before))
ORDER BY CASE WHEN state = 'pending' THEN 0 WHEN state = 'running' THEN 1 ELSE 2 END,
         finished_at, media_server_id
LIMIT 1;

-- name: ClaimLibrarySync :execrows
UPDATE library_syncs
SET state = 'running',
    cursor = CASE WHEN state = 'running' THEN cursor ELSE '' END,
    seen_count = CASE WHEN state = 'running' THEN seen_count ELSE 0 END,
    upserted_count = CASE WHEN state = 'running' THEN upserted_count ELSE 0 END,
    archived_count = CASE WHEN state = 'running' THEN archived_count ELSE 0 END,
    last_error = '', lease_token = sqlc.arg(token), lease_expires_at = sqlc.arg(expires_at),
    started_at = CASE WHEN state = 'running' THEN started_at ELSE sqlc.arg(now) END,
    finished_at = NULL
WHERE media_server_id = sqlc.arg(media_server_id)
  AND (state = 'pending'
       OR (state = 'running' AND lease_expires_at <= sqlc.arg(now))
       OR (state IN ('completed', 'failed') AND finished_at <= sqlc.arg(due_before)));

-- name: GetLibrarySync :one
SELECT media_server_id, state, cursor, seen_count, upserted_count, archived_count,
       last_error, lease_token, lease_expires_at, started_at, finished_at
FROM library_syncs WHERE media_server_id = sqlc.arg(media_server_id);

-- name: FenceLibrarySync :one
SELECT media_server_id FROM library_syncs
WHERE media_server_id = sqlc.arg(media_server_id) AND state = 'running' AND lease_token = sqlc.arg(token);

-- name: UpsertLibraryItem :execrows
INSERT INTO library_items (
    media_server_id, item_id, library_id, parent_id, item_type, name,
    series_id, series_name, season_id, season_number, index_number, runtime_ms,
    premiere_date, production_year, community_rating, genres, primary_image_tag,
    date_created, archived, first_seen_at, last_seen_at, updated_at
) VALUES (
    sqlc.arg(media_server_id), sqlc.arg(item_id), sqlc.arg(library_id), sqlc.arg(parent_id),
    sqlc.arg(item_type), sqlc.arg(name), sqlc.arg(series_id), sqlc.arg(series_name),
    sqlc.arg(season_id), sqlc.narg(season_number), sqlc.narg(index_number), sqlc.narg(runtime_ms),
    sqlc.narg(premiere_date), sqlc.narg(production_year), sqlc.narg(community_rating),
    sqlc.arg(genres), sqlc.arg(primary_image_tag), sqlc.narg(date_created),
    sqlc.arg(archived), sqlc.arg(first_seen_at), sqlc.arg(last_seen_at), sqlc.arg(updated_at)
)
ON CONFLICT (media_server_id, item_id) DO UPDATE SET
    library_id = excluded.library_id, parent_id = excluded.parent_id,
    item_type = excluded.item_type, name = excluded.name, series_id = excluded.series_id,
    series_name = excluded.series_name, season_id = excluded.season_id,
    season_number = excluded.season_number, index_number = excluded.index_number,
    runtime_ms = excluded.runtime_ms, premiere_date = excluded.premiere_date,
    production_year = excluded.production_year, community_rating = excluded.community_rating,
    genres = excluded.genres, primary_image_tag = excluded.primary_image_tag,
    date_created = excluded.date_created, archived = excluded.archived,
    last_seen_at = excluded.last_seen_at, updated_at = excluded.updated_at;

-- name: DeleteLibraryItemGenres :exec
DELETE FROM library_item_genres
WHERE library_item_genres.media_server_id = sqlc.arg(media_server_id)
  AND library_item_genres.item_id = sqlc.arg(item_id);

-- name: InsertLibraryItemGenre :exec
INSERT INTO library_item_genres (media_server_id, item_id, genre)
VALUES (sqlc.arg(media_server_id), sqlc.arg(item_id), sqlc.arg(genre));

-- name: CheckpointLibrarySync :execrows
UPDATE library_syncs
SET cursor = sqlc.arg(cursor), seen_count = seen_count + sqlc.arg(seen_delta),
    upserted_count = upserted_count + sqlc.arg(upserted_delta),
    lease_expires_at = sqlc.arg(expires_at)
WHERE media_server_id = sqlc.arg(media_server_id)
  AND state = 'running' AND lease_token = sqlc.arg(token);

-- name: ListMissingLibraryItemIDs :many
SELECT item_id FROM library_items
WHERE media_server_id = sqlc.arg(media_server_id) AND archived = FALSE
  AND last_seen_at < sqlc.arg(started_at) AND item_id > sqlc.arg(after_item_id)
ORDER BY item_id LIMIT CAST(sqlc.arg(row_limit) AS BIGINT);

-- name: ArchiveLibraryItem :execrows
UPDATE library_items SET archived = TRUE, updated_at = sqlc.arg(now)
WHERE media_server_id = sqlc.arg(media_server_id) AND item_id = sqlc.arg(item_id)
  AND archived = FALSE AND last_seen_at < sqlc.arg(started_at);

-- name: CheckpointLibrarySyncArchives :execrows
UPDATE library_syncs
SET archived_count = archived_count + sqlc.arg(archived_delta), cursor = sqlc.arg(cursor),
    lease_expires_at = sqlc.arg(expires_at)
WHERE media_server_id = sqlc.arg(media_server_id)
  AND state = 'running' AND lease_token = sqlc.arg(token);

-- name: DeleteArchivedLibraryItemGenres :execrows
DELETE FROM library_item_genres
WHERE library_item_genres.media_server_id = sqlc.arg(target_media_server_id)
  AND library_item_genres.item_id IN (
      SELECT li.item_id FROM library_items li
      WHERE li.media_server_id = sqlc.arg(target_media_server_id) AND li.archived = TRUE);

-- name: BackfillWatchCatalogItem :execrows
UPDATE watches
SET item_name = sqlc.arg(item_name), series_id = sqlc.narg(series_id),
    series_name = sqlc.arg(series_name), library_id = sqlc.arg(library_id)
WHERE media_server_id = sqlc.arg(media_server_id) AND item_id = sqlc.arg(item_id)
  AND (series_id IS NULL OR series_id = '' OR library_id = '');

-- name: RebuildLibraryItemRollup :exec
UPDATE library_items
SET plays = (SELECT COUNT(*) FROM watches w
             WHERE w.media_server_id = library_items.media_server_id AND w.item_id = library_items.item_id),
    watch_seconds = COALESCE((SELECT SUM(w.active_seconds) FROM watches w
             WHERE w.media_server_id = library_items.media_server_id AND w.item_id = library_items.item_id), 0),
    unique_users = (SELECT COUNT(DISTINCT w.media_user_id) FROM watches w
             WHERE w.media_server_id = library_items.media_server_id AND w.item_id = library_items.item_id),
    first_played_at = (SELECT MIN(w.started_at) FROM watches w
             WHERE w.media_server_id = library_items.media_server_id AND w.item_id = library_items.item_id),
    last_played_at = (SELECT MAX(w.started_at) FROM watches w
             WHERE w.media_server_id = library_items.media_server_id AND w.item_id = library_items.item_id)
WHERE library_items.media_server_id = sqlc.arg(media_server_id)
  AND library_items.item_id = sqlc.arg(item_id);

-- name: RebuildLibraryItemRollups :exec
UPDATE library_items
SET plays = (SELECT COUNT(*) FROM watches w
             WHERE w.media_server_id = library_items.media_server_id AND w.item_id = library_items.item_id),
    watch_seconds = COALESCE((SELECT SUM(w.active_seconds) FROM watches w
             WHERE w.media_server_id = library_items.media_server_id AND w.item_id = library_items.item_id), 0),
    unique_users = (SELECT COUNT(DISTINCT w.media_user_id) FROM watches w
             WHERE w.media_server_id = library_items.media_server_id AND w.item_id = library_items.item_id),
    first_played_at = (SELECT MIN(w.started_at) FROM watches w
             WHERE w.media_server_id = library_items.media_server_id AND w.item_id = library_items.item_id),
    last_played_at = (SELECT MAX(w.started_at) FROM watches w
             WHERE w.media_server_id = library_items.media_server_id AND w.item_id = library_items.item_id)
WHERE library_items.media_server_id = sqlc.arg(media_server_id);

-- name: CompleteLibrarySync :execrows
UPDATE library_syncs
SET state = 'completed', cursor = '',
    last_error = '', lease_token = '', lease_expires_at = NULL, finished_at = sqlc.arg(now)
WHERE media_server_id = sqlc.arg(media_server_id)
  AND state = 'running' AND lease_token = sqlc.arg(token);

-- name: FailLibrarySync :execrows
UPDATE library_syncs
SET state = 'failed', last_error = sqlc.arg(last_error), lease_token = '',
    lease_expires_at = NULL, finished_at = sqlc.arg(now)
WHERE media_server_id = sqlc.arg(media_server_id)
  AND state = 'running' AND lease_token = sqlc.arg(token);

-- name: CatalogLibraryTypeRows :many
WITH watch_stats AS (
    SELECT media_server_id, item_id, COUNT(*) AS plays,
           COALESCE(SUM(active_seconds), 0) AS watch_seconds
    FROM watches
    WHERE media_server_id = sqlc.arg(media_server_id)
      AND (CAST(sqlc.arg(window_enabled) AS INTEGER) = 0
           OR (started_at >= sqlc.arg(window_start) AND started_at < sqlc.arg(window_end)))
    GROUP BY media_server_id, item_id
)
SELECT li.library_id, li.item_type, COUNT(*) AS item_count,
       COALESCE(SUM(ws.plays), 0) AS plays,
       COALESCE(SUM(ws.watch_seconds), 0) AS watch_seconds
FROM library_items li
LEFT JOIN watch_stats ws ON ws.media_server_id = li.media_server_id AND ws.item_id = li.item_id
WHERE li.media_server_id = sqlc.arg(media_server_id) AND li.archived = FALSE
GROUP BY li.library_id, li.item_type
ORDER BY li.library_id, li.item_type
LIMIT 10001;

-- name: CatalogItemWindowSummary :one
SELECT COUNT(*) AS plays, COALESCE(SUM(active_seconds), 0) AS watch_seconds,
       COUNT(DISTINCT media_user_id) AS unique_users,
       MIN(started_at) AS first_played_at, MAX(started_at) AS last_played_at
FROM watches
WHERE media_server_id = sqlc.arg(media_server_id) AND item_id = sqlc.arg(item_id)
  AND started_at >= sqlc.arg(window_start) AND started_at < sqlc.arg(window_end);

-- Item pages use fixed filtered and unfiltered plans selected by the adapter.
-- name: ListCatalogItemsNameAsc :many
SELECT li.media_server_id, li.item_id, li.library_id, li.parent_id, li.item_type, li.name,
       li.series_id, li.series_name, li.season_id, li.season_number, li.index_number,
       li.runtime_ms, li.premiere_date, li.production_year, li.community_rating, li.genres,
       li.primary_image_tag, li.date_created, li.archived, li.plays, li.watch_seconds,
       li.unique_users, li.first_played_at, li.last_played_at, li.first_seen_at,
       li.last_seen_at, li.updated_at
FROM library_items li
    WHERE li.media_server_id = sqlc.arg(media_server_id) AND li.library_id = sqlc.arg(library_id)
      AND li.archived = (CAST(sqlc.arg(archived_filter) AS BIGINT) <> 0)
      AND (li.name > sqlc.arg(after_name)
           OR (li.name = sqlc.arg(after_name) AND li.item_id > sqlc.arg(after_item_id)))
    ORDER BY li.name ASC, li.item_id LIMIT CAST(sqlc.arg(page_size) AS BIGINT);

-- name: ListCatalogItemsNameAscFiltered :many
SELECT li.media_server_id, li.item_id, li.library_id, li.parent_id, li.item_type, li.name,
       li.series_id, li.series_name, li.season_id, li.season_number, li.index_number,
       li.runtime_ms, li.premiere_date, li.production_year, li.community_rating, li.genres,
       li.primary_image_tag, li.date_created, li.archived, li.plays, li.watch_seconds,
       li.unique_users, li.first_played_at, li.last_played_at, li.first_seen_at,
       li.last_seen_at, li.updated_at
FROM library_items li
    WHERE li.media_server_id = sqlc.arg(media_server_id) AND li.library_id = sqlc.arg(library_id)
      AND li.archived = (CAST(sqlc.arg(archived_filter) AS BIGINT) <> 0)
      AND li.item_type = CAST(sqlc.arg(item_type_filter) AS TEXT)
      AND (li.name > sqlc.arg(after_name)
           OR (li.name = sqlc.arg(after_name) AND li.item_id > sqlc.arg(after_item_id)))
    ORDER BY li.name ASC, li.item_id LIMIT CAST(sqlc.arg(page_size) AS BIGINT);

-- name: ListCatalogItemsNameDesc :many
SELECT li.media_server_id, li.item_id, li.library_id, li.parent_id, li.item_type, li.name,
       li.series_id, li.series_name, li.season_id, li.season_number, li.index_number,
       li.runtime_ms, li.premiere_date, li.production_year, li.community_rating, li.genres,
       li.primary_image_tag, li.date_created, li.archived, li.plays, li.watch_seconds,
       li.unique_users, li.first_played_at, li.last_played_at, li.first_seen_at,
       li.last_seen_at, li.updated_at
FROM library_items li
    WHERE li.media_server_id = sqlc.arg(media_server_id) AND li.library_id = sqlc.arg(library_id)
      AND li.archived = (CAST(sqlc.arg(archived_filter) AS BIGINT) <> 0)
      AND (li.name < sqlc.arg(after_name)
           OR (li.name = sqlc.arg(after_name) AND li.item_id > sqlc.arg(after_item_id)))
    ORDER BY li.name DESC, li.item_id LIMIT CAST(sqlc.arg(page_size) AS BIGINT);

-- name: ListCatalogItemsNameDescFiltered :many
SELECT li.media_server_id, li.item_id, li.library_id, li.parent_id, li.item_type, li.name,
       li.series_id, li.series_name, li.season_id, li.season_number, li.index_number,
       li.runtime_ms, li.premiere_date, li.production_year, li.community_rating, li.genres,
       li.primary_image_tag, li.date_created, li.archived, li.plays, li.watch_seconds,
       li.unique_users, li.first_played_at, li.last_played_at, li.first_seen_at,
       li.last_seen_at, li.updated_at
FROM library_items li
    WHERE li.media_server_id = sqlc.arg(media_server_id) AND li.library_id = sqlc.arg(library_id)
      AND li.archived = (CAST(sqlc.arg(archived_filter) AS BIGINT) <> 0)
      AND li.item_type = CAST(sqlc.arg(item_type_filter) AS TEXT)
      AND (li.name < sqlc.arg(after_name)
           OR (li.name = sqlc.arg(after_name) AND li.item_id > sqlc.arg(after_item_id)))
    ORDER BY li.name DESC, li.item_id LIMIT CAST(sqlc.arg(page_size) AS BIGINT);

-- name: ListCatalogItemsDateAsc :many
SELECT li.media_server_id, li.item_id, li.library_id, li.parent_id, li.item_type, li.name,
       li.series_id, li.series_name, li.season_id, li.season_number, li.index_number,
       li.runtime_ms, li.premiere_date, li.production_year, li.community_rating, li.genres,
       li.primary_image_tag, li.date_created, li.archived, li.plays, li.watch_seconds,
       li.unique_users, li.first_played_at, li.last_played_at, li.first_seen_at,
       li.last_seen_at, li.updated_at
FROM library_items li
    WHERE li.media_server_id = sqlc.arg(media_server_id) AND li.library_id = sqlc.arg(library_id)
      AND li.archived = (CAST(sqlc.arg(archived_filter) AS BIGINT) <> 0)
      AND ((CAST(sqlc.arg(after_null) AS INTEGER) = 0 AND (li.date_created IS NULL OR li.date_created > sqlc.arg(after_time)
               OR (li.date_created = sqlc.arg(after_time) AND li.item_id > sqlc.arg(after_item_id))))
           OR (CAST(sqlc.arg(after_null) AS INTEGER) <> 0 AND li.date_created IS NULL AND li.item_id > sqlc.arg(after_item_id)))
    ORDER BY (li.date_created IS NULL), li.date_created ASC, li.item_id LIMIT CAST(sqlc.arg(page_size) AS BIGINT);

-- name: ListCatalogItemsDateAscFiltered :many
SELECT li.media_server_id, li.item_id, li.library_id, li.parent_id, li.item_type, li.name,
       li.series_id, li.series_name, li.season_id, li.season_number, li.index_number,
       li.runtime_ms, li.premiere_date, li.production_year, li.community_rating, li.genres,
       li.primary_image_tag, li.date_created, li.archived, li.plays, li.watch_seconds,
       li.unique_users, li.first_played_at, li.last_played_at, li.first_seen_at,
       li.last_seen_at, li.updated_at
FROM library_items li
    WHERE li.media_server_id = sqlc.arg(media_server_id) AND li.library_id = sqlc.arg(library_id)
      AND li.archived = (CAST(sqlc.arg(archived_filter) AS BIGINT) <> 0)
      AND li.item_type = CAST(sqlc.arg(item_type_filter) AS TEXT)
      AND ((CAST(sqlc.arg(after_null) AS INTEGER) = 0 AND (li.date_created IS NULL OR li.date_created > sqlc.arg(after_time)
               OR (li.date_created = sqlc.arg(after_time) AND li.item_id > sqlc.arg(after_item_id))))
           OR (CAST(sqlc.arg(after_null) AS INTEGER) <> 0 AND li.date_created IS NULL AND li.item_id > sqlc.arg(after_item_id)))
    ORDER BY (li.date_created IS NULL), li.date_created ASC, li.item_id LIMIT CAST(sqlc.arg(page_size) AS BIGINT);

-- name: ListCatalogItemsDateDesc :many
SELECT li.media_server_id, li.item_id, li.library_id, li.parent_id, li.item_type, li.name,
       li.series_id, li.series_name, li.season_id, li.season_number, li.index_number,
       li.runtime_ms, li.premiere_date, li.production_year, li.community_rating, li.genres,
       li.primary_image_tag, li.date_created, li.archived, li.plays, li.watch_seconds,
       li.unique_users, li.first_played_at, li.last_played_at, li.first_seen_at,
       li.last_seen_at, li.updated_at
FROM library_items li
    WHERE li.media_server_id = sqlc.arg(media_server_id) AND li.library_id = sqlc.arg(library_id)
      AND li.archived = (CAST(sqlc.arg(archived_filter) AS BIGINT) <> 0)
      AND ((CAST(sqlc.arg(after_null) AS INTEGER) = 0 AND (li.date_created IS NULL OR li.date_created < sqlc.arg(after_time)
               OR (li.date_created = sqlc.arg(after_time) AND li.item_id > sqlc.arg(after_item_id))))
           OR (CAST(sqlc.arg(after_null) AS INTEGER) <> 0 AND li.date_created IS NULL AND li.item_id > sqlc.arg(after_item_id)))
    ORDER BY (li.date_created IS NULL), li.date_created DESC, li.item_id LIMIT CAST(sqlc.arg(page_size) AS BIGINT);

-- name: ListCatalogItemsDateDescFiltered :many
SELECT li.media_server_id, li.item_id, li.library_id, li.parent_id, li.item_type, li.name,
       li.series_id, li.series_name, li.season_id, li.season_number, li.index_number,
       li.runtime_ms, li.premiere_date, li.production_year, li.community_rating, li.genres,
       li.primary_image_tag, li.date_created, li.archived, li.plays, li.watch_seconds,
       li.unique_users, li.first_played_at, li.last_played_at, li.first_seen_at,
       li.last_seen_at, li.updated_at
FROM library_items li
    WHERE li.media_server_id = sqlc.arg(media_server_id) AND li.library_id = sqlc.arg(library_id)
      AND li.archived = (CAST(sqlc.arg(archived_filter) AS BIGINT) <> 0)
      AND li.item_type = CAST(sqlc.arg(item_type_filter) AS TEXT)
      AND ((CAST(sqlc.arg(after_null) AS INTEGER) = 0 AND (li.date_created IS NULL OR li.date_created < sqlc.arg(after_time)
               OR (li.date_created = sqlc.arg(after_time) AND li.item_id > sqlc.arg(after_item_id))))
           OR (CAST(sqlc.arg(after_null) AS INTEGER) <> 0 AND li.date_created IS NULL AND li.item_id > sqlc.arg(after_item_id)))
    ORDER BY (li.date_created IS NULL), li.date_created DESC, li.item_id LIMIT CAST(sqlc.arg(page_size) AS BIGINT);

-- name: ListCatalogItemsPremiereAsc :many
SELECT li.media_server_id, li.item_id, li.library_id, li.parent_id, li.item_type, li.name,
       li.series_id, li.series_name, li.season_id, li.season_number, li.index_number,
       li.runtime_ms, li.premiere_date, li.production_year, li.community_rating, li.genres,
       li.primary_image_tag, li.date_created, li.archived, li.plays, li.watch_seconds,
       li.unique_users, li.first_played_at, li.last_played_at, li.first_seen_at,
       li.last_seen_at, li.updated_at
FROM library_items li
    WHERE li.media_server_id = sqlc.arg(media_server_id) AND li.library_id = sqlc.arg(library_id)
      AND li.archived = (CAST(sqlc.arg(archived_filter) AS BIGINT) <> 0)
      AND ((CAST(sqlc.arg(after_null) AS INTEGER) = 0 AND (li.premiere_date IS NULL OR li.premiere_date > sqlc.arg(after_time)
               OR (li.premiere_date = sqlc.arg(after_time) AND li.item_id > sqlc.arg(after_item_id))))
           OR (CAST(sqlc.arg(after_null) AS INTEGER) <> 0 AND li.premiere_date IS NULL AND li.item_id > sqlc.arg(after_item_id)))
    ORDER BY (li.premiere_date IS NULL), li.premiere_date ASC, li.item_id LIMIT CAST(sqlc.arg(page_size) AS BIGINT);

-- name: ListCatalogItemsPremiereAscFiltered :many
SELECT li.media_server_id, li.item_id, li.library_id, li.parent_id, li.item_type, li.name,
       li.series_id, li.series_name, li.season_id, li.season_number, li.index_number,
       li.runtime_ms, li.premiere_date, li.production_year, li.community_rating, li.genres,
       li.primary_image_tag, li.date_created, li.archived, li.plays, li.watch_seconds,
       li.unique_users, li.first_played_at, li.last_played_at, li.first_seen_at,
       li.last_seen_at, li.updated_at
FROM library_items li
    WHERE li.media_server_id = sqlc.arg(media_server_id) AND li.library_id = sqlc.arg(library_id)
      AND li.archived = (CAST(sqlc.arg(archived_filter) AS BIGINT) <> 0)
      AND li.item_type = CAST(sqlc.arg(item_type_filter) AS TEXT)
      AND ((CAST(sqlc.arg(after_null) AS INTEGER) = 0 AND (li.premiere_date IS NULL OR li.premiere_date > sqlc.arg(after_time)
               OR (li.premiere_date = sqlc.arg(after_time) AND li.item_id > sqlc.arg(after_item_id))))
           OR (CAST(sqlc.arg(after_null) AS INTEGER) <> 0 AND li.premiere_date IS NULL AND li.item_id > sqlc.arg(after_item_id)))
    ORDER BY (li.premiere_date IS NULL), li.premiere_date ASC, li.item_id LIMIT CAST(sqlc.arg(page_size) AS BIGINT);

-- name: ListCatalogItemsPremiereDesc :many
SELECT li.media_server_id, li.item_id, li.library_id, li.parent_id, li.item_type, li.name,
       li.series_id, li.series_name, li.season_id, li.season_number, li.index_number,
       li.runtime_ms, li.premiere_date, li.production_year, li.community_rating, li.genres,
       li.primary_image_tag, li.date_created, li.archived, li.plays, li.watch_seconds,
       li.unique_users, li.first_played_at, li.last_played_at, li.first_seen_at,
       li.last_seen_at, li.updated_at
FROM library_items li
    WHERE li.media_server_id = sqlc.arg(media_server_id) AND li.library_id = sqlc.arg(library_id)
      AND li.archived = (CAST(sqlc.arg(archived_filter) AS BIGINT) <> 0)
      AND ((CAST(sqlc.arg(after_null) AS INTEGER) = 0 AND (li.premiere_date IS NULL OR li.premiere_date < sqlc.arg(after_time)
               OR (li.premiere_date = sqlc.arg(after_time) AND li.item_id > sqlc.arg(after_item_id))))
           OR (CAST(sqlc.arg(after_null) AS INTEGER) <> 0 AND li.premiere_date IS NULL AND li.item_id > sqlc.arg(after_item_id)))
    ORDER BY (li.premiere_date IS NULL), li.premiere_date DESC, li.item_id LIMIT CAST(sqlc.arg(page_size) AS BIGINT);

-- name: ListCatalogItemsPremiereDescFiltered :many
SELECT li.media_server_id, li.item_id, li.library_id, li.parent_id, li.item_type, li.name,
       li.series_id, li.series_name, li.season_id, li.season_number, li.index_number,
       li.runtime_ms, li.premiere_date, li.production_year, li.community_rating, li.genres,
       li.primary_image_tag, li.date_created, li.archived, li.plays, li.watch_seconds,
       li.unique_users, li.first_played_at, li.last_played_at, li.first_seen_at,
       li.last_seen_at, li.updated_at
FROM library_items li
    WHERE li.media_server_id = sqlc.arg(media_server_id) AND li.library_id = sqlc.arg(library_id)
      AND li.archived = (CAST(sqlc.arg(archived_filter) AS BIGINT) <> 0)
      AND li.item_type = CAST(sqlc.arg(item_type_filter) AS TEXT)
      AND ((CAST(sqlc.arg(after_null) AS INTEGER) = 0 AND (li.premiere_date IS NULL OR li.premiere_date < sqlc.arg(after_time)
               OR (li.premiere_date = sqlc.arg(after_time) AND li.item_id > sqlc.arg(after_item_id))))
           OR (CAST(sqlc.arg(after_null) AS INTEGER) <> 0 AND li.premiere_date IS NULL AND li.item_id > sqlc.arg(after_item_id)))
    ORDER BY (li.premiere_date IS NULL), li.premiere_date DESC, li.item_id LIMIT CAST(sqlc.arg(page_size) AS BIGINT);

-- name: ListCatalogItemsPlaysAsc :many
SELECT li.media_server_id, li.item_id, li.library_id, li.parent_id, li.item_type, li.name,
       li.series_id, li.series_name, li.season_id, li.season_number, li.index_number,
       li.runtime_ms, li.premiere_date, li.production_year, li.community_rating, li.genres,
       li.primary_image_tag, li.date_created, li.archived, li.plays, li.watch_seconds,
       li.unique_users, li.first_played_at, li.last_played_at, li.first_seen_at,
       li.last_seen_at, li.updated_at
FROM library_items li
    WHERE li.media_server_id = sqlc.arg(media_server_id) AND li.library_id = sqlc.arg(library_id)
      AND li.archived = (CAST(sqlc.arg(archived_filter) AS BIGINT) <> 0)
      AND (li.plays > CAST(sqlc.arg(after_number) AS BIGINT)
           OR (li.plays = CAST(sqlc.arg(after_number) AS BIGINT) AND li.item_id > sqlc.arg(after_item_id)))
    ORDER BY li.plays ASC, li.item_id LIMIT CAST(sqlc.arg(page_size) AS BIGINT);

-- name: ListCatalogItemsPlaysAscFiltered :many
SELECT li.media_server_id, li.item_id, li.library_id, li.parent_id, li.item_type, li.name,
       li.series_id, li.series_name, li.season_id, li.season_number, li.index_number,
       li.runtime_ms, li.premiere_date, li.production_year, li.community_rating, li.genres,
       li.primary_image_tag, li.date_created, li.archived, li.plays, li.watch_seconds,
       li.unique_users, li.first_played_at, li.last_played_at, li.first_seen_at,
       li.last_seen_at, li.updated_at
FROM library_items li
    WHERE li.media_server_id = sqlc.arg(media_server_id) AND li.library_id = sqlc.arg(library_id)
      AND li.archived = (CAST(sqlc.arg(archived_filter) AS BIGINT) <> 0)
      AND li.item_type = CAST(sqlc.arg(item_type_filter) AS TEXT)
      AND (li.plays > CAST(sqlc.arg(after_number) AS BIGINT)
           OR (li.plays = CAST(sqlc.arg(after_number) AS BIGINT) AND li.item_id > sqlc.arg(after_item_id)))
    ORDER BY li.plays ASC, li.item_id LIMIT CAST(sqlc.arg(page_size) AS BIGINT);

-- name: ListCatalogItemsPlaysDesc :many
SELECT li.media_server_id, li.item_id, li.library_id, li.parent_id, li.item_type, li.name,
       li.series_id, li.series_name, li.season_id, li.season_number, li.index_number,
       li.runtime_ms, li.premiere_date, li.production_year, li.community_rating, li.genres,
       li.primary_image_tag, li.date_created, li.archived, li.plays, li.watch_seconds,
       li.unique_users, li.first_played_at, li.last_played_at, li.first_seen_at,
       li.last_seen_at, li.updated_at
FROM library_items li
    WHERE li.media_server_id = sqlc.arg(media_server_id) AND li.library_id = sqlc.arg(library_id)
      AND li.archived = (CAST(sqlc.arg(archived_filter) AS BIGINT) <> 0)
      AND (li.plays < CAST(sqlc.arg(after_number) AS BIGINT)
           OR (li.plays = CAST(sqlc.arg(after_number) AS BIGINT) AND li.item_id > sqlc.arg(after_item_id)))
    ORDER BY li.plays DESC, li.item_id LIMIT CAST(sqlc.arg(page_size) AS BIGINT);

-- name: ListCatalogItemsPlaysDescFiltered :many
SELECT li.media_server_id, li.item_id, li.library_id, li.parent_id, li.item_type, li.name,
       li.series_id, li.series_name, li.season_id, li.season_number, li.index_number,
       li.runtime_ms, li.premiere_date, li.production_year, li.community_rating, li.genres,
       li.primary_image_tag, li.date_created, li.archived, li.plays, li.watch_seconds,
       li.unique_users, li.first_played_at, li.last_played_at, li.first_seen_at,
       li.last_seen_at, li.updated_at
FROM library_items li
    WHERE li.media_server_id = sqlc.arg(media_server_id) AND li.library_id = sqlc.arg(library_id)
      AND li.archived = (CAST(sqlc.arg(archived_filter) AS BIGINT) <> 0)
      AND li.item_type = CAST(sqlc.arg(item_type_filter) AS TEXT)
      AND (li.plays < CAST(sqlc.arg(after_number) AS BIGINT)
           OR (li.plays = CAST(sqlc.arg(after_number) AS BIGINT) AND li.item_id > sqlc.arg(after_item_id)))
    ORDER BY li.plays DESC, li.item_id LIMIT CAST(sqlc.arg(page_size) AS BIGINT);

-- name: ListCatalogItemsWatchAsc :many
SELECT li.media_server_id, li.item_id, li.library_id, li.parent_id, li.item_type, li.name,
       li.series_id, li.series_name, li.season_id, li.season_number, li.index_number,
       li.runtime_ms, li.premiere_date, li.production_year, li.community_rating, li.genres,
       li.primary_image_tag, li.date_created, li.archived, li.plays, li.watch_seconds,
       li.unique_users, li.first_played_at, li.last_played_at, li.first_seen_at,
       li.last_seen_at, li.updated_at
FROM library_items li
    WHERE li.media_server_id = sqlc.arg(media_server_id) AND li.library_id = sqlc.arg(library_id)
      AND li.archived = (CAST(sqlc.arg(archived_filter) AS BIGINT) <> 0)
      AND (li.watch_seconds > CAST(sqlc.arg(after_number) AS BIGINT)
           OR (li.watch_seconds = CAST(sqlc.arg(after_number) AS BIGINT) AND li.item_id > sqlc.arg(after_item_id)))
    ORDER BY li.watch_seconds ASC, li.item_id LIMIT CAST(sqlc.arg(page_size) AS BIGINT);

-- name: ListCatalogItemsWatchAscFiltered :many
SELECT li.media_server_id, li.item_id, li.library_id, li.parent_id, li.item_type, li.name,
       li.series_id, li.series_name, li.season_id, li.season_number, li.index_number,
       li.runtime_ms, li.premiere_date, li.production_year, li.community_rating, li.genres,
       li.primary_image_tag, li.date_created, li.archived, li.plays, li.watch_seconds,
       li.unique_users, li.first_played_at, li.last_played_at, li.first_seen_at,
       li.last_seen_at, li.updated_at
FROM library_items li
    WHERE li.media_server_id = sqlc.arg(media_server_id) AND li.library_id = sqlc.arg(library_id)
      AND li.archived = (CAST(sqlc.arg(archived_filter) AS BIGINT) <> 0)
      AND li.item_type = CAST(sqlc.arg(item_type_filter) AS TEXT)
      AND (li.watch_seconds > CAST(sqlc.arg(after_number) AS BIGINT)
           OR (li.watch_seconds = CAST(sqlc.arg(after_number) AS BIGINT) AND li.item_id > sqlc.arg(after_item_id)))
    ORDER BY li.watch_seconds ASC, li.item_id LIMIT CAST(sqlc.arg(page_size) AS BIGINT);

-- name: ListCatalogItemsWatchDesc :many
SELECT li.media_server_id, li.item_id, li.library_id, li.parent_id, li.item_type, li.name,
       li.series_id, li.series_name, li.season_id, li.season_number, li.index_number,
       li.runtime_ms, li.premiere_date, li.production_year, li.community_rating, li.genres,
       li.primary_image_tag, li.date_created, li.archived, li.plays, li.watch_seconds,
       li.unique_users, li.first_played_at, li.last_played_at, li.first_seen_at,
       li.last_seen_at, li.updated_at
FROM library_items li
    WHERE li.media_server_id = sqlc.arg(media_server_id) AND li.library_id = sqlc.arg(library_id)
      AND li.archived = (CAST(sqlc.arg(archived_filter) AS BIGINT) <> 0)
      AND (li.watch_seconds < CAST(sqlc.arg(after_number) AS BIGINT)
           OR (li.watch_seconds = CAST(sqlc.arg(after_number) AS BIGINT) AND li.item_id > sqlc.arg(after_item_id)))
    ORDER BY li.watch_seconds DESC, li.item_id LIMIT CAST(sqlc.arg(page_size) AS BIGINT);

-- name: ListCatalogItemsWatchDescFiltered :many
SELECT li.media_server_id, li.item_id, li.library_id, li.parent_id, li.item_type, li.name,
       li.series_id, li.series_name, li.season_id, li.season_number, li.index_number,
       li.runtime_ms, li.premiere_date, li.production_year, li.community_rating, li.genres,
       li.primary_image_tag, li.date_created, li.archived, li.plays, li.watch_seconds,
       li.unique_users, li.first_played_at, li.last_played_at, li.first_seen_at,
       li.last_seen_at, li.updated_at
FROM library_items li
    WHERE li.media_server_id = sqlc.arg(media_server_id) AND li.library_id = sqlc.arg(library_id)
      AND li.archived = (CAST(sqlc.arg(archived_filter) AS BIGINT) <> 0)
      AND li.item_type = CAST(sqlc.arg(item_type_filter) AS TEXT)
      AND (li.watch_seconds < CAST(sqlc.arg(after_number) AS BIGINT)
           OR (li.watch_seconds = CAST(sqlc.arg(after_number) AS BIGINT) AND li.item_id > sqlc.arg(after_item_id)))
    ORDER BY li.watch_seconds DESC, li.item_id LIMIT CAST(sqlc.arg(page_size) AS BIGINT);

-- name: ListCatalogItemsLastPlayedAsc :many
SELECT li.media_server_id, li.item_id, li.library_id, li.parent_id, li.item_type, li.name,
       li.series_id, li.series_name, li.season_id, li.season_number, li.index_number,
       li.runtime_ms, li.premiere_date, li.production_year, li.community_rating, li.genres,
       li.primary_image_tag, li.date_created, li.archived, li.plays, li.watch_seconds,
       li.unique_users, li.first_played_at, li.last_played_at, li.first_seen_at,
       li.last_seen_at, li.updated_at
FROM library_items li
    WHERE li.media_server_id = sqlc.arg(media_server_id) AND li.library_id = sqlc.arg(library_id)
      AND li.archived = (CAST(sqlc.arg(archived_filter) AS BIGINT) <> 0)
      AND ((CAST(sqlc.arg(after_null) AS INTEGER) = 0 AND (li.last_played_at IS NULL OR li.last_played_at > sqlc.arg(after_time)
               OR (li.last_played_at = sqlc.arg(after_time) AND li.item_id > sqlc.arg(after_item_id))))
           OR (CAST(sqlc.arg(after_null) AS INTEGER) <> 0 AND li.last_played_at IS NULL AND li.item_id > sqlc.arg(after_item_id)))
    ORDER BY (li.last_played_at IS NULL), li.last_played_at ASC, li.item_id LIMIT CAST(sqlc.arg(page_size) AS BIGINT);

-- name: ListCatalogItemsLastPlayedAscFiltered :many
SELECT li.media_server_id, li.item_id, li.library_id, li.parent_id, li.item_type, li.name,
       li.series_id, li.series_name, li.season_id, li.season_number, li.index_number,
       li.runtime_ms, li.premiere_date, li.production_year, li.community_rating, li.genres,
       li.primary_image_tag, li.date_created, li.archived, li.plays, li.watch_seconds,
       li.unique_users, li.first_played_at, li.last_played_at, li.first_seen_at,
       li.last_seen_at, li.updated_at
FROM library_items li
    WHERE li.media_server_id = sqlc.arg(media_server_id) AND li.library_id = sqlc.arg(library_id)
      AND li.archived = (CAST(sqlc.arg(archived_filter) AS BIGINT) <> 0)
      AND li.item_type = CAST(sqlc.arg(item_type_filter) AS TEXT)
      AND ((CAST(sqlc.arg(after_null) AS INTEGER) = 0 AND (li.last_played_at IS NULL OR li.last_played_at > sqlc.arg(after_time)
               OR (li.last_played_at = sqlc.arg(after_time) AND li.item_id > sqlc.arg(after_item_id))))
           OR (CAST(sqlc.arg(after_null) AS INTEGER) <> 0 AND li.last_played_at IS NULL AND li.item_id > sqlc.arg(after_item_id)))
    ORDER BY (li.last_played_at IS NULL), li.last_played_at ASC, li.item_id LIMIT CAST(sqlc.arg(page_size) AS BIGINT);

-- name: ListCatalogItemsLastPlayedDesc :many
SELECT li.media_server_id, li.item_id, li.library_id, li.parent_id, li.item_type, li.name,
       li.series_id, li.series_name, li.season_id, li.season_number, li.index_number,
       li.runtime_ms, li.premiere_date, li.production_year, li.community_rating, li.genres,
       li.primary_image_tag, li.date_created, li.archived, li.plays, li.watch_seconds,
       li.unique_users, li.first_played_at, li.last_played_at, li.first_seen_at,
       li.last_seen_at, li.updated_at
FROM library_items li
    WHERE li.media_server_id = sqlc.arg(media_server_id) AND li.library_id = sqlc.arg(library_id)
      AND li.archived = (CAST(sqlc.arg(archived_filter) AS BIGINT) <> 0)
      AND ((CAST(sqlc.arg(after_null) AS INTEGER) = 0 AND (li.last_played_at IS NULL OR li.last_played_at < sqlc.arg(after_time)
               OR (li.last_played_at = sqlc.arg(after_time) AND li.item_id > sqlc.arg(after_item_id))))
           OR (CAST(sqlc.arg(after_null) AS INTEGER) <> 0 AND li.last_played_at IS NULL AND li.item_id > sqlc.arg(after_item_id)))
    ORDER BY (li.last_played_at IS NULL), li.last_played_at DESC, li.item_id LIMIT CAST(sqlc.arg(page_size) AS BIGINT);

-- name: ListCatalogItemsLastPlayedDescFiltered :many
SELECT li.media_server_id, li.item_id, li.library_id, li.parent_id, li.item_type, li.name,
       li.series_id, li.series_name, li.season_id, li.season_number, li.index_number,
       li.runtime_ms, li.premiere_date, li.production_year, li.community_rating, li.genres,
       li.primary_image_tag, li.date_created, li.archived, li.plays, li.watch_seconds,
       li.unique_users, li.first_played_at, li.last_played_at, li.first_seen_at,
       li.last_seen_at, li.updated_at
FROM library_items li
    WHERE li.media_server_id = sqlc.arg(media_server_id) AND li.library_id = sqlc.arg(library_id)
      AND li.archived = (CAST(sqlc.arg(archived_filter) AS BIGINT) <> 0)
      AND li.item_type = CAST(sqlc.arg(item_type_filter) AS TEXT)
      AND ((CAST(sqlc.arg(after_null) AS INTEGER) = 0 AND (li.last_played_at IS NULL OR li.last_played_at < sqlc.arg(after_time)
               OR (li.last_played_at = sqlc.arg(after_time) AND li.item_id > sqlc.arg(after_item_id))))
           OR (CAST(sqlc.arg(after_null) AS INTEGER) <> 0 AND li.last_played_at IS NULL AND li.item_id > sqlc.arg(after_item_id)))
    ORDER BY (li.last_played_at IS NULL), li.last_played_at DESC, li.item_id LIMIT CAST(sqlc.arg(page_size) AS BIGINT);

-- name: GetCatalogItem :one
SELECT media_server_id, item_id, library_id, parent_id, item_type, name, series_id,
       series_name, season_id, season_number, index_number, runtime_ms, premiere_date,
       production_year, community_rating, genres, primary_image_tag, date_created,
       archived, plays, watch_seconds, unique_users, first_played_at, last_played_at,
       first_seen_at, last_seen_at, updated_at
FROM library_items
WHERE media_server_id = sqlc.arg(media_server_id) AND item_id = sqlc.arg(item_id);

-- name: CatalogItemPlaySummary :one
WITH root AS (
    SELECT library_items.item_id, library_items.item_type FROM library_items
    WHERE library_items.media_server_id = sqlc.arg(server_key)
      AND library_items.item_id = sqlc.arg(catalog_key)
), target_items AS (
    SELECT item_id, item_type AS root_type FROM root
    UNION ALL
    SELECT li.item_id, root.item_type FROM root
    JOIN library_items li ON li.media_server_id = sqlc.arg(server_key)
        AND li.season_id = root.item_id AND li.item_id <> root.item_id
    WHERE root.item_type = 'Season'
    UNION ALL
    SELECT li.item_id, root.item_type FROM root
    JOIN library_items li ON li.media_server_id = sqlc.arg(server_key)
        AND li.series_id = root.item_id AND li.item_id <> root.item_id
    WHERE root.item_type = 'Series'
), target_watches AS (
    SELECT w.active_seconds, w.media_user_id, w.started_at FROM target_items ti
    JOIN watches w ON w.media_server_id = sqlc.arg(server_key) AND w.item_id = ti.item_id
    WHERE ti.root_type <> 'Series'
    UNION ALL
    SELECT w.active_seconds, w.media_user_id, w.started_at FROM root
    JOIN watches w ON w.media_server_id = sqlc.arg(server_key) AND w.item_id = root.item_id
    WHERE root.item_type = 'Series'
    UNION ALL
    SELECT w.active_seconds, w.media_user_id, w.started_at FROM root
    JOIN watches w ON w.media_server_id = sqlc.arg(server_key) AND w.series_id = root.item_id
    JOIN target_items ti ON ti.item_id = w.item_id AND ti.root_type = 'Series'
    WHERE root.item_type = 'Series' AND w.item_id <> root.item_id
)
SELECT COUNT(*) AS plays, COALESCE(SUM(active_seconds), 0) AS watch_seconds,
       COUNT(DISTINCT media_user_id) AS unique_users,
       MIN(started_at) AS first_played_at, MAX(started_at) AS last_played_at
FROM target_watches;

-- name: CatalogChildSummary :many
SELECT item_type, COUNT(*) AS item_count
FROM library_items
WHERE media_server_id = sqlc.arg(media_server_id) AND archived = FALSE
  AND (parent_id = sqlc.arg(item_id) OR series_id = sqlc.arg(item_id) OR season_id = sqlc.arg(item_id))
GROUP BY item_type ORDER BY item_type LIMIT 100;

-- name: ListCatalogItemHistory :many
WITH root AS (
    SELECT library_items.item_id, library_items.item_type FROM library_items
    WHERE library_items.media_server_id = sqlc.arg(server_key)
      AND library_items.item_id = sqlc.arg(catalog_key)
), target_items AS (
    SELECT item_id, item_type AS root_type FROM root
    UNION ALL
    SELECT li.item_id, root.item_type FROM root
    JOIN library_items li ON li.media_server_id = sqlc.arg(server_key)
        AND li.season_id = root.item_id AND li.item_id <> root.item_id
    WHERE root.item_type = 'Season'
    UNION ALL
    SELECT li.item_id, root.item_type FROM root
    JOIN library_items li ON li.media_server_id = sqlc.arg(server_key)
        AND li.series_id = root.item_id AND li.item_id <> root.item_id
    WHERE root.item_type = 'Series'
), target_watches AS (
    SELECT w.id, w.media_server_id, w.media_user_id, w.username, w.device_id, w.device_name,
           w.client, w.server_session_id, w.item_id, w.item_name, w.item_type, w.series_name,
           w.season_number, w.episode_number, w.play_method, w.state, w.started_at, w.last_seen_at,
           w.ended_at, w.active_seconds, w.last_position_ms, w.source, w.created_at, w.updated_at,
           w.library_id, w.library_name, w.stream_container, w.stream_video_codec,
           w.stream_audio_codec, w.stream_bitrate, w.stream_width, w.stream_height,
           w.stream_framerate_hundredths, w.stream_audio_channels, w.stream_is_video_direct,
           w.stream_is_audio_direct, w.stream_transcode_reasons, w.runtime_ms, w.import_record_id,
           w.series_id, w.import_source, w.import_provenance_guard
    FROM target_items ti
    JOIN watches w ON w.media_server_id = sqlc.arg(server_key) AND w.item_id = ti.item_id
    WHERE ti.root_type <> 'Series'
    UNION ALL
    SELECT w.id, w.media_server_id, w.media_user_id, w.username, w.device_id, w.device_name,
           w.client, w.server_session_id, w.item_id, w.item_name, w.item_type, w.series_name,
           w.season_number, w.episode_number, w.play_method, w.state, w.started_at, w.last_seen_at,
           w.ended_at, w.active_seconds, w.last_position_ms, w.source, w.created_at, w.updated_at,
           w.library_id, w.library_name, w.stream_container, w.stream_video_codec,
           w.stream_audio_codec, w.stream_bitrate, w.stream_width, w.stream_height,
           w.stream_framerate_hundredths, w.stream_audio_channels, w.stream_is_video_direct,
           w.stream_is_audio_direct, w.stream_transcode_reasons, w.runtime_ms, w.import_record_id,
           w.series_id, w.import_source, w.import_provenance_guard
    FROM root
    JOIN watches w ON w.media_server_id = sqlc.arg(server_key) AND w.item_id = root.item_id
    WHERE root.item_type = 'Series'
    UNION ALL
    SELECT w.id, w.media_server_id, w.media_user_id, w.username, w.device_id, w.device_name,
           w.client, w.server_session_id, w.item_id, w.item_name, w.item_type, w.series_name,
           w.season_number, w.episode_number, w.play_method, w.state, w.started_at, w.last_seen_at,
           w.ended_at, w.active_seconds, w.last_position_ms, w.source, w.created_at, w.updated_at,
           w.library_id, w.library_name, w.stream_container, w.stream_video_codec,
           w.stream_audio_codec, w.stream_bitrate, w.stream_width, w.stream_height,
           w.stream_framerate_hundredths, w.stream_audio_channels, w.stream_is_video_direct,
           w.stream_is_audio_direct, w.stream_transcode_reasons, w.runtime_ms, w.import_record_id,
           w.series_id, w.import_source, w.import_provenance_guard
    FROM root
    JOIN watches w ON w.media_server_id = sqlc.arg(server_key) AND w.series_id = root.item_id
    JOIN target_items ti ON ti.item_id = w.item_id AND ti.root_type = 'Series'
    WHERE root.item_type = 'Series' AND w.item_id <> root.item_id
)
SELECT w.id, w.media_server_id, w.media_user_id, w.username, w.device_id, w.device_name,
       w.client, w.server_session_id, w.item_id, w.item_name, w.item_type, w.series_name,
       w.season_number, w.episode_number, w.play_method, w.state, w.started_at, w.last_seen_at,
       w.ended_at, w.active_seconds, w.last_position_ms, w.source, w.created_at, w.updated_at,
       w.library_id, w.library_name, w.stream_container, w.stream_video_codec,
       w.stream_audio_codec, w.stream_bitrate, w.stream_width, w.stream_height,
       w.stream_framerate_hundredths, w.stream_audio_channels, w.stream_is_video_direct,
       w.stream_is_audio_direct, w.stream_transcode_reasons, w.runtime_ms, w.import_record_id,
       w.series_id, w.import_source, w.import_provenance_guard, ms.name AS media_server_name
FROM target_watches w
JOIN media_servers ms ON ms.id = w.media_server_id
WHERE w.started_at < sqlc.arg(after_started_at)
   OR (w.started_at = sqlc.arg(after_started_at) AND w.id < sqlc.arg(after_id))
ORDER BY w.started_at DESC, w.id DESC
LIMIT CAST(sqlc.arg(page_size) AS BIGINT);

-- name: ListRecentCatalogItems :many
SELECT li.media_server_id, li.item_id, li.library_id, li.parent_id, li.item_type, li.name,
       li.series_id, li.series_name, li.season_id, li.season_number, li.index_number,
       li.runtime_ms, li.premiere_date, li.production_year, li.community_rating, li.genres,
       li.primary_image_tag, li.date_created, li.archived, li.plays, li.watch_seconds,
       li.unique_users, li.first_played_at, li.last_played_at, li.first_seen_at,
       li.last_seen_at, li.updated_at
FROM library_items li
WHERE li.media_server_id = sqlc.arg(media_server_id) AND li.library_id = sqlc.arg(library_id)
  AND li.archived = FALSE
ORDER BY (li.date_created IS NULL), li.date_created DESC, li.item_id
LIMIT CAST(sqlc.arg(row_limit) AS BIGINT);

-- name: ListCatalogGenreRows :many
SELECT lig.genre, COUNT(DISTINCT lig.item_id) AS item_count,
       COUNT(w.id) AS plays, COALESCE(SUM(w.active_seconds), 0) AS watch_seconds
FROM library_item_genres lig
JOIN library_items li ON li.media_server_id = lig.media_server_id AND li.item_id = lig.item_id
LEFT JOIN watches w ON w.media_server_id = lig.media_server_id AND w.item_id = lig.item_id
  AND (CAST(sqlc.arg(window_enabled) AS INTEGER) = 0
       OR (w.started_at >= sqlc.arg(window_start) AND w.started_at < sqlc.arg(window_end)))
WHERE lig.media_server_id = sqlc.arg(media_server_id) AND li.library_id = sqlc.arg(library_id)
  AND li.archived = FALSE
GROUP BY lig.genre ORDER BY lig.genre LIMIT 1001;

-- name: ListStaleCatalogItems :many
SELECT li.media_server_id, li.item_id, li.library_id, li.parent_id, li.item_type, li.name,
       li.series_id, li.series_name, li.season_id, li.season_number, li.index_number,
       li.runtime_ms, li.premiere_date, li.production_year, li.community_rating, li.genres,
       li.primary_image_tag, li.date_created, li.archived, li.plays, li.watch_seconds,
       li.unique_users, li.first_played_at, li.last_played_at, li.first_seen_at,
       li.last_seen_at, li.updated_at
FROM library_items li
WHERE li.media_server_id = sqlc.arg(media_server_id) AND li.library_id = sqlc.arg(library_id)
  AND li.archived = FALSE AND (li.last_played_at IS NULL OR li.last_played_at < sqlc.arg(stale_before))
  AND ((CAST(sqlc.arg(after_null) AS INTEGER) <> 0 AND
       (li.last_played_at IS NOT NULL
        OR (li.last_played_at IS NULL AND
         (li.name > sqlc.arg(after_name) OR (li.name = sqlc.arg(after_name) AND li.item_id > sqlc.arg(after_item_id))))))
   OR (CAST(sqlc.arg(after_null) AS INTEGER) = 0 AND
       li.last_played_at IS NOT NULL AND
       (li.last_played_at > sqlc.arg(after_time) OR
        (li.last_played_at = sqlc.arg(after_time) AND
         (li.name > sqlc.arg(after_name) OR (li.name = sqlc.arg(after_name) AND li.item_id > sqlc.arg(after_item_id)))))))
ORDER BY (li.last_played_at IS NOT NULL), li.last_played_at, li.name, li.item_id
LIMIT CAST(sqlc.arg(page_size) AS BIGINT);

-- name: ListCatalogImportItems :many
SELECT item_id, name, item_type, series_id, series_name, library_id,
       season_number, index_number, runtime_ms
FROM library_items
WHERE media_server_id = sqlc.arg(media_server_id) AND archived = FALSE
  AND item_id > sqlc.arg(after_item_id)
ORDER BY item_id LIMIT CAST(sqlc.arg(row_limit) AS BIGINT);
