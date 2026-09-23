-- name: LockInviteByCodeHash :one
SELECT id, media_server_id, created_by_account_id, label, expires_at,
       max_uses, use_count, revoked_at, created_at, updated_at
FROM invites
WHERE code_hash = sqlc.arg(code_hash)
FOR UPDATE;

-- name: InviteHasProvisioningFailure :one
SELECT EXISTS (
    SELECT 1 FROM invite_provisioning_failures
    WHERE invite_id = sqlc.arg(invite_id)
);
