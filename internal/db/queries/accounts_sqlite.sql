-- SQLite account queries whose parameter syntax is engine-specific.

-- name: UsernamesByAccountIDs :many
SELECT id, username
FROM accounts
WHERE id IN (
    SELECT value FROM json_each(CAST(sqlc.arg(account_ids_json) AS TEXT))
)
ORDER BY id;
