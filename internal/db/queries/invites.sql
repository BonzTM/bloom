-- name: CreateInvite :exec
INSERT INTO invites (
    id, media_server_id, created_by_account_id, code_hash, label,
    expires_at, max_uses, use_count, revoked_at, created_at, updated_at
) VALUES (
    sqlc.arg(id), sqlc.arg(media_server_id), sqlc.arg(created_by_account_id),
    sqlc.arg(code_hash), sqlc.arg(label), sqlc.narg(expires_at), sqlc.narg(max_uses),
    0, NULL, sqlc.arg(created_at), sqlc.arg(updated_at)
);

-- name: CreateInviteLibrary :exec
INSERT INTO invite_libraries (invite_id, library_id)
VALUES (sqlc.arg(invite_id), sqlc.arg(library_id));

-- name: GetInvite :one
SELECT id, media_server_id, created_by_account_id, label, expires_at,
       max_uses, use_count, revoked_at, created_at, updated_at
FROM invites
WHERE id = sqlc.arg(id);

-- name: GetInviteByCodeHash :one
SELECT id, media_server_id, created_by_account_id, code_hash, label, expires_at,
       max_uses, use_count, revoked_at, created_at, updated_at,
       EXISTS (SELECT 1 FROM invite_provisioning_failures AS failure
               WHERE failure.invite_id = invites.id) AS blocked
FROM invites
WHERE code_hash = sqlc.arg(code_hash);

-- name: ListInvites :many
SELECT id, media_server_id, created_by_account_id, label, expires_at,
       max_uses, use_count, revoked_at, created_at, updated_at
FROM invites
WHERE CAST(sqlc.arg(has_cursor) AS INTEGER) = 0
   OR created_at < sqlc.arg(after_created_at)
   OR (created_at = sqlc.arg(after_created_at) AND id < sqlc.arg(after_id))
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg(page_size);

-- name: ListInviteLibraries :many
SELECT library_id
FROM invite_libraries
WHERE invite_id = sqlc.arg(invite_id)
ORDER BY library_id;

-- name: RevokeInvite :one
UPDATE invites
SET revoked_at = COALESCE(revoked_at, sqlc.arg(revoked_at)),
    updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id)
RETURNING id, media_server_id, created_by_account_id, label, expires_at,
          max_uses, use_count, revoked_at, created_at, updated_at;

-- name: InsertInviteRedemption :exec
INSERT INTO invite_redemptions (
    id, invite_id, media_server_id, media_user_id, username, redeemed_at
) VALUES (
    sqlc.arg(id), sqlc.arg(invite_id), sqlc.arg(media_server_id),
    sqlc.arg(media_user_id), sqlc.arg(username), sqlc.arg(redeemed_at)
);

-- name: IncrementInviteUse :execrows
UPDATE invites
SET use_count = use_count + 1, updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id);

-- name: InsertInviteProvisioningFailureIfAbsent :exec
INSERT INTO invite_provisioning_failures (
    id, invite_id, media_server_id, media_user_id, media_user_owned, account_id, username, reason,
    attempts, next_attempt_at, lease_token, lease_expires_at, last_error, terminal,
    created_at, updated_at
) VALUES (
    sqlc.arg(id), sqlc.arg(invite_id), sqlc.arg(media_server_id), sqlc.narg(media_user_id),
    sqlc.arg(media_user_owned), sqlc.narg(account_id), sqlc.arg(username), sqlc.arg(reason),
    0, sqlc.arg(next_attempt_at), '', NULL, sqlc.arg(last_error), sqlc.arg(terminal),
    sqlc.arg(created_at), sqlc.arg(updated_at)
)
ON CONFLICT (id) DO NOTHING;

-- name: ClaimInviteProvisioningFailure :one
UPDATE invite_provisioning_failures SET
    lease_token = sqlc.arg(lease_token), lease_expires_at = sqlc.arg(lease_expires_at),
    attempts = attempts + 1, terminal = attempts + 1 >= 8, updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id) AND terminal = FALSE AND attempts < 8
  AND next_attempt_at <= sqlc.arg(due_at)
  AND (lease_token = '' OR lease_expires_at <= sqlc.arg(due_at))
RETURNING id, invite_id, media_server_id, media_user_id, media_user_owned, account_id, username, reason,
          attempts, next_attempt_at, lease_token, lease_expires_at, last_error, terminal,
          created_at, updated_at;

-- name: GetClaimedInviteProvisioningFailure :one
SELECT id, invite_id, media_server_id, media_user_id, media_user_owned, account_id, username, reason,
       attempts, next_attempt_at, lease_token, lease_expires_at, last_error, terminal,
       created_at, updated_at
FROM invite_provisioning_failures
WHERE id = sqlc.arg(id) AND lease_token = sqlc.arg(lease_token);

-- name: CompleteInviteProvisioningCleanup :execrows
DELETE FROM invite_provisioning_failures
WHERE id = sqlc.arg(id) AND lease_token = sqlc.arg(lease_token);

-- name: RescheduleInviteProvisioningFailure :execrows
UPDATE invite_provisioning_failures SET
    next_attempt_at = sqlc.arg(next_attempt_at), lease_token = '', lease_expires_at = NULL,
    last_error = sqlc.arg(last_error), terminal = sqlc.arg(terminal), updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id) AND lease_token = sqlc.arg(lease_token);

-- name: ListInviteProvisioningFailures :many
SELECT failure.id, failure.invite_id, failure.media_server_id, server.name AS media_server_name,
       failure.media_user_owned, failure.username, failure.reason, failure.attempts, failure.next_attempt_at,
       failure.last_error, failure.terminal, failure.created_at, failure.updated_at
FROM invite_provisioning_failures AS failure
JOIN media_servers AS server ON server.id = failure.media_server_id
WHERE CAST(sqlc.arg(has_cursor) AS INTEGER) = 0
   OR failure.created_at < sqlc.arg(after_created_at)
   OR (failure.created_at = sqlc.arg(after_created_at) AND failure.id < sqlc.arg(after_id))
ORDER BY failure.created_at DESC, failure.id DESC
LIMIT sqlc.arg(page_size);

-- name: DismissInviteProvisioningFailure :execrows
DELETE FROM invite_provisioning_failures
WHERE id = sqlc.arg(id)
  AND (lease_token = '' OR (lease_expires_at IS NOT NULL AND lease_expires_at <= sqlc.arg(dismissed_at)));

-- name: InviteProvisioningFailureExists :one
SELECT EXISTS (SELECT 1 FROM invite_provisioning_failures WHERE id = sqlc.arg(id));

-- name: InviteProvisioningFailureDepth :one
SELECT COUNT(*) FROM invite_provisioning_failures;
