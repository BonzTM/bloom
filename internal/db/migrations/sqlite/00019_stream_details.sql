-- SQLite migration 00019: stream details on watches and position samples.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE watches ADD COLUMN stream_container TEXT
    CHECK (stream_container IS NULL OR length(CAST(stream_container AS BLOB)) <= 64);
ALTER TABLE watches ADD COLUMN stream_video_codec TEXT
    CHECK (stream_video_codec IS NULL OR length(CAST(stream_video_codec AS BLOB)) <= 64);
ALTER TABLE watches ADD COLUMN stream_audio_codec TEXT
    CHECK (stream_audio_codec IS NULL OR length(CAST(stream_audio_codec AS BLOB)) <= 64);
ALTER TABLE watches ADD COLUMN stream_bitrate INTEGER
    CHECK (stream_bitrate IS NULL OR stream_bitrate BETWEEN 0 AND 2147483647);
ALTER TABLE watches ADD COLUMN stream_width INTEGER
    CHECK (stream_width IS NULL OR stream_width BETWEEN 0 AND 65535);
ALTER TABLE watches ADD COLUMN stream_height INTEGER
    CHECK (stream_height IS NULL OR stream_height BETWEEN 0 AND 65535);
ALTER TABLE watches ADD COLUMN stream_framerate_hundredths INTEGER
    CHECK (stream_framerate_hundredths IS NULL OR stream_framerate_hundredths BETWEEN 0 AND 100000);
ALTER TABLE watches ADD COLUMN stream_audio_channels INTEGER
    CHECK (stream_audio_channels IS NULL OR stream_audio_channels BETWEEN 0 AND 64);
ALTER TABLE watches ADD COLUMN stream_is_video_direct INTEGER
    CHECK (stream_is_video_direct IS NULL OR stream_is_video_direct IN (0, 1));
ALTER TABLE watches ADD COLUMN stream_is_audio_direct INTEGER
    CHECK (stream_is_audio_direct IS NULL OR stream_is_audio_direct IN (0, 1));
ALTER TABLE watches ADD COLUMN stream_transcode_reasons TEXT
    CHECK (stream_transcode_reasons IS NULL OR length(CAST(stream_transcode_reasons AS BLOB)) <= 8192);

ALTER TABLE watch_positions ADD COLUMN stream_container TEXT
    CHECK (stream_container IS NULL OR length(CAST(stream_container AS BLOB)) <= 64);
ALTER TABLE watch_positions ADD COLUMN stream_video_codec TEXT
    CHECK (stream_video_codec IS NULL OR length(CAST(stream_video_codec AS BLOB)) <= 64);
ALTER TABLE watch_positions ADD COLUMN stream_audio_codec TEXT
    CHECK (stream_audio_codec IS NULL OR length(CAST(stream_audio_codec AS BLOB)) <= 64);
ALTER TABLE watch_positions ADD COLUMN stream_bitrate INTEGER
    CHECK (stream_bitrate IS NULL OR stream_bitrate BETWEEN 0 AND 2147483647);
ALTER TABLE watch_positions ADD COLUMN stream_width INTEGER
    CHECK (stream_width IS NULL OR stream_width BETWEEN 0 AND 65535);
ALTER TABLE watch_positions ADD COLUMN stream_height INTEGER
    CHECK (stream_height IS NULL OR stream_height BETWEEN 0 AND 65535);
ALTER TABLE watch_positions ADD COLUMN stream_framerate_hundredths INTEGER
    CHECK (stream_framerate_hundredths IS NULL OR stream_framerate_hundredths BETWEEN 0 AND 100000);
ALTER TABLE watch_positions ADD COLUMN stream_audio_channels INTEGER
    CHECK (stream_audio_channels IS NULL OR stream_audio_channels BETWEEN 0 AND 64);
ALTER TABLE watch_positions ADD COLUMN stream_is_video_direct INTEGER
    CHECK (stream_is_video_direct IS NULL OR stream_is_video_direct IN (0, 1));
ALTER TABLE watch_positions ADD COLUMN stream_is_audio_direct INTEGER
    CHECK (stream_is_audio_direct IS NULL OR stream_is_audio_direct IN (0, 1));
ALTER TABLE watch_positions ADD COLUMN stream_transcode_reasons TEXT
    CHECK (stream_transcode_reasons IS NULL OR length(CAST(stream_transcode_reasons AS BLOB)) <= 8192);
ALTER TABLE watch_positions ADD COLUMN is_transition INTEGER NOT NULL DEFAULT 0
    CHECK (is_transition IN (0, 1));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE watch_positions DROP COLUMN is_transition;
ALTER TABLE watch_positions DROP COLUMN stream_transcode_reasons;
ALTER TABLE watch_positions DROP COLUMN stream_is_audio_direct;
ALTER TABLE watch_positions DROP COLUMN stream_is_video_direct;
ALTER TABLE watch_positions DROP COLUMN stream_audio_channels;
ALTER TABLE watch_positions DROP COLUMN stream_framerate_hundredths;
ALTER TABLE watch_positions DROP COLUMN stream_height;
ALTER TABLE watch_positions DROP COLUMN stream_width;
ALTER TABLE watch_positions DROP COLUMN stream_bitrate;
ALTER TABLE watch_positions DROP COLUMN stream_audio_codec;
ALTER TABLE watch_positions DROP COLUMN stream_video_codec;
ALTER TABLE watch_positions DROP COLUMN stream_container;
ALTER TABLE watches DROP COLUMN stream_transcode_reasons;
ALTER TABLE watches DROP COLUMN stream_is_audio_direct;
ALTER TABLE watches DROP COLUMN stream_is_video_direct;
ALTER TABLE watches DROP COLUMN stream_audio_channels;
ALTER TABLE watches DROP COLUMN stream_framerate_hundredths;
ALTER TABLE watches DROP COLUMN stream_height;
ALTER TABLE watches DROP COLUMN stream_width;
ALTER TABLE watches DROP COLUMN stream_bitrate;
ALTER TABLE watches DROP COLUMN stream_audio_codec;
ALTER TABLE watches DROP COLUMN stream_video_codec;
ALTER TABLE watches DROP COLUMN stream_container;
-- +goose StatementEnd
