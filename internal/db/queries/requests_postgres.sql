-- name: LockAccountRequestQuota :exec
SELECT pg_advisory_xact_lock(hashtextextended(sqlc.arg(account_id), 0));

-- name: LockRequestTitle :exec
SELECT pg_advisory_xact_lock(hashtextextended(sqlc.arg(lock_key), 0));

-- name: ListRequestsForAvailability :many
SELECT * FROM requests
WHERE status = 'processing'
ORDER BY last_availability_check_at ASC NULLS FIRST, created_at ASC, id ASC
LIMIT sqlc.arg(page_size)
FOR UPDATE SKIP LOCKED;
