# 0008. Requests through pluggable metadata providers, request profiles, and download managers

- **Status:** Accepted
- **Date:** 2026-09-24

## Context

Bloom replaces Seerr for discovery and requests. The research in
[docs/research/seerr.md](../docs/research/seerr.md) establishes what to keep
(TMDB-backed discovery and search, movie and season requests, quotas,
approval, download progress, availability tracking, a published API) and what
users ask for that Seerr's model cannot express:

- One media server at a time, one metadata source with a hard-coded API key,
  and exactly two download managers (Radarr, Sonarr) with a parallel "4K"
  flag on every entity (`status4k`, `is4k`, default 4K servers). Music
  (seerr#96, 132 reactions), books, and anime instances all fail on that
  shape.
- Availability is always derived by merging media-server scans with *arr
  scans, which users want configurable (seerr#2235).
- Override rules cannot select an instance or decide approval; "advanced
  requests" is all-or-nothing.
- Settings live in a JSON file and third-party keys live in source.

Bloom already has the pieces the request flow needs: pluggable media servers
with capabilities including `provider_id_lookup` ([ADR 0004](0004-sqlite-default-postgres-parity.md),
the media-server slice), permission-based RBAC with `requests.create`,
`requests.approve`, and `requests.read.own` in the catalog, encrypted
credentials at rest, and audit and metrics conventions ([ADR 0006](0006-auth-and-authorization-model.md)).

## Decision

1. **Metadata comes through a consumer-owned provider seam.** `internal/core`
   defines a small `MetadataProvider` interface (search, movie details, series
   details with seasons) and later discovery lists. TMDB is the first adapter,
   behind `internal/metadata/tmdb` with types generated from its published
   OpenAPI where available. The TMDB key is operator-supplied through
   configuration or the settings API, encrypted at rest like every other
   credential, and never shipped in source. Provider calls are bounded
   (timeouts, retries only on retryable failures, a per-provider rate limit)
   and cached with a bounded TTL keyed by provider and id. Bloom stores a
   title snapshot (name, year, poster path) with each request so lists render
   without a provider round trip; images are linked to the provider's CDN, not
   proxied, in v1.
2. **A request names a title and a request profile, not a "4K" flag.** A
   request profile is a named, operator-defined target: one download-manager
   instance plus its quality profile, root folder, and tags, and the media
   kinds it accepts. Profiles replace Seerr's default/4K pairs and generalise
   to Lidarr, Readarr, and anything else that implements the download-manager
   seam. A request records the profile it was routed to; an operator may
   change it before approval.
3. **Requests are movies or sets of series seasons.** The model is
   `requests` (kind, provider, provider id, title snapshot, requester,
   profile, status, timestamps) with `request_seasons` rows for series. Season
   granularity matches what download managers can act on; the schema leaves
   room for episode rows later without a rewrite. Request statuses are
   `pending`, `approved`, `declined`, `processing`, `available`, and
   `failed`; transitions are explicit and audited.
4. **Approval is a permission, quotas are policy.** A request from an account
   holding `requests.approve` is approved on creation; any other request waits
   for an approver. Quotas are per role with an optional per-account override:
   a count of movies and a count of seasons within a rolling period, declined
   requests excluded. Quota checks and status transitions live in
   `internal/core` so no transport can bypass them.
5. **Download managers are a consumer-owned seam too.** `DownloadManager`
   covers adding a movie or series with the profile's options, monitoring
   seasons, and reading queue progress. Radarr and Sonarr are the first
   adapters with keys encrypted at rest and the same destination policy and
   bounds as the media-server clients. Dispatch happens on approval and is
   idempotent by the manager's own id for the title, so a retry never adds a
   duplicate.
6. **Availability comes from the media server by default.** Bloom marks a
   request available when the registered media server reports the title,
   using `provider_id_lookup` where the adapter supports it, on a bounded
   poll. Download-manager queue progress is shown but never sets
   `available` on its own. The source of truth is a per-installation setting
   so an operator without a media-server link can choose the download manager
   instead.
7. **Lifecycle changes emit domain events.** Creation, approval, decline,
   dispatch, availability, and failure are recorded and published on an
   in-process event seam that the later notification module consumes. Nothing
   in the request path sends a notification directly.

## Slicing

Two thin end-to-end slices, each merged on its own:

1. Metadata and requests: the TMDB adapter and its key handling, search and
   detail routes, request creation with quotas, listing (own and all), and
   approve or decline, with the request profile table seeded by the operator.
2. Fulfilment: the Radarr and Sonarr adapters, dispatch on approval, queue
   progress, and availability polling against the media server.

Explicit non-goals for these slices: a parallel 4K status, watchlists,
blocklists, override rules, issues, episode-level requests, removal requests,
and notifications (they arrive with the notification module).

## Consequences

### Good

- Any number of download managers of any kind can sit behind request
  profiles; music and books become adapters, not rewrites.
- No third-party key ships in the binary; the operator owns every credential
  and every credential is encrypted at rest.
- Availability has one configurable source, so the "media server says yes,
  *arr says no" class of confusion cannot happen.
- Quotas and transitions live in core and are covered by parity tests on both
  engines.

### Bad

- Operators must create at least one request profile before anyone can
  request; the UI has to make that a first-run step. Accepted: it replaces
  the hidden default-server logic that confused Seerr users.
- A title snapshot can go stale when the provider renames a title. Accepted:
  lists stay fast and offline-safe, and the detail view re-reads the provider.
- Polling the media server for availability adds load. Mitigated by a bounded
  interval that only runs while approved requests are outstanding.

### Neutral

- Discovery sliders, keyword and network browsing, and ratings are UI
  features on top of the provider seam and arrive after the two slices above.
- The event seam is in-process for now; a durable outbox can replace it when
  a consumer needs delivery guarantees.

## Alternatives Considered

- **Copy Seerr's `is4k` model.** Rejected: it is the root of every "more than
  two targets" request in Seerr's tracker.
- **Hard-code a TMDB application key.** Rejected: it violates the secrets
  rule in ADR 0006 and ties every installation to one quota.
- **Derive availability from the download manager only.** Rejected as the
  default: the media server is what users watch from; kept as an option.
