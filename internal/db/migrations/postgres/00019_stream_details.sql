-- PostgreSQL migration 00019: stream details on watches and position samples.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE watches
    ADD COLUMN stream_container TEXT COLLATE "C" CHECK (stream_container IS NULL OR octet_length(stream_container) <= 64),
    ADD COLUMN stream_video_codec TEXT COLLATE "C" CHECK (stream_video_codec IS NULL OR octet_length(stream_video_codec) <= 64),
    ADD COLUMN stream_audio_codec TEXT COLLATE "C" CHECK (stream_audio_codec IS NULL OR octet_length(stream_audio_codec) <= 64),
    ADD COLUMN stream_bitrate BIGINT CHECK (stream_bitrate IS NULL OR stream_bitrate BETWEEN 0 AND 2147483647),
    ADD COLUMN stream_width INTEGER CHECK (stream_width IS NULL OR stream_width BETWEEN 0 AND 65535),
    ADD COLUMN stream_height INTEGER CHECK (stream_height IS NULL OR stream_height BETWEEN 0 AND 65535),
    ADD COLUMN stream_framerate_hundredths INTEGER CHECK (stream_framerate_hundredths IS NULL OR stream_framerate_hundredths BETWEEN 0 AND 100000),
    ADD COLUMN stream_audio_channels INTEGER CHECK (stream_audio_channels IS NULL OR stream_audio_channels BETWEEN 0 AND 64),
    ADD COLUMN stream_is_video_direct BOOLEAN,
    ADD COLUMN stream_is_audio_direct BOOLEAN,
    ADD COLUMN stream_transcode_reasons TEXT COLLATE "C" CHECK (stream_transcode_reasons IS NULL OR octet_length(stream_transcode_reasons) <= 8192);

ALTER TABLE watch_positions
    ADD COLUMN stream_container TEXT COLLATE "C" CHECK (stream_container IS NULL OR octet_length(stream_container) <= 64),
    ADD COLUMN stream_video_codec TEXT COLLATE "C" CHECK (stream_video_codec IS NULL OR octet_length(stream_video_codec) <= 64),
    ADD COLUMN stream_audio_codec TEXT COLLATE "C" CHECK (stream_audio_codec IS NULL OR octet_length(stream_audio_codec) <= 64),
    ADD COLUMN stream_bitrate BIGINT CHECK (stream_bitrate IS NULL OR stream_bitrate BETWEEN 0 AND 2147483647),
    ADD COLUMN stream_width INTEGER CHECK (stream_width IS NULL OR stream_width BETWEEN 0 AND 65535),
    ADD COLUMN stream_height INTEGER CHECK (stream_height IS NULL OR stream_height BETWEEN 0 AND 65535),
    ADD COLUMN stream_framerate_hundredths INTEGER CHECK (stream_framerate_hundredths IS NULL OR stream_framerate_hundredths BETWEEN 0 AND 100000),
    ADD COLUMN stream_audio_channels INTEGER CHECK (stream_audio_channels IS NULL OR stream_audio_channels BETWEEN 0 AND 64),
    ADD COLUMN stream_is_video_direct BOOLEAN,
    ADD COLUMN stream_is_audio_direct BOOLEAN,
    ADD COLUMN stream_transcode_reasons TEXT COLLATE "C" CHECK (stream_transcode_reasons IS NULL OR octet_length(stream_transcode_reasons) <= 8192);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE watch_positions
    DROP COLUMN stream_transcode_reasons,
    DROP COLUMN stream_is_audio_direct,
    DROP COLUMN stream_is_video_direct,
    DROP COLUMN stream_audio_channels,
    DROP COLUMN stream_framerate_hundredths,
    DROP COLUMN stream_height,
    DROP COLUMN stream_width,
    DROP COLUMN stream_bitrate,
    DROP COLUMN stream_audio_codec,
    DROP COLUMN stream_video_codec,
    DROP COLUMN stream_container;
ALTER TABLE watches
    DROP COLUMN stream_transcode_reasons,
    DROP COLUMN stream_is_audio_direct,
    DROP COLUMN stream_is_video_direct,
    DROP COLUMN stream_audio_channels,
    DROP COLUMN stream_framerate_hundredths,
    DROP COLUMN stream_height,
    DROP COLUMN stream_width,
    DROP COLUMN stream_bitrate,
    DROP COLUMN stream_audio_codec,
    DROP COLUMN stream_video_codec,
    DROP COLUMN stream_container;
-- +goose StatementEnd
