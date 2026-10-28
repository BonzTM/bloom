-- PostgreSQL migration 00031: let metadata_providers hold an OMDb ratings key.
-- Older binaries read only the 'tmdb' row, so the wider check is safe to roll out.

-- +goose Up
ALTER TABLE metadata_providers DROP CONSTRAINT metadata_providers_kind_check;
ALTER TABLE metadata_providers ADD CONSTRAINT metadata_providers_kind_check
    CHECK (kind IN ('tmdb', 'omdb'));

-- +goose Down
DELETE FROM metadata_providers WHERE kind = 'omdb';
ALTER TABLE metadata_providers DROP CONSTRAINT metadata_providers_kind_check;
ALTER TABLE metadata_providers ADD CONSTRAINT metadata_providers_kind_check
    CHECK (kind IN ('tmdb'));
