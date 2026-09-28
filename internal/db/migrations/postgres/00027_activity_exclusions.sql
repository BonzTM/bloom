-- PostgreSQL migration 00027: activity keysets and per-server collection exclusions.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE imports ADD COLUMN unresolved_library_count BIGINT NOT NULL DEFAULT 0
    CHECK (unresolved_library_count >= 0 AND unresolved_library_count <= read_count);
CREATE TABLE media_server_exclusions (
    media_server_id TEXT NOT NULL REFERENCES media_servers(id) ON DELETE CASCADE,
    kind            TEXT NOT NULL CHECK (kind IN ('media_user', 'library')),
    external_id     TEXT NOT NULL CHECK (octet_length(external_id) BETWEEN 1 AND 128),
    PRIMARY KEY (media_server_id, kind, external_id)
);
CREATE INDEX media_server_exclusions_lookup_idx
    ON media_server_exclusions (media_server_id, external_id, kind);
CREATE INDEX watches_server_user_started_idx
    ON watches (media_server_id, media_user_id, started_at DESC, id DESC);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX watches_server_user_started_idx;
DROP TABLE media_server_exclusions;
ALTER TABLE imports DROP COLUMN unresolved_library_count;
-- +goose StatementEnd
