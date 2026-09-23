package runtime

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/BonzTM/bloom/internal/config"
	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/telemetry"
)

func bootstrapAdmin(
	ctx context.Context,
	cfg config.BootstrapConfig,
	store core.BootstrapAccountStore,
	logger *slog.Logger,
	audit auditEmitter,
	clock core.Clock,
) error {
	if cfg.Password.Len() == 0 {
		return nil
	}
	account, err := newAdminAccount(cfg.Username, string(cfg.Password.Bytes()), clock)
	if err != nil {
		logBootstrapAuditFailure(ctx, logger, emitBootstrapAdminFailure(ctx, audit, err))
		return err
	}
	return finishAdminBootstrap(ctx, store, account, logger, audit)
}

func finishAdminBootstrap(
	ctx context.Context,
	store core.BootstrapAccountStore,
	account core.Account,
	logger *slog.Logger,
	audit auditEmitter,
) error {
	created, err := store.CreateFirstAccountWithRole(ctx, account, "owner")
	if err != nil {
		startupErr := fmt.Errorf("bootstrap admin: persist account: %w", err)
		logBootstrapAuditFailure(ctx, logger, emitBootstrapAdminFailure(ctx, audit, startupErr))
		return startupErr
	}
	if !created {
		logBootstrapPasswordRemoval(logger)
		return nil
	}
	logger.Info("admin account created", "account_id", account.ID, "username", account.Username)
	logBootstrapAuditFailure(ctx, logger, emitAdminCreationAudits(ctx, audit, account.ID, "system", "startup"))
	logBootstrapPasswordRemoval(logger)
	return nil
}

func emitBootstrapAdminFailure(ctx context.Context, audit auditEmitter, err error) error {
	auditErr := audit.Emit(ctx, telemetry.AuditEvent{
		Actor: "system", Action: "account.create_admin", Resource: "config:bootstrap-admin",
		Result: telemetry.AuditFailure, Reason: createAdminFailureReason(err), Source: "startup",
	})
	return newAuditWriteFailures(auditErr, nil)
}

func logBootstrapAuditFailure(ctx context.Context, logger *slog.Logger, err error) {
	if err == nil {
		return
	}
	logger.ErrorContext(ctx, "write audit event", "error", err, "action", auditFailureActions(err))
}

func logBootstrapPasswordRemoval(logger *slog.Logger) {
	logger.Info("remove BLOOM_BOOTSTRAP_PASSWORD from the environment; an account already exists")
}
