## Summary

<!-- What changed and why, in two or three sentences. Link the issue or ADR. -->

-

## Change Type

- [ ] Feature
- [ ] Bug fix
- [ ] Refactor (no behavior change)
- [ ] Performance
- [ ] Docs / tooling only
- [ ] Dependency bump
- [ ] Breaking change (API, schema, config, or event contract)

## Gates

- [ ] `make verify` is green locally (Go gate, then web gate).
- [ ] Tests prove the behavior change, including negative and error paths.
- [ ] README, `.env.example`, and `api/openapi.yaml` updated for any new or changed key or endpoint.
- [ ] `CHANGELOG.md` entry added for every operator-visible change.
- [ ] Schema changes ship as a migration pair (SQLite and PostgreSQL) and `go tool sqlc generate` is clean.
- [ ] ADR linked below if this change is architectural or shifts a contract.

## Compatibility / Migration

<!-- Rollback story and any step operators must run. "None" is fine for non-breaking changes. -->

None.

## ADR

N/A
