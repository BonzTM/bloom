# 0001. Adopt the coding-handbook Go baseline stack

- **Status:** Accepted
- **Date:** 2026-09-21

## Context

Bloom is a new Go HTTP service with an embedded browser UI. The owner named the
coding handbook (`$HOME/git/coding-handbook`, `golang/AGENTS.md`) as the source of
truth for structure. The handbook's ADR process says the first record "is
effectively the project's stack" so that "every later deviation [is] legible"
(`golang/decisions/architecture-decision-records.md`).

## Decision

We adopt the handbook's Go defaults without exception for the backend:

- Layout: single module `github.com/BonzTM/bloom`, `cmd/bloom`, `internal/{api/http,
  core,db,config,runtime,telemetry,buildinfo,httputil,testutil}`, `api/` for the
  OpenAPI contract, `scripts/`.
- HTTP: `net/http` with `ServeMux`; `chi` only if routing complexity demands it.
- Persistence: `database/sql` plus `sqlc`; migrations with `goose` embedded via
  `embed.FS`. Engine choice is recorded separately in ADR 0004.
- Logging `log/slog`; metrics Prometheus; tracing OpenTelemetry via OTLP env.
- Config: explicit env plus flags in `internal/config`, fail-fast validation,
  committed `.env.example`.
- Build: `CGO_ENABLED=0` static binary, multi-stage image from the handbook
  `Dockerfile` template, nonroot runtime, `/livez` and `/readyz`, version stamped
  through `internal/buildinfo`.
- Gate: the handbook `Makefile`, `.golangci.yml`, and CI workflow; `make verify`
  must be green from the first commit.
- Dependency posture: the Approval Questions in
  `golang/decisions/framework-selection.md` are answered in writing for every new
  module.

## Consequences

### Good

- Every structural question already has an answer and a reference implementation
  (`golang/reference/exampleservice`).
- Deviations are forced through ADRs, so the reasons survive handoff.

### Bad

- The handbook assumes PostgreSQL as the only store and server-rendered HTML as
  the default web shape. Bloom needs both a SPA (ADR 0002) and SQLite parity
  (ADR 0004). Those are real deviations with real cost.

### Neutral

- The handbook is a moving target. Bloom pins to the handbook commit it was
  bootstrapped from and re-syncs deliberately, not implicitly.

## Alternatives Considered

- **Framework-first stack (Gin, Echo, Fiber, GORM).** Rejected: the handbook
  forbids "ORM, DI container, or web framework just to avoid writing explicit Go
  code" and none of Bloom's needs exceed `net/http`.
- **Multi-module or monorepo with separate services.** Rejected: one binary is a
  product requirement ("One image, one repo since this is one project").

## Links

- **Supersedes:** None.
- Related: ADR 0002 (SPA), ADR 0003 (frontend framework), ADR 0004 (datastore).
- Handbook: `golang/AGENTS.md`, `golang/foundations/project-setup.md`,
  `golang/decisions/framework-selection.md`, `golang/checklists/new-project.md`.
