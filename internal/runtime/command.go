package runtime

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"strings"

	"github.com/BonzTM/bloom/internal/config"
	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/db"
	"github.com/BonzTM/bloom/internal/telemetry"
)

// CommandInput supplies password input resolved at the process boundary from
// the bootstrap environment secret or a terminal. A nil reader means neither
// source is available.
type CommandInput struct {
	ReadPassword func() ([]byte, error)
}

var (
	errCreateAdminInvalidInput        = errors.New("invalid create-admin input")
	errCreateAdminPasswordUnavailable = errors.New("create-admin password unavailable")
)

// Execute dispatches the create-admin subcommand or starts the service.
func Execute(ctx context.Context, args []string, streams Streams, input CommandInput) error {
	if len(args) > 0 && args[0] == "create-admin" {
		return executeCreateAdmin(ctx, args[1:], streams, input)
	}
	cfg, err := config.Load(args)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	return Run(ctx, cfg, streams)
}

func executeCreateAdmin(ctx context.Context, args []string, streams Streams, input CommandInput) error {
	audit := telemetry.NewAuditLogger(streams.Audit, systemClock{})
	account, err := runCreateAdmin(ctx, args, streams, input)
	if auditErr := emitCreateAdminAudit(ctx, audit, account.ID, err); auditErr != nil {
		telemetry.NewLogger(streams.Log, config.TelemetryConfig{}).ErrorContext(ctx, "write audit event", "error", auditErr, "action", "account.create_admin")
	}
	return err
}

func runCreateAdmin(ctx context.Context, args []string, streams Streams, input CommandInput) (core.Account, error) {
	flags := flag.NewFlagSet("create-admin", flag.ContinueOnError)
	flags.SetOutput(consoleStream(streams))
	username := flags.String("username", "", "username for the first administrator")
	if err := flags.Parse(args); err != nil {
		return core.Account{}, fmt.Errorf("parse create-admin flags: %w", errors.Join(errCreateAdminInvalidInput, err))
	}
	if flags.NArg() != 0 || strings.TrimSpace(*username) == "" {
		return core.Account{}, fmt.Errorf("create-admin: --username is required and must not be blank: %w", errCreateAdminInvalidInput)
	}
	canonicalUsername, err := core.CanonicalUsername(*username)
	if err != nil {
		return core.Account{}, fmt.Errorf("create-admin: --username: %w", errors.Join(errCreateAdminInvalidInput, err))
	}
	password, err := bootstrapPassword(input)
	if err != nil {
		return core.Account{}, err
	}
	cfg, err := config.Load(nil)
	if err != nil {
		return core.Account{}, fmt.Errorf("load config: %w", err)
	}
	logger := telemetry.NewLogger(streams.Log, cfg.Telemetry)
	return createAdmin(ctx, cfg, canonicalUsername, password, logger, systemClock{})
}

func consoleStream(streams Streams) io.Writer {
	if streams.Console != nil {
		return streams.Console
	}
	return streams.Log
}

func bootstrapPassword(input CommandInput) (string, error) {
	if input.ReadPassword == nil {
		return "", fmt.Errorf("create-admin: no password source is available: %w", errCreateAdminPasswordUnavailable)
	}
	password, err := input.ReadPassword()
	if err != nil {
		return "", fmt.Errorf("create-admin: read password: %w", errors.Join(errCreateAdminPasswordUnavailable, err))
	}
	if len(password) == 0 {
		return "", core.ErrEmptyPassword
	}
	value := string(password)
	if err := core.ValidateNewPassword(value); err != nil {
		return "", err
	}
	return value, nil
}

func createAdmin(
	ctx context.Context,
	cfg config.Config,
	username, password string,
	logger *slog.Logger,
	clock core.Clock,
) (account core.Account, retErr error) {
	pool, err := db.Open(ctx, cfg.Database)
	if err != nil {
		return core.Account{}, fmt.Errorf("create-admin: open database: %w", err)
	}
	defer closeAdminResource(&retErr, pool, logger)
	store, _, err := db.NewAccountStores(pool, cfg.Database.Driver)
	if err != nil {
		return core.Account{}, fmt.Errorf("create-admin: build account store: %w", err)
	}
	if existing, lookupErr := store.GetAccountByUsername(ctx, username); lookupErr == nil {
		return existing, fmt.Errorf("create-admin: username %q: %w", username, core.ErrAlreadyExists)
	} else if !errors.Is(lookupErr, core.ErrNotFound) {
		return core.Account{}, fmt.Errorf("create-admin: check username: %w", lookupErr)
	}
	account, err = newAdminAccount(username, password, clock)
	if err != nil {
		return core.Account{}, err
	}
	if err := store.CreateAccount(ctx, account); err != nil {
		return account, fmt.Errorf("create-admin: persist account: %w", err)
	}
	logger.Info("admin account created", "account_id", account.ID, "username", account.Username)
	return account, nil
}

func closeAdminResource(result *error, closer io.Closer, logger *slog.Logger) {
	if result == nil || closer == nil || logger == nil {
		return
	}
	err := closer.Close()
	if err == nil {
		return
	}
	wrapped := fmt.Errorf("close create-admin database: %w", err)
	if *result != nil {
		*result = errors.Join(*result, wrapped)
		return
	}
	logger.Error("close create-admin database", "error", err)
}

func emitCreateAdminAudit(ctx context.Context, audit auditEmitter, accountID string, err error) error {
	result := telemetry.AuditSuccess
	reason := "created"
	resource := "account:" + accountID
	if err != nil {
		result = telemetry.AuditFailure
		reason = createAdminFailureReason(err)
		resource = "command:create-admin"
	}
	return audit.Emit(ctx, telemetry.AuditEvent{
		Actor: "cli", Action: "account.create_admin", Resource: resource,
		Result: result, Reason: reason, Source: "cli",
	})
}

func createAdminFailureReason(err error) string {
	switch {
	case errors.Is(err, errCreateAdminInvalidInput):
		return "invalid_input"
	case errors.Is(err, errCreateAdminPasswordUnavailable):
		return "password_unavailable"
	case errors.Is(err, core.ErrAlreadyExists):
		return "already_exists"
	case errors.Is(err, core.ErrEmptyPassword), errors.Is(err, core.ErrPasswordTooShort), errors.Is(err, core.ErrPasswordTooLong),
		errors.Is(err, core.ErrPasswordCompromised), errors.Is(err, core.ErrInvalidText):
		return "invalid_password"
	default:
		return "internal_error"
	}
}

type auditEmitter interface {
	Emit(context.Context, telemetry.AuditEvent) error
}

func newAdminAccount(username, password string, clock core.Clock) (core.Account, error) {
	canonicalUsername, err := core.CanonicalUsername(username)
	if err != nil {
		return core.Account{}, fmt.Errorf("create-admin: canonicalize username: %w", err)
	}
	hash, err := core.HashPassword(password)
	if err != nil {
		return core.Account{}, fmt.Errorf("create-admin: hash password: %w", err)
	}
	id, err := core.NewID()
	if err != nil {
		return core.Account{}, fmt.Errorf("create-admin: generate account id: %w", err)
	}
	return core.Account{
		ID: id, Username: canonicalUsername, PasswordHash: &hash,
		CreatedAt: core.NormalizeTime(clock.Now()),
	}, nil
}
