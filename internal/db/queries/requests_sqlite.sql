-- name: LockAccountRequestQuota :exec
SELECT 1
FROM accounts
WHERE id = sqlc.arg(account_id);
