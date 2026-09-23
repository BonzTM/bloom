# Pinned Jellyfin OpenAPI specification

- Upstream: `https://api.jellyfin.org/openapi/jellyfin-openapi-stable.json`
- Jellyfin API version: `12.1.0`
- Upstream SHA-256: `6cc7386ebf8a52a7264969fa63a786dc746e9e37af573babf8b35bcfa1b5021d`

The full stable document is committed so generation is deterministic and works
offline. A local `x-go-name` on `VideoRangeType` avoids an upstream enum-value
name collision when session models are generated; it does not alter the wire
contract. `oapi-codegen.yaml` limits generated code to `GetSessions`,
`GetSystemInfo`, and `GetVirtualFolders`. Run `make generate` after changing the
pin or generator.
