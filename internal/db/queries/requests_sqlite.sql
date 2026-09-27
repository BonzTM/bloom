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

-- name: MetadataRequestStates :many
WITH ranked AS (
    SELECT kind, provider, provider_id, status,
           ROW_NUMBER() OVER (
               PARTITION BY kind, provider, provider_id
               ORDER BY created_at DESC, id DESC
           ) AS position
    FROM requests
    WHERE requester_account_id = sqlc.arg(requester_account_id)
      AND provider = 'tmdb'
      AND kind || ':' || provider_id IN (
          SELECT value FROM json_each(CAST(sqlc.arg(title_keys_json) AS TEXT))
      )
)
SELECT kind, provider, provider_id, status
FROM ranked
WHERE position = 1
ORDER BY kind, provider_id;
