-- PostgreSQL migration 00018: durable invite provisioning reconciliation.

-- +goose Up
ALTER TABLE invite_provisioning_failures
    ADD COLUMN account_id TEXT REFERENCES accounts(id) ON DELETE SET NULL,
    ADD COLUMN media_user_owned BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN attempts INTEGER NOT NULL DEFAULT 0 CHECK (attempts BETWEEN 0 AND 8),
    ADD COLUMN next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT TIMESTAMPTZ '1970-01-01 00:00:00+00',
    ADD COLUMN lease_token TEXT NOT NULL DEFAULT '' CHECK (length(lease_token) IN (0, 36)),
    ADD COLUMN lease_expires_at TIMESTAMPTZ,
    ADD COLUMN last_error TEXT NOT NULL DEFAULT 'media user ownership is unconfirmed; manual resolution required' CHECK (octet_length(last_error) <= 512),
    ADD COLUMN terminal BOOLEAN NOT NULL DEFAULT TRUE;

CREATE INDEX invite_provisioning_failures_due_idx
    ON invite_provisioning_failures(terminal, next_attempt_at, created_at, id);

-- +goose Down
DROP INDEX invite_provisioning_failures_due_idx;
ALTER TABLE invite_provisioning_failures
    DROP COLUMN terminal,
    DROP COLUMN last_error,
    DROP COLUMN lease_expires_at,
    DROP COLUMN lease_token,
    DROP COLUMN next_attempt_at,
    DROP COLUMN attempts,
    DROP COLUMN media_user_owned,
    DROP COLUMN account_id;
