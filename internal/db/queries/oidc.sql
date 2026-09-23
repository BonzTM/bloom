-- OIDC identity queries are shared by SQLite and PostgreSQL.

-- name: GetAccountIdentity :one
SELECT ai.account_id, ai.provider, ai.issuer, ai.subject, ai.username_claim,
       ai.mapped_roles, ai.created_at, ai.last_login_at,
       a.username, a.password_hash, a.disabled, a.created_at AS account_created_at
FROM account_identities ai
JOIN accounts a ON a.id = ai.account_id
WHERE ai.issuer = sqlc.arg(issuer) AND ai.subject = sqlc.arg(subject);

-- name: CreateAccountIdentity :exec
INSERT INTO account_identities (
    account_id, provider, issuer, subject, username_claim, mapped_roles, created_at, last_login_at
) VALUES (
    sqlc.arg(account_id), sqlc.arg(provider), sqlc.arg(issuer), sqlc.arg(subject),
    sqlc.arg(username_claim), sqlc.arg(mapped_roles), sqlc.arg(created_at), sqlc.arg(last_login_at)
);

-- name: UpdateAccountIdentityLogin :execrows
UPDATE account_identities
SET username_claim = sqlc.arg(username_claim),
    mapped_roles = sqlc.arg(mapped_roles),
    last_login_at = sqlc.arg(last_login_at)
WHERE issuer = sqlc.arg(issuer) AND subject = sqlc.arg(subject);

-- name: RemoveRoleIDFromAccount :execrows
DELETE FROM account_roles
WHERE account_id = sqlc.arg(account_id) AND role_id = sqlc.arg(role_id) AND source = 'oidc';

-- name: AssignOIDCRoleIDToAccount :execrows
INSERT INTO account_roles (account_id, role_id, source)
VALUES (sqlc.arg(account_id), sqlc.arg(role_id), 'oidc')
ON CONFLICT DO NOTHING;
