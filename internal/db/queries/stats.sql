-- Statistics queries are portable across SQLite and PostgreSQL.

-- name: StatsTotals :one
SELECT
    COUNT(*) AS plays,
    COALESCE(SUM(w.active_seconds), 0) AS watch_seconds,
    COUNT(DISTINCT (w.media_server_id || ':' || w.media_user_id)) AS unique_users,
    COUNT(DISTINCT (
        w.media_server_id || ':' ||
        CASE
            WHEN LOWER(w.item_type) = 'movie' THEN 'movie:' || w.item_id
            WHEN w.series_name <> '' THEN 'series:' || w.series_name
            ELSE 'other:' || w.item_type
        END
    )) AS unique_titles
FROM watches w
WHERE w.started_at >= sqlc.arg(window_start)
  AND w.started_at < sqlc.arg(window_end)
  AND (CAST(sqlc.arg(media_server_filter) AS TEXT) = ''
       OR w.media_server_id = CAST(sqlc.arg(media_server_filter) AS TEXT))
  AND (CAST(sqlc.arg(user_server_filter) AS TEXT) = ''
       OR (w.media_server_id = CAST(sqlc.arg(user_server_filter) AS TEXT)
           AND w.media_user_id = CAST(sqlc.arg(media_user_filter) AS TEXT)));

-- name: StatsMovieTitles :many
SELECT w.media_server_id, w.item_id AS title_key, MAX(w.item_name) AS title_name,
       COUNT(*) AS plays, COALESCE(SUM(w.active_seconds), 0) AS watch_seconds,
       MAX(w.started_at) AS last_watched_at
FROM watches w
WHERE w.started_at >= sqlc.arg(window_start)
  AND w.started_at < sqlc.arg(window_end)
  AND LOWER(w.item_type) = 'movie'
  AND (CAST(sqlc.arg(media_server_filter) AS TEXT) = ''
       OR w.media_server_id = CAST(sqlc.arg(media_server_filter) AS TEXT))
  AND (CAST(sqlc.arg(user_server_filter) AS TEXT) = ''
       OR (w.media_server_id = CAST(sqlc.arg(user_server_filter) AS TEXT)
           AND w.media_user_id = CAST(sqlc.arg(media_user_filter) AS TEXT)))
GROUP BY w.media_server_id, w.item_id
ORDER BY plays DESC, watch_seconds DESC, title_key ASC, w.media_server_id ASC
LIMIT sqlc.arg(row_limit);

-- name: StatsSeriesTitles :many
SELECT w.media_server_id, w.series_name AS title_key, w.series_name AS title_name,
       COUNT(*) AS plays, COALESCE(SUM(w.active_seconds), 0) AS watch_seconds,
       MAX(w.started_at) AS last_watched_at
FROM watches w
WHERE w.started_at >= sqlc.arg(window_start)
  AND w.started_at < sqlc.arg(window_end)
  AND LOWER(w.item_type) <> 'movie'
  AND w.series_name <> ''
  AND (CAST(sqlc.arg(media_server_filter) AS TEXT) = ''
       OR w.media_server_id = CAST(sqlc.arg(media_server_filter) AS TEXT))
  AND (CAST(sqlc.arg(user_server_filter) AS TEXT) = ''
       OR (w.media_server_id = CAST(sqlc.arg(user_server_filter) AS TEXT)
           AND w.media_user_id = CAST(sqlc.arg(media_user_filter) AS TEXT)))
GROUP BY w.media_server_id, w.series_name
ORDER BY plays DESC, watch_seconds DESC, title_key ASC, w.media_server_id ASC
LIMIT sqlc.arg(row_limit);

-- name: StatsOtherTitles :many
SELECT w.media_server_id, w.item_type AS title_key, w.item_type AS title_name,
       COUNT(*) AS plays, COALESCE(SUM(w.active_seconds), 0) AS watch_seconds,
       MAX(w.started_at) AS last_watched_at
FROM watches w
WHERE w.started_at >= sqlc.arg(window_start)
  AND w.started_at < sqlc.arg(window_end)
  AND LOWER(w.item_type) <> 'movie'
  AND w.series_name = ''
  AND (CAST(sqlc.arg(media_server_filter) AS TEXT) = ''
       OR w.media_server_id = CAST(sqlc.arg(media_server_filter) AS TEXT))
  AND (CAST(sqlc.arg(user_server_filter) AS TEXT) = ''
       OR (w.media_server_id = CAST(sqlc.arg(user_server_filter) AS TEXT)
           AND w.media_user_id = CAST(sqlc.arg(media_user_filter) AS TEXT)))
GROUP BY w.media_server_id, w.item_type
ORDER BY plays DESC, watch_seconds DESC, title_key ASC, w.media_server_id ASC
LIMIT sqlc.arg(row_limit);

-- name: StatsUsers :many
SELECT w.media_server_id, w.media_user_id, MAX(w.username) AS username,
       COUNT(*) AS plays, COALESCE(SUM(w.active_seconds), 0) AS watch_seconds,
       MAX(w.started_at) AS last_watched_at
FROM watches w
WHERE w.started_at >= sqlc.arg(window_start)
  AND w.started_at < sqlc.arg(window_end)
  AND (CAST(sqlc.arg(media_server_filter) AS TEXT) = ''
       OR w.media_server_id = CAST(sqlc.arg(media_server_filter) AS TEXT))
GROUP BY w.media_server_id, w.media_user_id
ORDER BY plays DESC, watch_seconds DESC, w.media_user_id ASC, w.media_server_id ASC
LIMIT sqlc.arg(row_limit);

-- name: StatsClients :many
SELECT w.client AS name, COUNT(*) AS plays,
       COALESCE(SUM(w.active_seconds), 0) AS watch_seconds
FROM watches w
WHERE w.started_at >= sqlc.arg(window_start)
  AND w.started_at < sqlc.arg(window_end)
  AND (CAST(sqlc.arg(media_server_filter) AS TEXT) = ''
       OR w.media_server_id = CAST(sqlc.arg(media_server_filter) AS TEXT))
  AND (CAST(sqlc.arg(user_server_filter) AS TEXT) = ''
       OR (w.media_server_id = CAST(sqlc.arg(user_server_filter) AS TEXT)
           AND w.media_user_id = CAST(sqlc.arg(media_user_filter) AS TEXT)))
GROUP BY w.client
ORDER BY plays DESC, watch_seconds DESC, name ASC
LIMIT 1024;

-- name: StatsDevices :many
SELECT w.device_name AS name, COUNT(*) AS plays,
       COALESCE(SUM(w.active_seconds), 0) AS watch_seconds
FROM watches w
WHERE w.started_at >= sqlc.arg(window_start)
  AND w.started_at < sqlc.arg(window_end)
  AND (CAST(sqlc.arg(media_server_filter) AS TEXT) = ''
       OR w.media_server_id = CAST(sqlc.arg(media_server_filter) AS TEXT))
  AND (CAST(sqlc.arg(user_server_filter) AS TEXT) = ''
       OR (w.media_server_id = CAST(sqlc.arg(user_server_filter) AS TEXT)
           AND w.media_user_id = CAST(sqlc.arg(media_user_filter) AS TEXT)))
GROUP BY w.device_name
ORDER BY plays DESC, watch_seconds DESC, name ASC
LIMIT 1024;

-- name: StatsPlayMethods :many
SELECT w.play_method AS name, COUNT(*) AS plays,
       COALESCE(SUM(w.active_seconds), 0) AS watch_seconds
FROM watches w
WHERE w.started_at >= sqlc.arg(window_start)
  AND w.started_at < sqlc.arg(window_end)
  AND (CAST(sqlc.arg(media_server_filter) AS TEXT) = ''
       OR w.media_server_id = CAST(sqlc.arg(media_server_filter) AS TEXT))
  AND (CAST(sqlc.arg(user_server_filter) AS TEXT) = ''
       OR (w.media_server_id = CAST(sqlc.arg(user_server_filter) AS TEXT)
           AND w.media_user_id = CAST(sqlc.arg(media_user_filter) AS TEXT)))
GROUP BY w.play_method
ORDER BY plays DESC, watch_seconds DESC, name ASC
LIMIT 8;

-- name: StatsBucketRows :many
SELECT w.started_at, w.active_seconds
FROM watches w
WHERE w.started_at >= sqlc.arg(window_start)
  AND w.started_at < sqlc.arg(window_end)
  AND (CAST(sqlc.arg(media_server_filter) AS TEXT) = ''
       OR w.media_server_id = CAST(sqlc.arg(media_server_filter) AS TEXT))
  AND (CAST(sqlc.arg(user_server_filter) AS TEXT) = ''
       OR (w.media_server_id = CAST(sqlc.arg(user_server_filter) AS TEXT)
           AND w.media_user_id = CAST(sqlc.arg(media_user_filter) AS TEXT)))
ORDER BY w.started_at, w.id
LIMIT sqlc.arg(row_limit);

-- name: StatsUserRecentWatches :many
SELECT w.*, ms.name AS media_server_name
FROM watches w
JOIN media_servers ms ON ms.id = w.media_server_id
WHERE w.started_at >= sqlc.arg(window_start)
  AND w.started_at < sqlc.arg(window_end)
  AND w.media_server_id = sqlc.arg(user_server_id)
  AND w.media_user_id = sqlc.arg(media_user_id)
ORDER BY w.started_at DESC, w.id DESC
LIMIT 20;
