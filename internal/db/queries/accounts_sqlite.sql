-- SQLite account queries whose parameter syntax is engine-specific.

-- name: ListAdminAccounts :many
SELECT a.id, a.username, a.username_key, a.created_at,
       CASE WHEN a.password_hash IS NULL THEN 0 ELSE 1 END AS has_local,
       EXISTS (SELECT 1 FROM account_identities AS ai WHERE ai.account_id = a.id AND ai.provider = 'oidc') AS has_oidc
FROM accounts AS a
WHERE (CAST(sqlc.arg(search_key) AS TEXT) = ''
       OR instr(a.username_key, CAST(sqlc.arg(search_key) AS TEXT)) > 0)
  AND (a.username_key > sqlc.arg(after_username_key)
       OR (a.username_key = sqlc.arg(after_username_key) AND a.id > sqlc.arg(after_id)))
ORDER BY a.username_key, a.id
LIMIT sqlc.arg(page_size);

-- name: GetAdminAccount :one
SELECT a.id, a.username, a.username_key, a.created_at,
       CASE WHEN a.password_hash IS NULL THEN 0 ELSE 1 END AS has_local,
       EXISTS (SELECT 1 FROM account_identities AS ai WHERE ai.account_id = a.id AND ai.provider = 'oidc') AS has_oidc
FROM accounts AS a
WHERE a.id = sqlc.arg(id);

-- name: ListAdminAccountRoles :many
SELECT ar.account_id, r.name, ar.source
FROM account_roles AS ar
JOIN roles AS r ON r.id = ar.role_id
WHERE ar.account_id IN (
    SELECT value FROM json_each(CAST(sqlc.arg(account_ids_json) AS TEXT))
)
ORDER BY ar.account_id, r.name, ar.source;

-- name: ListAdminAccountMediaUsers :many
SELECT amu.account_id, amu.media_server_id, ms.name AS media_server_name,
       amu.media_user_id, amu.username, amu.source, amu.created_at, amu.updated_at, amu.suppressed_at
FROM account_media_users AS amu
JOIN media_servers AS ms ON ms.id = amu.media_server_id
WHERE amu.account_id IN (
    SELECT value FROM json_each(CAST(sqlc.arg(account_ids_json) AS TEXT))
)
ORDER BY amu.account_id, ms.name_key, amu.media_server_id;

-- name: UsernamesByAccountIDs :many
SELECT id, username
FROM accounts
WHERE id IN (
    SELECT value FROM json_each(CAST(sqlc.arg(account_ids_json) AS TEXT))
)
ORDER BY id;
