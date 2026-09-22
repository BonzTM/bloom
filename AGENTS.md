# AGENTS.md - Bloom Contract

Fast-path contract for autonomous agents and reviewers working in this repository.
Read this file first. It is scoped to this project; the full engineering rules live
in the Go handbook (`$HOME/git/coding-handbook/golang/AGENTS.md`, adopted without
backend exceptions by [ADR 0001](decisions/0001-adopt-handbook-go-baseline.md)).
Where this file is silent, the handbook governs. This file may add constraints but
never weakens it.

## Purpose

- This project is a **service**: a single self-hosted binary that replaces the
  companion apps a Jellyfin operator runs (invites and user management, playback
  statistics, media discovery and requests). The WHAT is
  [docs/requirements.md](docs/requirements.md); the HOW is the handbook.
- Use this file for project-specific invariants, change routing, and the verification bar.
- Use [README.md](README.md) for project shape, config keys, and how to run it.
- Use [decisions/](decisions/) for every architectural choice and its rationale.
  ADRs are immutable once accepted; supersede, never edit.

## Repo-Wide Invariants

- **Single module**: one `go.mod` at the root, module `github.com/BonzTM/bloom`, `go 1.26.0`.
- **Layout**: `cmd/bloom` + `internal/` per the handbook; `api/` holds the OpenAPI contract; `web/` is the SPA with its own toolchain (ADR 0002).
- **Thin main**: `cmd/bloom/main.go` loads config, installs `signal.NotifyContext`, and calls `internal/runtime.Run`. No business logic.
- **Context discipline**: `ctx context.Context` is the first parameter for I/O and long-running work; never stored in a struct.
- **Errors**: wrap with `%w`, inspect with `errors.Is`/`errors.As`, log once at the boundary that can act. Every JSON error is the `httputil.ErrorResponse` envelope; 5xx bodies are opaque.
- **Logging**: `log/slog`; no global loggers in reusable packages. Audit events go to the dedicated `telemetry.AuditLogger` stream, never the access log.
- **Config**: loaded and validated in `internal/config`, fail-fast at startup; every key carries the `BLOOM_` prefix and is documented in [README.md](README.md) and `.env.example`. `BLOOM_SECRET_KEY` is a `config.Secret` and never renders.
- **Persistence (ADR 0004)**: `database/sql` + `sqlc`, two engines at full parity. Every migration is TWO files with the same name under `internal/db/migrations/{sqlite,postgres}/`. Queries in `internal/db/queries/*.sql` are portable SQL using `sqlc.arg()`; business logic is never in SQL. Timestamps cross into storage through `core.NormalizeTime` (UTC, microseconds). The parity tests in `internal/db` must pass on SQLite (always) and PostgreSQL (`-tags=integration`).
- **Pluggable boundaries**: every external system sits behind a 1-3 method interface defined in `internal/core` at the consumer; adapters are wired explicitly in `internal/runtime`. No `init()` registration.
- **Auth (ADR 0006)**: not yet implemented; the seam is `internal/api/http/auth.go`. Do not mount a state-changing or account-scoped route without wiring it. Never copy a JWT-in-cookie or global-API-key pattern.
- **Testing**: every behavior change ships with a test. Hand-rolled fakes, never mock frameworks. DB boundaries need the real engine suite, not a mock.
- **Dependencies**: stdlib first; every new module answers the handbook's Approval Questions in the PR; pure Go only (`CGO_ENABLED=0`); no committed `replace` directives.
- **No AI attribution** in any file, commit, or comment.

## Change Routing

Route the change; do not guess where code belongs.

| If you are changing... | Start in | Read first |
|---|---|---|
| process startup, wiring, shutdown | `cmd/bloom/`, `internal/runtime/` | handbook `foundations/project-setup.md`, `foundations/shared-constructs.md` |
| domain logic, entities, store interfaces | `internal/core/` | handbook `foundations/package-design.md` |
| HTTP handlers, middleware, routes, error codes | `internal/api/http/`, `api/openapi.yaml` | handbook `services/http-services.md`, `foundations/serialization.md` |
| the embedded SPA handler, security headers for HTML | `internal/api/web/` | ADR 0002, handbook `services/web-apps.md` |
| queries, schema, migrations, engine adapters | `internal/db/`, `sqlc.yaml` | ADR 0004, handbook `services/database.md`, `recipes/add-migration.md` |
| config keys or startup defaults | `internal/config/`, `.env.example`, [README.md](README.md) | handbook `foundations/configuration.md` |
| logging, metrics, traces, health, audit stream | `internal/telemetry/` | handbook `operations/observability.md`, `operations/security.md` |
| identity providers, sessions, API keys, permissions | `internal/api/http/auth.go`, `internal/core/` | ADR 0006, handbook `services/web-apps.md`, `operations/security.md` |
| a media server, metadata, download, or notification integration | new adapter package + interface in `internal/core/` | [docs/requirements.md](docs/requirements.md) pluggable seams, `docs/research/media-server-apis.md` |
| CI, build, release, containers | `.github/workflows/`, `Makefile`, `Dockerfile` | handbook `operations/ci-and-release.md`, `operations/deployment.md` |
| a new dependency or framework | the consuming package + `go.mod` | handbook `decisions/framework-selection.md` |
| a non-obvious architectural decision | `decisions/NNNN-*.md` | handbook `decisions/architecture-decision-records.md` |

## Working Norms

- Prefer small, reviewable changes; match the repo's current shape, do not invent new architecture.
- Do not bypass boundaries: handlers do not query the DB, repositories carry no transport logic, `main` holds no business rules.
- When adding a dependency, document why stdlib or an existing dependency is insufficient.
- Write the proving test before claiming success whenever practical.
- If verification fails, fix it or report it clearly. Do not claim the change is done.
- Branch prefixes are `feature/`, `fix/`, `chore/`, `bug/`; commits and PRs are attributed to the owner only.

## Workflow

Several people and tools work in this repository at the same time. The rules
below keep that safe; they add to the handbook's `foundations/git-workflow.md`.

- **One worktree per change.** Never work on `main` in the primary checkout.
  Create a worktree under `.worktrees/` (git-ignored) from a fresh `origin/main`:
  `git fetch origin && git worktree add .worktrees/<branch> -b <branch> origin/main`.
  Remove it after the PR merges.
- **Assume `main` moved.** Before opening or updating a PR, `git fetch origin`
  and rebase the branch onto `origin/main`; resolve conflicts on the branch.
- **Tests first.** Write the failing test, make it pass, then refactor. Every
  handler, store method, middleware, and UI component ships with its tests,
  including negative and error paths. Logging, metrics, and audit events are
  behavior and get tests too.
- **Small units.** One responsibility per function, target 30 logical lines,
  never past the handbook's 60-line gate. Split a file before it becomes a
  catch-all; there are no god files here.
- **Review to zero.** Before a PR is ready, review the diff for security,
  correctness, regressions, backwards compatibility, test coverage, shared
  helper opportunities, naming, and handbook conformance, and fix every finding.
  Repeat until a review pass produces nothing. A PR with known findings is a draft.
- **PR hygiene.** Conventional Commits title, the WHY in the body, linked ADR or
  issue for architectural changes, under roughly 400 lines of hand-written diff.
  Watch the checks after opening and after every push; a red check is yours to
  fix before asking for review.
- **Merge discipline.** Squash-merge only, after `verify` and `integration` are
  green. Delete the branch and its worktree.
- **Attribution.** Every commit, PR, comment, and file speaks in the owner's
  voice. No tool names, trailers, or generated-by notes anywhere.

## Baseline Verification

```bash
make verify
```

`make verify` is the single ordered gate (tidy, format check, lint, `go vet`,
tests, race tests, `govulncheck`, build). Run the narrowest meaningful tests first, then this gate.
The PostgreSQL half of the parity proof runs in CI's `integration` job; locally:

```bash
BLOOM_TEST_POSTGRES_DSN='postgres://bloom:bloom@localhost:5432/bloom?sslmode=disable' \
  go test -tags=integration ./internal/db/...
```

After a schema or query change, `go tool sqlc generate` must produce no diff.
A change is not done until `make verify` passes locally and in CI.
