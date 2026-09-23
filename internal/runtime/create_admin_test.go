package runtime

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BonzTM/bloom/internal/config"
	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/db"
)

const adminTestSecret = "0123456789abcdef0123456789abcdef"

type failingWriter struct{ err error }

func (w failingWriter) Write([]byte) (int, error) { return 0, w.err }

type failingCloser struct{ err error }

func (c failingCloser) Close() error { return c.err }

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

func TestCreateAdminFailureReasonsAreFiniteAndTruthful(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{name: "invalid input", err: fmt.Errorf("bad flag: %w", errCreateAdminInvalidInput), want: "invalid_input"},
		{name: "password unavailable", err: fmt.Errorf("terminal: %w", errCreateAdminPasswordUnavailable), want: "password_unavailable"},
		{name: "empty password", err: core.ErrEmptyPassword, want: "invalid_password"},
		{name: "long password", err: core.ErrPasswordTooLong, want: "invalid_password"},
		{name: "duplicate", err: core.ErrAlreadyExists, want: "already_exists"},
		{name: "cancelled", err: context.Canceled, want: "internal_error"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := createAdminFailureReason(tt.err); got != tt.want {
				t.Errorf("createAdminFailureReason(%v) = %q, want %q", tt.err, got, tt.want)
			}
		})
	}
}

func TestRunCreateAdminClassifiesInputAndPasswordFailures(t *testing.T) {
	readerFailure := errors.New("terminal unavailable")
	tests := []struct {
		name  string
		args  []string
		input CommandInput
		want  string
	}{
		{name: "unsupported flag", args: []string{"--password", "secret"}, want: "invalid_input"},
		{name: "missing username", args: nil, want: "invalid_input"},
		{name: "blank username", args: []string{"--username", "  "}, want: "invalid_input"},
		{name: "short username", args: []string{"--username", "ab"}, want: "invalid_input"},
		{name: "leading separator", args: []string{"--username", "-owner"}, want: "invalid_input"},
		{name: "unsupported character", args: []string{"--username", "owner name"}, want: "invalid_input"},
		{name: "unexpected argument", args: []string{"--username", "owner", "extra"}, want: "invalid_input"},
		{name: "long username", args: []string{"--username", strings.Repeat("x", core.MaxUsernameCharacters+1)}, want: "invalid_input"},
		{name: "malformed username", args: []string{"--username", string([]byte{'o', 'w', 'n', 0xff, 'r'})}, want: "invalid_input"},
		{name: "missing password source", args: []string{"--username", "owner"}, want: "password_unavailable"},
		{name: "password read failure", args: []string{"--username", "owner"}, input: CommandInput{
			ReadPassword: func() ([]byte, error) { return nil, readerFailure },
		}, want: "password_unavailable"},
		{name: "empty password", args: []string{"--username", "owner"}, input: CommandInput{
			ReadPassword: func() ([]byte, error) { return nil, nil },
		}, want: "invalid_password"},
		{name: "short password", args: []string{"--username", "owner"}, input: CommandInput{
			ReadPassword: func() ([]byte, error) { return []byte("too-short"), nil },
		}, want: "invalid_password"},
		{name: "malformed password", args: []string{"--username", "owner"}, input: CommandInput{
			ReadPassword: func() ([]byte, error) { return append([]byte("long-enough-pass"), 0xff), nil },
		}, want: "invalid_password"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var audit strings.Builder
			err := executeCreateAdmin(context.Background(), tt.args, Streams{Log: io.Discard, Audit: &audit}, tt.input)
			if err == nil {
				t.Fatal("executeCreateAdmin succeeded, want failure")
			}
			if got := createAdminFailureReason(err); got != tt.want {
				t.Errorf("failure reason = %q, want %q (error %v)", got, tt.want, err)
			}
			if !strings.Contains(audit.String(), `"reason":"`+tt.want+`"`) {
				t.Errorf("audit = %s, want reason %q", audit.String(), tt.want)
			}
		})
	}
}

func TestExecuteCreateAdminRejectsShortPasswordSourcesWithoutAccount(t *testing.T) {
	tests := []struct {
		name  string
		input func(*testing.T) CommandInput
	}{
		{name: "environment", input: func(t *testing.T) CommandInput {
			t.Helper()
			t.Setenv("BLOOM_BOOTSTRAP_PASSWORD", "too-short")
			return CommandInput{ReadPassword: func() ([]byte, error) {
				return []byte(os.Getenv("BLOOM_BOOTSTRAP_PASSWORD")), nil
			}}
		}},
		{name: "terminal reader", input: func(t *testing.T) CommandInput {
			t.Helper()
			return CommandInput{ReadPassword: func() ([]byte, error) { return []byte("too-short"), nil }}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dsn := prepareAdminDatabase(t)
			setAdminEnvironment(t, dsn)
			var audit strings.Builder
			err := Execute(context.Background(), []string{"create-admin", "--username", "owner"},
				Streams{Log: io.Discard, Audit: &audit}, tt.input(t))
			if !errors.Is(err, core.ErrPasswordTooShort) {
				t.Fatalf("Execute(short password) = %v, want ErrPasswordTooShort", err)
			}
			if !strings.Contains(audit.String(), `"reason":"invalid_password"`) {
				t.Fatalf("audit = %s, want invalid_password", audit.String())
			}
			assertAdminAccountAbsent(t, dsn, "owner")
		})
	}
}

func TestExecuteCreateAdminRejectsMalformedPasswordSources(t *testing.T) {
	malformed := string(append([]byte("long-enough-pass"), 0xff))
	tests := []struct {
		name  string
		input func(*testing.T) CommandInput
	}{
		{name: "environment", input: func(t *testing.T) CommandInput {
			t.Helper()
			t.Setenv("BLOOM_BOOTSTRAP_PASSWORD", malformed)
			return CommandInput{ReadPassword: func() ([]byte, error) {
				return []byte(os.Getenv("BLOOM_BOOTSTRAP_PASSWORD")), nil
			}}
		}},
		{name: "terminal", input: func(*testing.T) CommandInput {
			return CommandInput{ReadPassword: func() ([]byte, error) { return []byte(malformed), nil }}
		}},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			err := Execute(context.Background(), []string{"create-admin", "--username", "owner"},
				Streams{Log: io.Discard, Audit: io.Discard}, testCase.input(t))
			if !errors.Is(err, core.ErrInvalidText) {
				t.Fatalf("Execute(malformed password) = %v, want ErrInvalidText", err)
			}
		})
	}
}

func TestExecuteCreateAdminDoesNotLogCompromisedPassword(t *testing.T) {
	const password = "Mailcreated5240"
	var log, audit, console strings.Builder
	input := CommandInput{ReadPassword: func() ([]byte, error) { return []byte(password), nil }}
	err := Execute(context.Background(), []string{"create-admin", "--username", "owner"},
		Streams{Log: &log, Audit: &audit, Console: &console}, input)
	if !errors.Is(err, core.ErrPasswordCompromised) {
		t.Fatalf("Execute(compromised password) = %v, want ErrPasswordCompromised", err)
	}
	if output := log.String() + audit.String() + console.String(); strings.Contains(output, password) {
		t.Fatalf("output leaked password: %s", output)
	}
}

func TestExecuteCreateAdminValidatesPasswordBeforeOpeningDatabase(t *testing.T) {
	t.Setenv("BLOOM_SECRET_KEY", adminTestSecret)
	t.Setenv("BLOOM_DB_DRIVER", "sqlite")
	t.Setenv("BLOOM_DB_DSN", "file:"+filepath.Join(t.TempDir(), "missing", "bloom.db"))
	t.Setenv("BLOOM_DB_MAX_OPEN_CONNS", "1")
	t.Setenv("BLOOM_DB_MAX_IDLE_CONNS", "1")
	input := CommandInput{ReadPassword: func() ([]byte, error) { return []byte("too-short"), nil }}
	err := Execute(context.Background(), []string{"create-admin", "--username", "owner"},
		Streams{Log: io.Discard, Audit: io.Discard}, input)
	if !errors.Is(err, core.ErrPasswordTooShort) {
		t.Fatalf("Execute(short password, inaccessible database) = %v, want ErrPasswordTooShort", err)
	}
}

func assertAdminAccountAbsent(t *testing.T, dsn, username string) {
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
	if _, err := store.GetAccountByUsername(context.Background(), username); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("GetAccountByUsername(%q) = %v, want ErrNotFound", username, err)
	}
}

func prepareAdminDatabase(t *testing.T) string {
	t.Helper()
	dsn := "file:" + filepath.Join(t.TempDir(), "bloom.db")
	pool, err := db.Open(context.Background(), adminDatabaseConfig(dsn))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := db.Migrate(context.Background(), pool, config.DriverSQLite); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if err := pool.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	return dsn
}

func adminDatabaseConfig(dsn string) config.DatabaseConfig {
	return config.DatabaseConfig{
		Driver: config.DriverSQLite, DSN: dsn, MaxOpenConns: 1, MaxIdleConns: 1,
		ConnMaxLifetime: time.Hour, ConnMaxIdleTime: time.Hour,
	}
}

func setAdminEnvironment(t *testing.T, dsn string) {
	t.Helper()
	t.Setenv("BLOOM_SECRET_KEY", adminTestSecret)
	t.Setenv("BLOOM_DB_DRIVER", "sqlite")
	t.Setenv("BLOOM_DB_DSN", dsn)
	t.Setenv("BLOOM_DB_MAX_OPEN_CONNS", "1")
	t.Setenv("BLOOM_DB_MAX_IDLE_CONNS", "1")
}
