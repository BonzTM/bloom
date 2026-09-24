-- name: LockAccountRequestQuota :exec
SELECT 1
FROM accounts
WHERE id = sqlc.arg(account_id);

-- name: LockRequestTitle :exec
SELECT length(sqlc.arg(lock_key));

-- name: ListRequestsForAvailability :many
SELECT * FROM requests
WHERE status = 'processing'
ORDER BY
    CASE WHEN last_availability_check_at IS NULL THEN 0 ELSE 1 END,
    last_availability_check_at ASC,
    created_at ASC,
    id ASC
LIMIT sqlc.arg(page_size);
