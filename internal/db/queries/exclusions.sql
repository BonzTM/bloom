-- Per-media-server exclusion settings shared by SQLite and PostgreSQL.

-- name: ListMediaServerExclusions :many
SELECT media_server_id, kind, external_id
FROM media_server_exclusions
WHERE media_server_id = sqlc.arg(media_server_id)
ORDER BY kind, external_id;

-- name: DeleteMediaServerExclusions :exec
DELETE FROM media_server_exclusions WHERE media_server_id = sqlc.arg(media_server_id);

-- name: InsertMediaServerExclusion :exec
INSERT INTO media_server_exclusions (media_server_id, kind, external_id)
VALUES (sqlc.arg(media_server_id), sqlc.arg(kind), sqlc.arg(external_id));
