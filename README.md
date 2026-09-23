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

Apply the database migrations before starting Bloom. Then create the first
local account with the `create-admin` command. Supply the password through the
environment for automation:

```bash
go run ./cmd/bloom -migrate
export BLOOM_BOOTSTRAP_PASSWORD='choose-a-long-unique-password'
go run ./cmd/bloom create-admin --username owner
unset BLOOM_BOOTSTRAP_PASSWORD
```

When `BLOOM_BOOTSTRAP_PASSWORD` is unset and the command runs in a terminal,
Bloom prompts for the password without echoing it. The password is never
accepted as a command-line flag. Run the command only once for each bootstrap
account; an existing username is refused. The command assigns the built-in
`owner` role atomically with account creation. Usernames are canonicalized with
the PRECIS UsernameCaseMapped profile. They must contain 3 through 64 letters,
digits, `.`, `_`, or `-`, and cannot start or end with a separator. New local
passwords must contain 15 through 1024 Unicode characters and must not exceed
4096 UTF-8 bytes. Bloom
rejects malformed UTF-8 and passwords in its embedded offline common-password
denylist. It applies no character-composition rules.

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

### Running against PostgreSQL

```bash
export BLOOM_DB_DRIVER=postgres
export BLOOM_DB_DSN='postgres://bloom:bloom@localhost:5432/bloom?sslmode=disable'
go run ./cmd/bloom -migrate && go run ./cmd/bloom
```

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
| `BLOOM_DB_DRIVER` | `sqlite` \| `postgres` | no | `sqlite` | no | Database engine. Anything else fails startup. |
| `BLOOM_DB_DSN` | string | postgres: yes | sqlite: `file:bloom.db?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)` | yes | Data source name. Required when the driver is `postgres`. For compatibility, startup supplies `_pragma=foreign_keys(1)` when a configured SQLite DSN omits a foreign-key pragma; an explicit disable still fails startup. |
| `BLOOM_DB_MAX_OPEN_CONNS` | int | no | `25` | no | Pool cap on open connections. |
| `BLOOM_DB_MAX_IDLE_CONNS` | int | no | `25` | no | Pool idle floor; must be `<=` max open. |
| `BLOOM_DB_CONN_MAX_LIFETIME` | duration | no | `30m` | no | Bound on connection age. |
| `BLOOM_DB_CONN_MAX_IDLE_TIME` | duration | no | `5m` | no | Reap idle connections after this long. |
| `BLOOM_DB_MIGRATE_ON_STARTUP` | bool | no | `false` | no | Apply embedded migrations before serving. Single-writer convenience; production uses `-migrate`. |
| `BLOOM_SECRET_KEY` | string | **yes** | — | **yes** | Master secret (at least 32 bytes) that derives the at-rest encryption key for stored credentials ([ADR 0006](decisions/0006-auth-and-authorization-model.md)). Never logged. |
| `BLOOM_BOOTSTRAP_PASSWORD` | string | create-admin: conditional | — | **yes** | Password read only by `create-admin`. When unset, an interactive terminal prompt is required. |
| `BLOOM_SESSION_COOKIE_SECURE` | bool | no | `true` | no | Set the `Secure` session-cookie flag. Disable only for plaintext local development. |
| `BLOOM_SESSION_LIFETIME` | duration | no | `24h` | no | Absolute lifetime of a browser session. |
| `BLOOM_SESSION_IDLE_TIMEOUT` | duration | no | `30m` | no | Invalidate a browser session after this period of inactivity. Must not exceed the lifetime. |
| `BLOOM_LOGIN_RATE_REFILL_INTERVAL` | duration | no | `1m` | no | Per-IP and per-username login buckets regain one attempt per interval. |
| `BLOOM_LOGIN_RATE_BURST` | int | no | `5` | no | Maximum immediately available login attempts in each IP or username bucket. |
| `BLOOM_LOGIN_RATE_MAX_KEYS` | int | no | `10000` | no | Bound on combined IP and username rate-limit entries held in memory. Valid range: 2-100000. |
| `BLOOM_LOGIN_MAX_CONCURRENT` | int | no | `4` | no | Maximum concurrent Argon2id password verifications. Excess attempts fail fast with `503`. Valid range: 1-64. |
| `BLOOM_TRUSTED_PROXY_CIDRS` | comma-separated CIDRs | no | — | no | Trust `X-Forwarded-For` only when the direct peer is in this allowlist. Empty disables forwarded addresses. |
| `BLOOM_LOG_LEVEL` | string | no | `info` | no | `slog` level: `debug`, `info`, `warn`, `error`. |
| `BLOOM_LOG_FORMAT` | `json` \| `text` | no | `json` | no | Log record format. |
| `BLOOM_OTLP_ENDPOINT` | string | no | — | no | OTLP/HTTP trace collector `host:port`. Empty disables span export. |
| `BLOOM_OTLP_INSECURE` | bool | no | `false` | no | Send spans over plaintext HTTP. |
| `BLOOM_TRACE_SAMPLE_RATIO` | float | no | `1.0` | no | Head-based sampling ratio in `[0,1]`. |
| `BLOOM_SHUTDOWN_GRACE` | duration | no | `15s` | no | Drain budget on `SIGTERM`; keep it under the platform's termination grace. |

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

After a new image passes the smoke test, or after a rerun reuses the immutable
digest, the workflow opens a pull request in `bonztm/homelab`. That pull request
pins both the `bloom` container and the `migrate` init container in
`apps/internal/bloom/deployment.yaml` to the same `main-<commit>` tag and image
digest. A rerun updates the existing pull request on the workflow-owned
`chore/bloom-main` branch. Each run recreates that branch from the current
homelab `main`, renders the Bloom manifests, and skips the commit when the
manifest is already current. The branch push uses an exact-OID lease. After each
push, the workflow re-reads homelab `main`; if it moved, the workflow recreates
the branch from the newer base, reapplies and renders the change, and retries up
to three times before failing. It opens or updates the pull request only after a
push whose base still matches the re-read `main`. If `main` changes after that
check, GitHub may mark the pull request behind or conflicting; the next Bloom run
recreates the branch from the new base. If the deployment file does not exist
yet, the workflow skips the pull request without failing the image build.

Pushing a `v<major>.<minor>.<patch>` tag keeps the release flow separate from
deployment. A `v1.2.3` release publishes immutable `v1.2.3` and `1.2.3` tags.
It moves `1.2` and `latest` only when `1.2.3` is newer than the version that the
alias already references. A prerelease such as `v1.2.3-rc.1` publishes only
that exact tag. Invalid release tags fail before an image is pushed. Manual runs
accept only `main` or an actual valid release tag; a similarly named branch is
rejected. Tagged releases do not open homelab pull requests. Image runs share a
serialized queue so promotion and the reusable deployment branch cannot race.

[GitHub creates a newly published container package as private by
default](https://docs.github.com/en/packages/learn-github-packages/configuring-a-packages-access-control-and-visibility#about-visibility-of-packages).
After the first image is published, open the
[`bonztm/bloom` package settings](https://github.com/users/BonzTM/packages/container/bloom/settings)
and change its visibility to public. This is a one-time bootstrap step. The
deploy job pulls the promoted digest without logging in before it opens or
updates a pull request. It fails with an actionable error while anonymous pulls
are disabled. If the first deploy job reaches that check before the visibility
change, make the package public and rerun the failed workflow.

The repository also needs one secret before the first main deployment:

1. Create a fine-grained personal access token for the `bonztm/homelab`
   repository.
2. Grant the token **Contents: Read and write** and **Pull requests: Read and
   write** repository permissions. Do not grant additional repository access or
   permissions.
3. Add the token to the Bloom repository as an Actions secret named
   `HOMELAB_DEPLOY_TOKEN`.

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
