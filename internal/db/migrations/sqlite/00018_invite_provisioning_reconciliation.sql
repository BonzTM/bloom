-- SQLite migration 00018: durable invite provisioning reconciliation.

-- +goose Up
ALTER TABLE invite_provisioning_failures ADD COLUMN account_id TEXT REFERENCES accounts(id) ON DELETE SET NULL;
ALTER TABLE invite_provisioning_failures ADD COLUMN media_user_owned INTEGER NOT NULL DEFAULT 0 CHECK (media_user_owned IN (0, 1));
ALTER TABLE invite_provisioning_failures ADD COLUMN attempts INTEGER NOT NULL DEFAULT 0 CHECK (attempts BETWEEN 0 AND 8);
ALTER TABLE invite_provisioning_failures ADD COLUMN next_attempt_at TEXT NOT NULL DEFAULT '1970-01-01T00:00:00Z';
ALTER TABLE invite_provisioning_failures ADD COLUMN lease_token TEXT NOT NULL DEFAULT '' CHECK (length(lease_token) IN (0, 36));
ALTER TABLE invite_provisioning_failures ADD COLUMN lease_expires_at TEXT;
ALTER TABLE invite_provisioning_failures ADD COLUMN last_error TEXT NOT NULL DEFAULT 'media user ownership is unconfirmed; manual resolution required' CHECK (length(CAST(last_error AS BLOB)) <= 512);
ALTER TABLE invite_provisioning_failures ADD COLUMN terminal INTEGER NOT NULL DEFAULT 1 CHECK (terminal IN (0, 1));

CREATE INDEX invite_provisioning_failures_due_idx
    ON invite_provisioning_failures(terminal, next_attempt_at, created_at, id);

-- +goose Down
DROP INDEX invite_provisioning_failures_due_idx;
ALTER TABLE invite_provisioning_failures DROP COLUMN terminal;
ALTER TABLE invite_provisioning_failures DROP COLUMN last_error;
ALTER TABLE invite_provisioning_failures DROP COLUMN lease_expires_at;
ALTER TABLE invite_provisioning_failures DROP COLUMN lease_token;
ALTER TABLE invite_provisioning_failures DROP COLUMN next_attempt_at;
ALTER TABLE invite_provisioning_failures DROP COLUMN attempts;
ALTER TABLE invite_provisioning_failures DROP COLUMN media_user_owned;
ALTER TABLE invite_provisioning_failures DROP COLUMN account_id;
