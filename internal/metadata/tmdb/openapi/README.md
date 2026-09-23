# Pinned TMDB OpenAPI specification

- Upstream: `https://developer.themoviedb.org/openapi/tmdb-api.json`
- Upstream last-modified header: `2026-03-23T14:27:00.500Z`
- Retrieved: `2026-09-23`
- SHA-256: `1c709375fe994e58c5a35aba7c9abfa7f28aa7103ed6c2052ea5ab0b62af081b`

The full published document is committed so generation is deterministic and
works offline. `oapi-codegen.yaml` limits generated code to Bloom's search,
movie-details, and series-details operations. Run `make generate` after
changing the pin or generator.
