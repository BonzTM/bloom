# 0009. Notifications through pluggable channels fed by domain events

- **Status:** Accepted
- **Date:** 2026-09-24

## Context

Requests now emit lifecycle events on an in-process bus ([ADR 0008](0008-requests-and-metadata.md)
item 7: created, approved, declined, dispatched, available, failed), and
nothing consumes them yet. Seerr users expect to be told what happened to a
request; the research in [docs/research/seerr.md](../docs/research/seerr.md)
(Notifications) lists ten agents and twelve event types, and the ranked asks
show what Seerr's model cannot do:

- One instance per agent (seerr#804): two Discord webhooks are impossible.
- Only the requester is told (overseerr#789, seerr#375): nobody can subscribe
  to a title they did not request.
- Templates exist for the webhook agent only (overseerr#405).
- Agent settings live in a JSON file next to the secrets they contain.

Jellystat users ask for the same thing for playback (jellystat#6), and the
requirements ([section C](../docs/requirements.md)) name webhook, Discord,
and email as the first agents, with Telegram, Pushover, Gotify, ntfy, Slack,
Pushbullet, and web push later.

## Decision

1. **Channels are a consumer-owned seam.** `internal/core` defines a
   `NotificationChannel` with at most three methods: send one rendered
   message, probe the configuration, and report its kind. Adapters live in
   `internal/notify/<kind>`; webhook (JSON POST with a shared-secret header),
   Discord (webhook URL), and email (SMTP with STARTTLS or implicit TLS) ship
   first. Any number of channels of any kind may be registered, each with its
   own name, so two Discord servers are two channels.
2. **Secrets are encrypted at rest and never returned.** Webhook URLs, Discord
   webhook URLs, SMTP passwords, and shared secrets are stored through
   `internal/secrets` like every other credential; the API reports only that
   they are set. Creates require every credential for the selected kind;
   replacements may omit credentials to retain the stored values. Adapters use
   the same destination policy and bounds as the other outbound clients.
3. **Subscriptions bind events to channels.** A channel subscribes to a set of
   event types from the request lifecycle. Version one addresses operators:
   every subscribed event goes to the channel. Per-account preferences and
   "notify me too" on a title are the next slice; the event payload carries
   the requester so the routing can grow without changing the seam.
4. **Delivery is durable and bounded.** Every request mutation writes an event
   row in the same transaction as the state change. The in-process bus is only
   a wake-up hint. A supervised worker enriches unfanned events, atomically
   creates one delivery per enabled subscribed channel, and marks the event
   fanned. It then claims deliveries with a lease, renders the message, sends
   it with a timeout, retries transient failures with capped backoff up to a
   bounded count, and records the outcome per channel. Missing username
   enrichment falls back to an empty value instead of dropping the event.
   Nothing in the request path waits on a network call. Delivery is
   at-least-once: a crash after the provider accepts a send but before outbox
   completion may produce a duplicate. Webhooks carry the delivery UUID in the
   signed payload, `X-Bloom-Delivery-ID`, and `Idempotency-Key`; email uses it
   in `Message-ID`; Discord has no deduplication field. Disabling a channel
   fails only pending deliveries without a live lease. A live send records its
   natural outcome, while new claims exclude disabled channels. Deleting a
   channel tombstones it until no live lease remains. Claims and tombstone
   reaping serialize on the channel row and re-check eligibility after locking.
   A channel that keeps failing is marked degraded and shown as such; nothing
   is silently dropped.
5. **Messages are rendered from named templates in Go.** Each event type has a
   default subject and body; an operator may override a channel's template
   text with bounded plain-text templates using a fixed set of named fields.
   Rendering is escaped for the channel's format. Every Discord embed title,
   description, field name, and field value is Markdown-escaped before
   truncation; email and webhook JSON remain plain text.
6. **Every send is observable.** Metrics per channel kind and outcome, an
   audit event for channel changes and test sends, and a per-channel delivery
   log that never contains the secret or the full webhook URL.

## Slicing

1. Channels, subscriptions, the outbox and worker, the three adapters, a
   test-send route, and the admin UI to manage channels.
2. Per-account preferences and subscribing to a title's availability; playback
   events (a new session started) as a second event source.

Explicit non-goals for these slices: web push, the remaining agents, and
per-user template overrides.

## Consequences

### Good

- Any event source can be delivered anywhere; the request bus is the first,
  playback the second, without touching the seam.
- No secret sits in a settings file; everything the adapters need is
  encrypted like the media-server and download-manager keys.
- A crash between the state change, fan-out, and send loses nothing, because
  the event and delivery rows record what must still be processed.

### Bad

- An outbox adds a table and a worker. Accepted: the playback collector and
  the fulfilment worker already follow the same supervised pattern.
- Operators write templates in plain text with named fields rather than a
  full template language. Accepted: it keeps rendering bounded and escapable.
- At-least-once delivery can duplicate a provider-side effect after an
  ambiguous send. Accepted: exactly-once delivery to an external endpoint is
  not achievable; stable delivery identifiers let capable receivers
  deduplicate.

### Neutral

- Channels of later kinds are adapters behind the same seam; adding one is a
  package, an allowlist entry, and a settings form.
