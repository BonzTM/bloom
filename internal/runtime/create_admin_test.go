package runtime

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"slices"
	"strings"
	"testing"

	"github.com/BonzTM/bloom/internal/config"
	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/db"
	"github.com/BonzTM/bloom/internal/telemetry"
)

type failingCloser struct{ err error }

func (c failingCloser) Close() error { return c.err }

type actionFailingAudit struct {
	events []telemetry.AuditEvent
	errors map[string]error
}

func (a *actionFailingAudit) Emit(_ context.Context, event telemetry.AuditEvent) error {
	a.events = append(a.events, event)
	return a.errors[event.Action]
}

func TestExecuteCreateAdminFromInjectedPassword(t *testing.T) {
	dsn := prepareAdminDatabase(t)
	setAdminEnvironment(t, dsn)
	input := CommandInput{ReadPassword: func() ([]byte, error) { return []byte("bootstrap-secret"), nil }}

	if err := Execute(context.Background(), []string{"create-admin", "--username", "owner"}, Streams{Log: io.Discard, Audit: io.Discard}, input); err != nil {
		t.Fatalf("Execute(create-admin): %v", err)
	}

	pool, err := db.Open(context.Background(), adminDatabaseConfig(dsn))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = pool.Close() }()
	store, _, err := db.NewAccountStores(pool, config.DriverSQLite)
	if err != nil {
		t.Fatalf("NewAccountStores: %v", err)
	}
	account, err := store.GetAccountByUsername(context.Background(), "owner")
	if err != nil {
		t.Fatalf("GetAccountByUsername: %v", err)
	}
	if account.PasswordHash == nil || account.Disabled {
		t.Fatalf("account = %+v", account)
	}
	authorizer, _, err := db.NewAuthorizationStores(pool, config.DriverSQLite)
	if err != nil {
		t.Fatalf("NewAuthorizationStores: %v", err)
	}
	snapshot, err := authorizer.Snapshot(context.Background(), account.ID)
	if err != nil || !slices.Contains(snapshot.Permissions, core.PermissionAdminRoles) ||
		len(snapshot.RoleNames) != 1 || snapshot.RoleNames[0] != "owner" {
		t.Fatalf("admin authorization = %+v, %v", snapshot, err)
	}
	matched, err := core.VerifyPassword(*account.PasswordHash, "bootstrap-secret")
	if err != nil || !matched {
		t.Fatalf("VerifyPassword = %v, %v", matched, err)
	}
}

func TestExecuteCreateAdminRefusesDuplicate(t *testing.T) {
	dsn := prepareAdminDatabase(t)
	setAdminEnvironment(t, dsn)
	input := CommandInput{ReadPassword: func() ([]byte, error) { return []byte("bootstrap-secret"), nil }}
	args := []string{"create-admin", "--username", "owner"}
	if err := Execute(context.Background(), args, Streams{Log: io.Discard, Audit: io.Discard}, input); err != nil {
		t.Fatalf("first Execute: %v", err)
	}
	if err := Execute(context.Background(), args, Streams{Log: io.Discard, Audit: io.Discard}, input); !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("duplicate Execute = %v, want already exists", err)
	}
}

func TestExecuteCreateAdminCanonicalizesUsername(t *testing.T) {
	dsn := prepareAdminDatabase(t)
	setAdminEnvironment(t, dsn)
	input := CommandInput{ReadPassword: func() ([]byte, error) { return []byte("bootstrap-secret"), nil }}
	if err := Execute(context.Background(), []string{"create-admin", "--username", "ＯWNER"},
		Streams{Log: io.Discard, Audit: io.Discard}, input); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	pool, err := db.Open(context.Background(), adminDatabaseConfig(dsn))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = pool.Close() }()
	store, _, err := db.NewAccountStores(pool, config.DriverSQLite)
	if err != nil {
		t.Fatalf("NewAccountStores: %v", err)
	}
	account, err := store.GetAccountByUsername(context.Background(), "Owner")
	if err != nil || account.Username != "owner" {
		t.Fatalf("canonical account = %+v, %v; want owner", account, err)
	}
}

func TestExecuteCreateAdminReadsTerminal(t *testing.T) {
	dsn := prepareAdminDatabase(t)
	setAdminEnvironment(t, dsn)
	called := false
	input := CommandInput{ReadPassword: func() ([]byte, error) {
		called = true
		return []byte("terminal-secret"), nil
	}}
	if err := Execute(context.Background(), []string{"create-admin", "--username", "owner"}, Streams{Log: io.Discard, Audit: io.Discard}, input); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !called {
		t.Fatal("terminal password reader was not called")
	}
}

func TestExecuteCreateAdminRequiresPasswordAndUsername(t *testing.T) {
	dsn := prepareAdminDatabase(t)
	setAdminEnvironment(t, dsn)

	if err := Execute(context.Background(), []string{"create-admin", "--username", "owner"}, Streams{Log: io.Discard, Audit: io.Discard}, CommandInput{}); err == nil {
		t.Fatal("Execute without password succeeded")
	}
	input := CommandInput{ReadPassword: func() ([]byte, error) { return []byte("bootstrap-secret"), nil }}
	if err := Execute(context.Background(), []string{"create-admin"}, Streams{Log: io.Discard, Audit: io.Discard}, input); err == nil {
		t.Fatal("Execute without username succeeded")
	}
	if err := Execute(context.Background(), []string{"create-admin", "--password", "leak"}, Streams{Log: io.Discard, Audit: io.Discard}, CommandInput{}); err == nil {
		t.Fatal("Execute accepted a password flag")
	}
}

func TestExecuteCreateAdminAuditsSuccessAndFailure(t *testing.T) {
	dsn := prepareAdminDatabase(t)
	setAdminEnvironment(t, dsn)
	input := CommandInput{ReadPassword: func() ([]byte, error) { return []byte("bootstrap-secret"), nil }}
	var audit strings.Builder
	streams := Streams{Log: io.Discard, Audit: &audit}
	args := []string{"create-admin", "--username", "owner"}
	if err := Execute(context.Background(), args, streams, input); err != nil {
		t.Fatalf("success Execute: %v", err)
	}
	if !strings.Contains(audit.String(), `"action":"account.create_admin"`) ||
		!strings.Contains(audit.String(), `"action":"role.assign"`) ||
		!strings.Contains(audit.String(), `"result":"success"`) ||
		!strings.Contains(audit.String(), `"source":"cli"`) {
		t.Fatalf("success audit = %s", audit.String())
	}
	audit.Reset()
	if err := Execute(context.Background(), args, streams, input); err == nil {
		t.Fatal("duplicate Execute succeeded")
	}
	if !strings.Contains(audit.String(), `"result":"failure"`) ||
		!strings.Contains(audit.String(), `"reason":"already_exists"`) {
		t.Fatalf("failure audit = %s", audit.String())
	}
}

func TestCreateAdminAttemptsBothSuccessAuditsAndJoinsFailures(t *testing.T) {
	createErr := errors.New("create audit failed")
	roleErr := errors.New("role audit failed")
	audit := &actionFailingAudit{errors: map[string]error{
		"account.create_admin":          createErr,
		telemetry.AuditActionRoleAssign: roleErr,
	}}
	err := emitCreateAdminAudits(context.Background(), audit, "account-1", nil)
	if !errors.Is(err, createErr) || !errors.Is(err, roleErr) {
		t.Fatalf("emitCreateAdminAudits = %v, want both failures", err)
	}
	wantRole := telemetry.AuditEvent{
		Actor: "cli", Action: telemetry.AuditActionRoleAssign, Resource: "account:account-1",
		Role: "owner", Result: telemetry.AuditSuccess, Reason: "assigned", Source: "cli",
	}
	if len(audit.events) != 2 || audit.events[0].Action != "account.create_admin" || audit.events[1] != wantRole {
		t.Fatalf("audit attempts = %+v", audit.events)
	}
	if actions := auditFailureActions(err); !slices.Equal(actions, []string{"account.create_admin", telemetry.AuditActionRoleAssign}) {
		t.Fatalf("failed actions = %v", actions)
	}
}

func TestExecuteCreateAdminAuditSinkFailureIsLoggedOnceAndPreservesResult(t *testing.T) {
	sinkErr := errors.New("audit unavailable")
	tests := []struct {
		name      string
		password  string
		wantError error
	}{
		{name: "command success", password: "bootstrap-secret"},
		{name: "command failure", password: "too-short", wantError: core.ErrPasswordTooShort},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			dsn := prepareAdminDatabase(t)
			setAdminEnvironment(t, dsn)
			var logs strings.Builder
			input := CommandInput{ReadPassword: func() ([]byte, error) { return []byte(testCase.password), nil }}
			err := Execute(context.Background(), []string{"create-admin", "--username", "owner"},
				Streams{Log: &logs, Audit: failingWriter{err: sinkErr}}, input)
			if testCase.wantError == nil && err != nil {
				t.Fatalf("Execute = %v, want success", err)
			}
			if testCase.wantError != nil && !errors.Is(err, testCase.wantError) {
				t.Fatalf("Execute = %v, want %v", err, testCase.wantError)
			}
			if got := strings.Count(logs.String(), `"msg":"write audit event"`); got != 1 {
				t.Fatalf("operational audit errors = %d, want 1: %s", got, logs.String())
			}
			if strings.Contains(logs.String(), testCase.password) {
				t.Fatalf("operational log leaked password: %s", logs.String())
			}
		})
	}
}

func TestCloseAdminResourceHandlesFailure(t *testing.T) {
	closeErr := errors.New("close failed")
	t.Run("joins an existing command error", func(t *testing.T) {
		commandErr := errors.New("create failed")
		result := commandErr
		closeAdminResource(&result, failingCloser{err: closeErr}, slog.New(slog.DiscardHandler))
		if !errors.Is(result, commandErr) || !errors.Is(result, closeErr) {
			t.Fatalf("close result = %v, want both errors", result)
		}
	})
	t.Run("logs after successful creation", func(t *testing.T) {
		var logs strings.Builder
		var result error
		closeAdminResource(&result, failingCloser{err: closeErr}, slog.New(slog.NewJSONHandler(&logs, nil)))
		if result != nil {
			t.Fatalf("close changed successful command result: %v", result)
		}
		if got := strings.Count(logs.String(), `"msg":"close create-admin database"`); got != 1 {
			t.Fatalf("close logs = %d, want 1: %s", got, logs.String())
		}
	})
}
