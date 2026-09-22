# 0007. Schema changes stay in per-engine SQL; data transformations that need Go run as goose Go migrations

- **Status:** Accepted
- **Date:** 2026-09-22

## Context

ADR 0004 requires every migration to ship as a pair of per-engine SQL files so
that dialect differences are explicit and reviewable, and it relies on goose
to run them. The first authentication slice needed a data transformation that
SQL cannot express on either engine: computing a PRECIS UsernameCaseMapped
comparison key for every existing username so that uniqueness is
case-insensitive and Unicode-correct on SQLite and PostgreSQL alike. A first
attempt placed that logic in Go behind placeholder SQL files, which broke ADR
0004's promise that the SQL files are the runnable source of truth.

goose supports Go migrations registered with the same version sequence and
run through the same provider, inside a transaction together with the version
record, so a data step can be atomic without leaving SQL files that do nothing.

## Decision

- Every schema change remains a per-engine SQL migration pair, as ADR 0004
  requires. No placeholder or no-op SQL migration files exist.
- A data transformation that cannot be expressed in portable SQL runs as a
  goose Go migration: engine-agnostic, registered through the provider, given
  its own version number between the schema steps that prepare and finish it,
  and executed inside goose's transaction with its version record.
- The pattern is three steps: SQL adds nullable columns and any backup table,
  Go backfills them and fails loudly on collisions, SQL finishes with the
  constraints. Up, down, and re-apply are proven on both engines.

## Consequences

### Good

- Dialect differences stay visible in SQL files; Go migrations carry no
  engine-specific logic.
- Version and data move together or not at all.

### Bad

- A Go migration is compiled into the binary, so the goose CLI alone cannot
  apply it; operators run the migration through the application binary, which
  is already the documented path.

### Neutral

- Go migrations are expected to be rare. Each one is called out in the
  changelog like any other migration.

## Alternatives Considered

- **Engine-specific SQL functions for the transformation.** Rejected: neither
  engine can evaluate PRECIS, and ADR 0004 forbids business logic in SQL.
- **Go migration logic behind placeholder SQL files.** Rejected: it makes the
  SQL files lie about what runs.
- **Rewriting rows at application startup.** Rejected: unversioned, not
  atomic, and repeated on every start.

## Links

- **Supersedes:** None. Refines ADR 0004.
- Related: ADR 0004, ADR 0006.
- Handbook: `golang/recipes/add-migration.md`, `golang/services/database.md`.
