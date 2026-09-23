-- Media-server queries are portable across SQLite and PostgreSQL.

-- name: CreateMediaServer :exec
INSERT INTO media_servers (id, kind, name, name_key, base_url, allow_insecure, credential_ciphertext, created_at, updated_at)
VALUES (sqlc.arg(id), sqlc.arg(kind), sqlc.arg(name), sqlc.arg(name_key), sqlc.arg(base_url), sqlc.arg(allow_insecure), sqlc.arg(credential_ciphertext), sqlc.arg(created_at), sqlc.arg(updated_at));

-- name: GetMediaServer :one
SELECT id, kind, name, base_url, allow_insecure, credential_ciphertext, created_at, updated_at
FROM media_servers
WHERE id = sqlc.arg(id);

-- name: ListMediaServers :many
SELECT id, kind, name, base_url, allow_insecure, created_at, updated_at
FROM media_servers
WHERE name_key > sqlc.arg(after_name_key)
ORDER BY name_key, id
LIMIT sqlc.arg(page_size);

-- name: DeleteMediaServer :execrows
DELETE FROM media_servers
WHERE id = sqlc.arg(id);
