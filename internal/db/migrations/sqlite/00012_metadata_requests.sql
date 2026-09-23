-- SQLite migration 00012: metadata provider settings, request profiles, requests, and quotas.

-- +goose Up
CREATE TABLE metadata_providers (
    kind TEXT PRIMARY KEY CHECK (kind IN ('tmdb')),
    credential_ciphertext BLOB NOT NULL CHECK (length(credential_ciphertext) > 0),
    key_id TEXT NOT NULL CHECK (length(CAST(key_id AS BLOB)) BETWEEN 1 AND 128),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE request_profiles (
    id TEXT PRIMARY KEY CHECK (length(id) = 36),
    name TEXT NOT NULL UNIQUE CHECK (length(CAST(name AS BLOB)) BETWEEN 1 AND 100),
    accepts_movies INTEGER NOT NULL CHECK (accepts_movies IN (0, 1)),
    accepts_series INTEGER NOT NULL CHECK (accepts_series IN (0, 1)),
    download_manager_kind TEXT NOT NULL CHECK (length(CAST(download_manager_kind AS BLOB)) BETWEEN 1 AND 500),
    download_manager_instance TEXT NOT NULL CHECK (length(CAST(download_manager_instance AS BLOB)) BETWEEN 1 AND 500),
    quality_profile TEXT NOT NULL CHECK (length(CAST(quality_profile AS BLOB)) BETWEEN 1 AND 500),
    root_folder TEXT NOT NULL CHECK (length(CAST(root_folder AS BLOB)) BETWEEN 1 AND 500),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    CHECK (accepts_movies = 1 OR accepts_series = 1)
);

CREATE TABLE request_profile_tags (
    profile_id TEXT NOT NULL REFERENCES request_profiles(id) ON DELETE CASCADE,
    position INTEGER NOT NULL CHECK (position BETWEEN 0 AND 31),
    tag TEXT NOT NULL CHECK (length(CAST(tag AS BLOB)) BETWEEN 1 AND 100),
    PRIMARY KEY (profile_id, position),
    UNIQUE (profile_id, tag)
);

CREATE TABLE requests (
    id TEXT PRIMARY KEY CHECK (length(id) = 36),
    kind TEXT NOT NULL CHECK (kind IN ('movie', 'series')),
    provider TEXT NOT NULL CHECK (provider IN ('tmdb')),
    provider_id TEXT NOT NULL CHECK (length(CAST(provider_id AS BLOB)) BETWEEN 1 AND 20),
    title TEXT NOT NULL CHECK (length(CAST(title AS BLOB)) BETWEEN 1 AND 500),
    release_year INTEGER NOT NULL CHECK (release_year BETWEEN 0 AND 9999),
    poster_path TEXT NOT NULL CHECK (length(CAST(poster_path AS BLOB)) <= 500),
    requester_account_id TEXT NOT NULL REFERENCES accounts(id) ON DELETE RESTRICT,
    profile_id TEXT NOT NULL REFERENCES request_profiles(id) ON DELETE RESTRICT,
    status TEXT NOT NULL CHECK (status IN ('pending', 'approved', 'declined', 'processing', 'available', 'failed')),
    decision_reason TEXT NOT NULL DEFAULT '' CHECK (length(CAST(decision_reason AS BLOB)) <= 1000),
    decided_by_account_id TEXT REFERENCES accounts(id) ON DELETE RESTRICT,
    decided_at TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE UNIQUE INDEX requests_one_active_title_profile_idx
    ON requests(provider, provider_id, kind, profile_id)
    WHERE status NOT IN ('declined', 'failed');
CREATE INDEX requests_newest_idx ON requests(created_at DESC, id DESC);
CREATE INDEX requests_requester_newest_idx ON requests(requester_account_id, created_at DESC, id DESC);

CREATE TABLE request_seasons (
    request_id TEXT NOT NULL REFERENCES requests(id) ON DELETE CASCADE,
    season_number INTEGER NOT NULL CHECK (season_number BETWEEN 1 AND 999),
    status TEXT NOT NULL CHECK (status IN ('pending', 'approved', 'declined', 'processing', 'available', 'failed')),
    PRIMARY KEY (request_id, season_number)
);

CREATE TABLE role_request_quotas (
    role_id TEXT PRIMARY KEY REFERENCES roles(id) ON DELETE CASCADE,
    movie_limit INTEGER NOT NULL CHECK (movie_limit >= 0),
    movie_period_seconds INTEGER NOT NULL CHECK (movie_period_seconds >= 0),
    season_limit INTEGER NOT NULL CHECK (season_limit >= 0),
    season_period_seconds INTEGER NOT NULL CHECK (season_period_seconds >= 0),
    CHECK ((movie_limit = 0) = (movie_period_seconds = 0)),
    CHECK ((season_limit = 0) = (season_period_seconds = 0))
);

CREATE TABLE account_request_quotas (
    account_id TEXT PRIMARY KEY REFERENCES accounts(id) ON DELETE CASCADE,
    movie_limit INTEGER NOT NULL CHECK (movie_limit >= 0),
    movie_period_seconds INTEGER NOT NULL CHECK (movie_period_seconds >= 0),
    season_limit INTEGER NOT NULL CHECK (season_limit >= 0),
    season_period_seconds INTEGER NOT NULL CHECK (season_period_seconds >= 0),
    CHECK ((movie_limit = 0) = (movie_period_seconds = 0)),
    CHECK ((season_limit = 0) = (season_period_seconds = 0))
);

-- +goose Down
DROP TABLE account_request_quotas;
DROP TABLE role_request_quotas;
DROP TABLE request_seasons;
DROP TABLE requests;
DROP TABLE request_profile_tags;
DROP TABLE request_profiles;
DROP TABLE metadata_providers;
