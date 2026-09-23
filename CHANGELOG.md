# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).
Every operator-visible change (configuration keys, migrations, ports, API
contracts) gets an entry here.

## [Unreleased]

### Removed

- The image workflow no longer opens deployment pull requests or needs a
  deployment token; it publishes and promotes images only.

### Changed

- `BLOOM_SECRET_KEY` is read from the environment only and the `-secret-key`
  flag is removed, so usage output and process arguments can never carry a
  secret. `BLOOM_OIDC_CLIENT_SECRET` follows the same rule.
- `create-admin` is now the recovery path for administrator access. Normal
  first-run setup starts Bloom with `BLOOM_BOOTSTRAP_PASSWORD`, signs in, and
  then removes the variable from the service environment.
- Successful login responses now include the account's sorted role names and
  effective permissions, matching `GET /api/v1/auth/me`.

### Added

- Runtime restart coverage on SQLite and PostgreSQL proves that database-backed
  browser sessions survive process replacement and logout revocations remain
  effective.
- Invites in the web UI: `/admin/invites` (guarded by `users.invite`) creates
  an invite for a registered media server with an expiry and a use limit,
  shows the link once, lists invites with their status, and revokes them; the
  public `/invite/<code>` page lets the invited person choose a username and
  password and creates their account on the media server.
- Adaptive per-server Jellyfin playback collection from `GET /Sessions`, with
  persisted pause/resume segments, bounded position samples, missed-poll stop
  detection, restart recovery, and bounded failure backoff.
- SQLite/PostgreSQL migration `00011_playback_collection` for watches, watch
  segments, and watch positions, with source attribution on every row. Deleting
  a media server stops its collector before cascading its playback data.
- Administrator playback APIs `GET /api/v1/playback/now` and cursor-paged
  `GET /api/v1/playback/history`, guarded by `stats.read.all` and returning the
  watch source.
- Playback settings `BLOOM_PLAYBACK_POLL_ACTIVE`,
  `BLOOM_PLAYBACK_POLL_IDLE`, `BLOOM_PLAYBACK_MISSED_POLLS`, and
  `BLOOM_PLAYBACK_RESUME_WINDOW`, plus poll, latency, open-watch, and closure
  metrics.
- Media servers page in the administration area (`/admin/media-servers`,
  guarded by `admin.settings`): register a Jellyfin server with its API key,
  see the capabilities Bloom detected, and remove it.
- Invite storage on SQLite and PostgreSQL through migration `00010`, including
  library selections and redemption records while retaining only code hashes.
- Administrative invite create, list, get, and revoke routes guarded by
  `users.invite`, plus public preview and acceptance routes with rate limits,
  CSRF protection, audit events, metrics, and compensating Jellyfin user
  deletion when library access cannot be applied.
- Automatic first-administrator startup bootstrap with the `admin` username,
  the built-in `owner` role, and the optional `BLOOM_BOOTSTRAP_USERNAME`
  override. Existing accounts are never changed, and concurrent startup
  attempts converge on one account.
- Administration area in the web UI: an `Admin` entry for accounts holding an
  admin permission, and a roles page that lists every role with its kind and
  permissions, paged through the roles API.
- Jellyfin media-server registration, encrypted API-key storage, connection
  probes, capability reporting, library listing, `admin.settings` route guards,
  audit events, outbound metrics, and SQLite/PostgreSQL migration `00007`.
- Pinned Jellyfin 12.1.0 OpenAPI input and scoped `oapi-codegen` output for the
  system-information and virtual-folder operations, with `make generate` and a
  stale-generation verification gate.
- Generic OIDC sign-in with provider discovery, authorization-code exchange,
  PKCE, verified issuer/subject identities, role-claim mapping, bounded
  dependency retries and telemetry, `GET /api/v1/auth/providers`,
  `POST /api/v1/auth/oidc/start`, and `GET /api/v1/auth/oidc/callback`.
- `BLOOM_PUBLIC_URL` and OIDC settings `BLOOM_OIDC_ENABLED`,
  `BLOOM_OIDC_DISPLAY_NAME`, `BLOOM_OIDC_ISSUER_URL`, `BLOOM_OIDC_CLIENT_ID`,
  `BLOOM_OIDC_CLIENT_SECRET`, `BLOOM_OIDC_REDIRECT_URL`, `BLOOM_OIDC_SCOPES`,
  `BLOOM_OIDC_USERNAME_CLAIM`, `BLOOM_OIDC_ROLE_CLAIM`,
  `BLOOM_OIDC_ROLE_MAP`, `BLOOM_OIDC_DEFAULT_ROLE`,
  `BLOOM_OIDC_ALLOW_INSECURE_ISSUER`, `BLOOM_OIDC_DISCOVERY_TIMEOUT`,
  `BLOOM_OIDC_TOKEN_EXCHANGE_TIMEOUT`, and `BLOOM_OIDC_JWKS_FETCH_TIMEOUT`.
- Migration `00008_oidc_identities` adds issuer-and-subject-keyed OIDC links.
  Migration `00009_account_role_sources` records `manual` or `oidc` role-grant
  provenance. Apply both before rollout. Their down migrations restore the
  prior schema; rolling back `00009` preserves the effective assignment but
  collapses overlapping source provenance.
- Main-branch and tagged-release image publishing with candidate-first smoke
  tests, source- and signer-bound attestation plus SBOM verification for
  immutable-image reuse, required CI and PostgreSQL gates, pre-promotion
  provenance, ancestry-guarded `main` and version-guarded stable aliases,
  deterministic bounded seven-day cleanup of unpromoted candidates,
  anonymous-pull validation, and an SBOM.
- Bootstrap of the service: Go HTTP server with `/livez`, `/readyz`, `/metrics`,
  and `GET /api/v1/version`; embedded React UI; SQLite (default) and PostgreSQL
  storage at parity; container image and CI.
- Repository hygiene: Dependabot, pull request template, code owners, security
  policy, and this changelog.
- Local authentication with `POST /api/v1/auth/login`, `POST /api/v1/auth/logout`,
  and `GET /api/v1/auth/me`; server-side `bloom_session` cookies; and the
  `create-admin` recovery command.
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
- `BLOOM_BOOTSTRAP_PASSWORD` as automatic first-administrator input and as
  non-interactive secret input for the `create-admin` recovery command, plus
  `BLOOM_TRUSTED_PROXY_CIDRS` as an off-by-default forwarded-client-address
  allowlist.
- A shared new-password policy for automatic bootstrap and `create-admin`: 15
  through 1024 Unicode characters, subject to the existing 4096-byte input
  bound and an embedded offline common-password denylist, with no
  character-composition rules.

### Fixed

- SQLite database startup now supplies the foreign-key pragma when the
  configured DSN omits it. Explicitly disabled foreign keys still fail
  startup, and shipped container DSNs now declare the enabled pragma.
- Media-server credentials are bound to registration metadata and carry a
  derived key id. HTTPS is the default, plaintext HTTP requires a persisted and
  audited override, unsafe special-purpose destinations are denied at validation
  and dial time, environment proxies are bypassed, retries are transient-only,
  and every media-server handler and dependency call has a bounded budget.

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
