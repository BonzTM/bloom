package runtime

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BonzTM/bloom/internal/config"
	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/db"
)

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
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			if got := createAdminFailureReason(testCase.err); got != testCase.want {
				t.Errorf("createAdminFailureReason(%v) = %q, want %q", testCase.err, got, testCase.want)
			}
		})
	}
}

func TestRunCreateAdminClassifiesInputAndPasswordFailures(t *testing.T) {
	readerFailure := errors.New("terminal unavailable")
	tests := createAdminInputFailureCases(readerFailure)
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			var audit strings.Builder
			err := executeCreateAdmin(context.Background(), testCase.args,
				Streams{Log: io.Discard, Audit: &audit}, testCase.input)
			if err == nil {
				t.Fatal("executeCreateAdmin succeeded, want failure")
			}
			if got := createAdminFailureReason(err); got != testCase.want {
				t.Errorf("failure reason = %q, want %q (error %v)", got, testCase.want, err)
			}
			if !strings.Contains(audit.String(), `"reason":"`+testCase.want+`"`) {
				t.Errorf("audit = %s, want reason %q", audit.String(), testCase.want)
			}
		})
	}
}

type createAdminInputFailureCase struct {
	name  string
	args  []string
	input CommandInput
	want  string
}

func createAdminInputFailureCases(readerFailure error) []createAdminInputFailureCase {
	return []createAdminInputFailureCase{
		{name: "unsupported flag", args: []string{"--password", "secret"}, want: "invalid_input"},
		{name: "missing username", want: "invalid_input"},
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
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			dsn := prepareAdminDatabase(t)
			setAdminEnvironment(t, dsn)
			var audit strings.Builder
			err := Execute(context.Background(), []string{"create-admin", "--username", "owner"},
				Streams{Log: io.Discard, Audit: &audit}, testCase.input(t))
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
