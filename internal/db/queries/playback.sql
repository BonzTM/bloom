-- Playback queries are portable across SQLite and PostgreSQL.

-- name: UpsertPlaybackWatch :exec
INSERT INTO watches (
    id, media_server_id, media_user_id, username, device_id, device_name, client,
    server_session_id, item_id, item_name, item_type, series_name, library_id,
    library_name, season_number,
    episode_number, play_method, state, started_at, last_seen_at, ended_at,
    active_seconds, last_position_ms, source, created_at, updated_at
) VALUES (
    sqlc.arg(id), sqlc.arg(media_server_id), sqlc.arg(media_user_id), sqlc.arg(username),
    sqlc.arg(device_id), sqlc.arg(device_name), sqlc.arg(client), sqlc.arg(server_session_id),
    sqlc.arg(item_id), sqlc.arg(item_name), sqlc.arg(item_type), sqlc.arg(series_name),
    sqlc.arg(library_id), sqlc.arg(library_name),
    sqlc.narg(season_number), sqlc.narg(episode_number), sqlc.arg(play_method), sqlc.arg(state),
    sqlc.arg(started_at), sqlc.arg(last_seen_at), sqlc.narg(ended_at), sqlc.arg(active_seconds),
    sqlc.arg(last_position_ms), sqlc.arg(source), sqlc.arg(created_at), sqlc.arg(updated_at)
)
ON CONFLICT (id) DO UPDATE SET
    username = excluded.username,
    device_name = excluded.device_name,
    client = excluded.client,
    server_session_id = excluded.server_session_id,
    item_name = excluded.item_name,
    item_type = excluded.item_type,
    series_name = excluded.series_name,
    library_id = CASE WHEN watches.library_id = '' THEN excluded.library_id ELSE watches.library_id END,
    library_name = CASE WHEN watches.library_id = '' THEN excluded.library_name ELSE watches.library_name END,
    season_number = excluded.season_number,
    episode_number = excluded.episode_number,
    play_method = excluded.play_method,
    state = excluded.state,
    last_seen_at = excluded.last_seen_at,
    ended_at = excluded.ended_at,
    active_seconds = excluded.active_seconds,
    last_position_ms = excluded.last_position_ms,
    updated_at = excluded.updated_at;

-- name: ListOpenPlaybackWatches :many
SELECT w.*, ms.name AS media_server_name
FROM watches w
JOIN media_servers ms ON ms.id = w.media_server_id
WHERE w.media_server_id = sqlc.arg(media_server_id) AND w.state <> 'stopped'
ORDER BY w.started_at, w.id
LIMIT 102400;

-- name: ListNowPlaying :many
SELECT w.*, ms.name AS media_server_name
FROM watches w
JOIN media_servers ms ON ms.id = w.media_server_id
WHERE w.state <> 'stopped'
  AND (w.started_at < sqlc.arg(before_started_at)
       OR (w.started_at = sqlc.arg(before_started_at) AND w.id < sqlc.arg(before_id)))
ORDER BY w.started_at DESC, w.id DESC
LIMIT sqlc.arg(page_size);

-- name: ListUnresolvedWatchItemIDs :many
SELECT w.item_id
FROM watches w
WHERE w.media_server_id = sqlc.arg(media_server_id) AND w.library_id = ''
GROUP BY w.item_id
ORDER BY MIN(w.started_at), w.item_id
LIMIT sqlc.arg(row_limit);

-- name: BackfillWatchLibrary :execrows
UPDATE watches
SET library_id = sqlc.arg(library_id), library_name = sqlc.arg(library_name)
WHERE media_server_id = sqlc.arg(media_server_id)
  AND item_id = sqlc.arg(item_id)
  AND library_id = '';

-- name: ListPlaybackHistory :many
SELECT w.*, ms.name AS media_server_name
FROM watches w
JOIN media_servers ms ON ms.id = w.media_server_id
WHERE w.state = 'stopped'
  AND (CAST(sqlc.arg(media_server_filter) AS TEXT) = ''
       OR w.media_server_id = CAST(sqlc.arg(media_server_filter) AS TEXT))
  AND (w.started_at < sqlc.arg(before_started_at)
       OR (w.started_at = sqlc.arg(before_started_at) AND w.id < sqlc.arg(before_id)))
ORDER BY w.started_at DESC, w.id DESC
LIMIT sqlc.arg(page_size);

-- name: FindRecentPlaybackWatch :one
SELECT w.*, ms.name AS media_server_name
FROM watches w
JOIN media_servers ms ON ms.id = w.media_server_id
WHERE w.state = 'stopped'
  AND w.media_server_id = sqlc.arg(media_server_id)
  AND w.media_user_id = sqlc.arg(media_user_id)
  AND w.device_id = sqlc.arg(device_id)
  AND w.item_id = sqlc.arg(item_id)
  AND (w.server_session_id = sqlc.arg(server_session_id)
       OR w.server_session_id = '' OR sqlc.arg(server_session_id) = '')
  AND w.ended_at >= sqlc.arg(ended_after)
ORDER BY w.ended_at DESC, w.id DESC
LIMIT 1;

-- name: ListRecentPlaybackWatches :many
SELECT w.*, ms.name AS media_server_name
FROM watches w
JOIN media_servers ms ON ms.id = w.media_server_id
WHERE w.state = 'stopped'
  AND w.media_server_id = sqlc.arg(media_server_id)
  AND w.ended_at >= sqlc.arg(ended_after)
ORDER BY w.ended_at DESC, w.id DESC
LIMIT sqlc.arg(page_size);

-- name: CreateWatchSegment :exec
INSERT INTO watch_segments (watch_id, started_at, ended_at, source)
VALUES (sqlc.arg(watch_id), sqlc.arg(started_at), NULL, sqlc.arg(source))
ON CONFLICT (watch_id, started_at) DO NOTHING;

-- name: CloseOpenWatchSegment :execrows
UPDATE watch_segments
SET ended_at = sqlc.arg(ended_at)
WHERE watch_id = sqlc.arg(watch_id) AND ended_at IS NULL;

-- name: UpsertWatchPosition :exec
INSERT INTO watch_positions (watch_id, observed_at, position_ms, paused, play_method, source)
VALUES (sqlc.arg(watch_id), sqlc.arg(observed_at), sqlc.arg(position_ms), sqlc.arg(paused), sqlc.arg(play_method), sqlc.arg(source))
ON CONFLICT (watch_id, observed_at) DO UPDATE SET
    position_ms = excluded.position_ms,
    paused = excluded.paused,
    play_method = excluded.play_method,
    source = excluded.source;

-- name: TrimWatchPositions :exec
DELETE FROM watch_positions
WHERE watch_positions.watch_id = sqlc.arg(watch_id)
  AND watch_positions.observed_at NOT IN (
      SELECT kept.observed_at
      FROM watch_positions AS kept
      WHERE kept.watch_id = sqlc.arg(watch_id)
      ORDER BY kept.observed_at DESC
      LIMIT 512
  );
