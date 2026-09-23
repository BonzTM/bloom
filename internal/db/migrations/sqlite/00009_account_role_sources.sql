-- SQLite migration 00009: preserve role-assignment provenance.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE account_roles RENAME TO account_roles_legacy;
CREATE TABLE account_roles (
    account_id TEXT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    role_id    TEXT NOT NULL REFERENCES roles(id) ON DELETE CASCADE,
    source     TEXT NOT NULL DEFAULT 'manual' CHECK (source IN ('manual', 'oidc')),
    PRIMARY KEY (account_id, role_id, source)
) STRICT;
INSERT INTO account_roles (account_id, role_id, source)
SELECT account_id, role_id, 'manual' FROM account_roles_legacy;
DROP TABLE account_roles_legacy;
CREATE INDEX account_roles_role_id_idx ON account_roles (role_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE account_roles RENAME TO account_roles_with_sources;
CREATE TABLE account_roles (
    account_id TEXT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    role_id    TEXT NOT NULL REFERENCES roles(id) ON DELETE CASCADE,
    PRIMARY KEY (account_id, role_id)
) STRICT;
INSERT INTO account_roles (account_id, role_id)
SELECT DISTINCT account_id, role_id FROM account_roles_with_sources;
DROP TABLE account_roles_with_sources;
CREATE INDEX account_roles_role_id_idx ON account_roles (role_id);
-- +goose StatementEnd
