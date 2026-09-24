-- PostgreSQL migration 00014: support unfiltered bounded statistics windows.

-- +goose Up
-- +goose StatementBegin
CREATE INDEX watches_started_idx ON watches (started_at DESC, id DESC);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX watches_started_idx;
-- +goose StatementEnd
