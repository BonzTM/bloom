package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/BonzTM/bloom/internal/config"
	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/db"
	"github.com/BonzTM/bloom/internal/telemetry"
)

type captureAudit struct {
	events []telemetry.AuditEvent
}

func (a *captureAudit) Emit(_ context.Context, event telemetry.AuditEvent) error {
	a.events = append(a.events, event)
	return nil
}

func TestExecuteGrantRoleRecoversRolelessAccountIdempotently(t *testing.T) {
	dsn := prepareAdminDatabase(t)
	setAdminEnvironment(t, dsn)
	account := createRolelessAccount(t, dsn, "legacy")
	var audit strings.Builder
	args := []string{"grant-role", "--username", "legacy", "--role", "owner"}
	if err := Execute(context.Background(), args, Streams{Log: io.Discard, Audit: &audit}, CommandInput{}); err != nil {
		t.Fatalf("Execute(grant-role): %v", err)
	}
	assertAccountRoles(t, dsn, account.ID, []string{"owner"})
	assertGrantRoleAudit(t, audit.String(), account.ID, "assigned")
	audit.Reset()
	if err := Execute(context.Background(), args, Streams{Log: io.Discard, Audit: &audit}, CommandInput{}); err != nil {
		t.Fatalf("Execute(idempotent grant-role): %v", err)
	}
	assertGrantRoleAudit(t, audit.String(), account.ID, "already_held")
}

func assertGrantRoleAudit(t *testing.T, output, accountID, reason string) {
	t.Helper()
	var got map[string]any
	if err := json.Unmarshal([]byte(output), &got); err != nil {
		t.Fatalf("decode grant-role audit: %v: %s", err, output)
	}
	want := map[string]string{
		"level": "INFO", "msg": "audit", "log_type": "audit", "actor": "cli", "subject_id": "",
		"action": telemetry.AuditActionRoleAssign, "resource": "account:" + accountID, "permission": "", "role": "owner",
		"result": "success", "reason": reason, "source": "cli", "request_id": "",
	}
	if len(got) != len(want)+1 {
		t.Fatalf("grant-role audit fields = %v, want %v plus time", got, want)
	}
	for field, value := range want {
		if got[field] != value {
			t.Fatalf("grant-role audit %s = %v, want %q", field, got[field], value)
		}
	}
	stamp, ok := got["time"].(string)
	parsed, err := time.Parse(time.RFC3339Nano, stamp)
	if !ok || err != nil || parsed.Location() != time.UTC {
		t.Fatalf("audit time = %q, %v; want RFC3339 UTC", stamp, err)
	}
}

func TestExecuteGrantRoleAuditSinkFailureIsLoggedAndPreservesResult(t *testing.T) {
	dsn := prepareAdminDatabase(t)
	setAdminEnvironment(t, dsn)
	account := createRolelessAccount(t, dsn, "legacy")
	var logs strings.Builder
	err := Execute(context.Background(), []string{"grant-role", "--username", "legacy", "--role", "owner"},
		Streams{Log: &logs, Audit: failingWriter{err: errors.New("audit unavailable")}}, CommandInput{})
	if err != nil {
		t.Fatalf("Execute(grant-role) = %v, want success", err)
	}
	assertAccountRoles(t, dsn, account.ID, []string{"owner"})
	if strings.Count(logs.String(), `"msg":"write audit event"`) != 1 ||
		!strings.Contains(logs.String(), `"action":"role.assign"`) {
		t.Fatalf("grant-role audit failure log = %s", logs.String())
	}
}

func TestExecuteGrantRoleRefusesUnknownUserAndRole(t *testing.T) {
	dsn := prepareAdminDatabase(t)
	setAdminEnvironment(t, dsn)
	_ = createRolelessAccount(t, dsn, "legacy")
	tests := []struct {
		name, username, role, reason string
	}{
		{name: "unknown user", username: "missing", role: "owner", reason: "unknown_user"},
		{name: "unknown role", username: "legacy", role: "missing", reason: "unknown_role"},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			var audit strings.Builder
			err := Execute(context.Background(), []string{"grant-role", "--username", testCase.username, "--role", testCase.role},
				Streams{Log: io.Discard, Audit: &audit}, CommandInput{})
			if !errors.Is(err, core.ErrNotFound) {
				t.Fatalf("Execute(grant-role) = %v, want ErrNotFound", err)
			}
			if !strings.Contains(audit.String(), `"action":"role.assign"`) ||
				!strings.Contains(audit.String(), `"reason":"`+testCase.reason+`"`) {
				t.Fatalf("grant failure audit = %s", audit.String())
			}
		})
	}
}

func TestGrantRoleAuditOutcomes(t *testing.T) {
	storageErr := errors.New("storage unavailable")
	tests := []grantRoleAuditTestCase{
		{
			name: "invalid input", result: grantRoleResult{role: "owner"}, err: errGrantRoleInvalidInput,
			want: wantGrantRoleAudit("", "owner", telemetry.AuditFailure, "invalid_input"),
		},
		{
			name: "unknown user", result: grantRoleResult{role: "owner"}, err: core.ErrNotFound,
			want: wantGrantRoleAudit("", "owner", telemetry.AuditFailure, "unknown_user"),
		},
		{
			name: "unknown role", result: grantRoleResult{accountID: "account-1", role: "missing"}, err: core.ErrNotFound,
			want: wantGrantRoleAudit("account-1", "missing", telemetry.AuditFailure, "unknown_role"),
		},
		{
			name: "internal storage failure", result: grantRoleResult{accountID: "account-1", role: "owner"}, err: storageErr,
			want: wantGrantRoleAudit("account-1", "owner", telemetry.AuditFailure, "internal_error"),
		},
		{
			name: "success", result: grantRoleResult{accountID: "account-1", role: "owner", assigned: true},
			want: wantGrantRoleAudit("account-1", "owner", telemetry.AuditSuccess, "assigned"),
		},
		{
			name: "idempotent", result: grantRoleResult{accountID: "account-1", role: "owner"},
			want: wantGrantRoleAudit("account-1", "owner", telemetry.AuditSuccess, "already_held"),
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			audit := &captureAudit{}
			if err := emitGrantRoleAudit(context.Background(), audit, testCase.result, testCase.err); err != nil {
				t.Fatalf("emitGrantRoleAudit: %v", err)
			}
			if len(audit.events) != 1 || audit.events[0] != testCase.want {
				t.Fatalf("audit events = %+v, want [%+v]", audit.events, testCase.want)
			}
		})
	}
}

type grantRoleAuditTestCase struct {
	name   string
	result grantRoleResult
	err    error
	want   telemetry.AuditEvent
}

func wantGrantRoleAudit(accountID, role string, result telemetry.AuditResult, reason string) telemetry.AuditEvent {
	resource := "account:" + accountID
	if accountID == "" {
		resource = "account:unresolved"
	}
	return telemetry.AuditEvent{
		Actor: "cli", Action: telemetry.AuditActionRoleAssign, Resource: resource,
		Role: role, Result: result, Reason: reason, Source: "cli",
	}
}

func TestExecuteGrantRoleRejectsControlCharacters(t *testing.T) {
	dsn := prepareAdminDatabase(t)
	setAdminEnvironment(t, dsn)
	var audit strings.Builder
	invalidRole := "owner\nbad"
	err := Execute(context.Background(), []string{"grant-role", "--username", "legacy", "--role", invalidRole},
		Streams{Log: io.Discard, Audit: &audit}, CommandInput{})
	if !errors.Is(err, errGrantRoleInvalidInput) {
		t.Fatalf("Execute(control role) = %v, want invalid input", err)
	}
	var event map[string]any
	if err := json.Unmarshal([]byte(audit.String()), &event); err != nil {
		t.Fatalf("decode invalid-role audit: %v", err)
	}
	if event["role"] != "" || strings.Contains(audit.String(), invalidRole) {
		t.Fatalf("invalid-role audit contains unvalidated role: %s", audit.String())
	}
}

func createRolelessAccount(t *testing.T, dsn, username string) core.Account {
	t.Helper()
	pool, err := db.Open(context.Background(), adminDatabaseConfig(dsn))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = pool.Close() }()
	store, _, err := db.NewAccountStores(pool, config.DriverSQLite)
	if err != nil {
		t.Fatalf("NewAccountStores: %v", err)
	}
	account := core.Account{
		ID: mustRuntimeID(t), Username: username,
		CreatedAt: time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC),
	}
	if err := store.CreateAccount(context.Background(), account); err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	return account
}

func assertAccountRoles(t *testing.T, dsn, accountID string, want []string) {
	t.Helper()
	pool, err := db.Open(context.Background(), adminDatabaseConfig(dsn))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = pool.Close() }()
	authorizer, _, err := db.NewAuthorizationStores(pool, config.DriverSQLite)
	if err != nil {
		t.Fatalf("NewAuthorizationStores: %v", err)
	}
	snapshot, err := authorizer.Snapshot(context.Background(), accountID)
	if err != nil || !slices.Equal(snapshot.RoleNames, want) {
		t.Fatalf("Snapshot roles = %v, %v; want %v", snapshot.RoleNames, err, want)
	}
}

func mustRuntimeID(t *testing.T) string {
	t.Helper()
	id, err := core.NewID()
	if err != nil {
		t.Fatalf("NewID: %v", err)
	}
	return id
}
