-- SQLite activity query. instr() treats q as a literal substring.

-- name: ListActivityWatches :many
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
WHERE (CAST(sqlc.arg(media_server_filter) AS TEXT) = '' OR w.media_server_id = CAST(sqlc.arg(media_server_filter) AS TEXT))
  AND (CAST(sqlc.arg(media_user_filter) AS TEXT) = '' OR w.media_user_id = CAST(sqlc.arg(media_user_filter) AS TEXT))
  AND (CAST(sqlc.arg(library_filter) AS TEXT) = '' OR w.library_id = CAST(sqlc.arg(library_filter) AS TEXT)
       OR EXISTS (SELECT 1 FROM library_items li WHERE li.media_server_id = w.media_server_id
                  AND li.item_id = w.item_id AND li.library_id = CAST(sqlc.arg(library_filter) AS TEXT)))
  AND (CAST(sqlc.arg(item_type_filter) AS TEXT) = '' OR w.item_type = CAST(sqlc.arg(item_type_filter) AS TEXT))
  AND (CAST(sqlc.arg(client_filter) AS TEXT) = '' OR w.client = CAST(sqlc.arg(client_filter) AS TEXT))
  AND (CAST(sqlc.arg(device_filter) AS TEXT) = '' OR w.device_id = CAST(sqlc.arg(device_filter) AS TEXT))
  AND (CAST(sqlc.arg(play_method_filter) AS TEXT) = '' OR w.play_method = CAST(sqlc.arg(play_method_filter) AS TEXT))
  AND (CAST(sqlc.arg(source_filter) AS TEXT) = '' OR w.source = CAST(sqlc.arg(source_filter) AS TEXT))
  AND (CAST(sqlc.arg(import_source_filter) AS TEXT) = '' OR w.import_source = CAST(sqlc.arg(import_source_filter) AS TEXT))
  AND (CAST(sqlc.arg(started_after_set) AS INTEGER) = 0 OR w.started_at >= sqlc.arg(started_after))
  AND (CAST(sqlc.arg(started_before_set) AS INTEGER) = 0 OR w.started_at < sqlc.arg(started_before))
  AND (CAST(sqlc.arg(search_text) AS TEXT) = ''
       OR instr(lower(w.item_name), lower(CAST(sqlc.arg(search_text) AS TEXT))) > 0
       OR instr(lower(w.series_name), lower(CAST(sqlc.arg(search_text) AS TEXT))) > 0)
  AND (w.started_at < sqlc.arg(before_started_at)
       OR (w.started_at = sqlc.arg(before_started_at) AND w.id < sqlc.arg(before_id)))
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
