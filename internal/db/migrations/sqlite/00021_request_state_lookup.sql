-- SQLite migration 00022: index caller-scoped metadata request-state lookups.

-- +goose Up
CREATE INDEX requests_metadata_state_idx
    ON requests(requester_account_id, provider, kind, provider_id, created_at DESC, id DESC);

-- +goose Down
DROP INDEX requests_metadata_state_idx;
