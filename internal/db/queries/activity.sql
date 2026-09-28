-- Per-user timeline query shared by SQLite and PostgreSQL. Activity title
-- matching is engine-specific so q remains a literal substring.

-- name: ListTimelineWatches :many
SELECT w.id, w.media_server_id, w.media_user_id, w.username, w.device_id, w.device_name,
       w.client, w.server_session_id, w.item_id, w.item_name, w.item_type, w.series_name,
       w.season_number, w.episode_number, w.play_method, w.state, w.started_at, w.last_seen_at,
       w.ended_at, w.active_seconds, w.last_position_ms, w.source, w.created_at, w.updated_at,
       w.library_id, w.library_name, w.stream_container, w.stream_video_codec,
       w.stream_audio_codec, w.stream_bitrate, w.stream_width, w.stream_height,
       w.stream_framerate_hundredths, w.stream_audio_channels, w.stream_is_video_direct,
       w.stream_is_audio_direct, w.stream_transcode_reasons, w.runtime_ms, w.import_record_id,
       w.series_id, w.import_source, w.import_provenance_guard, w.import_origin_record_id,
       ms.name AS media_server_name
FROM watches w
JOIN media_servers ms ON ms.id = w.media_server_id
WHERE w.media_server_id = sqlc.arg(media_server_id)
  AND w.media_user_id = sqlc.arg(media_user_id)
  AND (w.started_at, w.id) < (sqlc.arg(before_started_at), CAST(sqlc.arg(before_id) AS TEXT))
  AND NOT EXISTS (
      SELECT 1 FROM media_server_exclusions e
      WHERE e.media_server_id = w.media_server_id
        AND ((e.kind = 'media_user' AND e.external_id = w.media_user_id)
          OR (e.kind = 'library' AND (e.external_id = w.library_id OR EXISTS (
              SELECT 1 FROM library_items excluded_item
              WHERE excluded_item.media_server_id = w.media_server_id
                AND excluded_item.item_id = w.item_id
                AND excluded_item.library_id = e.external_id))))
  )
ORDER BY w.started_at DESC, w.id DESC
LIMIT sqlc.arg(page_size);
