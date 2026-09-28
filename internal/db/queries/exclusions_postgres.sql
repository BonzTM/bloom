-- PostgreSQL exclusion replacement locking.

-- name: LockMediaServerForExclusionReplace :one
SELECT id FROM media_servers
WHERE id = sqlc.arg(id)
FOR UPDATE;
