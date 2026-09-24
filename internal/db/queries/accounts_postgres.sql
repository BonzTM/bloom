-- PostgreSQL account queries whose parameter syntax is engine-specific.

-- name: UsernamesByAccountIDs :many
SELECT id, username
FROM accounts
WHERE id IN (
    SELECT value FROM jsonb_array_elements_text(CAST(sqlc.arg(account_ids_json) AS jsonb))
)
ORDER BY id;
