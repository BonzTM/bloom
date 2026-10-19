# Pinned TMDB OpenAPI specification

- Upstream: `https://developer.themoviedb.org/openapi/tmdb-api.json`
- Upstream last-modified header: `2026-03-23T14:27:00.500Z`
- Retrieved: `2026-09-23`
- Upstream SHA-256: `1c709375fe994e58c5a35aba7c9abfa7f28aa7103ed6c2052ea5ab0b62af081b`
- Bloom-patched SHA-256: `87ce46d3e0263aabc0b06de4bd5cefe3add8006c8e71ecc3d2760d15f62e99b0`

The full published document, with the compatibility corrections below, is
committed so generation is deterministic and works offline. `oapi-codegen.yaml`
limits generated code to Bloom's search, movie-details, and series-details
operations. Run `make generate` after changing the pin or generator.

Bloom corrects response fields that differ from TMDB's published schema:

- fractional list and season `vote_average` values are numbers, not integers;
- nullable dates and image paths accept JSON `null`;
- nullable previous episode objects accept JSON `null`; the upstream open
  schema for next episodes already accepts both objects and `null`.
