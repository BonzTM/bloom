package runtime

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"strings"

	"github.com/BonzTM/bloom/internal/config"
	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/db"
	"github.com/BonzTM/bloom/internal/telemetry"
)

var errGrantRoleInvalidInput = errors.New("invalid grant-role input")

type grantRoleResult struct {
	accountID string
	role      string
	assigned  bool
}

func executeGrantRole(ctx context.Context, args []string, streams Streams) error {
	audit := telemetry.NewAuditLogger(streams.Audit, systemClock{})
	result, err := runGrantRole(ctx, args, streams)
	if auditErr := emitGrantRoleAudit(ctx, audit, result, err); auditErr != nil {
		telemetry.NewLogger(streams.Log, config.TelemetryConfig{}).ErrorContext(ctx, "write audit event",
			"error", auditErr, "action", telemetry.AuditActionRoleAssign)
	}
	return err
}

func runGrantRole(ctx context.Context, args []string, streams Streams) (grantRoleResult, error) {
	username, role, err := parseGrantRoleArgs(args, streams)
	if err != nil {
		return grantRoleResult{role: role}, err
	}
	cfg, err := config.Load(nil)
	if err != nil {
		return grantRoleResult{role: role}, fmt.Errorf("load config: %w", err)
	}
	logger := telemetry.NewLogger(streams.Log, cfg.Telemetry)
	return grantRole(ctx, cfg, username, role, logger)
}

func parseGrantRoleArgs(args []string, streams Streams) (string, string, error) {
	flags := flag.NewFlagSet("grant-role", flag.ContinueOnError)
	flags.SetOutput(consoleStream(streams))
	username := flags.String("username", "", "existing account username")
	role := flags.String("role", "", "role name to grant")
	if err := flags.Parse(args); err != nil {
		return "", "", fmt.Errorf("parse grant-role flags: %w", errors.Join(errGrantRoleInvalidInput, err))
	}
	if !core.ValidRoleName(*role) {
		return "", "", fmt.Errorf("grant-role: --username and --role are required: %w", errGrantRoleInvalidInput)
	}
	if flags.NArg() != 0 || strings.TrimSpace(*username) == "" {
		return "", *role, fmt.Errorf("grant-role: --username and --role are required: %w", errGrantRoleInvalidInput)
	}
	canonical, err := core.CanonicalUsername(*username)
	if err != nil {
		return "", *role, fmt.Errorf("grant-role: --username: %w", errors.Join(errGrantRoleInvalidInput, err))
	}
	return canonical, *role, nil
}

func grantRole(
	ctx context.Context,
	cfg config.Config,
	username, role string,
	logger *slog.Logger,
) (result grantRoleResult, retErr error) {
	result.role = role
	pool, err := db.Open(ctx, cfg.Database)
	if err != nil {
		return result, fmt.Errorf("grant-role: open database: %w", err)
	}
	defer closeCommandDatabase(&retErr, pool, logger, "grant-role")
	accounts, _, err := db.NewAccountStores(pool, cfg.Database.Driver)
	if err != nil {
		return result, fmt.Errorf("grant-role: build account store: %w", err)
	}
	roleStore, err := db.NewAdminAccountStore(pool, cfg.Database.Driver)
	if err != nil {
		return result, fmt.Errorf("grant-role: build role store: %w", err)
	}
	account, err := accounts.GetAccountByUsername(ctx, username)
	if err != nil {
		return result, fmt.Errorf("grant-role: find username %q: %w", username, err)
	}
	result.accountID = account.ID
	result.assigned, err = roleStore.GrantRole(ctx, account.ID, role)
	if err != nil {
		return result, fmt.Errorf("grant-role: grant %q: %w", role, err)
	}
	logger.Info("account role granted", "account_id", account.ID, "role", role, "assigned", result.assigned)
	return result, nil
}

func emitGrantRoleAudit(ctx context.Context, audit auditEmitter, result grantRoleResult, err error) error {
	auditResult := telemetry.AuditFailure
	if err == nil {
		auditResult = telemetry.AuditSuccess
	}
	event := telemetry.RoleAssignmentAuditEvent(
		"cli", result.accountID, result.role, auditResult, grantRoleFailureReason(result, err), "cli",
	)
	return audit.Emit(ctx, event)
}

func grantRoleFailureReason(result grantRoleResult, err error) string {
	if err == nil && result.assigned {
		return "assigned"
	}
	if err == nil {
		return "already_held"
	}
	if errors.Is(err, errGrantRoleInvalidInput) {
		return "invalid_input"
	}
	if errors.Is(err, core.ErrNotFound) && result.accountID == "" {
		return "unknown_user"
	}
	if errors.Is(err, core.ErrNotFound) {
		return "unknown_role"
	}
	return "internal_error"
}
