-- SQLite claim rechecks eligibility in the guarded update inside one transaction.

-- name: SelectClaimableImport :one
SELECT * FROM imports
WHERE state = 'pending' OR (state = 'running' AND lease_expires_at <= sqlc.arg(now))
ORDER BY created_at, id
LIMIT 1;

-- name: ClaimImport :execrows
UPDATE imports
SET state = 'running', lease_token = sqlc.arg(token), lease_expires_at = sqlc.arg(expires_at),
    started_at = COALESCE(started_at, sqlc.arg(now)), updated_at = sqlc.arg(now)
WHERE id = sqlc.arg(id)
  AND (state = 'pending' OR (state = 'running' AND lease_expires_at <= sqlc.arg(now)));

-- name: FenceImportBatch :execrows
UPDATE imports SET lease_token = lease_token
WHERE id = sqlc.arg(id) AND state = 'running' AND lease_token = sqlc.arg(token);

-- name: LockWatchDedup :one
SELECT CAST(1 AS INTEGER);
