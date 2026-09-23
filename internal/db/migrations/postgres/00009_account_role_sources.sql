-- PostgreSQL migration 00009: preserve role-assignment provenance.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE account_roles ADD COLUMN source TEXT NOT NULL DEFAULT 'manual';
ALTER TABLE account_roles ADD CONSTRAINT account_roles_source_check CHECK (source IN ('manual', 'oidc'));
ALTER TABLE account_roles DROP CONSTRAINT account_roles_pkey;
ALTER TABLE account_roles ADD PRIMARY KEY (account_id, role_id, source);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DELETE FROM account_roles a
USING account_roles b
WHERE a.account_id = b.account_id AND a.role_id = b.role_id AND a.source = 'oidc' AND b.source = 'manual';
ALTER TABLE account_roles DROP CONSTRAINT account_roles_pkey;
ALTER TABLE account_roles DROP CONSTRAINT account_roles_source_check;
ALTER TABLE account_roles DROP COLUMN source;
ALTER TABLE account_roles ADD PRIMARY KEY (account_id, role_id);
-- +goose StatementEnd
