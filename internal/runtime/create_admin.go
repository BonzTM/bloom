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

var (
	errCreateAdminInvalidInput        = errors.New("invalid create-admin input")
	errCreateAdminPasswordUnavailable = errors.New("create-admin password unavailable")
)

func executeCreateAdmin(ctx context.Context, args []string, streams Streams, input CommandInput) error {
	audit := telemetry.NewAuditLogger(streams.Audit, systemClock{})
	account, err := runCreateAdmin(ctx, args, streams, input)
	if auditErr := emitCreateAdminAudits(ctx, audit, account.ID, err); auditErr != nil {
		telemetry.NewLogger(streams.Log, config.TelemetryConfig{}).ErrorContext(ctx, "write audit event",
			"error", auditErr, "action", auditFailureActions(auditErr))
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
	pool, err := db.Open(ctx, cfg.Database, logger)
	if err != nil {
		return core.Account{}, fmt.Errorf("create-admin: open database: %w", err)
	}
	defer closeAdminResource(&retErr, pool, logger)
	store, _, err := db.NewAccountStores(pool, cfg.Database.Driver)
	if err != nil {
		return core.Account{}, fmt.Errorf("create-admin: build account store: %w", err)
	}
	adminStore, err := db.NewAdminAccountStore(pool, cfg.Database.Driver)
	if err != nil {
		return core.Account{}, fmt.Errorf("create-admin: build role store: %w", err)
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
	if err := adminStore.CreateAccountWithRole(ctx, account, "owner"); err != nil {
		return account, fmt.Errorf("create-admin: persist account: %w", err)
	}
	logger.Info("admin account created", "account_id", account.ID, "username", account.Username)
	return account, nil
}

func closeAdminResource(result *error, closer io.Closer, logger *slog.Logger) {
	closeCommandDatabase(result, closer, logger, "create-admin")
}

func emitCreateAdminAudits(ctx context.Context, audit auditEmitter, accountID string, err error) error {
	result := telemetry.AuditSuccess
	reason := "created"
	resource := "account:" + accountID
	if err != nil {
		result = telemetry.AuditFailure
		reason = createAdminFailureReason(err)
		resource = "command:create-admin"
	}
	createErr := audit.Emit(ctx, telemetry.AuditEvent{
		Actor: "cli", Action: "account.create_admin", Resource: resource,
		Result: result, Reason: reason, Source: "cli",
	})
	if err != nil {
		return newAuditWriteFailures(createErr, nil)
	}
	roleErr := audit.Emit(ctx, telemetry.RoleAssignmentAuditEvent(
		"cli", accountID, "owner", telemetry.AuditSuccess, "assigned", "cli",
	))
	return newAuditWriteFailures(createErr, roleErr)
}

type auditWriteFailures struct {
	createAdmin error
	roleAssign  error
}

func newAuditWriteFailures(createAdmin, roleAssign error) error {
	if createAdmin == nil && roleAssign == nil {
		return nil
	}
	return &auditWriteFailures{createAdmin: createAdmin, roleAssign: roleAssign}
}

func (e *auditWriteFailures) Error() string {
	return errors.Join(e.createAdmin, e.roleAssign).Error()
}

func (e *auditWriteFailures) Unwrap() []error {
	errs := make([]error, 0, 2)
	if e.createAdmin != nil {
		errs = append(errs, e.createAdmin)
	}
	if e.roleAssign != nil {
		errs = append(errs, e.roleAssign)
	}
	return errs
}

func auditFailureActions(err error) []string {
	var failures *auditWriteFailures
	if !errors.As(err, &failures) {
		return []string{"unknown"}
	}
	actions := make([]string, 0, 2)
	if failures.createAdmin != nil {
		actions = append(actions, "account.create_admin")
	}
	if failures.roleAssign != nil {
		actions = append(actions, telemetry.AuditActionRoleAssign)
	}
	return actions
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
