-- PostgreSQL migration 00006: permission roles and account assignments.
-- Built-in identifiers are stable and seed inserts are idempotent.

-- +goose Up
-- +goose StatementBegin
CREATE TABLE roles (
    id          TEXT PRIMARY KEY,
    name        TEXT NOT NULL UNIQUE,
    description TEXT NOT NULL,
    built_in    BOOLEAN NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL
);

CREATE TABLE role_permissions (
    role_id    TEXT NOT NULL REFERENCES roles(id) ON DELETE CASCADE,
    permission TEXT NOT NULL,
    PRIMARY KEY (role_id, permission)
);

CREATE TABLE account_roles (
    account_id TEXT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    role_id    TEXT NOT NULL REFERENCES roles(id) ON DELETE CASCADE,
    PRIMARY KEY (account_id, role_id)
);
CREATE INDEX account_roles_role_id_idx ON account_roles (role_id);

INSERT INTO roles (id, name, description, built_in, created_at) VALUES
    ('00000000-0000-4000-8000-000000000001', 'owner', 'Full access to every Bloom permission.', TRUE, '1970-01-01T00:00:00Z'),
    ('00000000-0000-4000-8000-000000000002', 'member', 'Access to owned data and creating requests.', TRUE, '1970-01-01T00:00:00Z')
ON CONFLICT DO NOTHING;

INSERT INTO role_permissions (role_id, permission) VALUES
    ('00000000-0000-4000-8000-000000000001', 'users.read'),
    ('00000000-0000-4000-8000-000000000001', 'users.invite'),
    ('00000000-0000-4000-8000-000000000001', 'users.manage'),
    ('00000000-0000-4000-8000-000000000001', 'requests.read.own'),
    ('00000000-0000-4000-8000-000000000001', 'requests.create'),
    ('00000000-0000-4000-8000-000000000001', 'requests.approve'),
    ('00000000-0000-4000-8000-000000000001', 'stats.read.own'),
    ('00000000-0000-4000-8000-000000000001', 'stats.read.all'),
    ('00000000-0000-4000-8000-000000000001', 'admin.settings'),
    ('00000000-0000-4000-8000-000000000001', 'admin.roles'),
    ('00000000-0000-4000-8000-000000000002', 'requests.read.own'),
    ('00000000-0000-4000-8000-000000000002', 'requests.create'),
    ('00000000-0000-4000-8000-000000000002', 'stats.read.own')
ON CONFLICT DO NOTHING;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE account_roles;
DROP TABLE role_permissions;
DROP TABLE roles;
-- +goose StatementEnd
