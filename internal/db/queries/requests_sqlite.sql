-- name: LockAccountRequestQuota :exec
SELECT 1
FROM accounts
WHERE id = sqlc.arg(account_id);

-- name: LockRequestTitle :exec
SELECT length(sqlc.arg(lock_key));

-- name: ListRequestsForAvailability :many
SELECT id, kind, provider, provider_id, title, release_year, poster_path,
       requester_account_id, profile_id, status, decision_reason, decided_by_account_id,
       decided_at, created_at, updated_at, download_manager_item_id, failure_reason,
       download_manager_id, dispatch_quality_profile, dispatch_root_folder, dispatch_tags,
       dispatch_lease_expires_at, dispatch_lease_token, last_availability_check_at
FROM requests
WHERE status = 'processing'
ORDER BY
    CASE WHEN last_availability_check_at IS NULL THEN 0 ELSE 1 END,
    last_availability_check_at ASC,
    created_at ASC,
    id ASC
LIMIT sqlc.arg(page_size);

-- name: MetadataRequestStates :many
WITH title_keys AS (
    SELECT
        substr(value, 1, instr(value, ':') - 1) AS kind,
        substr(value, instr(value, ':') + 1) AS provider_id
    FROM json_each(CAST(sqlc.arg(title_keys_json) AS TEXT))
)
SELECT request.kind, request.provider, request.provider_id, request.status
FROM title_keys
JOIN requests AS request ON request.id = (
    SELECT latest.id
    FROM requests AS latest
    WHERE latest.requester_account_id = sqlc.arg(requester_account_id)
      AND latest.provider = 'tmdb'
      AND latest.kind = title_keys.kind
      AND latest.provider_id = title_keys.provider_id
    ORDER BY latest.created_at DESC, latest.id DESC
    LIMIT 1
)
ORDER BY request.kind, request.provider_id;
