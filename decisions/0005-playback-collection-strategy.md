# 0005. Collect playback with a sessions poller and layer push sources on top

- **Status:** Accepted
- **Date:** 2026-09-21

## Context

Bloom must record what every user is watching and has watched, with accurate
watch time, across a pluggable set of media servers. The research in
[docs/research/media-server-apis.md](../docs/research/media-server-apis.md) and
[docs/research/jellystat.md](../docs/research/jellystat.md) establishes:

- The only signal available on every server without a plugin is the active
  sessions snapshot: Jellyfin `GET /Sessions`, Emby `GET /Sessions`, Plex
  `GET /status/sessions`.
- Jellyfin also offers a websocket `SessionsStart` push (Jellystat uses it for
  Jellyfin 10.11 and later with REST fallback), the Webhook plugin
  (`PlaybackStart`, `PlaybackProgress`, `PlaybackStop` with `PlayedToCompletion`;
  progress cadence is client-driven and no retry semantics are documented), and
  the Playback Reporting plugin with its own SQLite store.
- Jellystat identifies a watch by `UserId + DeviceId + NowPlayingItemId` rather
  than the server's session id, accumulates wall-clock deltas on pause and stop,
  and merges a stop within a configurable window of a prior record at 80 percent
  or less progress. Its transcoding and stream snapshot is taken once at start,
  so mid-play method changes are lost.
- Jellystat's most-reacted pain points are server load from one-second polling,
  duplicated or miscounted plays, and data lost while the collector is down
  with no working backfill.

## Decision

Playback collection is a per-server **collector** in `internal/core` fed by one
or more **event sources** behind a small consumer-defined interface. Rules:

1. **Baseline source is polling the sessions snapshot.** Every media-server
   adapter implements it. The interval is adaptive: short while sessions are
   active, long when idle, both configurable and both bounded. Default targets
   are five seconds active and thirty seconds idle, tuned by measurement.
2. **Push sources are optional layers**, registered per server when available:
   Jellyfin websocket sessions, Jellyfin Webhook plugin, Plex webhooks. A push
   event never creates state on its own; it is folded into the same collector
   state machine as a poll observation, so the two cannot disagree.
3. **A watch is a Bloom-owned entity**, keyed by server, user, device, and item,
   with explicit `started`, `paused`, `resumed`, and `stopped` transitions and
   position samples. Watch time is the sum of active intervals, not wall clock
   between first and last sighting. Play method and stream details are recorded
   as a series so mid-play changes are kept.
4. **Stop detection is explicit or timed out.** A `PlaybackStop` event closes a
   watch immediately. Absent that, a watch closes after a bounded number of
   consecutive polls without its session. A later sighting of the same key
   within a configurable resume window reopens it instead of creating a
   duplicate, which is Jellystat's merge rule made deterministic.
5. **In-progress watches are persisted**, not held only in memory, so a restart
   resumes them and short outages lose at most one poll interval.
6. **Historical backfill is a separate import**, from the server's `UserData`
   (`PlayCount`, `LastPlayedDate`) and, where installed, the Playback Reporting
   plugin. Imports are idempotent by source record id and can be re-run.
7. **Every collected row carries its source** (`poll`, `websocket`, `webhook`,
   `import`) so accuracy questions can be answered from data.

## Consequences

### Good

- Works on a bare Jellyfin, Emby, or Plex with no plugin. Accuracy improves when
  push sources are enabled without changing the data model.
- Deterministic watch lifecycle removes the duplicate and miscount class of
  bugs users report against Jellystat.
- Persisted in-progress state makes restarts safe.

### Bad

- A watch can be misattributed if a client reuses a device id across users on
  the same item within the resume window. Accepted; the server's own session id
  is added as a secondary discriminator when present.
- Polling at five seconds bounds position precision to that granularity when no
  push source is present. Users who need finer data enable the websocket or
  webhook source.
- Webhook delivery has no documented retries on Jellyfin, so webhooks alone are
  never trusted to close a watch; the poller remains the source of truth for
  liveness.

### Neutral

- Per-source ingest is bounded work: timeouts on every call, bounded retry with
  jitter, and a bounded in-memory queue between sources and the collector, per
  the handbook's resilience rules.

## Alternatives Considered

- **Webhook plugin only.** Rejected: requires a plugin, offers no liveness
  signal, and has no documented retry.
- **Playback Reporting plugin as the store.** Rejected: Jellyfin-only, and its
  admin-only raw-SQL endpoint is a poor integration surface.
- **Server-side session id as the watch key.** Rejected: Jellystat's experience
  shows clients churn session ids across pause and reconnect.
- **One-second polling like early Jellystat.** Rejected: the load complaints are
  the top-reacted issues against it.

## Links

- **Supersedes:** None.
- Related: ADR 0004 (write batching on SQLite), ADR 0006 (auth, for who may read
  whose history).
- Research: `docs/research/media-server-apis.md` sections 3 and 8;
  `docs/research/jellystat.md` sections 3, 5, and 6.
