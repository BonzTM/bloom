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
WITH title_keys AS (
    SELECT
        split_part(value, ':', 1) AS kind,
        split_part(value, ':', 2) AS provider_id
    FROM jsonb_array_elements_text(CAST(sqlc.arg(title_keys_json) AS jsonb)) AS keys(value)
)
SELECT request.kind, request.provider, request.provider_id, request.status
FROM title_keys
CROSS JOIN LATERAL (
    SELECT latest.kind, latest.provider, latest.provider_id, latest.status
    FROM requests AS latest
    WHERE latest.requester_account_id = sqlc.arg(requester_account_id)
      AND latest.provider = 'tmdb'
      AND latest.kind = title_keys.kind
      AND latest.provider_id = title_keys.provider_id
    ORDER BY latest.created_at DESC, latest.id DESC
    LIMIT 1
) AS request
ORDER BY request.kind, request.provider_id;
