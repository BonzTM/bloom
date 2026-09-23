-- name: LockAccountRequestQuota :exec
SELECT pg_advisory_xact_lock(hashtextextended(sqlc.arg(account_id), 0));

-- name: LockRequestTitle :exec
SELECT pg_advisory_xact_lock(hashtextextended(sqlc.arg(lock_key), 0));
