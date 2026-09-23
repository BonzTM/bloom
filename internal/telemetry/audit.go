package telemetry

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"time"
)

// Audit logging implements ADR 0006 item 8 and the handbook's
// operations/security.md ### Audit Logging: audit logs answer "who did what, to
// what, when, and with what result" for security-relevant actions (login
// success and failure by provider, logout, password change, API key create and
// revoke, role change, invite create/accept/revoke, trusted-proxy acceptance).
// They are DISTINCT from the operational/access logs that NewLogger produces:
// this logger has its OWN handler and sink so audit evidence can carry its own
// retention, access controls, and integrity guarantees, and is never sampled
// or rotated with the access log.
//
// The schema is fixed and low-cardinality. Records carry the full who/what/
// when/where on every entry and NEVER carry secrets or PII payloads: the fact
// and identity of an action (actor, action, resource id, result), not its
// sensitive contents. Failed-login correlation uses a purpose-separated HMAC
// key derived from BLOOM_SECRET_KEY over the submitted username after Bloom's
// PRECIS UsernameCaseMapped comparison key. The digest is truncated to 128
// bits and rendered as a username-prefixed hexadecimal identifier; the
// username itself is never recorded.
//
// Emit returns sink failures to its caller. The HTTP caller logs one
// operational error, increments bloom_audit_write_failures_total, and lets the
// request continue. A failed stderr write must not block an otherwise valid
// sign-in; the error and counter make the lost audit evidence observable.

// AuditResult is the low-cardinality outcome of an audited action. A denial or
// failure is as important to record as a success, so the set is closed and
// explicit rather than a free-form string.
type AuditResult string

const (
	// AuditActionRoleAssign is the stable action for every account role assignment.
	AuditActionRoleAssign = "role.assign"
	// AuditSuccess marks an allowed, completed action.
	AuditSuccess AuditResult = "success"
	// AuditFailure marks an authentication failure: the caller could not be
	// established (missing or invalid credential).
	AuditFailure AuditResult = "failure"
	// AuditDenied marks an authorization denial: the caller is known but lacks the
	// required permission or ownership for the action.
	AuditDenied AuditResult = "denied"
)

// AuditEvent is one audit record. Every field is non-sensitive identity or
// metadata: Actor identifies WHO, Action/Resource identify WHAT, Result is the
// outcome, Time is WHEN (UTC), and RequestID is WHERE (the correlation id). No
// token, header, body, raw username, or other payload is ever placed here.
type AuditEvent struct {
	// Actor is the acting account id, or "anonymous" when authentication did not
	// establish a principal.
	Actor string
	// SubjectID is an optional non-reversible identifier used to correlate
	// anonymous attempts without recording the submitted username.
	SubjectID string
	// Action is the audited operation, e.g. "auth.login", "apikey.create".
	// Low-cardinality and stable.
	Action string
	// Resource is the target resource identifier (an id or route), never its
	// contents. Empty when the action has no specific target.
	Resource string
	// Permission is the finite catalog identifier evaluated by an authorization
	// decision. It is empty for events that are not permission checks.
	Permission string
	// Role is the assigned role name. It is empty for events that are not role
	// assignments.
	Role string
	// Kind is the finite integration kind for integration configuration events.
	Kind string
	// AllowInsecure records an explicit plaintext-transport exception.
	AllowInsecure bool
	// Result is the outcome: success, failure, or denied.
	Result AuditResult
	// Reason is a finite audit-safe outcome detail such as "bad_password".
	Reason string
	// Source is the validated client address or service identity such as "cli".
	Source string
	// RequestID is the correlation id tying the audit record to the access log and
	// trace for the same request.
	RequestID string
}

// AuditLogger emits structured audit events to a dedicated sink. It wraps a
// private *slog.Logger so callers cannot reach the underlying handler and mix
// operational logs into the audit stream. Time is stamped from an injected
// clock (UTC) so records are deterministic in tests.
type AuditLogger struct {
	logger *slog.Logger
	now    func() time.Time
}

// AuditClock supplies the audit timestamp. Production wires the system clock;
// tests wire a fixed clock for deterministic records.
type AuditClock interface {
	Now() time.Time
}

// RoleAssignmentAuditEvent builds the shared representation for an account
// role assignment. An unresolved account never exposes the submitted username.
func RoleAssignmentAuditEvent(
	actor, accountID, role string,
	result AuditResult,
	reason, source string,
) AuditEvent {
	resource := "account:" + accountID
	if accountID == "" {
		resource = "account:unresolved"
	}
	return AuditEvent{
		Actor: actor, Action: AuditActionRoleAssign, Resource: resource,
		Role: role, Result: result, Reason: reason, Source: source,
	}
}

// NewAuditLogger builds an AuditLogger writing JSON records to w, which is the
// audit sink, SEPARATE from the application logger's writer so audit evidence
// is governed independently. The handler is fixed to JSON at info level: audit
// records are evidence, not debug output, and are not level-filtered away. The
// clock stamps each record's UTC time.
func NewAuditLogger(w io.Writer, clock AuditClock) *AuditLogger {
	// A dedicated handler with a stable "audit" marker attribute so a downstream
	// collector can route this stream even if writers are later multiplexed.
	h := slog.NewJSONHandler(w, &slog.HandlerOptions{Level: slog.LevelInfo})
	logger := slog.New(h).With("log_type", "audit")
	return &AuditLogger{logger: logger, now: clock.Now}
}

// Emit writes one audit event. The record carries the fixed who/what/when/where
// schema; the timestamp is forced to UTC. Emit is safe for concurrent use (the
// slog handler serializes writes). It deliberately accepts only an AuditEvent so
// no caller can attach arbitrary (possibly sensitive) attributes to the stream.
func (a *AuditLogger) Emit(ctx context.Context, e AuditEvent) error {
	record := slog.NewRecord(a.now().UTC(), slog.LevelInfo, "audit", 0)
	record.AddAttrs(
		slog.String("actor", e.Actor),
		slog.String("subject_id", e.SubjectID),
		slog.String("action", e.Action),
		slog.String("resource", e.Resource),
		slog.String("permission", e.Permission),
		slog.String("role", e.Role),
		slog.String("kind", e.Kind),
		slog.Bool("allow_insecure", e.AllowInsecure),
		slog.String("result", string(e.Result)),
		slog.String("reason", e.Reason),
		slog.String("source", e.Source),
		slog.String("request_id", e.RequestID),
	)
	if err := a.logger.Handler().Handle(ctx, record); err != nil {
		return fmt.Errorf("write audit record: %w", err)
	}
	return nil
}

// NopAuditLogger returns an AuditLogger that discards records. It is the default
// when no audit sink is wired (e.g. in tests that do not assert on audit
// output), so callers never need a nil check before Emit.
func NopAuditLogger() *AuditLogger {
	return NewAuditLogger(io.Discard, systemClock{})
}

// systemClock is the default UTC clock used by NopAuditLogger so the package has
// no ambient dependency on the wall clock at construction sites.
type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now() }
