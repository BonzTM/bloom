package db

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/BonzTM/bloom/internal/config"
	"github.com/BonzTM/bloom/internal/core"
)

func provisioningFailureRecordError(err error) error {
	return fmt.Errorf("insert invite provisioning failure: %w", errors.Join(core.ErrInviteProvisioningFailureRecord, err))
}

func redeemTransactionError(provisioningErr *core.InviteProvisioningError, transactionErr error) error {
	if transactionErr == nil {
		if provisioningErr == nil {
			return nil
		}
		return provisioningErr
	}
	if provisioningErr == nil {
		return transactionErr
	}
	return errors.Join(provisioningErr.Err, transactionErr)
}

func dismissalConflict(exists bool, err error) error {
	if err != nil {
		return fmt.Errorf("inspect invite provisioning failure after dismissal: %w", err)
	}
	if exists {
		return core.ErrInviteProvisioningFailureLeased
	}
	return core.ErrNotFound
}

const maxInviteQueryPageSize = 101

var dummyInviteCodeHash = sha256.Sum256([]byte("bloom fixed-work invite lookup dummy"))

func dummyInviteLookup() core.InviteCodeLookup {
	stamp := time.Unix(0, 0).UTC()
	return core.InviteCodeLookup{
		Invite: core.Invite{
			ID:            "00000000-0000-4000-8000-000000000000",
			MediaServerID: "00000000-0000-4000-8000-000000000001",
			CreatedBy:     "00000000-0000-4000-8000-000000000002",
			Label:         "Unavailable", CreatedAt: stamp, UpdatedAt: stamp,
		},
		CodeHash: dummyInviteCodeHash,
	}
}

// NewInviteStores returns the invite read and serialized-write seams.
func NewInviteStores(pool *sql.DB, driver config.Driver) (core.InviteReader, core.InviteStore, error) {
	if pool == nil {
		return nil, nil, fmt.Errorf("invite stores: %w", core.ErrInvalidArgument)
	}
	switch driver {
	case config.DriverSQLite:
		adapter := newSQLiteInvites(pool)
		return adapter, adapter, nil
	case config.DriverPostgres:
		adapter := newPostgresInvites(pool)
		return adapter, adapter, nil
	default:
		return nil, nil, fmt.Errorf("unsupported database driver %q", driver)
	}
}

func validateInviteForStorage(invite core.Invite) error {
	if err := core.ValidateInvite(invite, invite.CreatedAt.Add(-time.Microsecond)); err != nil {
		return err
	}
	if invite.UseCount != 0 || invite.RevokedAt != nil || !invite.UpdatedAt.Equal(invite.CreatedAt) {
		return core.ErrInvalidArgument
	}
	return nil
}

func validateInvitePage(after *core.InviteCursor, pageSize int) error {
	if pageSize < 1 || pageSize > maxInviteQueryPageSize {
		return core.ErrInvalidArgument
	}
	if after != nil && (!core.ValidID(after.ID) || after.CreatedAt.IsZero()) {
		return core.ErrInvalidArgument
	}
	return nil
}

func validateRedemption(invite core.Invite, redemption core.InviteRedemption) error {
	if !core.ValidID(redemption.ID) || redemption.InviteID != invite.ID ||
		redemption.MediaServerID != invite.MediaServerID || !core.ValidAccountMediaUserID(redemption.MediaUserID) ||
		redemption.RedeemedAt.IsZero() {
		return core.ErrInvalidArgument
	}
	if redemption.AccountID != "" && !core.ValidID(redemption.AccountID) {
		return core.ErrInvalidArgument
	}
	return core.ValidateJellyfinUsername(redemption.Username)
}

func validateProvisioningFailure(failure core.InviteProvisioningFailure) error {
	if !core.ValidID(failure.ID) || !core.ValidID(failure.InviteID) || !core.ValidID(failure.MediaServerID) ||
		failure.CreatedAt.IsZero() || failure.UpdatedAt.IsZero() || !failure.UpdatedAt.Equal(failure.CreatedAt) {
		return core.ErrInvalidArgument
	}
	if failure.MediaUserID != "" && (len(failure.MediaUserID) > 128 || !core.ValidID(failure.MediaUserID)) {
		return core.ErrInvalidArgument
	}
	if failure.MediaUserOwned && failure.MediaUserID == "" {
		return core.ErrInvalidArgument
	}
	if failure.AccountID != "" && !core.ValidID(failure.AccountID) {
		return core.ErrInvalidArgument
	}
	if err := core.ValidateJellyfinUsername(failure.Username); err != nil {
		return err
	}
	if failure.Reason != core.InviteProvisioningCleanupFailed &&
		failure.Reason != core.InviteProvisioningCreateAmbiguous {
		return core.ErrInvalidArgument
	}
	if failure.Reason == core.InviteProvisioningCleanupFailed && !failure.MediaUserOwned {
		return core.ErrInvalidArgument
	}
	if !failure.MediaUserOwned && (!failure.Terminal || failure.LastError != core.InviteManualResolutionError) {
		return core.ErrInvalidArgument
	}
	if len(failure.LastError) > core.MaxInviteProvisioningErrorBytes {
		return core.ErrInvalidArgument
	}
	return nil
}

func validateProvisioningLease(lease core.InviteProvisioningLease, at time.Time) error {
	if !core.ValidID(lease.Token) || at.IsZero() || !lease.ExpiresAt.After(at) {
		return core.ErrInvalidArgument
	}
	return nil
}

func validateProvisioningFailurePage(after *core.InviteProvisioningFailureCursor, pageSize int) error {
	if pageSize < 1 || pageSize > maxInviteQueryPageSize {
		return core.ErrInvalidArgument
	}
	if after != nil && (!core.ValidID(after.ID) || after.CreatedAt.IsZero()) {
		return core.ErrInvalidArgument
	}
	return nil
}

func completeProvisioningPolicy(
	ctx context.Context, failure core.InviteProvisioningFailure, redemption core.InviteRedemption,
	invite core.Invite, insertRedemption func(context.Context, core.InviteRedemption) error,
	insertLink func(context.Context, core.InviteRedemption) (bool, error), increment func(context.Context) error,
	complete func(context.Context) error,
) error {
	if !failure.MediaUserOwned || failure.LeaseToken == "" || redemption.AccountID != failure.AccountID {
		return core.ErrInvalidArgument
	}
	if err := validateRedemption(invite, redemption); err != nil {
		return fmt.Errorf("validate reconciled invite redemption: %w", err)
	}
	if err := insertRedemption(ctx, redemption); err != nil {
		return err
	}
	if _, err := insertLink(ctx, redemption); err != nil {
		return err
	}
	if err := increment(ctx); err != nil {
		return err
	}
	return complete(ctx)
}

func nullableInviteMediaUserID(value string) sql.NullString {
	return sql.NullString{String: value, Valid: value != ""}
}

func inviteAvailable(invite core.Invite, now time.Time) error {
	if invite.Status(now) != core.InviteActive {
		return core.ErrInviteUnavailable
	}
	return nil
}

func nullableInt(value *int) sql.NullInt64 {
	if value == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: int64(*value), Valid: true}
}

func pointerInt(value sql.NullInt64) *int {
	if !value.Valid {
		return nil
	}
	result := int(value.Int64)
	return &result
}
