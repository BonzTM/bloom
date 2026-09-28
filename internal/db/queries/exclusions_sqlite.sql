-- SQLite exclusion replacement transaction parity.

-- name: LockMediaServerForExclusionReplace :one
SELECT id FROM media_servers
WHERE id = sqlc.arg(id);
