-- name: LockAccountRequestQuota :exec
SELECT pg_advisory_xact_lock(hashtextextended(sqlc.arg(account_id), 0));
