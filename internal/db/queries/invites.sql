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
SELECT id, media_server_id, created_by_account_id, label, expires_at,
       max_uses, use_count, revoked_at, created_at, updated_at
FROM invites
WHERE code_hash = sqlc.arg(code_hash)
  AND NOT EXISTS (
      SELECT 1 FROM invite_provisioning_failures
      WHERE invite_provisioning_failures.invite_id = invites.id
  );

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

-- name: InsertInviteProvisioningFailure :exec
INSERT INTO invite_provisioning_failures (
    id, invite_id, media_server_id, media_user_id, username, reason, created_at, updated_at
) VALUES (
    sqlc.arg(id), sqlc.arg(invite_id), sqlc.arg(media_server_id), sqlc.narg(media_user_id),
    sqlc.arg(username), sqlc.arg(reason), sqlc.arg(created_at), sqlc.arg(updated_at)
);
