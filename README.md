# bloom

Bloom is a single, self-hosted, container-first web service that replaces the
cluster of companion apps a Jellyfin operator runs today: invites and user
management (Wizarr), per-user playback statistics (Jellystat), and media
discovery and requests (Seerr). One binary, one API, one UI. It manages and
observes a media server through its API; it does not replace Jellyfin.

A *bloom* is the collective noun for a gathering of jellyfish, and the word also
means growth, which is what an invite system does for a server.

The product intake, scope, and research digest live in
[docs/requirements.md](docs/requirements.md). Every architectural decision is an
ADR under [decisions/](decisions/).

## Project Shape

- **Shape**: service (HTTP JSON API plus an embedded SPA, [ADR 0002](decisions/0002-embed-spa-in-binary.md))
- **Entrypoint**: `cmd/bloom/main.go`
- **Module**: `github.com/BonzTM/bloom`
- **Go**: 1.26+ (`go 1.26.0` in `go.mod`; tool pins may raise it)
- **Runtime surface**: HTTP `:8080` serving `/api/v1/*`, `/livez`, `/readyz`, `/metrics`
- **Store**: SQLite by default, PostgreSQL at full parity ([ADR 0004](decisions/0004-sqlite-default-postgres-parity.md))

## Quickstart

```bash
git clone https://github.com/BonzTM/bloom.git
cd bloom
make web-ci                              # frontend dependencies (once, and after lockfile changes)
make verify                              # Go gate, then the web gate (see web/README.md)
cp .env.example .env                     # then set BLOOM_SECRET_KEY (openssl rand -base64 48)
export BLOOM_SECRET_KEY="$(openssl rand -base64 48)"
go run ./cmd/bloom -migrate              # apply the embedded migrations to bloom.db
export BLOOM_BOOTSTRAP_PASSWORD='choose-a-long-unique-password'
go run ./cmd/bloom                       # start the service on :8080
```

Then, in another shell:

```bash
curl -s localhost:8080/livez             # ok
curl -s localhost:8080/readyz            # ready (503 JSON envelope if the DB is unreachable)
curl -s localhost:8080/api/v1/version    # {"name":"bloom","version":"dev","commit":"unknown"}
curl -s localhost:8080/metrics | head    # Prometheus exposition
scripts/smoke.sh                         # the same checks, scripted
```

## First run

Apply the database migrations before starting Bloom. Set a unique bootstrap
password, then start Bloom. When the accounts table is empty, startup creates
the first local account as `admin` and assigns the built-in `owner` role:

```bash
go run ./cmd/bloom -migrate
export BLOOM_BOOTSTRAP_PASSWORD='choose-a-long-unique-password'
go run ./cmd/bloom
```

Sign in as `admin`. Then remove the bootstrap secret from the service
environment:

```bash
unset BLOOM_BOOTSTRAP_PASSWORD
```

Set `BLOOM_BOOTSTRAP_USERNAME` before startup to use a username other than
`admin`. When any account already exists, startup does not create an account,
reset a password, or grant a role. It logs a reminder to remove
`BLOOM_BOOTSTRAP_PASSWORD`. The `-migrate` invocation never bootstraps an
account.

Use `create-admin` only as a recovery path when no suitable administrator can
sign in:

```bash
export BLOOM_BOOTSTRAP_PASSWORD='choose-a-long-unique-password'
go run ./cmd/bloom create-admin --username recovery-admin
unset BLOOM_BOOTSTRAP_PASSWORD
```

When the password variable is unset and the recovery command runs in a
terminal, Bloom prompts without echoing the password. A password is never
accepted as a command-line flag. Both automatic bootstrap and the recovery
command assign the `owner` role atomically with account creation and enforce
the same username and password policy. Usernames are canonicalized with the
PRECIS UsernameCaseMapped profile. They must contain 3 through 64 letters,
digits, `.`, `_`, or `-`, and cannot start or end with a separator. New local
passwords must contain 15 through 1024 Unicode characters and must not exceed
4096 UTF-8 bytes. Bloom rejects malformed UTF-8 and passwords in its embedded
offline common-password denylist. It applies no character-composition rules.

`make verify` is the single gate; it must pass before any change is considered done.
See [AGENTS.md](AGENTS.md) for the full contributor contract and verification bar.

### Authorization

Bloom uses permission-based roles. Permissions are stable strings grouped by
module, such as `users.read`, `requests.create`, and `admin.roles`. Published
permission identifiers are part of the API contract. New identifiers may be
added, but existing identifiers are never renamed or removed. The public,
cacheable catalog is available from `GET /api/v1/auth/permissions`.

The built-in `owner` role has every permission and cannot be edited or deleted.
The built-in `member` role can read its own request and statistics data and can
create requests. Accounts may hold multiple roles; their effective permission
set is the union of those roles. Bloom reads that set from the database for each
authenticated request. It does not cache authorization decisions.

`GET /api/v1/auth/me` returns the current account, its sorted role names, and
its sorted effective permissions. `GET /api/v1/roles` requires `admin.roles`
and returns roles with their permissions using cursor pagination. The default
page size is 50 roles and the enforced maximum is 100. A missing session gets
`401 unauthorized`; a signed-in account without the required permission gets
`403 forbidden`. This slice does not expose role-editing endpoints.

Browser sessions and logout revocations are stored in the configured database,
so they persist across process and container restarts. Persist the database and
keep `BLOOM_SECRET_KEY` unchanged across deployments; no process-local session
state needs to be preserved.

When OIDC is enabled, `GET /api/v1/auth/providers` advertises the configured
display name, `POST /api/v1/auth/oidc/start` with an
`application/x-www-form-urlencoded` body and optional `return_to` field begins
discovery-backed authorization-code sign-in with PKCE, and the callback is
`GET /api/v1/auth/oidc/callback`. State, nonce, verifier, and return path live
only in Bloom's server-side session and expire after ten minutes. A linked
identity is keyed by its verified issuer and subject. An unknown identity is
provisioned only when `BLOOM_OIDC_DEFAULT_ROLE` is explicitly non-empty; the
secure default does not provision accounts. Claim-mapped roles are recorded as
OIDC grants and reconciled on each sign-in. Manual grants remain independent,
including a manual grant of the same role. Effective authorization is their
union. Bloom does not retain provider access or refresh tokens.

Failed browser callbacks redirect to the configured public URL at
`/login?error=<code>`. The closed code set is `unknown_identity`,
`provisioning_disabled`, `state_invalid`, `token_invalid`,
`provider_unavailable`, `disabled`, and `internal_error`. Provider error text is
never copied into the redirect. Callers whose `Accept` header does not include
`text/html` receive the documented JSON error envelope instead.

### Running against PostgreSQL

```bash
export BLOOM_DB_DRIVER=postgres
export BLOOM_DB_DSN='postgres://bloom:bloom@localhost:5432/bloom?sslmode=disable'
go run ./cmd/bloom -migrate && go run ./cmd/bloom
```

### Adding a media server

Sign in as an account with `admin.settings`, then register each Jellyfin server
through `POST /api/v1/media-servers`. Bloom requires an HTTPS base URL, probes
`GET /System/Info` before saving anything, and encrypts the API key
with a key derived from `BLOOM_SECRET_KEY`. The API key is write-only. Bloom
never returns it from the API or includes it in application or audit logs.

Plaintext HTTP exposes the unrestricted Jellyfin administrator credential to
the network. Use it only for a trusted local deployment that cannot enable TLS,
and set `"allow_insecure": true` on that registration. Bloom stores and returns
that exception, emits a warning, and records it in the create audit event.

Private destination ranges are allowed for self-hosted servers. Bloom rejects
loopback, link-local, multicast, unspecified, and cloud-metadata destinations.
It resolves and checks the address again when each connection is dialed to
prevent DNS rebinding into a rejected range. Credentialed Jellyfin requests do
not honor environment proxy settings; deploy a TLS endpoint directly reachable
from Bloom rather than relying on `HTTP_PROXY` or `HTTPS_PROXY`.

The following example keeps both passwords out of command-line arguments:

```bash
read -rsp 'Bloom password: ' BLOOM_LOGIN_PASSWORD; echo
curl -sS -c bloom.cookies -H 'Content-Type: application/json' \
  --data-binary @- http://localhost:8080/api/v1/auth/login <<EOF
{"username":"owner","password":"${BLOOM_LOGIN_PASSWORD}"}
EOF
unset BLOOM_LOGIN_PASSWORD

read -rsp 'Jellyfin API key: ' JELLYFIN_API_KEY; echo
curl -sS -b bloom.cookies -H 'Content-Type: application/json' \
  --data-binary @- http://localhost:8080/api/v1/media-servers <<EOF
{"kind":"jellyfin","name":"Home","base_url":"https://jellyfin.example.com","api_key":"${JELLYFIN_API_KEY}"}
EOF
unset JELLYFIN_API_KEY
```

Use `GET /api/v1/media-servers` to list registrations. Use
`POST /api/v1/media-servers/{id}/probe` to recheck a connection and
`GET /api/v1/media-servers/{id}/libraries` to list its libraries. Deleting a
registration removes its encrypted credential and its associated playback data.

### Statistics

Bloom polls `GET /Sessions` on every registered Jellyfin server and records
Bloom-owned watches with source `poll`. Every watch, active segment, and
position sample stores its observation source. A watch retains the source that
opened it if later observations arrive from another source. Bloom stores playing
and paused intervals as separate segments, so active time excludes observed
pauses. It also retains the newest 512 position and play-method samples per
watch. Open watches are persisted on every observation and restored after a
restart.

Polling is adaptive and jittered. The default interval is five seconds while a
watch is open and thirty seconds while the server is idle. Failed polls use
bounded backoff and do not count as missing sessions. A watch closes at its last
successful sighting after three successful polls omit it. A matching sighting
within five minutes reopens that watch.

Polling has finite accuracy. A play shorter than the poll interval can be
missed. Position precision is bounded by the active polling interval. A client
that remains present and unpaused while stalled is counted as active. Bloom
does not import activity from before collection started in this release.

An authenticated account with `stats.read.all` can use cursor-paged
`GET /api/v1/playback/now` and `GET /api/v1/playback/history`. The history route accepts an optional
`media_server_id` filter. Successful reads emit no playback audit event;
authorization denials continue to use the shared security audit stream.

An account with `stats.read.all` can also read the statistics dashboards at
`GET /api/v1/stats/overview`, `/daily`, `/patterns`, `/titles`, `/users`, and
`/users/{media_server_id}/{media_user_id}`. One recorded watch row is one play.
Watch time is the row's recorded `active_seconds`. A watch belongs to the
half-open window when `started_at` is at or after the window start and before
the response's end time. The `days` parameter defaults to 30 and accepts 1
through 365 rolling 24-hour days. The optional `media_server_id` limits the SQL
query to one server.

Every response states its resolved window and time zone. `tz` defaults to UTC
and accepts an IANA time-zone name of at most 64 bytes. Bloom converts
`started_at` to that zone in Go for daily, weekday, and hour buckets; totals,
rankings, and breakdowns remain portable SQL on both database engines. A
bounded in-process LRU caches complete results for 30 seconds by default.
`BLOOM_STATS_CACHE_TTL=0` disables it. Cache expiry is the invalidation policy,
so a dashboard can trail a newly recorded watch by at most the configured TTL.

Per-library dashboards are a later slice because recorded watches do not yet
carry a library identifier. Own-statistics access for non-administrator
accounts is also later because Bloom accounts are not yet linked to
media-server users.

### Invites

An account with `users.invite` can create an invite with
`POST /api/v1/invites`. Each invite targets one registered media server. It can
have an optional future expiry, a limit of 1 through 1000 uses, and either all
libraries or an explicit selection from that server. The create response is the
only response that contains the 26-character invite code. Copy and share its
`accept_path` immediately. Bloom stores only a SHA-256 digest of the code and
cannot recover it later.

Use `GET /api/v1/invites` to review status and use counts. Use
`DELETE /api/v1/invites/{id}` to revoke an invite. Revocation retains the invite
and its redemption history; Bloom does not offer invite deletion.

The recipient opens `/invite/<code>` and chooses a Jellyfin username and
password. Usernames must not have leading or trailing whitespace or control
characters and must follow Jellyfin's username character rule; Bloom adds its
own bound of 1 through 64 UTF-8 bytes, which Jellyfin does not have. Passwords must contain 15 through 1024 Unicode
characters, must not exceed 4096 UTF-8 bytes, and must not appear in Bloom's
offline common-password denylist. Bloom sends the password to Jellyfin for user
creation. It never stores, logs, or audits the password.

Bloom applies the invite's library access immediately after creating the
Jellyfin user. If that update fails, Bloom deletes the new user and returns an
upstream failure. A used, expired, exhausted, revoked, or unknown code returns
the same public not-found response. A username that Jellyfin rejects as taken
or invalid returns a conflict response.

The public invite routes are rate limited per client address and per code
with the same `BLOOM_LOGIN_RATE_*` settings that bound sign-in attempts, so a
code cannot be guessed by enumeration.

### Requests

Bloom starts without a metadata credential. An account with `admin.settings`
stores the TMDB API key with `PUT /api/v1/metadata/providers/tmdb/key`. Bloom
encrypts the key under `BLOOM_SECRET_KEY`, never returns it, and exposes only
its presence through `GET` on the same route. Removing the key disables new
metadata searches and returns `metadata_not_configured` until another key is
stored.

Register each Radarr or Sonarr instance with `POST /api/v1/download-managers`.
Bloom verifies the API key before storing it encrypted. Use `GET` on that
collection to list registrations and
`GET /api/v1/download-managers/{id}/options` to read its quality profiles, root
folders, and tags. Empty option collections are returned as JSON arrays, never
`null`. Bloom never returns an API key. Deletion is refused while a request
profile references the instance.

Create at least one request profile with `POST /api/v1/request-profiles`.
Profiles select the kinds supported by the named instance: Radarr handles
movies and Sonarr handles series. They also select its quality profile, root
folder, and tags. List profiles from the collection and update a profile with
`PUT /api/v1/request-profiles/{id}`. Delete a profile with `DELETE` on the same
item route. Bloom refuses deletion after a request references the profile.

Accounts with `requests.create` can search TMDB, open movie or series details,
and submit a movie or selected series seasons to `POST /api/v1/requests`.
Accounts with `requests.approve` are exempt from quotas and their own requests
are approved immediately. Other requests remain pending until an approver uses
the request's `/approve` or `/decline` route. Approved requests are dispatched
under an exclusive ten-minute lease and move to `processing`; an approved
request whose worker stops is reclaimed after its lease expires. Only the
current lease owner can record dispatch success or failure. Permanent failures
move to `failed` and may be approved again.
`GET /api/v1/requests/{id}/progress` reads current queue state for the owner or
an approver. Bloom snapshots the manager registration,
quality profile, root folder, and tags when dispatch starts. Editing the request
profile cannot redirect an in-flight request. Deleting that snapshotted manager
causes the next availability check to move the request to `failed`, where an
approver can retry it after correcting the profile.

Bloom marks processing requests available from registered media servers by
default. Set `BLOOM_REQUEST_AVAILABILITY_SOURCE=download_manager` to use the
manager's completed-file signal instead. The poller runs only while processing
requests exist and uses `BLOOM_REQUEST_AVAILABILITY_INTERVAL` between checks.
Queue completion never marks a request available. Radarr's movie file flag and
Sonarr's requested-season file statistics are the download-manager authority.
Transient upstream failures remain in `processing`; permanent authorization,
missing-resource, and malformed-response failures move the request to `failed`.

Role quotas are managed at `/api/v1/roles/{id}/request-quota` with
`admin.roles`, or from the Roles page in the web UI. Account overrides are managed at
`/api/v1/accounts/{id}/request-quota` with `admin.settings`. Movie and season
limits each use a rolling period in days. A zero limit and zero period disable
that limit. Declined requests do not consume quota. When several assigned roles
define quotas, Bloom permits the request when any applicable role quota permits
it; an account override replaces the role calculation.

Accounts with `requests.read.own` see their own requests. Approvers see all
requests and can filter by status or requester. Lists are cursor-paged newest
first.

The web UI covers the same flow. Administrators register Radarr and Sonarr
instances under Download managers, store the TMDB key and build profiles from
an instance's options under Request settings, and decide pending requests,
retry failed ones, and read queue progress under Requests in the
administration area. Anyone who may request media searches, browses
posters, picks seasons, and follows their own requests under Requests in the
main navigation. Posters load from `image.tmdb.org`, the only third-party
origin the page allows for images.

### Back up the master secret

Back up `BLOOM_SECRET_KEY` with the database and keep it stable across restarts,
replicas, restores, and upgrades. The value derives both the credential key and
the installation-specific Jellyfin device identifier. Bloom stores a derived
key identifier in each encrypted envelope, so starting with a different value
reports a wrong-key failure instead of treating every credential as corrupt.

If the key is lost or changed, restore the original key. If it cannot be
restored, delete and re-register every media server with its API key. A
supported credential re-encryption command is planned but is not included in
this release. Do not attempt rotation by changing the environment value alone.

## Configuration

All configuration is loaded once in `internal/config` from environment variables
(with flag overrides, `-http-addr` for `BLOOM_HTTP_ADDR` and so on) and validated
at startup. The process fails fast with an actionable message if a required value
is missing or malformed. Commit changes to `.env.example` alongside any change to
this table.

| Key | Type | Required | Default | Secret | Description |
|---|---|---|---|---|---|
| `BLOOM_HTTP_ADDR` | string | no | `:8080` | no | Listen address for the HTTP server. |
| `BLOOM_HTTP_READ_HEADER_TIMEOUT` | duration | no | `5s` | no | Bound on reading request headers. |
| `BLOOM_HTTP_READ_TIMEOUT` | duration | no | `15s` | no | Bound on reading a request including the body. |
| `BLOOM_HTTP_WRITE_TIMEOUT` | duration | no | `15s` | no | Bound on writing a response. |
| `BLOOM_HTTP_IDLE_TIMEOUT` | duration | no | `60s` | no | Idle keep-alive connection lifetime. |
| `BLOOM_HTTP_MAX_BODY_BYTES` | int | no | `1048576` | no | Cap on non-streaming request bodies. |
| `BLOOM_PUBLIC_URL` | HTTP(S) origin | no | `http://localhost:8080` | no | Externally visible Bloom origin used for callback-error redirects and OIDC redirect validation. |
| `BLOOM_DB_DRIVER` | `sqlite` \| `postgres` | no | `sqlite` | no | Database engine. Anything else fails startup. |
| `BLOOM_DB_DSN` | string | postgres: yes | sqlite: `file:bloom.db?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)` | yes | Data source name. Required when the driver is `postgres`. For compatibility, startup supplies `_pragma=foreign_keys(1)` when a configured SQLite DSN omits a foreign-key pragma; an explicit disable still fails startup. |
| `BLOOM_DB_MAX_OPEN_CONNS` | int | no | `25` | no | Pool cap on open connections. |
| `BLOOM_DB_MAX_IDLE_CONNS` | int | no | `25` | no | Pool idle floor; must be `<=` max open. |
| `BLOOM_DB_CONN_MAX_LIFETIME` | duration | no | `30m` | no | Bound on connection age. |
| `BLOOM_DB_CONN_MAX_IDLE_TIME` | duration | no | `5m` | no | Reap idle connections after this long. |
| `BLOOM_DB_MIGRATE_ON_STARTUP` | bool | no | `false` | no | Apply embedded migrations before serving. Single-writer convenience; production uses `-migrate`. |
| `BLOOM_SECRET_KEY` | string | **yes** | — | **yes** | Stable master secret (at least 32 bytes) that derives stored-credential keys and the Jellyfin device id ([ADR 0006](decisions/0006-auth-and-authorization-model.md)). Back it up with the database. Never log or rotate it by replacement; see “Back up the master secret.” |
| `BLOOM_BOOTSTRAP_USERNAME` | string | no | `admin` | no | Username for automatic first-administrator bootstrap. Uses the normal username policy. |
| `BLOOM_BOOTSTRAP_PASSWORD` | string | startup: optional; create-admin: conditional | — | **yes** | Enables automatic first-administrator bootstrap when set. The recovery command reads it non-interactively; when unset, that command requires a terminal prompt. Remove it after the first account exists. |
| `BLOOM_SESSION_COOKIE_SECURE` | bool | no | `true` | no | Set the `Secure` session-cookie flag. Disable only for plaintext local development. |
| `BLOOM_SESSION_LIFETIME` | duration | no | `24h` | no | Absolute lifetime of a browser session. |
| `BLOOM_SESSION_IDLE_TIMEOUT` | duration | no | `30m` | no | Invalidate a browser session after this period of inactivity. Must not exceed the lifetime. |
| `BLOOM_LOGIN_RATE_REFILL_INTERVAL` | duration | no | `1m` | no | Per-IP and per-username login buckets, and the per-IP and per-code buckets on the public invite routes, regain one attempt per interval. |
| `BLOOM_LOGIN_RATE_BURST` | int | no | `5` | no | Maximum immediately available attempts in each login bucket and each public invite bucket. |
| `BLOOM_LOGIN_RATE_MAX_KEYS` | int | no | `10000` | no | Bound on rate-limit entries held in memory, for the login buckets and separately for the public invite buckets. Valid range: 2-100000. |
| `BLOOM_LOGIN_MAX_CONCURRENT` | int | no | `4` | no | Maximum concurrent Argon2id password verifications. Excess attempts fail fast with `503`. Valid range: 1-64. |
| `BLOOM_TRUSTED_PROXY_CIDRS` | comma-separated CIDRs | no | — | no | Trust `X-Forwarded-For` only when the direct peer is in this allowlist. Empty disables forwarded addresses. |
| `BLOOM_OIDC_ENABLED` | bool | no | `false` | no | Enable one generic OpenID Connect provider. Discovery runs at startup and startup fails if it cannot complete. |
| `BLOOM_OIDC_DISPLAY_NAME` | string | OIDC: yes | `OpenID Connect` | no | Sign-in method label returned by `GET /api/v1/auth/providers`. |
| `BLOOM_OIDC_ISSUER_URL` | URL | OIDC: yes | — | no | OIDC issuer, limited to 2,048 bytes. HTTPS is required except loopback HTTP with the development flag. |
| `BLOOM_OIDC_CLIENT_ID` | string | OIDC: yes | — | no | OAuth client identifier. |
| `BLOOM_OIDC_CLIENT_SECRET` | string | OIDC: yes | — | **yes** | Environment-only OAuth client secret. It is wrapped in `config.Secret`, never rendered or logged, and never accepted as a flag. Rotate it with a rolling restart. |
| `BLOOM_OIDC_REDIRECT_URL` | URL | OIDC: yes | — | no | Exact callback URL: `BLOOM_PUBLIC_URL` plus `/api/v1/auth/oidc/callback`, with no query or fragment. HTTPS is required except loopback HTTP in explicit development mode. |
| `BLOOM_OIDC_SCOPES` | space-separated strings | no | `openid profile email` | no | Requested scopes. `openid` is required; at most 16 values are accepted. |
| `BLOOM_OIDC_USERNAME_CLAIM` | string | no | `preferred_username` | no | Verified ID-token claim used to derive the canonical Bloom username. |
| `BLOOM_OIDC_ROLE_CLAIM` | string | no | — | no | Optional verified claim containing one role value or an array of role values. |
| `BLOOM_OIDC_ROLE_MAP` | comma-separated mappings | no | — | no | Claim-value-to-Bloom-role mappings such as `bloom-admins=owner,bloom-users=member`. Requires a role claim. |
| `BLOOM_OIDC_DEFAULT_ROLE` | role name | no | — | no | Explicit opt-in to JIT provisioning. The role is assigned when no mapped role applies; empty keeps unknown identities denied. |
| `BLOOM_OIDC_ALLOW_INSECURE_ISSUER` | bool | no | `false` | no | Development-only opt-in for an HTTP issuer and callback on `localhost` or a loopback IP. |
| `BLOOM_OIDC_DISCOVERY_TIMEOUT` | duration | no | `5s` | no | Per-attempt and total client bound for startup discovery. Valid range: `100ms`-`30s`. |
| `BLOOM_OIDC_TOKEN_EXCHANGE_TIMEOUT` | duration | no | `5s` | no | Authorization-code exchange bound. Valid range: `100ms`-`30s`; token POSTs are not retried. |
| `BLOOM_OIDC_JWKS_FETCH_TIMEOUT` | duration | no | `5s` | no | JWKS fetch bound. Valid range: `100ms`-`30s`; cached-key misses perform at most one bounded refetch. |
| `BLOOM_PLAYBACK_POLL_ACTIVE` | duration | no | `5s` | no | Jittered polling interval while any watch is open. Valid range: `1s`-`60s`. |
| `BLOOM_PLAYBACK_POLL_IDLE` | duration | no | `30s` | no | Jittered polling interval while no watch is open. Valid range: `5s`-`10m`. |
| `BLOOM_PLAYBACK_MISSED_POLLS` | int | no | `3` | no | Consecutive successful polls that may omit a session before its watch closes. Valid range: 1-100. |
| `BLOOM_PLAYBACK_RESUME_WINDOW` | duration | no | `5m` | no | Window in which a matching stopped watch reopens. Valid range: `1s`-`24h`. |
| `BLOOM_PLAYBACK_STORE_TIMEOUT` | duration | no | `5s` | no | Per-operation deadline for playback database loads, lookups, and saves. Valid range: `100ms`-`30s`. |
| `BLOOM_STATS_CACHE_TTL` | duration | no | `30s` | no | TTL for the bounded in-process statistics result cache. Must be at least `0`; `0` disables caching. |
| `BLOOM_REQUEST_AVAILABILITY_SOURCE` | `media_server` \| `download_manager` | no | `media_server` | no | Authority used to mark processing requests available. Queue progress is never the authority. |
| `BLOOM_REQUEST_AVAILABILITY_INTERVAL` | duration | no | `5m` | no | Poll interval while processing requests exist. Valid range: `1m`-`24h`. |
| `BLOOM_LOG_LEVEL` | string | no | `info` | no | `slog` level: `debug`, `info`, `warn`, `error`. |
| `BLOOM_LOG_FORMAT` | `json` \| `text` | no | `json` | no | Log record format. |
| `BLOOM_OTLP_ENDPOINT` | string | no | — | no | OTLP/HTTP trace collector `host:port`. Empty disables span export. |
| `BLOOM_OTLP_INSECURE` | bool | no | `false` | no | Send spans over plaintext HTTP. |
| `BLOOM_TRACE_SAMPLE_RATIO` | float | no | `1.0` | no | Head-based sampling ratio in `[0,1]`. |
| `BLOOM_SHUTDOWN_GRACE` | duration | no | `15s` | no | Total ordered shutdown budget on `SIGTERM`; keep it under the platform's termination grace. Bloom drains HTTP, stops playback collectors, then closes the OIDC provider and media-server idle connections before the database and telemetry tracer. Unused time carries forward within the same absolute deadline. Size the grace to more than twice the longest admitted request so the drain reservation can finish it. |

The `-migrate` flag (no env key) applies the embedded goose migrations for the
configured engine and exits; it is how a deployment's migration Job invokes the
same image ahead of a rollout. Canonical usernames migrate in three ordered
steps: per-engine SQL migration 00003 adds nullable key and backup storage,
engine-neutral Go migration 00004 backfills PRECIS UsernameCaseMapped keys in
Goose's transaction, and per-engine SQL migration 00005 makes the key `NOT NULL`
and exactly unique. A collision aborts 00004 without changing its version or
account data. Migration `00006_roles` adds roles, role permissions, and account
role assignments on both engines and seeds the built-in `owner` and `member`
roles. Existing accounts are deliberately left without roles; the migration
logs a notice instead of guessing their authority.

Migration `00008_oidc_identities` adds issuer-and-subject-keyed OIDC identity
links on both engines. Migration `00009_account_role_sources` adds `manual` and
`oidc` provenance so OIDC reconciliation cannot remove a manual grant. Apply
both before the new binary receives traffic. Their down migrations restore the
prior schema. Rolling back `00009` collapses duplicate manual/OIDC grants to one
effective assignment, so preserve a backup if the provenance must be restored.
Migration `00010_invites` adds invite metadata, library selections, and
redemption records on both engines. Its down migration removes those records,
so back up the database before schema rollback when invite history matters.

Migration `00011_playback_collection` adds watches, active-time segments, and
bounded position samples on both engines. Apply it before enabling this binary.
Deleting a media-server registration first stops and awaits its collector, then
cascades to all three playback tables.

Migration `00014_stats_started_index` adds the unfiltered watch-start index used
by bounded statistics windows on both engines. Its down migration removes only
that index.

Migration `00012_metadata_requests` adds encrypted metadata-provider settings,
request profiles and tags, media requests and seasons, and role and account
rolling request quotas on both engines. Apply it before enabling metadata and
request routes.

### Upgrading existing accounts to roles

Back up the database before applying migration `00006_roles`. After migration,
grant a built-in role to an existing roleless account:

```bash
go run ./cmd/bloom grant-role --username <existing-name> --role owner
```

The command refuses unknown accounts and roles. Repeating the same grant is a
successful no-op. Start Bloom, sign in as that account, and verify that
`GET /api/v1/auth/me` lists `owner` under `roles` and includes `admin.roles`
under `permissions`. Then verify that `GET /api/v1/roles` returns `200`.

To roll back the application, stop Bloom and redeploy the prior binary; the
additive role tables can remain in place. To roll back the schema itself,
restore the pre-upgrade database backup because migrating down removes the role
assignments.

## Architecture

A request enters `internal/api/http` (request id, security headers, recovery,
OpenTelemetry span, access log and metrics), which calls into `internal/core`;
`internal/core` talks to storage only through consumer-defined interfaces that
`internal/db` satisfies with one adapter per engine over `sqlc`-generated code.
`internal/runtime` wires those pieces and owns the ordered shutdown;
`cmd/bloom` only loads config and installs signal handling.

Layout follows the handbook default (`cmd/` + `internal/`):

- `cmd/bloom` — process wiring, signal handling, shutdown; no business logic.
- `internal/runtime` — assembly, startup order, bounded shutdown, `-migrate` mode.
- `internal/core` — domain types and the interfaces consumed from the outside (`AccountStore`, `Authorizer`, `RoleReader`, `Clock`).
- `internal/api/http` — JSON API transport adapter; `auth.go` is the [ADR 0006](decisions/0006-auth-and-authorization-model.md) seam.
- `internal/db` — pool, embedded per-engine migrations, shared queries, `sqlite/` and `postgres/` generated packages, engine adapters, parity tests.
- `internal/config` — loading, defaults, validation, fail-fast startup, `Secret`.
- `internal/httputil` — JSON helpers, the error envelope, request-size cap.
- `internal/telemetry` — logger, Prometheus metrics, OpenTelemetry tracing, readiness, audit stream.
- `internal/buildinfo` — `Name`, `Version`, `Commit` stamped via `-ldflags`.
- `internal/testutil` — fake clock and other test-only helpers.
- `api/openapi.yaml` — the published wire contract the SPA builds against.
- `web/` — the SPA source, built by its own toolchain and embedded per [ADR 0002](decisions/0002-embed-spa-in-binary.md).
- `scripts/` — operator and CI helper scripts.

Authoritative contributor rules: [AGENTS.md](AGENTS.md).
Change routing by file area: the Change Routing table in [AGENTS.md](AGENTS.md).
Architecture decisions and their rationale: [decisions/](decisions/).
Product requirements and research: [docs/](docs/).

## Testing

```bash
make test     # go test ./...
make race     # go test -race ./...
make cover    # coverage profile + report
```

The database parity suite in `internal/db` runs against in-memory SQLite on
every `make verify` and against a live PostgreSQL when
`BLOOM_TEST_POSTGRES_DSN` is set with the `integration` build tag:

```bash
docker run --rm -d -p 5432:5432 -e POSTGRES_USER=bloom -e POSTGRES_PASSWORD=bloom -e POSTGRES_DB=bloom postgres:16
BLOOM_TEST_POSTGRES_DSN='postgres://bloom:bloom@localhost:5432/bloom?sslmode=disable' \
  go test -tags=integration ./internal/db/...
```

CI runs both halves for pull requests and `main` pushes. The image workflow also
calls the same gate for each `main` or release-tag commit before building.
Every behavior change ships with a test that proves it.

## Release And Deploy

Bloom images are published at `ghcr.io/bonztm/bloom`. For a commit without an
existing immutable tag, a merge to `main` builds an amd64 image under a
run-specific `candidate-<run>-<attempt>` tag. It tests that image by digest and
promotes the same digest to the immutable `main-<full-commit>` tag. The mutable
`main` tag moves only when its current revision is an ancestor of the incoming
commit. A stale rerun leaves `main`
unchanged and does not open a deployment pull request. The build starts only
after the reusable CI workflow passes both `make verify` and the PostgreSQL
integration suite for the same commit. Before building, the workflow resolves
the immutable tag.

Release proof has two repository-visibility modes. In a public repository, the
workflow records signed provenance before promotion. A rerun reuses an existing
immutable digest only after verifying that provenance against the exact source
commit, source ref, and image workflow signer. Promotion repeats the
source-commit and signer checks as a self-test. In a private repository, GitHub
artifact attestations are unavailable without a GitHub Enterprise Cloud plan, so the
workflow reports and skips only those provenance steps. In both modes, fresh
candidates are built and smoke-tested by digest, image labels and versions are
checked, and an attached SPDX SBOM must be present. Private-mode reuse therefore
rejects a legacy immutable image without an SBOM or with mismatched labels.
Making the repository public turns signed provenance on for the next fresh
build with no workflow change. An immutable tag that was published while the
repository was private has no attestation, so a rerun of that same commit will
refuse to reuse it once attestations are required; delete that package version
once and rerun to rebuild it with provenance.

A weekly cleanup inspects a rotating window of at most 1,000 package versions and
deletes at most 100 versions older than seven days only when every tag on that
version starts with `candidate-`. It computes the first page as
`(((GITHUB_RUN_NUMBER - 1) * 10) modulo 100) + 1`. Successive runs start at pages
1, 11, through 91, scan at most ten consecutive pages, then repeat without stored
cursor state. Cleanup runs never overlap: an active run finishes, and further
runs wait in a bounded queue (GitHub keeps up to 100 pending) rather than
replacing one another.
It reports when the scan or delete cap defers work to a later rotation. GHCR
stores tags on a shared digest version, so promoted versions carry both their
candidate tag and public tags. Those promoted candidates remain in the registry
because deleting their package version would also delete the promoted image.

The workflow publishes images and does nothing else; deploying them is your
platform's job. The `main` alias is ancestry-guarded and never moves backwards,
so a platform that always pulls the alias can only run the same or a newer
build than it ran before.

Pushing a `v<major>.<minor>.<patch>` tag keeps the release flow separate from
deployment. A `v1.2.3` release publishes immutable `v1.2.3` and `1.2.3` tags.
It moves `1.2` and `latest` only when `1.2.3` is newer than the version that the
alias already references. A prerelease such as `v1.2.3-rc.1` publishes only
that exact tag. Invalid release tags fail before an image is pushed. Manual runs
accept only `main` or an actual valid release tag; a similarly named branch is
rejected. Image runs share a serialized queue so promotions cannot race.

[GitHub creates a newly published container package as private by
default](https://docs.github.com/en/packages/learn-github-packages/configuring-a-packages-access-control-and-visibility#about-visibility-of-packages).
After the first image is published, open the
[`bonztm/bloom` package settings](https://github.com/users/BonzTM/packages/container/bloom/settings)
and change its visibility to public. This is a one-time bootstrap step; the
promote job validates that anonymous pulls work and fails with an actionable
error while they are disabled.

Release builds add `-trimpath` and stamp `internal/buildinfo` through the
Dockerfile's `VERSION`, `COMMIT`, and `CREATED` build arguments. Run the image
with `-migrate` as a Job before rolling the Deployment. SQLite single-container
deployments may set `BLOOM_DB_MIGRATE_ON_STARTUP=true`. Use `GET /livez` for
liveness, `GET /readyz` for database-aware readiness, and `GET /metrics` for
Prometheus metrics.

## Ownership And Support

- **Owner**: BonzTM.
- **Issues**: https://github.com/BonzTM/bloom/issues.
- **Security reports**: do not open a public issue; contact the owner directly.
