# 0004. SQLite by default, PostgreSQL at full parity, one SQL layer

- **Status:** Accepted
- **Date:** 2026-09-21

## Context

The handbook's default primary store is PostgreSQL, and its persistence default is
`database/sql` then `sqlc`, with `goose` for migrations
(`golang/decisions/framework-selection.md`). The owner requires:

> sqlite, but with postgres capabilities if a user really wants to use postgres
> (we will use postgres). Both should have complete parity and should be easy to
> maintain long term.

Prior art shows both failure modes. Wizarr is SQLite-only and its users ask for
PostgreSQL. Jellystat is PostgreSQL-only, with "most reporting logic in PL/pgSQL
functions and materialized views", and its most-reacted declined issue is a
request for SQLite (`docs/research/jellystat.md`). Bloom must serve self-hosting users
who want one container and one volume, and operators who already run PostgreSQL.

Tooling facts (verified 2026-09-21):

- `sqlc` engines are "One of `postgresql`, `mysql` or `sqlite`", and the config's
  `sql` key is a collection where "Each mapping in the `sql` collection" carries
  its own `engine`, `schema`, and `queries` (docs.sqlc.dev config reference).
- `goose` supports "postgres, mysql, sqlite3, ..." and `SetDialect` accepts
  `"sqlite3"`/`"sqlite"` and `"postgres"`/`"pgx"` (pressly/goose `dialect.go`);
  migrations embed via `embed.FS` with `goose.SetBaseFS`.
- `modernc.org/sqlite` is "a sql/database driver using a CGo-free port of the C
  SQLite3 library", registered as driver name `sqlite`, which keeps the
  `CGO_ENABLED=0` static build the handbook requires (pkg.go.dev).

## Decision

Bloom supports exactly two engines, SQLite (default) and PostgreSQL, behind one
repository layer in `internal/db`, with these rules:

1. **Portable SQL first.** Schema and queries are written in the common subset of
   both dialects. Business logic lives in Go, never in stored procedures,
   triggers, or materialized views. Aggregations that would tempt PL/pgSQL are
   computed in Go or in plain SQL that both engines run.
2. **One migration set per engine, generated from one source of intent.** Each
   `goose` migration ships as `internal/db/migrations/sqlite/NNNN_x.sql` and
   `internal/db/migrations/postgres/NNNN_x.sql`. A test asserts both directories
   have identical file lists and that the resulting schemas expose the same
   tables and columns. Dialect differences (types, `AUTOINCREMENT` vs
   `GENERATED ... AS IDENTITY`, `TIMESTAMPTZ` vs integer epoch) are confined to
   these files.
3. **`sqlc` with two `sql` entries** in `sqlc.yaml`, one per engine, generating
   into `internal/db/sqlite` and `internal/db/postgres` from the same
   `internal/db/queries/*.sql` files where the SQL is identical, and from an
   engine-specific override file only where it is not. A test fails the build
   if a query name exists for one engine and not the other.
4. **A consumer-defined `Store` interface set in `internal/core`** is the only
   thing the rest of the app sees. Both generated packages are adapted to it.
5. **Timestamps are stored as UTC** in a representation both engines round-trip
   without loss; Wizarr's "naive UTC-assumed datetimes" bug class
   (`docs/research/wizarr.md`) is designed out.
6. **Parity is proven, not asserted.** The repository test suite runs against
   both engines in CI on every push: SQLite in-process, PostgreSQL as a service
   container. The migration up/down/up test from the handbook's `add-migration`
   recipe runs on both.
7. **Drivers:** `modernc.org/sqlite` and `pgx/v5` via `database/sql` stdlib
   interface (`sql_package: database/sql`) so the adapter code is shared.

## Consequences

### Good

- Self-hosted default is a single file on a single volume; no second container.
- Operators with PostgreSQL get it with no loss of features, satisfying the
  owner's own deployment.
- No engine-specific reporting logic to port later, unlike Jellystat.

### Bad

- Every schema change is two files and two generated packages. Estimated tax:
  roughly a third more time per migration. Mitigated by the parity tests and by
  keeping SQL portable so most query files are shared verbatim.
- Portable SQL forgoes PostgreSQL-only features (partial indexes with
  expressions, `JSONB` operators, window-function extensions) unless SQLite has
  an equivalent. Re-evaluate if a statistics query cannot meet its latency
  target on both engines.
- SQLite's single-writer model bounds write concurrency. Playback event ingest
  must batch writes and use WAL mode. Re-evaluate if a real deployment exceeds
  SQLite's write capacity; the answer then is "use PostgreSQL", which this ADR
  already supports.

### Neutral

- The handbook's "no SQLite stand-in for a Postgres feature" testing rule is
  honoured by construction: SQLite is a supported target, not a test double.
- Engine selection is one config value (`BLOOM_DB_DRIVER`, `BLOOM_DB_DSN`).

## Alternatives Considered

- **PostgreSQL only** (handbook default, Jellystat's choice). Rejected: fails the
  owner's requirement and the strongest user ask against Jellystat.
- **SQLite only** (Wizarr's choice). Rejected: fails the owner's own deployment
  and Wizarr users' PostgreSQL asks.
- **A query builder or ORM to abstract the dialect** (GORM, Bun, ent). Rejected:
  the handbook forbids "ORMs as the day-one default"; dialect abstraction layers
  hide exactly the query plans the handbook wants visible, and they still leak on
  DDL and on aggregate functions.
- **Single migration set with `goose` templating or runtime string substitution.**
  Rejected: hides dialect differences in code paths that are hard to review; two
  explicit files per migration are easier to audit and diff.
- **Embedded PostgreSQL in the container** to avoid dual dialects. Rejected:
  defeats the single-binary, `CGO_ENABLED=0`, small-image contract.

## Links

- **Supersedes:** None.
- Related: ADR 0001.
- Research: `docs/research/wizarr.md`, `docs/research/jellystat.md`.
- Sources: https://docs.sqlc.dev/en/v1.31.1/reference/config.html ,
  https://github.com/pressly/goose , https://pkg.go.dev/modernc.org/sqlite
- Handbook: `golang/decisions/framework-selection.md`,
  `golang/services/database.md`, `golang/recipes/add-migration.md`.
