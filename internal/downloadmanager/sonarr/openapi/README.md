# Pinned Sonarr OpenAPI specification

- Upstream: `https://github.com/Sonarr/Sonarr/blob/553c4aeae13d3e7a718422595ab055e0aaf2a3f0/src/Sonarr.Api.V3/openapi.json`
- Commit: `553c4aeae13d3e7a718422595ab055e0aaf2a3f0`
- Commit date: `2025-02-12`
- Upstream SHA-256: `e8d6e400ae200ad4045c803d981538a142142a87f4253703e8c3509c07ade6ba`

The published document does not contain operation IDs. The committed copy adds
stable operation IDs only to the operations listed in `oapi-codegen.yaml`.
No request or response schema is changed. The complete document is committed so
generation is deterministic and works offline.

Sonarr resolves a TMDB series through `GET /api/v3/series/lookup?term=tmdb:<id>`.
The pinned implementation passes the term to SkyHook, whose TMDB helper uses
that same prefix:
`https://github.com/Sonarr/Sonarr/blob/553c4aeae13d3e7a718422595ab055e0aaf2a3f0/src/NzbDrone.Core/MetadataSource/SkyHook/SkyHookProxy.cs#L100-L104`.
