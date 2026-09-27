-- PostgreSQL migration 00020: nullable item runtime on playback watches.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE watches ADD COLUMN runtime_ms BIGINT
    CHECK (runtime_ms IS NULL OR runtime_ms BETWEEN 0 AND 9223372036854);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE watches DROP COLUMN runtime_ms;
-- +goose StatementEnd
