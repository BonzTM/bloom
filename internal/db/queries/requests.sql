-- Metadata, request profile, request, and quota queries shared by both engines.

-- name: GetMetadataProvider :one
SELECT kind, credential_ciphertext, key_id, created_at, updated_at
FROM metadata_providers
WHERE kind = sqlc.arg(kind);

-- name: UpsertMetadataProvider :exec
INSERT INTO metadata_providers (kind, credential_ciphertext, key_id, created_at, updated_at)
VALUES (sqlc.arg(kind), sqlc.arg(credential_ciphertext), sqlc.arg(key_id), sqlc.arg(created_at), sqlc.arg(updated_at))
ON CONFLICT (kind) DO UPDATE SET
    credential_ciphertext = excluded.credential_ciphertext,
    key_id = excluded.key_id,
    updated_at = excluded.updated_at;

-- name: DeleteMetadataProvider :execrows
DELETE FROM metadata_providers WHERE kind = sqlc.arg(kind);

-- name: CreateRequestProfile :exec
INSERT INTO request_profiles (
    id, name, accepts_movies, accepts_series, download_manager_kind,
    download_manager_instance, quality_profile, root_folder, created_at, updated_at
) VALUES (
    sqlc.arg(id), sqlc.arg(name), sqlc.arg(accepts_movies), sqlc.arg(accepts_series),
    sqlc.arg(download_manager_kind), sqlc.arg(download_manager_instance),
    sqlc.arg(quality_profile), sqlc.arg(root_folder), sqlc.arg(created_at), sqlc.arg(updated_at)
);

-- name: UpdateRequestProfile :execrows
UPDATE request_profiles SET
    name = sqlc.arg(name), accepts_movies = sqlc.arg(accepts_movies),
    accepts_series = sqlc.arg(accepts_series), download_manager_kind = sqlc.arg(download_manager_kind),
    download_manager_instance = sqlc.arg(download_manager_instance), quality_profile = sqlc.arg(quality_profile),
    root_folder = sqlc.arg(root_folder), updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id);

-- name: DeleteRequestProfile :execrows
DELETE FROM request_profiles WHERE id = sqlc.arg(id);

-- name: DeleteRequestProfileTags :exec
DELETE FROM request_profile_tags WHERE profile_id = sqlc.arg(profile_id);

-- name: CreateRequestProfileTag :exec
INSERT INTO request_profile_tags (profile_id, position, tag)
VALUES (sqlc.arg(profile_id), sqlc.arg(position), sqlc.arg(tag));

-- name: GetRequestProfile :one
SELECT id, name, accepts_movies, accepts_series, download_manager_kind,
       download_manager_instance, quality_profile, root_folder, created_at, updated_at
FROM request_profiles WHERE id = sqlc.arg(id);

-- name: ListRequestProfiles :many
SELECT id, name, accepts_movies, accepts_series, download_manager_kind,
       download_manager_instance, quality_profile, root_folder, created_at, updated_at
FROM request_profiles
WHERE name > sqlc.arg(after_name)
ORDER BY name, id
LIMIT sqlc.arg(page_size);

-- name: ListRequestProfileTags :many
SELECT tag FROM request_profile_tags WHERE profile_id = sqlc.arg(profile_id) ORDER BY position;

-- name: CreateRequest :exec
INSERT INTO requests (
    id, kind, provider, provider_id, title, release_year, poster_path,
    requester_account_id, profile_id, status, decision_reason,
    decided_by_account_id, decided_at, created_at, updated_at
) VALUES (
    sqlc.arg(id), sqlc.arg(kind), sqlc.arg(provider), sqlc.arg(provider_id),
    sqlc.arg(title), sqlc.arg(release_year), sqlc.arg(poster_path),
    sqlc.arg(requester_account_id), sqlc.arg(profile_id), sqlc.arg(status),
    sqlc.arg(decision_reason), sqlc.narg(decided_by_account_id),
    sqlc.narg(decided_at), sqlc.arg(created_at), sqlc.arg(updated_at)
);

-- name: CreateRequestSeason :exec
INSERT INTO request_seasons (request_id, season_number, status)
VALUES (sqlc.arg(request_id), sqlc.arg(season_number), sqlc.arg(status));

-- name: CountActiveRequestSeason :one
SELECT COUNT(*)
FROM request_seasons
JOIN requests ON requests.id = request_seasons.request_id
WHERE requests.kind = 'series'
  AND requests.provider = sqlc.arg(provider)
  AND requests.provider_id = sqlc.arg(provider_id)
  AND requests.profile_id = sqlc.arg(profile_id)
  AND requests.status IN ('pending', 'approved', 'processing')
  AND request_seasons.season_number = sqlc.arg(season_number);

-- name: GetRequest :one
SELECT id, kind, provider, provider_id, title, release_year, poster_path,
       requester_account_id, profile_id, status, decision_reason,
       decided_by_account_id, decided_at, created_at, updated_at
FROM requests WHERE id = sqlc.arg(id);

-- name: ListRequests :many
SELECT id, kind, provider, provider_id, title, release_year, poster_path,
       requester_account_id, profile_id, status, decision_reason,
       decided_by_account_id, decided_at, created_at, updated_at
FROM requests
WHERE (CAST(sqlc.arg(has_requester) AS INTEGER) = 0 OR requester_account_id = sqlc.arg(requester_id))
  AND (CAST(sqlc.arg(has_status) AS INTEGER) = 0 OR status = sqlc.arg(status_filter))
  AND (CAST(sqlc.arg(has_cursor) AS INTEGER) = 0
       OR created_at < sqlc.arg(after_created_at)
       OR (created_at = sqlc.arg(after_created_at) AND id < sqlc.arg(after_id)))
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg(page_size);

-- name: ListRequestSeasons :many
SELECT season_number, status FROM request_seasons WHERE request_id = sqlc.arg(request_id) ORDER BY season_number;

-- name: TransitionRequest :execrows
UPDATE requests SET
    status = sqlc.arg(to_status), decision_reason = sqlc.arg(decision_reason),
    decided_by_account_id = sqlc.arg(decided_by_account_id), decided_at = sqlc.arg(decided_at),
    updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id) AND status = sqlc.arg(from_status);

-- name: TransitionRequestSeasons :exec
UPDATE request_seasons SET status = sqlc.arg(to_status) WHERE request_id = sqlc.arg(request_id);

-- name: CountRequestedMoviesSince :one
SELECT COUNT(*) FROM requests
WHERE requester_account_id = sqlc.arg(account_id)
  AND kind = 'movie' AND status <> 'declined' AND created_at >= sqlc.arg(since_time);

-- name: CountRequestedSeasonsSince :one
SELECT COUNT(*) FROM request_seasons
JOIN requests ON requests.id = request_seasons.request_id
WHERE requests.requester_account_id = sqlc.arg(account_id)
  AND requests.status <> 'declined' AND requests.created_at >= sqlc.arg(since_time);

-- name: GetRoleRequestQuota :one
SELECT role_id, movie_limit, movie_period_seconds, season_limit, season_period_seconds
FROM role_request_quotas WHERE role_id = sqlc.arg(role_id);

-- name: ListRoleRequestQuotasForAccount :many
SELECT q.role_id, q.movie_limit, q.movie_period_seconds, q.season_limit, q.season_period_seconds
FROM role_request_quotas q
JOIN account_roles ar ON ar.role_id = q.role_id
WHERE ar.account_id = sqlc.arg(account_id)
ORDER BY q.role_id;

-- name: UpsertRoleRequestQuota :exec
INSERT INTO role_request_quotas (role_id, movie_limit, movie_period_seconds, season_limit, season_period_seconds)
VALUES (sqlc.arg(role_id), sqlc.arg(movie_limit), sqlc.arg(movie_period_seconds), sqlc.arg(season_limit), sqlc.arg(season_period_seconds))
ON CONFLICT (role_id) DO UPDATE SET
    movie_limit = excluded.movie_limit, movie_period_seconds = excluded.movie_period_seconds,
    season_limit = excluded.season_limit, season_period_seconds = excluded.season_period_seconds;

-- name: DeleteRoleRequestQuota :execrows
DELETE FROM role_request_quotas WHERE role_id = sqlc.arg(role_id);

-- name: GetAccountRequestQuota :one
SELECT account_id, movie_limit, movie_period_seconds, season_limit, season_period_seconds
FROM account_request_quotas WHERE account_id = sqlc.arg(account_id);

-- name: UpsertAccountRequestQuota :exec
INSERT INTO account_request_quotas (account_id, movie_limit, movie_period_seconds, season_limit, season_period_seconds)
VALUES (sqlc.arg(account_id), sqlc.arg(movie_limit), sqlc.arg(movie_period_seconds), sqlc.arg(season_limit), sqlc.arg(season_period_seconds))
ON CONFLICT (account_id) DO UPDATE SET
    movie_limit = excluded.movie_limit, movie_period_seconds = excluded.movie_period_seconds,
    season_limit = excluded.season_limit, season_period_seconds = excluded.season_period_seconds;

-- name: DeleteAccountRequestQuota :execrows
DELETE FROM account_request_quotas WHERE account_id = sqlc.arg(account_id);
