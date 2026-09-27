-- History-import queries shared by SQLite and PostgreSQL.

-- name: CreateImport :exec
INSERT INTO imports (
    id, media_server_id, source, state, cursor, read_count, imported_count,
    skipped_count, duplicate_count, last_error, lease_token, lease_expires_at,
    requested_by, created_at, started_at, finished_at, updated_at
) VALUES (
    sqlc.arg(id), sqlc.arg(media_server_id), sqlc.arg(source), sqlc.arg(state),
    sqlc.arg(cursor), sqlc.arg(read_count), sqlc.arg(imported_count),
    sqlc.arg(skipped_count), sqlc.arg(duplicate_count), sqlc.arg(last_error),
    sqlc.arg(lease_token), sqlc.narg(lease_expires_at), sqlc.arg(requested_by),
    sqlc.arg(created_at), sqlc.narg(started_at), sqlc.narg(finished_at), sqlc.arg(updated_at)
);

-- name: GetImport :one
SELECT * FROM imports WHERE id = sqlc.arg(id);

-- name: ListActiveBloomImportCursors :many
SELECT cursor FROM imports
WHERE source = 'bloom_export' AND state IN ('pending', 'running')
ORDER BY id
LIMIT 10001;

-- name: ListImports :many
SELECT * FROM imports
WHERE (created_at < sqlc.arg(before_created_at)
       OR (created_at = sqlc.arg(before_created_at) AND id < sqlc.arg(before_id)))
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg(page_size);

-- name: CancelImport :execrows
UPDATE imports
SET state = 'cancelled', finished_at = sqlc.arg(now), updated_at = sqlc.arg(now),
    lease_token = '', lease_expires_at = NULL
WHERE id = sqlc.arg(id) AND state IN ('pending', 'running');

-- name: RenewImportLease :execrows
UPDATE imports
SET lease_expires_at = sqlc.arg(expires_at), updated_at = sqlc.arg(now)
WHERE id = sqlc.arg(id) AND state = 'running' AND lease_token = sqlc.arg(token);

-- name: FinishImport :execrows
UPDATE imports
SET state = sqlc.arg(state), last_error = sqlc.arg(last_error),
    finished_at = sqlc.arg(now), updated_at = sqlc.arg(now),
    lease_token = '', lease_expires_at = NULL
WHERE id = sqlc.arg(id) AND state = 'running' AND lease_token = sqlc.arg(token);

-- name: CheckpointImport :execrows
UPDATE imports
SET cursor = sqlc.arg(cursor), read_count = read_count + sqlc.arg(read_delta),
    imported_count = imported_count + sqlc.arg(imported_delta),
    skipped_count = skipped_count + sqlc.arg(skipped_delta),
    duplicate_count = duplicate_count + sqlc.arg(duplicate_delta),
    lease_expires_at = sqlc.arg(expires_at), updated_at = sqlc.arg(now)
WHERE id = sqlc.arg(id) AND state = 'running' AND lease_token = sqlc.arg(token);

-- name: FindCollectedImportDuplicate :one
SELECT EXISTS (
    SELECT 1 FROM watches
    WHERE media_server_id = sqlc.arg(media_server_id)
      AND media_user_id = sqlc.arg(media_user_id)
      AND item_id = sqlc.arg(item_id)
      AND source <> 'import'
      AND started_at >= sqlc.arg(start_after)
      AND started_at <= sqlc.arg(start_before)
);

-- name: InsertImportedWatch :execrows
INSERT INTO watches (
    id, media_server_id, media_user_id, username, device_id, device_name, client,
    server_session_id, item_id, item_name, item_type, series_name, library_id,
    library_name, season_number, episode_number, play_method,
    stream_container, stream_video_codec, stream_audio_codec, stream_bitrate,
    stream_width, stream_height, stream_framerate_hundredths, stream_audio_channels,
    stream_is_video_direct, stream_is_audio_direct, stream_transcode_reasons,
    state, started_at, last_seen_at, ended_at, active_seconds, last_position_ms, source,
    created_at, updated_at, import_source, import_record_id
) VALUES (
    sqlc.arg(id), sqlc.arg(media_server_id), sqlc.arg(media_user_id), sqlc.arg(username),
    sqlc.arg(device_id), sqlc.arg(device_name), sqlc.arg(client), '', sqlc.arg(item_id), sqlc.arg(item_name),
    sqlc.arg(item_type), sqlc.arg(series_name), sqlc.arg(library_id), sqlc.arg(library_name),
    sqlc.narg(season_number), sqlc.narg(episode_number), sqlc.arg(play_method),
    sqlc.narg(stream_container), sqlc.narg(stream_video_codec), sqlc.narg(stream_audio_codec),
    sqlc.narg(stream_bitrate), sqlc.narg(stream_width), sqlc.narg(stream_height),
    sqlc.narg(stream_framerate_hundredths), sqlc.narg(stream_audio_channels),
    sqlc.narg(stream_is_video_direct), sqlc.narg(stream_is_audio_direct),
    sqlc.narg(stream_transcode_reasons), 'stopped', sqlc.arg(started_at), sqlc.arg(ended_at),
    sqlc.arg(ended_at), sqlc.arg(active_seconds), sqlc.arg(last_position_ms), 'import',
    sqlc.arg(now), sqlc.arg(now), sqlc.arg(import_source), sqlc.arg(import_record_id)
)
ON CONFLICT (media_server_id, import_source, import_record_id)
WHERE import_record_id IS NOT NULL DO NOTHING;
