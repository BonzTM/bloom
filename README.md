# bloom

Bloom is a single, self-hosted, container-first web service that replaces the
cluster of companion apps a Jellyfin operator otherwise runs: invites and user
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
The built-in `member` role has `requests.read.own`, `requests.create`, and
`stats.read.own`. The role can read its own request and statistics data and can
create requests. Accounts may hold multiple roles; their effective permission
set is the union of those roles. Bloom reads that set from the database for each
authenticated request. It does not cache authorization decisions.

`users.invite` grants invite administration and the least-privilege
`GET /api/v1/invites/servers` selector. It does not grant media-server settings
access. `admin.settings` grants provisioning-failure inspection and dismissal,
in addition to the configuration routes described below.

`GET /api/v1/auth/me` returns the current account, its sorted role names, and
its sorted effective permissions. `GET /api/v1/roles` requires `admin.roles`
and returns roles with their permissions using cursor pagination. The default
page size is 50 roles and the enforced maximum is 100. A missing session gets
`401 unauthorized`; a signed-in account without the required permission gets
`403 forbidden`. Roles are read-only over the API; there are no role-editing endpoints.

Browser sessions and logout revocations are stored in the configured database,
so they persist across process and container restarts. Persist the database and
keep `BLOOM_SECRET_KEY` unchanged across deployments; no process-local session
state needs to be preserved. A session lasts 30 days and ends after 7 days
without a request; `BLOOM_SESSION_LIFETIME` and `BLOOM_SESSION_IDLE_TIMEOUT`
change both bounds.

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
`GET /api/v1/media-servers/{id}/libraries` to list its libraries. Administrators
use `GET /api/v1/media-servers/{id}/users` to list a server's users and choose
one to link to a Bloom account. Deleting a
registration removes its encrypted credential and its associated playback data.

Signed-in accounts with `stats.read.all`, `stats.read.own`, or
`requests.read.own` can load registered-server artwork from
`GET /api/v1/media-servers/{id}/items/{item_id}/image`. The required `type`
is `Primary`, `Backdrop`, or `Thumb`; `max_width` defaults to 400 and accepts
64 through 1280. Bloom builds the Jellyfin image URL, keeps the API key on the
server, accepts only JPEG, PNG, or WebP responses up to 4 MiB, and privately
caches successful images for one day. The route forwards Jellyfin ETags and
honors `If-None-Match`.

### Statistics

Bloom polls `GET /Sessions` on every registered Jellyfin server and records
Bloom-owned watches with source `poll`. Every watch, active segment, and
position sample stores its observation source. A watch retains the source that
opened it if later observations arrive from another source. Bloom stores playing
and paused intervals as separate segments, so active time excludes observed
pauses. It also retains up to 512 position, play-method, and stream-detail
samples per watch. Pause, play-method, and stream transitions are retained
ahead of position-only samples, with the newest transitions retained when the
bound contains transitions only. Each sample can include the observed container, video and
audio codecs, bitrate, dimensions, framerate, audio channels, direct-stream
flags, and transcode reasons. A position, pause, play-method, or stream-detail
change creates a sample; an otherwise unchanged observation does not. The
direct-play audio fields follow Jellyfin's selected audio-stream index and fall
back to the default track; framerates are rounded to hundredths. The latest
stream details are also stored on the watch for list responses. Each watch
also stores Jellyfin's nullable item runtime in milliseconds and updates it
when Jellyfin reports a change; position samples do not duplicate the runtime.
Open watches are persisted on every observation and restored after a
restart. Bloom resolves each item's Jellyfin `CollectionFolder` ancestor and
stores that library on the watch. A per-server cache bounds ancestor lookups to
4096 entries for 24 hours. After each successful poll, Bloom also attempts up to
25 oldest unresolved item identifiers so watches recorded before library
resolution are filled in gradually.

Polling is adaptive and jittered. The default interval is five seconds while a
watch is open and thirty seconds while the server is idle. Failed polls use
bounded backoff and do not count as missing sessions. A watch closes at its last
successful sighting after three successful polls omit it. A matching sighting
within five minutes reopens that watch.

Polling has finite accuracy. A play shorter than the poll interval can be
missed. Position precision is bounded by the active polling interval. A client
that remains present and unpaused while stalled is counted as active. Bloom can
import activity from before collection started as described below.

An authenticated account with `stats.read.all` can use cursor-paged
`GET /api/v1/playback/now` and `GET /api/v1/playback/history`. The history route accepts an optional
`media_server_id` filter. `GET /api/v1/playback/watches/{id}` returns one visible
open or finished watch; unknown watches and watches hidden by user or library
exclusions return `404 not_found`. `GET /api/v1/playback/watches/{id}/positions`
returns that watch's retained samples, up to 512, in newest-first order. Watch list and
per-user statistics detail responses include the latest stream details when
available. Those watch responses also include nullable `runtime_ms` so clients
can calculate progress and remaining time. Successful reads emit no playback audit event;
authorization denials continue to use the shared security audit stream.

### Activity, timelines, and collection exclusions

An account with `stats.read.all` can list visible watches across every user at
`GET /api/v1/activity`. Results use newest-first `(started_at, id)` keyset
pagination. `limit` defaults to 50 and accepts 1 through 100. Pass the returned
`next_cursor` as `cursor`. The route accepts `media_server_id`,
`media_user_id`, `library_id`, `item_type`, `client`, `device_id`,
`play_method`, `source`, `import_source`, `started_after`, `started_before`,
and `q`. Title search is an ASCII case-insensitive literal substring of
`item_name` or `series_name`; other scripts match exactly. Search is limited
to 128 UTF-8 bytes. A catalog assignment can satisfy `library_id` when an
older watch has no stored library.

`GET /api/v1/media-servers/{id}/users/{media_user_id}/timeline` uses the same
permission. It groups consecutive watches of the same `item_id` when their
start times are within `gap_seconds`. The gap defaults to six hours and accepts
1 second through 7 days. Each result contains the first start, last end, play
count, summed active seconds, and item identity. A page groups no more than 500
raw watches. A group that crosses that fetch boundary continues on the next
page.

An account with `admin.settings` can read or atomically replace a server's
collection exclusions at `GET` or `PUT
/api/v1/media-servers/{id}/exclusions`. The `PUT` body contains complete
`excluded_media_user_ids` and `excluded_library_ids` arrays. Each identifier
is limited to 128 UTF-8 bytes. The arrays may contain no more than 500 entries
in total. Replacement does not delete existing watches or catalog rows.

The collector drops sessions for excluded users, items resolved to an excluded
library, and items whose library cannot be resolved while the server has at
least one excluded library. Every history-import source counts excluded-user,
excluded-library, and policy-relevant unresolved-library rows as skipped.
Catalog synchronization does not walk an excluded library. Per-server settings
use a one-minute in-process lease-and-refresh cache, which is refreshed
immediately after replacement.

Visibility is also enforced on existing data. Playback-now,
playback-history, watch-position access, every administrator and own-user
statistics query, catalog library/item/detail/history/recent/genre/stale
reads, catalog activity rollups, the activity list, and per-user timelines
exclude matching users and libraries. Changing exclusions rebuilds stored
catalog rollups and clears the statistics and catalog result caches.

### Importing history

An account with `admin.settings` can create and monitor import jobs through
`POST /api/v1/imports`, `GET /api/v1/imports`, `GET /api/v1/imports/{id}`,
and `POST /api/v1/imports/{id}/cancel`. Only one pending or running job for a
media server and source is allowed. The worker processes one job at a time per
Bloom process in batches of 500 and checkpoints each batch with its cursor and
counters. A stopped process resumes after the last committed batch.

When an imported row has no library identifier, exclusion filtering first
checks Bloom's catalog and then asks the media server to resolve the item. A
resolved excluded library is skipped. If both lookups miss while the server has
at least one excluded library, Bloom fails closed: it skips the row and
increments the job's bounded `unresolved_library` counter once. The counter
means "rows skipped because their library could not be resolved while libraries
are excluded." With no excluded libraries, unresolved rows are imported as
before. Resolution results, including misses, are cached only for the current
500-row batch.

The `playback_reporting` source reads Jellyfin's Playback Reporting plugin by
ascending database `rowid`. The plugin must be installed on the selected
server. Create this job with JSON containing `media_server_id` and
`source: "playback_reporting"`.

The `jellyfin_userdata` source enumerates every user reported by Jellyfin in
media-user ID order, including users without a Bloom account, then walks the
synced catalog for each user. It checkpoints the user and item cursors and
creates at most one synthetic watch per user and item, at `LastPlayedDate`,
when `PlayCount` is positive. The synthetic watch records the Jellyfin user ID
and name. Its watch time is zero because Jellyfin user data does not retain
session duration. A Playback Reporting, Jellystat-style, Bloom export, or
collected watch for the same user and item supersedes the synthetic watch
regardless of commit order.

The `jellystat` source accepts a Jellystat backup through the same multipart
route. In Jellystat, open **Settings → Backup**, create or select a backup, and
download the `.jsonl` file. Upload it with `media_server_id`,
`source: jellystat`, and the `file` part. Bloom verifies that the first line is
a Jellystat table marker before creating the job. It streams the
database-staged upload twice: a first pass builds only the user, item, and
episode lookups needed by playback rows, and a second pass imports activity.
The combined lookups are limited to 250,000 entries and 64 MiB of stored lookup
text. No lookup data is written to disk. A backup is limited to 1,000,000 JSONL
rows, and each JSON payload is limited to 64 KiB. Malformed and non-activity
lines increment the skipped counter.

Jellystat imports carry over user and item identity, movie or episode names,
series and season metadata when available, runtime, active seconds, play
method, client, device, and the activity finalisation time. They do not carry
Jellystat aggregate counters, library assignment, IP addresses, raw play-state
JSON, media-stream JSON, transcoding JSON, segments, or position samples.
Jellystat stores `ActivityDateInserted` when a record is finalised or last
merged, not when playback starts. Bloom therefore approximates `started_at` as
that timestamp minus `PlaybackDuration`; pauses and merge gaps cannot be
reconstructed. Rows that Jellystat imported from Playback Reporting are kept
and cross-deduplicated against a direct Playback Reporting import by the
separate upstream row ID. Every Jellystat watch keeps its own activity ID as
`import_record_id`; imported plugin rows also carry the plugin row ID as
`import_origin_record_id`.
Unknown users and items are retained with their source IDs as display names.
Playback Reporting and Jellystat activity durations share a plausible maximum
of seven days per playback activity; longer rows are skipped.

The `bloom_export` source accepts a multipart
`file` part containing either a Bloom `.zip` export or the previous JSONL shape,
plus `media_server_id` and `source: bloom_export` fields. Bloom detects ZIP by
content rather than filename. Uploads are limited to 256 MiB, and only one
upload is staged at a time per process.

An export archive larger than the 256 MiB upload cap cannot be re-imported
through the browser yet. Its trailing `summary.json` reports the uncompressed
`watches.jsonl` and `imports.jsonl` byte sizes so an operator can check the
payload size before attempting an import.

Uploads are held in the database as fixed 1 MiB chunks until the job completes,
fails, or is cancelled. The terminal state change and chunk deletion use the
same transaction. Each worker scan also deletes a bounded batch of unlinked
uploads older than `BLOOM_IMPORT_TRANSFER_TIMEOUT`. Bloom does not create an
import data directory or stage uploads on the pod filesystem.

`GET /api/v1/exports/watches` streams one `application/zip` download directly
from bounded database pages. The optional `media_server_id` filter remains;
there are no `cursor` or `limit` parameters. The archive contains
`manifest.json`, every watch in newest-first `(started_at, id)` order as
`watches.jsonl`, import-job provenance as `imports.jsonl`, and record counts and
entry byte sizes in the trailing `summary.json`. Import that ZIP on another
Bloom instance to move watch history between SQLite and PostgreSQL. Bloom ZIP
imports require both `manifest.json` and `summary.json`. The importer verifies
that the number of watch records read matches the count in `summary.json`. The
previous JSONL shape remains accepted for compatibility. Re-import is idempotent
because the original watch ID is the source record ID. Each watch line includes
`import_origin_record_id` when upstream provenance is present; imports also
accept older watch lines without it. A transfer that does not
form a complete ZIP archive is truncated and must be retried.

Bloom's collected watch wins when an imported and collected watch have the same
media server, media user, and item and start within
`BLOOM_PLAYBACK_RESUME_WINDOW`. This rule applies whether collection or import
commits first. Bloom exports restore the watch snapshot, including device,
series, library, episode, final-position, stream, and end-time fields. Imported
watches do not restore segments or position samples. After a valid ascending
row ID, Playback Reporting rows with malformed dates, durations, identity, or
text fields are skipped without blocking later rows.

### Library catalog

Bloom keeps a type-agnostic local catalog for every registered media server.
The catalog stores Jellyfin's item type, hierarchy, display metadata, genres,
runtime, dates, rating, and primary image tag. Missing items are archived only
after a complete successful walk and a final Jellyfin lookup, in batches of at
most 100 identifiers, confirms that each item is absent. Bloom never deletes
them during sync.

The catalog worker performs a bounded full walk when Bloom starts and every
`BLOOM_LIBRARY_SYNC_INTERVAL` thereafter. The default is `1h`; accepted values
range from `5m` through `24h`. An account with `admin.settings` can request an
immediate walk with
`POST /api/v1/media-servers/{id}/catalog/sync`. The route returns `202`, `404`
for an unknown server, and `409` when that server already has a pending or
running walk. A shutdown leaves the leased row and checkpoint intact. Lease
recovery replays the current library from its first page. Items are archived
only when every library completes under the same sync generation.

Accounts with `stats.read.all` can read catalog coverage, paged library items,
item details and descendant history, recently added items, genre totals, and
stale items through the `/api/v1/media-servers/{id}/libraries` and
`/api/v1/media-servers/{id}/items` routes documented in `api/openapi.yaml`.
Item, history, and stale routes use bounded keyset pagination: pass `limit`,
then pass the returned `next_cursor` as `cursor`; these routes do not accept an
offset. Each cursor is bound to its server, library or item, filters, sort, and
fixed time window. Reusing it with another query returns `422`.
Descending date sorts place unknown dates last, while stale results place
never-played items first. Genre totals are aggregated from the normalized
catalog genre index rather than by loading the library into memory. Item-list
activity sorts use maintained all-time rollups. The optional `days` window
limits the play totals returned for each item without changing that ordering.

An account with `stats.read.all` can also read the statistics dashboards at
`GET /api/v1/stats/overview`, `/daily`, `/patterns`, `/titles`, `/users`,
`/libraries`, and `/users/{media_server_id}/{media_user_id}`. One recorded watch row is one play.
Watch time is the row's recorded `active_seconds`. A watch belongs to the
half-open window when `started_at` is at or after the window start and before
the response's end time. The `days` parameter defaults to 30 and accepts 1
through 365 rolling 24-hour days. The optional `media_server_id` limits the SQL
query to one server. Every report also accepts `library_id` when
`media_server_id` is present. The `/libraries` report returns at most 50 ranked
library rows. Watches whose library is unresolved appear in one row with empty
`library_id` and `library_name` values.

Title rows include `unique_users`, the distinct media-user count for that
title. `GET /api/v1/stats/titles` accepts `order=plays|unique_users` and
defaults to `plays`, so clients can request most-viewed and most-popular title
rankings separately. The selected order is part of the statistics cache key.

Every response states its resolved window and time zone. `tz` defaults to UTC
and accepts an IANA time-zone name of at most 64 bytes; the host-dependent
`Local` value is rejected. Bloom converts
`started_at` to that zone in Go for daily, weekday, and hour buckets; totals,
rankings, and breakdowns remain portable SQL on both database engines. A
bounded in-process LRU caches complete results for 30 seconds by default. A
cached window end is rounded down to its cache-TTL boundary; with caching
disabled, it is rounded down to the second.
`BLOOM_STATS_CACHE_TTL=0` disables it. Cache expiry is the invalidation policy,
so a dashboard can trail a newly recorded watch by at most the configured TTL.

Bloom links an account to at most one media user on each server. A link is
created from a signed-in invite acceptance, an exact username match during an
own-data request, or an administrator assignment. Anonymous invite acceptance
does not create an account link. An invite acceptance carrying an invalid,
expired, deleted-account, or disabled-account session is rejected with `401`;
clear the cookie or sign in again before accepting. A media user can belong to
at most one Bloom account on a server.

An account with `stats.read.own` can list its links with
`GET /api/v1/me/media-users` and read its per-user dashboard with
`GET /api/v1/stats/me`. Both routes try an exact username match on each
unlinked server before responding. The statistics route accepts the same
`days`, `tz`, `media_server_id`, and server-scoped `library_id` parameters as
the administrator reports. Without `media_server_id`, it uses the account's
only link or the first link ordered by media-server name. The response window
identifies the selected `media_server_id` and `media_user_id`.

An account with `admin.settings` can inspect links with
`GET /api/v1/accounts/{id}/media-users`, assign one with
`PUT /api/v1/accounts/{id}/media-users/{media_server_id}`, and remove one with
`DELETE` on that item route. Removal suppresses automatic username matching;
`PUT` relinks the user and clears that suppression. The list hides suppressed
links unless `include_suppressed=true` is supplied. The `PUT` body is
`{"media_user_id":"..."}`.

The Statistics page shows the library ranking and filters every report by
one library. Accounts with `stats.read.own` see their own dashboard under
My statistics, one per media-server user linked to the account.

The web UI shows the same reports under Statistics in the administration
area: totals, most-watched titles, most active people, breakdowns, plays and
watch time per day, and plays by weekday and hour in the viewer's own time
zone, with each person linking to their own dashboard. Every chart offers its
numbers as a table.

### Invites

An account with `users.invite` can create an invite with
`POST /api/v1/invites`. Each invite targets one registered media server. It can
have an optional future expiry, a limit of 1 through 1000 uses, and either all
libraries or an explicit selection from that server. The create response is the
only response that contains the 26-character invite code. Copy and share its
`accept_path` immediately. Bloom stores only a SHA-256 digest of the code and
cannot recover it later.

`GET /api/v1/invites/servers` uses the same cursor and page-size bounds as the
administrative media-server list, but returns only each server's `id` and
`name`. It requires `users.invite`, not `admin.settings`, and never returns a
base URL, credential, or capability detail.

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
Jellyfin user. If that update fails, Bloom deletes the user only when the create
response confirmed that this request created it, then returns an upstream
failure. A malformed, non-canonical, used, expired, exhausted, revoked, or
unknown code performs the same fixed database work and returns the same public
not-found response. A username that Jellyfin rejects as taken or invalid
returns a conflict response.

Bloom records any unresolved cleanup or ambiguous user creation as a durable
provisioning failure. Failure persistence uses one stable identifier across a
transaction and its bounded fallback, so retrying an unknown commit outcome
does not duplicate the obligation. A supervised worker claims at most 50 due
rows per pass with expiring leases. It retries deletion or policy completion
only for a media user whose create response confirmed ownership, with capped
exponential backoff. An ambiguous create resolved only by username is terminal
for manual resolution; Bloom never deletes that user or changes its policy.
Every worker database operation has its own timeout. A provisioner panic is
reduced to a generic retry or terminal outcome after releasing the provisioner
and operation context. The eighth other failed attempt becomes terminal. An
account with `admin.settings` can
inspect safe failure fields at
`GET /api/v1/invites/provisioning-failures` and can auditably dismiss a row with
`DELETE /api/v1/invites/provisioning-failures/{id}`. Dismissal returns
`409 invite_provisioning_failure_leased` while a worker lease is live. A process
crash leaves the lease to expire so another pass can reclaim the row.
`bloom_invite_provisioning_backlog` reports unresolved rows, and
`bloom_invite_provisioning_reconciliations_total{reason,outcome}` counts bounded
worker outcomes without usernames, invite codes, passwords, or error text.

The public invite routes are rate limited per client address and per code
with the same `BLOOM_LOGIN_RATE_*` settings that bound sign-in attempts, so a
code cannot be guessed by enumeration.

### Requests

Bloom starts without a metadata credential. An account with `admin.settings`
stores TMDB's v4 API Read Access Token with
`PUT /api/v1/metadata/providers/tmdb/key`. TMDB shows this value under
**Settings**, **API**, **API Read Access Token**. Bloom verifies the token,
encrypts it under `BLOOM_SECRET_KEY`, never returns it, and exposes only its
valid presence through `GET` on the same route. A previously stored v3 API key
is treated as not configured and must be replaced with the API Read Access
Token. Removing the token disables new metadata searches and returns
`metadata_not_configured` until another token is stored.

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

Accounts with `requests.create` or `requests.read.own` can search TMDB and
browse the cursor-paged discover rows at `/api/v1/metadata/discover/`: weekly
trending movies and series from TMDB `/trending/all/week`, popular movies from
`/movie/popular`, popular series from `/tv/popular`, upcoming movies from
`/movie/upcoming`, and upcoming series from `/tv/on_the_air`. TMDB does not
publish a direct upcoming-series list, so its on-the-air list is the closest
equivalent. Bloom exposes at most 20 TMDB pages with 20 titles per page and
adds the signed-in account's latest request state to every title. Movie and
series genre lists are available from `GET /api/v1/metadata/genres?kind=...`
and are cached for one day; discover pages use the existing metadata cache
TTL.
If a metadata error reports the `unreachable` reason, the Bloom server cannot
reach `api.themoviedb.org`; a firewall or network policy is the usual cause.
Accounts with `requests.create` can open movie or series details and submit a
movie or selected series seasons to `POST /api/v1/requests`.
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
first. Every request response includes `requester_account_id` and the account's
current display name in `requester_username`. The username is empty if the
account no longer exists; Bloom does not copy usernames into request records.

### Notifications

An account with `admin.settings` can register webhook, Discord, and email
channels through `/api/v1/notification-channels`. Bloom sends a test before it
stores a new or enabled replacement configuration. Disabling a channel skips
the connectivity test and terminally fails pending deliveries that do not have
a live lease. A delivery already being sent records its natural outcome.
Deleting a channel hides and disables it immediately, then removes its rows
after any live lease finishes or expires. Delivery claims and tombstone reaping
serialize on the channel row, and eligibility is re-checked after that lock.
Webhook deliveries are JSON request
event payloads signed with HMAC-SHA256 in `X-Bloom-Signature`; Discord uses one
embed; email uses plain text over authenticated STARTTLS or implicit TLS. A
channel may opt in to private
destination ranges. Loopback, unspecified, multicast, link-local, and
cloud-metadata destinations remain denied even with that opt-in, and redirects
remain disabled.

Each channel subscribes to one or more request events: `created`, `approved`,
`declined`, `dispatched`, `available`, or `failed`. Credentials and full
webhook URLs are encrypted under `BLOOM_SECRET_KEY`. Read routes return only
credential-presence flags. Creating a channel requires its kind-specific
credentials: a webhook URL and shared secret, a Discord webhook URL, or an SMTP
password. Updating a channel without those credentials retains the stored
values. The `/test` route sends immediately and records an audit event.

Subject and body templates may use only `Title`, `Kind`, `Status`, `Requester`,
`Actor`, `Reason`, `RequestID`, and `OccurredAt`. Each template is limited to
4096 bytes and is parsed before storage. Bloom supplies defaults for every
event. Every Discord embed title, description, field name, and field value is
Markdown-escaped before provider limits are applied. Email and generic webhooks
receive plain text or the structured event payload.

Every request create and state transition commits a durable event in the same
database transaction as the request mutation. The in-process bus only wakes a
supervised worker, which enriches unfanned events and atomically creates one
delivery row for every enabled subscribed channel. Missing account display
names become empty payload fields and never discard an event. The request path
performs no notification network call. The worker claims deliveries with
expiring leases and retries transient failures with capped exponential backoff
for at most eight attempts. An expired eighth claim is terminally failed so it
cannot block later work. Three consecutive terminal failures mark a channel
degraded; the next successful delivery clears that state. The `/deliveries`
route exposes safe recent status, attempt, and timestamp fields. Delivery
counters and outbox depth are exported as metrics.
Sent and terminally failed rows, followed by their fully fanned event rows, are
pruned in bounded batches after `BLOOM_NOTIFY_RETENTION`.

Delivery is at-least-once. A process failure after an external provider accepts
a message but before Bloom records completion can cause the same delivery to be
sent again. Webhooks include the delivery UUID in the signed `delivery_id`
field and in both `X-Bloom-Delivery-ID` and `Idempotency-Key`; receivers should
deduplicate on that value. Email sets `Message-ID: <delivery-id@bloom>` for the
same purpose. Discord webhooks do not receive an idempotency field. Discord
embed titles, descriptions, fields, and total text are truncated rune-safely
with an ellipsis to the provider's limits.

The Playback page shows each watch's stream (resolution, codecs, bitrate)
and links to its sample series with the transcode reasons.

The web UI covers the same flow. Administrators register Radarr and Sonarr
instances under Download managers, store the TMDB API Read Access Token, build
profiles from an instance's options under Request settings, decide pending
requests, retry failed ones, and read queue progress under Requests in the
administration area. Anyone who may request media searches, browses
posters, picks seasons, and follows their own requests under Requests in the
main navigation. Posters load from `image.tmdb.org`, the only third-party
origin the page allows for images.
Administrators register webhook, Discord, and email channels under
Notifications, choose the request events each one receives, send a test
message, and read each channel's delivery log with its retries and last
error.

### Back up the master secret

Back up `BLOOM_SECRET_KEY` with the database and keep it stable across restarts,
replicas, restores, and upgrades. The value derives both the credential key and
the installation-specific Jellyfin device identifier. Bloom stores a derived
key identifier in each encrypted envelope, so starting with a different value
reports a wrong-key failure instead of treating every credential as corrupt.

If the key is lost or changed, restore the original key. If it cannot be
restored, delete and re-register every media server with its API key. There
is no credential re-encryption command. Do not attempt rotation by changing
the environment value alone.

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
| `BLOOM_IMPORT_TRANSFER_TIMEOUT` | duration | no | `10m` | no | Per-request deadline for a Bloom export upload or streamed watch export. Valid range: `30s`-`2h`. |
| `BLOOM_PUBLIC_URL` | HTTP(S) origin | no | `http://localhost:8080` | no | Externally visible Bloom origin used for callback-error redirects and OIDC redirect validation. Limited to 2,048 valid UTF-8 bytes with no control characters. |
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
| `BLOOM_SESSION_LIFETIME` | duration | no | `720h` | no | Absolute lifetime of a browser session (30 days). Every session ends when it reaches this age, active or not. |
| `BLOOM_SESSION_IDLE_TIMEOUT` | duration | no | `168h` | no | End a browser session after this period without a request (7 days). Each request extends it. Must not exceed the lifetime. |
| `BLOOM_LOGIN_RATE_REFILL_INTERVAL` | duration | no | `1m` | no | Per-IP and per-username login buckets, and the per-IP and per-code buckets on the public invite routes, regain one attempt per interval. |
| `BLOOM_LOGIN_RATE_BURST` | int | no | `5` | no | Maximum immediately available attempts in each login bucket and each public invite bucket. |
| `BLOOM_LOGIN_RATE_MAX_KEYS` | int | no | `10000` | no | Bound on rate-limit entries held in memory, for the login buckets and separately for the public invite buckets. Valid range: 2-100000. |
| `BLOOM_LOGIN_MAX_CONCURRENT` | int | no | `4` | no | Maximum concurrent Argon2id password verifications. Excess attempts fail fast with `503`. Valid range: 1-64. |
| `BLOOM_TRUSTED_PROXY_CIDRS` | comma-separated CIDRs | no | — | no | Trust `X-Forwarded-For` only when the direct peer is in this allowlist. Empty disables forwarded addresses. |
| `BLOOM_OIDC_ENABLED` | bool | no | `false` | no | Enable one generic OpenID Connect provider. Discovery runs at startup and startup fails if it cannot complete. |
| `BLOOM_OIDC_DISPLAY_NAME` | string | no | `OpenID Connect` | no | Sign-in method label returned by `GET /api/v1/auth/providers`. 1-80 bytes. |
| `BLOOM_OIDC_ISSUER_URL` | URL | OIDC: yes | — | no | Canonical OIDC issuer without a query or fragment, limited to 2,048 bytes. HTTPS is required except loopback HTTP with the development flag. |
| `BLOOM_OIDC_CLIENT_ID` | string | OIDC: yes | — | no | OAuth client identifier, limited to 512 valid UTF-8 bytes with no control characters. |
| `BLOOM_OIDC_CLIENT_SECRET` | string | OIDC: yes | — | **yes** | Environment-only OAuth client secret, limited to 4,096 valid UTF-8 bytes with no control characters. It is wrapped in `config.Secret`, never rendered or logged, and never accepted as a flag. Rotate it with a rolling restart. |
| `BLOOM_OIDC_REDIRECT_URL` | URL | OIDC: yes | — | no | Exact callback URL: `BLOOM_PUBLIC_URL` plus `/api/v1/auth/oidc/callback`, with no query or fragment. HTTPS is required except loopback HTTP in explicit development mode. |
| `BLOOM_OIDC_SCOPES` | space-separated strings | no | `openid profile email` | no | Requested scopes. `openid` is required; at most 16 values of up to 512 valid UTF-8 bytes each are accepted, with no control characters. |
| `BLOOM_OIDC_USERNAME_CLAIM` | string | no | `preferred_username` | no | Verified ID-token claim used to derive the canonical Bloom username. Limited to 512 valid UTF-8 bytes with no control characters. |
| `BLOOM_OIDC_ROLE_CLAIM` | string | no | — | no | Optional verified claim containing one role value or an array of role values. Limited to 512 valid UTF-8 bytes with no control characters. |
| `BLOOM_OIDC_ROLE_MAP` | comma-separated mappings | no | — | no | Up to 64 claim-value-to-Bloom-role mappings such as `bloom-admins=owner,bloom-users=member`. Claim keys are limited to 512 valid UTF-8 bytes with no control characters. Requires a role claim. |
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
| `BLOOM_IMPORT_WORKER_INTERVAL` | duration | no | `10s` | no | Interval between history-import job scans. Valid range: `1s`-`1h`. |
| `BLOOM_LIBRARY_SYNC_INTERVAL` | duration | no | `1h` | no | Interval between bounded full library-catalog walks. Valid range: `5m`-`24h`. |
| `BLOOM_STATS_CACHE_TTL` | duration | no | `30s` | no | TTL for the bounded in-process statistics result cache. Must be at least `0`; `0` disables caching. |
| `BLOOM_REQUEST_AVAILABILITY_SOURCE` | `media_server` \| `download_manager` | no | `media_server` | no | Authority used to mark processing requests available. Queue progress is never the authority. |
| `BLOOM_REQUEST_AVAILABILITY_INTERVAL` | duration | no | `5m` | no | Poll interval while processing requests exist. Valid range: `1m`-`24h`. |
| `BLOOM_NOTIFY_RETENTION` | duration | no | `720h` | no | Retention for sent and terminally failed notification delivery rows. Must be positive. |
| `BLOOM_NOTIFY_WORKER_INTERVAL` | duration | no | `5s` | no | Interval between notification outbox scans. Valid range: `1s`-`1h`. |
| `BLOOM_INVITE_RECONCILE_INTERVAL` | duration | no | `5m` | no | Interval between invite provisioning reconciliation passes. Valid range: `1s`-`1h`. |
| `BLOOM_INVITE_STORE_TIMEOUT` | duration | no | `5s` | no | Timeout for each invite reconciliation database operation. Valid range: `100ms`-`30s`. |
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
Migration `00018_invite_provisioning_reconciliation` adds leased retry state,
attempt and error bounds, terminal state, media-user ownership provenance, and
optional accepting-account identity to provisioning failures on both engines.
Apply it before running the reconciliation worker.

Migration `00011_playback_collection` adds watches, active-time segments, and
bounded position samples on both engines. Apply it before enabling this binary.
Deleting a media-server registration first stops and awaits its collector, then
cascades to all three playback tables.

Migration `00014_stats_started_index` adds the unfiltered watch-start index used
by bounded statistics windows on both engines. Its down migration removes only
that index.

Migration `00016_watch_libraries` adds the library identity columns and the
server-library-start index used by filtered statistics on both engines. Its
down migration removes the index and both columns.

Migration `00017_account_media_users` adds account-to-media-user links on both
engines, including administrator suppression state. Deleting an account or
media server cascades to its links. Its down migration removes the link table.

Migration `00019_stream_details` adds nullable, bounded stream-detail columns
to watches and watch-position samples, plus the position-sample transition
marker used for retention, on both engines. Apply it before enabling
stream-detail collection. Its down migration removes those columns and loses
the recorded stream-detail series.

Migration `00020_watch_runtime` adds the nullable, bounded `runtime_ms` column
to watches on both engines. Its down migration removes the column and loses
the recorded runtimes.

Migration `00022_history_imports` adds leased import jobs, imported-watch
provenance, and the per-server/source-record uniqueness index on both engines.
Its down migration removes import jobs and provenance columns but leaves watch
rows that were imported before rollback.

Migration `00023_import_uploads` adds fixed-size database upload chunks and
orphan-cleanup indexes on both engines. Its down migration removes staged
uploads; terminal jobs and imported watches are unaffected.

Migration `00024_library_catalog` adds the type-agnostic catalog, normalized
item genres, durable per-server sync state, indexed per-item playback rollups,
keyset-listing indexes, and nullable watch `series_id` on both engines. It also
admits the `jellyfin_userdata` import source and changes that source's
imported-watch identity to include the media user. Its down migration removes
catalog data and the new provenance while preserving other watch rows.

Migration `00025_jellystat_import_source` widens import provenance on both
engines for Jellystat backups. Rolling it down removes Jellystat import jobs,
their staged uploads, and watches imported from Jellystat before restoring the
previous source constraints, so preserve a database backup before rollback.

Migration `00026_watch_import_origin` adds nullable, 128-byte
`import_origin_record_id` watch provenance and a partial server/origin index on
both engines. It backfills the Playback Reporting row ID from legacy Jellystat
`plugin:` markers without changing `import_record_id`; the old importer did not
retain the original Jellystat activity ID, so that value cannot be recovered.
Rolling the migration down removes the origin column and index while preserving
the watches and their existing record IDs.

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

### Error handling

Every JSON API failure uses the `ErrorResponse` envelope with a stable `code`,
a safe `message`, and a `request_id` for log correlation. Failures may include
`reason`, which is omitted when empty. Classified media-server,
download-manager, and metadata-provider failures populate it.
Metadata uses `unreachable` for connection, DNS, or timeout failures before a
response; `unauthorized` for TMDB 401; `malformed` for an unusable response;
and `unavailable` for TMDB 429 or 5xx responses after bounded retries.
Media-server and download-manager failures use `unreachable` for
connection, timeout, DNS, refused-destination, and retryable-status failures;
`unauthorized` for upstream 401 or 403 responses; `not_found` for an upstream
404; and `malformed` for unexpected statuses, redirects, or responses that do
not match the upstream contract. Other error codes omit `reason`. Upstream
response bodies, hostnames, and credentials are never included in the envelope.

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
