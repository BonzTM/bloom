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
	account, err := newAdminAccount(canonicalUsername, password, systemClock{})
	if err != nil {
		return core.Account{}, err
	}
	cfg, err := config.Load(nil)
	if err != nil {
		return core.Account{}, fmt.Errorf("load config: %w", err)
	}
	logger := telemetry.NewLogger(streams.Log, cfg.Telemetry)
	return createAdmin(ctx, cfg, account, logger)
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
	return string(password), nil
}

func createAdmin(
	ctx context.Context,
	cfg config.Config,
	account core.Account,
	logger *slog.Logger,
) (result core.Account, retErr error) {
	pool, err := db.Open(ctx, cfg.Database, logger)
	if err != nil {
		return core.Account{}, fmt.Errorf("create-admin: open database: %w", err)
	}
	defer closeAdminResource(&retErr, pool, logger)
	adminStore, err := db.NewAdminAccountStore(pool, cfg.Database.Driver)
	if err != nil {
		return core.Account{}, fmt.Errorf("create-admin: build role store: %w", err)
	}
	if err := persistAdminAccount(ctx, adminStore, account); err != nil {
		return account, err
	}
	logger.Info("admin account created", "account_id", account.ID, "username", account.Username)
	return account, nil
}

type adminAccountCreator interface {
	CreateAccountWithRole(context.Context, core.Account, string) error
}

func persistAdminAccount(ctx context.Context, store adminAccountCreator, account core.Account) error {
	if err := store.CreateAccountWithRole(ctx, account, "owner"); err != nil {
		return fmt.Errorf("create-admin: persist account: %w", err)
	}
	return nil
}

func closeAdminResource(result *error, closer io.Closer, logger *slog.Logger) {
	closeCommandDatabase(result, closer, logger, "create-admin")
}

func emitCreateAdminAudits(ctx context.Context, audit auditEmitter, accountID string, err error) error {
	if err == nil {
		return emitAdminCreationAudits(ctx, audit, accountID, "cli", "cli")
	}
	createErr := audit.Emit(ctx, telemetry.AuditEvent{
		Actor: "cli", Action: "account.create_admin", Resource: "command:create-admin",
		Result: telemetry.AuditFailure, Reason: createAdminFailureReason(err), Source: "cli",
	})
	return newAuditWriteFailures(createErr, nil)
}

func emitAdminCreationAudits(ctx context.Context, audit auditEmitter, accountID, actor, source string) error {
	createErr := audit.Emit(ctx, telemetry.AuditEvent{
		Actor: actor, Action: "account.create_admin", Resource: "account:" + accountID,
		Result: telemetry.AuditSuccess, Reason: "created", Source: source,
	})
	roleErr := audit.Emit(ctx, telemetry.RoleAssignmentAuditEvent(
		actor, accountID, "owner", telemetry.AuditSuccess, "assigned", source,
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
	if validationErr := core.ValidateNewPassword(password); validationErr != nil {
		return core.Account{}, validationErr
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
