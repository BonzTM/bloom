-- name: GetImport :one
SELECT id, media_server_id, source, state, cursor, read_count, imported_count,
       skipped_count, duplicate_count, last_error, lease_token, lease_expires_at,
       requested_by, created_at, started_at, finished_at, updated_at
FROM imports WHERE id = sqlc.arg(id);

-- name: ListImports :many
SELECT id, media_server_id, source, state, cursor, read_count, imported_count,
       skipped_count, duplicate_count, last_error, lease_token, lease_expires_at,
       requested_by, created_at, started_at, finished_at, updated_at
FROM imports
WHERE (CAST(sqlc.arg(media_server_id) AS TEXT) = ''
       OR media_server_id = CAST(sqlc.arg(media_server_id) AS TEXT))
  AND (created_at < sqlc.arg(before_created_at)
       OR (created_at = sqlc.arg(before_created_at) AND id < sqlc.arg(before_id)))
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg(page_size);

-- PostgreSQL claim serializes claimers without blocking unrelated jobs.

-- name: SelectClaimableImport :one
SELECT id, media_server_id, source, state, cursor, read_count, imported_count,
       skipped_count, duplicate_count, last_error, lease_token, lease_expires_at,
       requested_by, created_at, started_at, finished_at, updated_at
FROM imports
WHERE state = 'pending' OR (state = 'running' AND lease_expires_at <= sqlc.arg(now))
ORDER BY created_at, id
LIMIT 1
FOR UPDATE SKIP LOCKED;

-- name: ClaimImport :execrows
UPDATE imports
SET state = 'running', lease_token = sqlc.arg(token), lease_expires_at = sqlc.arg(expires_at),
    started_at = COALESCE(started_at, sqlc.arg(now)), updated_at = sqlc.arg(now)
WHERE id = sqlc.arg(id)
  AND (state = 'pending' OR (state = 'running' AND lease_expires_at <= sqlc.arg(now)));

-- name: FenceImportBatch :one
SELECT id FROM imports
WHERE id = sqlc.arg(id) AND state = 'running' AND lease_token = sqlc.arg(token)
FOR UPDATE;

-- name: LockWatchDedup :exec
SELECT pg_advisory_xact_lock(hashtextextended(sqlc.arg(lock_key), 0));
