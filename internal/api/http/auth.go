package http

// Authentication and authorization seam.
//
// Nothing is implemented here yet, deliberately. ADR 0006 defines the model
// this file will carry, and it is NOT the handbook reference's bearer-JWT
// verifier, so that code was not copied:
//
//   - Bloom owns the principal: every caller is a Bloom account
//     (core.Account); media-server users are linked identities on it.
//   - Identity providers are pluggable and concurrent behind a consumer-defined
//     IdentityProvider seam in internal/core: local Argon2id passwords,
//     media-server login, generic OIDC (discovery, PKCE, id_token against the
//     issuer JWKS), and an off-by-default trusted-proxy header gated by a CIDR
//     allowlist.
//   - Browser sessions are server-side (scs) in Bloom's own database on both
//     engines, with Secure/HttpOnly/SameSite=Lax cookies, token regenerated on
//     login; CSRF is the stdlib net/http.CrossOriginProtection. JWTs are never
//     used as a cookie session substitute.
//   - API keys are per account, crypto/rand, stored hashed, shown once.
//   - Authorization is permission-based RBAC with string permission ids grouped
//     by module ("users.invite", "requests.approve", "stats.read.own", ...).
//     Route-level checks run in middleware from a static route-to-permission
//     table; ownership checks run in internal/core. Default is deny. The
//     permission catalog is part of the api/ contract and only grows additively.
//   - Auth events are emitted on the dedicated audit stream
//     (telemetry.AuditLogger), never the access log.
//
// Middleware placement, when it lands, follows the handbook's order in
// server.go: authentication runs ahead of logging so a rejected credential is
// rejected early; per-route authorization runs just inside logging so the
// decision and the matched route land in the access log.
//
// Until then every route mounted in server.go is either a probe or a public,
// read-only endpoint (GET /api/v1/version). Do not mount a state-changing or
// account-scoped route without wiring this seam first.
