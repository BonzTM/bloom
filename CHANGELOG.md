# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).
Every operator-visible change (configuration keys, migrations, ports, API
contracts) gets an entry here.

## [Unreleased]

### Changed

- Successful login responses now include the account's sorted role names and
  effective permissions, matching `GET /api/v1/auth/me`.

### Added

- Administration area in the web UI: an `Admin` entry for accounts holding an
  admin permission, and a roles page that lists every role with its kind and
  permissions, paged through the roles API.
- Main-branch and tagged-release image publishing with candidate-first smoke
  tests, source- and signer-bound attestation plus SBOM verification for
  immutable-image reuse, required CI and PostgreSQL gates, pre-promotion
  provenance, ancestry-guarded `main` and version-guarded stable aliases,
  deterministic bounded seven-day cleanup of unpromoted candidates,
  anonymous-pull validation, an SBOM, and one reusable automated homelab
  deployment pull request.
- Bootstrap of the service: Go HTTP server with `/livez`, `/readyz`, `/metrics`,
  and `GET /api/v1/version`; embedded React UI; SQLite (default) and PostgreSQL
  storage at parity; container image and CI.
- Repository hygiene: Dependabot, pull request template, code owners, security
  policy, and this changelog.
- Local authentication with `POST /api/v1/auth/login`, `POST /api/v1/auth/logout`,
  and `GET /api/v1/auth/me`; server-side `bloom_session` cookies; and the
  `create-admin` bootstrap command.
- Permission-based authorization with a stable public catalog at
  `GET /api/v1/auth/permissions`, effective roles and permissions in
  `GET /api/v1/auth/me`, guarded role listing at `GET /api/v1/roles`, denial
  audit events, and per-permission denial metrics.
- Migration `00006_roles` for roles, role permissions, and account-role
  assignments on SQLite and PostgreSQL. It seeds immutable built-in `owner`
  and `member` roles and leaves existing accounts role-less with an operator
  notice instead of guessing assignments. `create-admin` now assigns `owner`
  atomically and audits the assignment.
- The `grant-role --username <name> --role <role>` recovery command assigns a
  role to an existing role-less account. Repeating an existing assignment
  succeeds without duplication. Unknown accounts, unknown roles, and storage
  failures return an error; audit-sink failures are logged without masking the
  assignment result.
- Migration `00002_local_auth_sessions` for local Argon2id credentials,
  account-disable state, and database-backed sessions on SQLite and PostgreSQL.
- Migrations `00003_canonical_usernames`, Go migration `00004`, and
  `00005_require_canonical_usernames` stage the nullable schema expansion,
  PRECIS UsernameCaseMapped backfill, and exact `NOT NULL` uniqueness contract
  on SQLite and PostgreSQL. Key collisions leave version and account data
  unchanged. Rolling back all three restores the original display usernames.
- Authentication settings `BLOOM_SESSION_COOKIE_SECURE`,
  `BLOOM_SESSION_LIFETIME`, `BLOOM_SESSION_IDLE_TIMEOUT`,
  `BLOOM_LOGIN_RATE_REFILL_INTERVAL`, `BLOOM_LOGIN_RATE_BURST`, and
  `BLOOM_LOGIN_RATE_MAX_KEYS`, plus `BLOOM_LOGIN_MAX_CONCURRENT` for bounded
  Argon2id admission.
- `BLOOM_BOOTSTRAP_PASSWORD` as non-interactive secret input for
  `create-admin`, and `BLOOM_TRUSTED_PROXY_CIDRS` as an off-by-default
  forwarded-client-address allowlist.
- A new-password policy for `create-admin`: 15 through 1024 Unicode characters,
  subject to the existing 4096-byte input bound and an embedded offline
  common-password denylist, with no character-composition rules.

### Fixed

- SQLite database startup now supplies the foreign-key pragma when the
  configured DSN omits it. Explicitly disabled foreign keys still fail
  startup, and shipped container DSNs now declare the enabled pragma.
- Image publishing skips the artifact attestation steps, with a notice, when
  attestations are unavailable for the repository's plan, and keeps every other
  proof step; a public repository gets signed provenance automatically.
- Authentication hardening now isolates session middleware from SPA assets,
  bounds password and account-store work, parses complete trusted-proxy chains,
  prevents stale session resurrection, and keeps expired-session cleanup ahead
  of creation throughput with failure telemetry.
- Login rate-limit admission now rejects from existing exhausted buckets before
  allocating new keys, uses bounded expiry bookkeeping, and reports the earliest
  capacity availability. Session expiry and cleanup use the injected clock, and
  cleanup failures log their wrapped cause once per failure streak.
- CI now builds and scans with the same Go toolchain as local and container
  builds (`toolchain go1.27.1` in go.mod).
