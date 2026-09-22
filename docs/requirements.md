# Bloom — Product Requirements (Intake)

Status: draft, captured 2026-09-21 from the owner's stated intent. This file is the
"WHAT". The "HOW" comes from the coding handbook (`$HOME/git/coding-handbook`,
`golang/AGENTS.md` and `typescript/AGENTS.md`). Research that backs each area lives
in [research/](research/).

## Vision

Bloom is a single, self-hosted, container-first web service that replaces the
cluster of companion apps a Jellyfin operator runs today: invites and user
management (Wizarr), per-user playback statistics (Jellystat), and media
discovery/requests (Seerr). One binary, one API, one UI.

The name: a *bloom* is the collective noun for a gathering of jellyfish, and the
word also means growth, which is what an invite system does for a server.

## Owner's stated scope (verbatim)

> all of Wizarr's account invite/management functionality. maybe even look
> through issues/discussions to find what users are asking for. Essentially rich
> user management, permissions, etc, with an invite system.

> Jellystat - rich statistics about every user on the jellyfin server. What they
> are watching, what they have watched, and plenty of stats/dashboards about it
> all

> all of Seerr functionality (minus Plex, likely, but leaving media servers
> pluggable is important in case we want to add a plex, emby, or upcoming media
> server functionality). So integrated users, requests, quotas, alerts,
> integration with all of the major searching APIs and the major download helper
> tools (think sonarr/radarr and more).

> Ideally everything is pluggable and extensible. Since there are lots of
> external dependencies and integrations, we want to be able to slot a new
> software in anytime.

Answers to the three bootstrap questions (owner, 2026-09-21):

> sqlite, but with postgres capabilities if a user really wants to use postgres
> (we will use postgres). Both should have complete parity and should be easy to
> maintain long term.

> One image, one repo since this is one project. I would agree with web/, dist/
> and so on and so forth.

> bloom will replace these external tools but will not replace jellyfin.

> We chose typescript + react in our handbook, so let that bias the decision
> some as well

> OIDC is pretty important feature to me, so we will build the app with support
> for sure.

The owner accepted ADRs 0001 through 0006 on 2026-09-22.

## Functional areas

### A. Users, invites, permissions (mirrors Wizarr)

- OIDC login is a v1 requirement, not a later provider (owner, 2026-09-22).

- Invite links: create, expire, limit uses, bind to libraries/permissions, revoke.
- Account creation on the media server through the invite flow.
- User lifecycle: list, edit, disable, delete, expiry/auto-removal.
- Permission and library assignment pushed to the media server's user policy.
- Onboarding wizard content shown after signup.
- Detail and user asks: [research/wizarr.md](research/wizarr.md).

### B. Statistics (mirrors Jellystat)

- Now playing: live sessions per user, device, client, play method.
- Playback history per user and per item, with watch time.
- Dashboards: most watched, most active users, library growth, client/device
  breakdown, time-of-day, per-library stats.
- Data collection must be accurate for watch time and survive restarts.
- Detail and user asks: [research/jellystat.md](research/jellystat.md).

### C. Discovery and requests (mirrors Seerr)

- Browse and search movies/TV via metadata providers (TMDB first).
- Requests with season/episode granularity, 4K flows, auto-approve rules.
- Quotas per user per period; request status and availability tracking.
- Permissions model that covers requests, auto-approve, 4K, admin, and more.
- Notifications ("alerts") across the common agents (Discord, email, Telegram,
  Pushover, webhook, and others) for every request lifecycle event.
- Download-helper integrations: Sonarr and Radarr first; the boundary must admit
  more (Lidarr, Readarr, Prowlarr, and tools not yet named).
- Detail and user asks: [research/seerr.md](research/seerr.md).

### Explicit non-goals for v1

- Plex as a media server (the boundary must allow it later; not built in v1).
- Any feature not listed above. Additions go through this file first.

## Architectural principle: pluggable boundaries

Every external system sits behind a small, consumer-defined Go interface in
`internal/core`, per the handbook's package-design rules ("Define interfaces where
they are consumed", "Keep interfaces small: 1-3 methods"). Concrete adapters live
in their own packages and are wired explicitly in `internal/runtime`; no `init()`
registration.

Planned seams (names are provisional):

| Seam | First implementation | Later candidates |
|---|---|---|
| Media server | Jellyfin | Emby (same ancestry, easiest), Plex, future servers |
| Identity provider | local, media-server login | OIDC, trusted proxy, LDAP |
| Metadata provider | TMDB | TVDB (Seerr uses it for anime numbering), MusicBrainz, OpenLibrary |
| Request target (download manager) | Sonarr, Radarr | Lidarr, Readarr, Whisparr |
| Indexer manager | none in v1 | Prowlarr |
| Notification agent | webhook, Discord, email | Telegram, Pushover, Gotify, ntfy, Slack, Pushbullet, web push |
| Playback event source | sessions polling | Jellyfin websocket, Jellyfin webhook plugin, Plex webhooks, Playback Reporting import |

Each seam must be exercisable with a hand-rolled fake in tests, per the handbook's
test-doubles rule. Detail per seam: [research/media-server-apis.md](research/media-server-apis.md).

## Spec-intake status (handbook `checklists/spec-intake.md`)

| Box | Answer | Source |
|---|---|---|
| Shape | HTTP/API service plus SPA frontend, built from `web/` and embedded in the binary; one repo, one image | owner; [ADR 0001](../decisions/0001-adopt-handbook-go-baseline.md), [ADR 0002](../decisions/0002-embed-spa-in-binary.md) |
| Browser client / CORS | yes, same-origin SPA served by the binary; no CORS by default | derived; ADR pending |
| Backend language | Go, handbook layout | owner |
| Frontend | TypeScript with the handbook React stack; Svelte evaluated and declined | owner bias; [ADR 0003](../decisions/0003-react-frontend-stack.md) |
| Target platform | container-first; Kubernetes-shaped per handbook default | owner: "docker/container-first" |
| Authentication | Bloom-owned accounts with pluggable, concurrent identity providers: local, media-server login, OIDC, trusted proxy; server-side sessions; per-account API keys | [ADR 0006](../decisions/0006-auth-and-authorization-model.md) |
| Authorization | permission-based RBAC with string permission ids grouped by module; default deny; ownership checks in core | [ADR 0006](../decisions/0006-auth-and-authorization-model.md) |
| Tenancy | single-tenant (one operator, one or more media servers) | handbook default; flagged |
| Primary store | SQLite by default (pure-Go driver), PostgreSQL at full parity; portable SQL, per-engine migrations, parity tests on both engines in CI | owner; [ADR 0004](../decisions/0004-sqlite-default-postgres-parity.md) |
| Secrets | env vars injected by the runtime | handbook default |
| Registry | `ghcr.io` | handbook default |
| Observability | Prometheus `/metrics`, OTel traces via OTLP env, `slog` JSON to stdout | handbook default |
| Compliance | none assumed; PII rules still apply (usernames, emails, IPs, watch history are personal data) | handbook default; flagged |
| SLOs | starting targets per handbook, tuned later | handbook default |

## Product stance

- Bloom **replaces** Wizarr, Jellystat, and Seerr for its users. It does not proxy
  to running instances of them.
- Bloom **does not replace Jellyfin** or any media server. It manages and observes
  them through their APIs.

## Decisions recorded (0001-0006 accepted 2026-09-22; 0007 accepted 2026-09-22)

| ADR | Decision |
|---|---|
| [0001](../decisions/0001-adopt-handbook-go-baseline.md) | Adopt the handbook Go baseline stack without backend exceptions |
| [0002](../decisions/0002-embed-spa-in-binary.md) | UI is a Vite SPA in `web/`, embedded in the Go binary |
| [0003](../decisions/0003-react-frontend-stack.md) | Handbook React stack; Svelte declined |
| [0004](../decisions/0004-sqlite-default-postgres-parity.md) | SQLite default, PostgreSQL at full parity, one portable SQL layer |
| [0005](../decisions/0005-playback-collection-strategy.md) | Sessions poller as baseline, push sources layered on top |
| [0006](../decisions/0006-auth-and-authorization-model.md) | Pluggable identity providers, server-side sessions, permission RBAC |
| [0007](../decisions/0007-data-migrations-in-go.md) | Schema stays in per-engine SQL; Go-only data transformations run as goose Go migrations |

## Research digest: what the evidence says v1 must get right

Each item is grounded in the linked report. Numbers are GitHub reaction counts
at research time (2026-09-21).

### Users and invites ([wizarr.md](research/wizarr.md))

- Keep: invite codes from a CSPRNG, expiry and use limits, per-invite library
  and permission scoping, multi-server invites, expiry job with delete-or-disable,
  onboarding wizard with server-side step ordering, API keys hashed and shown once.
- Add, because users ask: self-service password reset (#484, 21), an email channel
  (D#737, 17), identity-provider provisioning such as Authentik (#1033, 21),
  invite templates and profiles (#738, #771), referral invites (#487), optional
  email on signup (#728), sub-path hosting (D#477).
- Avoid: naive datetimes (#586), library access stored in two places, plaintext
  secrets in columns, upgrade-time migration breakage (#590, #802, #748, #991),
  service-worker white screens (#483).

### Statistics ([jellystat.md](research/jellystat.md), [media-server-apis.md](research/media-server-apis.md))

- Keep: now-playing cards with progress and play method, most-watched and
  most-active cards, daily, weekday, and hourly charts, per-user, per-library,
  and per-item history with filtering, activity timeline, backup and restore.
- Add, because users ask: non-admin viewer accounts so users can see their own
  stats (#261, 12), stream-type and transcode-reason charts (#211), Live TV
  (#177), stale-media reports (#26, #409, #172), working Playback Reporting
  import and backfill (#236, #533), multi-server, low idle load (#328, #298).
- Avoid: reporting logic in database-specific procedures (blocks SQLite parity),
  one-second polling, memory growth from unbounded in-memory state (#369),
  string-built SQL in handlers (three critical advisories).
- Jellystat's maintainer has announced a from-scratch rewrite; Bloom should
  expect a moving competitor, not an abandoned one.

### Discovery and requests ([seerr.md](research/seerr.md))

- Keep: TMDB discovery sliders including custom keyword, genre, studio, network,
  and streaming sliders; search prefixes; movie and TV-season requests with a
  parallel 4K flow; advanced request options; quotas; issues with comments;
  watchlists; blocklist; override rules; all ten notification agents and twelve
  event types; the 41-locale i18n bar; PWA manifest; a published OpenAPI file.
- Add, because users ask: OIDC login (#183, 276), music requests (#96, 132),
  base-URL hosting (#274, 120), custom branding (106), removal requests (#308,
  93), subscribe-to-notifications (#789, #375, 92), episode-level requests
  (#264, 90 and 72), Lidarr (88), books (54, 48), calendar (49), stats in the UI
  (#981, 49), per-episode availability notifications (48), parental controls
  (42, 40), anime-specific instances (34, 26), multiple auth providers (#100,
  32), multiple media servers (13, 14), per-user API keys (#2582).
- Avoid: hard-coded provider API keys in source, settings in a JSON file rather
  than the database, one media server at a time, unauthenticated endpoints that
  were meant to be inactive (the v3.1.0 CVEs).
- Seerr is the merged successor of Jellyseerr and Overseerr as of 2026-02-10 and
  is actively developed; Bloom competes with a live project of 12.6k stars.

### Media-server boundary ([media-server-apis.md](research/media-server-apis.md))

- Jellyfin API keys are unrestricted admin credentials with no user identity;
  every user-scoped call must pass `userId` explicitly.
- `POST /Users/{id}/Policy` replaces the whole policy object, so adapters do
  read-modify-write.
- Jellyfin 12.1 has no server-side provider-id filter on `/Items`; Bloom owns a
  local provider-id index. Emby has `AnyProviderIdEquals`; Plex needs `Guid[]`.
- Plex cannot create password users or reset passwords; the adapter must report
  capabilities, not pretend.
- No official Go SDK for Jellyfin. Generate types from the pinned OpenAPI spec
  with `oapi-codegen` scoped to the operations used, over a thin hand-written
  transport.
