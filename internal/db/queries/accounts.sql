-- accounts.sql is the sqlc source of truth for the account store. It is SHARED
-- by both engines (ADR 0004 item 3): sqlc.yaml compiles it once against the
-- SQLite schema into internal/db/sqlite and once against the PostgreSQL schema
-- into internal/db/postgres. Only portable SQL belongs here; named parameters
-- (sqlc.arg) are used because both engines accept them, whereas $1 and ? are
-- engine-specific. Regenerate with: go tool sqlc generate.

-- name: CreateAccount :exec
INSERT INTO accounts (id, username, created_at)
VALUES (sqlc.arg(id), sqlc.arg(username), sqlc.arg(created_at));

-- name: GetAccount :one
SELECT id, username, created_at
FROM accounts
WHERE id = sqlc.arg(id);

-- name: GetAccountByUsername :one
SELECT id, username, created_at
FROM accounts
WHERE username = sqlc.arg(username);
