-- name: LockInviteByCodeHash :one
SELECT id, media_server_id, created_by_account_id, code_hash, label, expires_at,
       max_uses, use_count, revoked_at, created_at, updated_at
FROM invites
WHERE code_hash = sqlc.arg(code_hash)
FOR UPDATE;

-- name: InviteHasProvisioningFailure :one
SELECT EXISTS (
    SELECT 1 FROM invite_provisioning_failures
    WHERE invite_id = sqlc.arg(invite_id)
);

-- name: LockInviteProvisioningFailureForClaim :one
SELECT id FROM invite_provisioning_failures
WHERE terminal = FALSE AND attempts < 8 AND next_attempt_at <= sqlc.arg(due_at)
  AND (lease_token = '' OR lease_expires_at <= sqlc.arg(due_at))
ORDER BY next_attempt_at, created_at, id LIMIT 1
FOR UPDATE SKIP LOCKED;

-- name: LockInviteByID :one
SELECT id, media_server_id, created_by_account_id, label, expires_at,
       max_uses, use_count, revoked_at, created_at, updated_at
FROM invites WHERE id = sqlc.arg(id)
FOR UPDATE;
