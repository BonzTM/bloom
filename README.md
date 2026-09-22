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

`make verify` is the single gate; it must pass before any change is considered done.
See [AGENTS.md](AGENTS.md) for the full contributor contract and verification bar.

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
| `BLOOM_DB_DSN` | string | postgres: yes | sqlite: `file:bloom.db?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)` | yes | Data source name. Required when the driver is `postgres`. |
| `BLOOM_DB_MAX_OPEN_CONNS` | int | no | `25` | no | Pool cap on open connections. |
| `BLOOM_DB_MAX_IDLE_CONNS` | int | no | `25` | no | Pool idle floor; must be `<=` max open. |
| `BLOOM_DB_CONN_MAX_LIFETIME` | duration | no | `30m` | no | Bound on connection age. |
| `BLOOM_DB_CONN_MAX_IDLE_TIME` | duration | no | `5m` | no | Reap idle connections after this long. |
| `BLOOM_DB_MIGRATE_ON_STARTUP` | bool | no | `false` | no | Apply embedded migrations before serving. Single-writer convenience; production uses `-migrate`. |
| `BLOOM_SECRET_KEY` | string | **yes** | — | **yes** | Master secret (at least 32 bytes) that derives the at-rest encryption key for stored credentials ([ADR 0006](decisions/0006-auth-and-authorization-model.md)). Never logged. |
| `BLOOM_LOG_LEVEL` | string | no | `info` | no | `slog` level: `debug`, `info`, `warn`, `error`. |
| `BLOOM_LOG_FORMAT` | `json` \| `text` | no | `json` | no | Log record format. |
| `BLOOM_OTLP_ENDPOINT` | string | no | — | no | OTLP/HTTP trace collector `host:port`. Empty disables span export. |
| `BLOOM_OTLP_INSECURE` | bool | no | `false` | no | Send spans over plaintext HTTP. |
| `BLOOM_TRACE_SAMPLE_RATIO` | float | no | `1.0` | no | Head-based sampling ratio in `[0,1]`. |
| `BLOOM_SHUTDOWN_GRACE` | duration | no | `15s` | no | Drain budget on `SIGTERM`; keep it under the platform's termination grace. |

The `-migrate` flag (no env key) applies the embedded goose migrations for the
configured engine and exits; it is how a deployment's migration Job invokes the
same image ahead of a rollout.

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
- `internal/core` — domain types and the interfaces consumed from the outside (`AccountStore`, `Clock`).
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

Bloom images are published at `ghcr.io/bonztm/bloom`. A merge to `main` builds
an amd64 image under a run-specific `candidate-<run>-<attempt>` tag. It tests
that image by digest and records its provenance before promoting the same digest
to the mutable `main` tag and the immutable `main-<full-commit>` tag. The build
starts only after the reusable CI workflow passes both `make verify` and the
PostgreSQL integration suite for the same commit. A rerun uses the commit's
timestamp as the image creation time. It fails instead of replacing an existing
immutable tag that points to another digest. The workflow also records an SBOM
for the published image.

A weekly cleanup deletes package versions older than seven days only when every
tag on that version starts with `candidate-`. GHCR stores tags on a shared
digest version, so promoted versions carry both their candidate tag and public
tags. Those promoted candidates remain in the registry because deleting their
package version would also delete the promoted image.

After the smoke test passes, the workflow opens a pull request in
`bonztm/homelab`. That pull request pins both the `bloom` container and the
`migrate` init container in `apps/internal/bloom/deployment.yaml` to the same
`main-<commit>` tag and image digest. A rerun updates the existing pull request
on the workflow-owned `chore/bloom-main` branch. Each run recreates that branch
from the current homelab `main`, renders the Bloom manifests, and skips the
commit when the manifest is already current. If homelab `main` changes before
the branch is pushed, the run fails and a rerun starts from the newer base. If
it changes after the push, GitHub may mark the pull request behind or
conflicting; the next Bloom run recreates the branch from the new base. If the
deployment file does not exist yet, the workflow skips the pull request without
failing the image build.

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
