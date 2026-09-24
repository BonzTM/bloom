-- name: GetAccountMediaUser :one
SELECT amu.account_id, amu.media_server_id, ms.name AS media_server_name,
       amu.media_user_id, amu.username, amu.source, amu.created_at, amu.updated_at
FROM account_media_users AS amu
JOIN media_servers AS ms ON ms.id = amu.media_server_id
WHERE amu.account_id = sqlc.arg(account_id)
  AND amu.media_server_id = sqlc.arg(media_server_id);

-- name: ListAccountMediaUsers :many
SELECT amu.account_id, amu.media_server_id, ms.name AS media_server_name,
       amu.media_user_id, amu.username, amu.source, amu.created_at, amu.updated_at
FROM account_media_users AS amu
JOIN media_servers AS ms ON ms.id = amu.media_server_id
WHERE amu.account_id = sqlc.arg(account_id)
ORDER BY ms.name_key, amu.media_server_id
LIMIT sqlc.arg(page_size);

-- name: SetAccountMediaUser :exec
INSERT INTO account_media_users (
    account_id, media_server_id, media_user_id, username, source, created_at, updated_at
) VALUES (
    sqlc.arg(account_id), sqlc.arg(media_server_id), sqlc.arg(media_user_id),
    sqlc.arg(username), sqlc.arg(source), sqlc.arg(created_at), sqlc.arg(updated_at)
)
ON CONFLICT (account_id, media_server_id) DO UPDATE SET
    media_user_id = excluded.media_user_id,
    username = excluded.username,
    source = excluded.source,
    updated_at = excluded.updated_at;

-- name: CreateAccountMediaUserIfAbsent :execrows
INSERT INTO account_media_users (
    account_id, media_server_id, media_user_id, username, source, created_at, updated_at
) VALUES (
    sqlc.arg(account_id), sqlc.arg(media_server_id), sqlc.arg(media_user_id),
    sqlc.arg(username), sqlc.arg(source), sqlc.arg(created_at), sqlc.arg(updated_at)
)
ON CONFLICT DO NOTHING;

-- name: DeleteAccountMediaUser :execrows
DELETE FROM account_media_users
WHERE account_id = sqlc.arg(account_id)
  AND media_server_id = sqlc.arg(media_server_id);
