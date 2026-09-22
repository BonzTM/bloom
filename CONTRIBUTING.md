# Contributing To Bloom

Thanks for contributing. This repo follows the Go Project Handbook
(`$HOME/git/coding-handbook/golang`, adopted by
[ADR 0001](decisions/0001-adopt-handbook-go-baseline.md)); this file is the local
entry point and points at the handbook for the detail. When in doubt, the
handbook is the contract, and [AGENTS.md](AGENTS.md) is the project's fast path.

## Setup

```bash
git clone https://github.com/BonzTM/bloom.git
cd bloom
make verify
```

`make verify` is the single gate: it runs tidy, fmt-check, lint, `go vet`, tests,
`-race`, `govulncheck`, and build. It must be green before you open a PR.
Coverage is a separate `make cover`. Requires Go 1.26 or newer; tool dependencies
(golangci-lint, govulncheck, sqlc, goose) are managed via `go get -tool` /
`go tool` (no `tools.go`).

The PostgreSQL half of the [ADR 0004](decisions/0004-sqlite-default-postgres-parity.md)
parity suite needs a live server:

```bash
BLOOM_TEST_POSTGRES_DSN='postgres://bloom:bloom@localhost:5432/bloom?sslmode=disable' \
  go test -tags=integration ./internal/db/...
```

A complete worked example of the layout and proof style lives in the handbook at
`golang/reference/exampleservice`.

## Branches, Commits, And PRs

Follow the handbook's `foundations/git-workflow.md`:

- Trunk-based: branch off `main`, keep the branch short-lived, delete it after merge. `main` is protected.
- Branch names use `feature/`, `fix/`, `chore/`, or `bug/` prefixes.
- Keep PRs small and single-purpose (guideline: under ~400 lines of human-authored diff). Split refactors from behavior changes.
- Write the PR title as a [Conventional Commits](https://www.conventionalcommits.org/en/v1.0.0/) subject (`type(scope): description`, imperative mood) because we **squash-merge** and that title becomes the commit on `main`.
- Put the WHY in the commit/PR body. Link the issue or ADR for architectural changes.
- Commits and PRs are attributed to the author only; no tool trailers.
- A PR merges only after CI `make verify` and the `integration` job are green.

## Schema Changes

Every migration is two files with the same name, one under
`internal/db/migrations/sqlite/` and one under `internal/db/migrations/postgres/`,
each with `-- +goose Up` and `-- +goose Down`. Queries in `internal/db/queries/`
must be portable to both engines. After editing either, run
`go tool sqlc generate` and commit the output; the parity tests fail if the
engines drift. See the handbook's `recipes/add-migration.md` for expand/contract
rules on destructive changes.

## Definition Of Done

A change is not done until it clears the handbook's
`checklists/feature-definition-of-done.md`. In short:

- Behavior is implemented at the right boundary and covered by tests that fail without the change; DB and external boundaries have real integration tests.
- Contracts (`api/openapi.yaml`), config keys (`.env.example`, README), and exported symbols are documented; backward-incompatible changes carry a deprecation plan.
- `make verify` is green from a clean tree; coverage did not regress on mandatory paths.
- Operator-visible changes (new env vars, migrations, ports, contract changes) are called out in the PR body.

## Where To File Issues

- Bugs and feature requests: https://github.com/BonzTM/bloom/issues
- Security reports: do **not** open a public issue; contact the owner directly.
- Proposing a change to an architectural decision: open a new ADR in `decisions/`
  per the handbook's `decisions/architecture-decision-records.md` before the
  implementing PR. Accepted ADRs are never edited, only superseded.

## Maintainers

Owner: BonzTM. Questions go to the issue tracker.
