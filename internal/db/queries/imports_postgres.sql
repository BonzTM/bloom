-- PostgreSQL claim serializes claimers without blocking unrelated jobs.

-- name: SelectClaimableImport :one
SELECT * FROM imports
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
