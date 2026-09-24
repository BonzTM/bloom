-- Download-manager registration queries shared by both engines.

-- name: CreateDownloadManager :exec
INSERT INTO download_managers (
    id, kind, name, name_key, base_url, allow_insecure,
    credential_ciphertext, key_id, created_at, updated_at
) VALUES (
    sqlc.arg(id), sqlc.arg(kind), sqlc.arg(name), sqlc.arg(name_key),
    sqlc.arg(base_url), sqlc.arg(allow_insecure), sqlc.arg(credential_ciphertext),
    sqlc.arg(key_id), sqlc.arg(created_at), sqlc.arg(updated_at)
);

-- name: GetDownloadManager :one
SELECT id, kind, name, base_url, allow_insecure, credential_ciphertext,
       key_id, created_at, updated_at
FROM download_managers WHERE id = sqlc.arg(id);

-- name: GetDownloadManagerByName :one
SELECT id, kind, name, base_url, allow_insecure, credential_ciphertext,
       key_id, created_at, updated_at
FROM download_managers WHERE name_key = sqlc.arg(name_key);

-- name: ListDownloadManagers :many
SELECT id, kind, name, base_url, allow_insecure, created_at, updated_at
FROM download_managers
WHERE name_key > sqlc.arg(after_name_key)
ORDER BY name_key, id
LIMIT sqlc.arg(page_size);

-- name: CountRequestProfilesForDownloadManager :one
SELECT COUNT(*) FROM request_profiles
WHERE download_manager_kind = sqlc.arg(kind)
  AND download_manager_instance = sqlc.arg(name);

-- name: DeleteDownloadManager :execrows
DELETE FROM download_managers WHERE id = sqlc.arg(id);
