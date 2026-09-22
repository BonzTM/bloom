# 0002. Ship the UI as a Vite-built SPA embedded in the Go binary

- **Status:** Accepted
- **Date:** 2026-09-21

## Context

The handbook's default web shape is server-rendered `html/template`. It states:
"If the spec says 'web app' without qualification, server-rendered is the default;
reach for a SPA only when the spec demands rich client-side interactivity, and
record that as an ADR" (`golang/services/web-apps.md`). It also says a SPA "is a
separate frontend artifact talking to a JSON API" whose "toolchain lives outside
the Go repo."

Bloom's UI is a statistics dashboard with live "now playing" sessions and charts,
a poster grid with search and request flows, and admin tables and forms. The
owner also required the product be "api-first" and ship as "one image, one repo".
Every comparable product (Jellyseerr, Jellystat, Maintainerr, Homarr,
Streamystats, Jellyfin Web) ships a SPA; see
[research/frontend-react-vs-svelte.md](../docs/research/frontend-react-vs-svelte.md)
section 8.

## Decision

The UI is a single-page application. Its source lives in `web/` with its own
`package.json` and lockfile. `vite build` writes `web/dist/`, which the Go binary
embeds with the standard library `embed` package and serves from
`internal/api/web`, falling back to `index.html` for client-side routes. The JSON
API under `/api/v1` is the only way the UI talks to the backend, and the OpenAPI
document in `api/` is its contract.

The Go handbook's rule that the SPA toolchain lives "outside the Go repo" is
satisfied in spirit: `web/` is a separate package with its own toolchain and
`npm run verify` gate, and no Go code imports anything from it except the built
`dist/` tree.

## Consequences

### Good

- One image, one process, one port. Same-origin means no CORS layer.
- The API is exercised by a real client from day one, which keeps it honest.

### Bad

- Two toolchains in one repo: `make verify` must run both gates. CI time grows.
- The multi-stage Dockerfile gains a Node build stage before the Go build stage.
- HTML security headers, CSP, and the `index.html` fallback are Bloom's job, not
  a static host's.

### Neutral

- Server-side sessions and CSRF guidance from `web-apps.md` still apply to the
  auth endpoints, because the SPA uses cookies against the same origin.

## Alternatives Considered

- **Server-rendered `html/template` plus HTMX** (Wizarr's shape). Rejected: live
  sessions, charts, and poster browsing would fight the model.
- **Separate frontend container behind a reverse proxy.** Rejected by the owner:
  "One image, one repo since this is one project."

## Links

- **Supersedes:** None.
- Related: ADR 0001, ADR 0003.
- Handbook: `golang/services/web-apps.md` (deviation), `typescript/AGENTS.md`,
  `typescript/services/react-applications.md`.
