-- PostgreSQL migration 00008: external account identity links for OIDC sign-in.

-- +goose Up
-- +goose StatementBegin
CREATE TABLE account_identities (
    account_id    TEXT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    provider      TEXT NOT NULL,
    issuer        TEXT NOT NULL,
    subject       TEXT NOT NULL,
    username_claim TEXT NOT NULL,
    mapped_roles  TEXT NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL,
    last_login_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (issuer, subject)
);
CREATE INDEX account_identities_account_id_idx ON account_identities (account_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE account_identities;
-- +goose StatementEnd
