-- Authorization queries are shared by SQLite and PostgreSQL. Effective
-- permissions are computed from current database state for every request.

-- name: ListAccountPermissions :many
SELECT DISTINCT rp.permission
FROM account_roles ar
JOIN role_permissions rp ON rp.role_id = ar.role_id
WHERE ar.account_id = sqlc.arg(account_id)
ORDER BY rp.permission;

-- name: GetAuthorizationSnapshot :many
SELECT r.name AS role_name, rp.permission
FROM account_roles ar
JOIN roles r ON r.id = ar.role_id
LEFT JOIN role_permissions rp ON rp.role_id = r.id
WHERE ar.account_id = sqlc.arg(account_id)
ORDER BY r.name, rp.permission;

-- name: ListRolesWithPermissions :many
WITH role_page AS (
    SELECT id, name, description, built_in, created_at
    FROM roles
    WHERE name > sqlc.arg(after_name)
    ORDER BY name
    LIMIT sqlc.arg(page_size)
)
SELECT role_page.id, role_page.name, role_page.description, role_page.built_in,
       role_page.created_at, rp.permission
FROM role_page
LEFT JOIN role_permissions rp ON rp.role_id = role_page.id
ORDER BY role_page.name, rp.permission;

-- name: GetRoleIDByName :one
SELECT id
FROM roles
WHERE name = sqlc.arg(role_name);

-- name: AssignRoleIDToAccount :execrows
INSERT INTO account_roles (account_id, role_id)
VALUES (sqlc.arg(account_id), sqlc.arg(role_id))
ON CONFLICT DO NOTHING;
