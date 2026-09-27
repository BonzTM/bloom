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
          SELECT value FROM jsonb_array_elements_text(CAST(sqlc.arg(title_keys_json) AS jsonb))
      )
)
SELECT kind, provider, provider_id, status
FROM ranked
WHERE position = 1
ORDER BY kind, provider_id;
