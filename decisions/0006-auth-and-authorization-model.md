# 0006. Pluggable identity providers, server-side sessions, permission-based RBAC

- **Status:** Accepted
- **Date:** 2026-09-21

## Context

Bloom fronts a media server for two audiences: operators who administer it, and
end users who accept invites, make requests, and view their own history. The
handbook's silent default is "bearer JWT validated against the issuer's JWKS
(OIDC-shaped)" for services, but for browser apps it says sessions are
"server-side sessions via `github.com/alexedwards/scs/v2`", CSRF is the stdlib
`net/http.CrossOriginProtection`, and "Do not use JWTs in cookies as a session
substitute" (`golang/services/web-apps.md`).

What the products Bloom replaces do, from `docs/research/`:

- Seerr: Plex PIN OAuth, Jellyfin/Emby password and Quick Connect, local
  email/password, and one admin-level `X-Api-Key`. "Only one media-server auth
  source at a time." OIDC is "not shipped" and is the top-voted issue
  (276 reactions, open since 2022). Forward-auth, LDAP, and per-user API keys
  are open asks. Authorization is a 30-value permission bitmask.
- Wizarr: multi-admin with scrypt hashes, WebAuthn passkeys as second factor,
  LDAP admin bind, and a proxy-SSO mode via `DISABLE_BUILTIN_AUTH` that "logs
  anyone hitting `/login` in as admin". API keys are hashed and shown once.
- Security history to design against: Seerr v3.1.0 fixed unauthenticated
  registration through an inactive endpoint, a profile IDOR leaking tokens, and
  unauthenticated push-subscription endpoints. Jellystat had three critical
  SQL-injection advisories. Wizarr stored API keys and notification passwords
  in plaintext columns.
- Media-server credentials: Jellyfin API keys "are unrestricted" admin
  credentials with no user identity; Plex accounts live on plex.tv and Bloom
  can never set their passwords (`docs/research/media-server-apis.md`).

## Decision

1. **Bloom owns the principal.** Every human or API caller is a Bloom `account`.
   Media-server users are linked identities on an account, never the account
   itself. One account may hold identities on several media servers.
2. **Identity providers are pluggable and concurrent.** A consumer-defined
   `IdentityProvider` seam in `internal/core` with these implementations, each
   individually enabled by the operator:
   - Local username/password with Argon2id hashing.
   - Media-server login through the `MediaServer` adapter (Jellyfin and Emby
     `AuthenticateByName` and Quick Connect; Plex PIN flow when Plex lands).
   - Generic OIDC using discovery, PKCE, and `id_token` validation against the
     issuer's JWKS; claims-to-account mapping is configurable.
   - Trusted-proxy header (forward auth) that is off by default and, when on,
     accepts the header only from a configured CIDR allowlist. Bloom never
     grants admin from a header alone.
   Several providers may be active at once. An account may link more than one.
3. **Browser sessions are server-side** with `scs`, stored in Bloom's own
   database on both engines, cookie flags `Secure`, `HttpOnly`, `SameSite=Lax`,
   token regenerated on login and destroyed on logout. CSRF is the stdlib
   `CrossOriginProtection`. GET never mutates.
4. **API keys are per account**, generated from `crypto/rand`, stored as a
   hash, shown once, and carry no more authority than the owning account. An
   operator's key is simply an operator's key; there is no global master key.
5. **Authorization is permission-based RBAC.** Permissions are stable string
   identifiers grouped by module (`users.invite`, `requests.approve`,
   `stats.read.all`, `stats.read.own`, `admin.settings`). Roles are named sets
   of permissions; accounts hold roles. Route-level checks run in middleware
   from a static route-to-permission table; ownership checks (own requests, own
   history, own profile) run in `internal/core` so they cannot be bypassed by a
   new transport. Default is deny. The permission catalog is part of the API
   contract in `api/` and only grows additively.
6. **Secrets at rest are encrypted.** Media-server API keys, download-manager
   keys, notification credentials, and OIDC client secrets are encrypted with a
   key derived from one operator-supplied `BLOOM_SECRET_KEY`. The API never
   returns them; the UI shows presence, not value.
7. **Invite acceptance is the one unauthenticated write path.** Codes are
   `crypto/rand`, at least 128 bits, single-use or counted, expiring, with
   state held server-side. The endpoint is rate limited by IP and by code.
8. **Auth events are audited** to a dedicated `slog` stream: login success and
   failure by provider, logout, password change, API key create and revoke, role
   change, invite create, accept, and revoke, and trusted-proxy acceptance.

## Consequences

### Good

- OIDC and forward-auth on day one address the single most requested Seerr
  feature and Wizarr's Authentik provisioning ask without a special mode.
- Accounts with linked identities make multi-server support possible, which
  Seerr's model ("one media-server auth source at a time") cannot express.
- String permissions extend when new pluggable modules arrive; a bitmask would
  have to be renumbered or widened.

### Bad

- More moving parts than Seerr's single key and Wizarr's admin-only login.
  Mitigated by shipping local login enabled and everything else off.
- The trusted-proxy provider is a foot-gun if misconfigured. Mitigated by the
  CIDR allowlist, off-by-default, and a startup warning when enabled.
- Sessions in the database add write load on SQLite. Mitigated by session
  lifetime and by `scs` touching the row only on change.

### Neutral

- Media-server passwords are never stored by Bloom; the invite flow passes the
  user's chosen password straight to the media server over the adapter.
- Plex accounts cannot be created or reset by Bloom, which the `MediaServer`
  capability model already reports as unsupported.

## Alternatives Considered

- **Bitmask permissions** (Seerr). Rejected: 30 values already, no namespace,
  awkward for pluggable modules and for a documented API contract.
- **JWT in a cookie.** Rejected by the handbook: "no revocation, growing claims,
  and a signature check is not a logout."
- **Media-server identity as the account** (Seerr). Rejected: blocks
  multi-server, local-only users, and OIDC.
- **One global admin API key** (Seerr). Rejected: no attribution, no revocation
  granularity, and per-user keys are an open ask.
- **Proxy SSO that grants admin to any request** (Wizarr's
  `DISABLE_BUILTIN_AUTH`). Rejected as unsafe by construction.

## Links

- **Supersedes:** None.
- Related: ADR 0002 (same-origin cookie sessions), ADR 0004 (session store on
  both engines), ADR 0005 (who may read whose history).
- Research: `docs/research/seerr.md` (Authentication methods, Permissions, Top
  asks), `docs/research/wizarr.md` (section 6), `docs/research/jellystat.md`
  (security advisories), `docs/research/media-server-apis.md` (sections 1, 7).
- Handbook: `golang/services/web-apps.md`, `golang/operations/security.md`,
  `golang/checklists/spec-intake.md` (Identity & Access).
