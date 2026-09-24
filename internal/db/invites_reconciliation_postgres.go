package db

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/db/postgres"
)

var _ core.InviteProvisioningFailureStore = (*postgresInvites)(nil)

func (s *postgresInvites) ClaimInviteProvisioningFailure(
	ctx context.Context, lease core.InviteProvisioningLease, at time.Time,
) (core.InviteProvisioningFailure, error) {
	if err := validateProvisioningLease(lease, at); err != nil {
		return core.InviteProvisioningFailure{}, err
	}
	var failure core.InviteProvisioningFailure
	err := withTransaction(ctx, s.pool, func(tx *sql.Tx) error {
		q := postgres.New(tx)
		stamp := core.NormalizeTime(at)
		id, err := q.LockInviteProvisioningFailureForClaim(ctx, stamp)
		if err != nil {
			return mapNotFound("lock invite provisioning failure", err)
		}
		row, err := q.ClaimInviteProvisioningFailure(ctx, postgres.ClaimInviteProvisioningFailureParams{
			LeaseToken: lease.Token, LeaseExpiresAt: postgresNullableTime(&lease.ExpiresAt),
			UpdatedAt: stamp, ID: id, DueAt: stamp,
		})
		if err != nil {
			return mapNotFound("claim invite provisioning failure", err)
		}
		failure = mapPostgresClaimedFailure(row)
		failure.LibraryIDs, err = q.ListInviteLibraries(ctx, failure.InviteID)
		return err
	})
	return failure, err
}

func (s *postgresInvites) CompleteInviteProvisioningCleanup(ctx context.Context, id, token string) error {
	rows, err := s.q.CompleteInviteProvisioningCleanup(ctx, postgres.CompleteInviteProvisioningCleanupParams{
		ID: id, LeaseToken: token,
	})
	return provisioningRows("complete invite provisioning cleanup", rows, err)
}

func (s *postgresInvites) CompleteInviteProvisioningPolicy(
	ctx context.Context, failure core.InviteProvisioningFailure, redemption core.InviteRedemption, at time.Time,
) error {
	if at.IsZero() {
		return core.ErrInvalidArgument
	}
	return withTransaction(ctx, s.pool, func(tx *sql.Tx) error {
		q := postgres.New(tx)
		stored, err := q.GetClaimedInviteProvisioningFailure(ctx, postgres.GetClaimedInviteProvisioningFailureParams{
			ID: failure.ID, LeaseToken: failure.LeaseToken,
		})
		if err != nil {
			return mapNotFound("get claimed invite provisioning failure", err)
		}
		return s.completePostgresPolicy(ctx, q, mapPostgresStoredFailure(stored), redemption, at)
	})
}

func (s *postgresInvites) completePostgresPolicy(
	ctx context.Context, q *postgres.Queries, failure core.InviteProvisioningFailure,
	redemption core.InviteRedemption, at time.Time,
) error {
	row, err := q.LockInviteByID(ctx, failure.InviteID)
	if err != nil {
		return mapNotFound("lock invite for provisioning completion", err)
	}
	invite := postgresInvite(row.ID, row.MediaServerID, row.CreatedByAccountID, row.Label,
		row.ExpiresAt, row.MaxUses, row.UseCount, row.RevokedAt, row.CreatedAt, row.UpdatedAt)
	return completeProvisioningPolicy(ctx, failure, redemption, invite,
		func(ctx context.Context, value core.InviteRedemption) error {
			return insertPostgresRedemption(ctx, q, value)
		},
		func(ctx context.Context, value core.InviteRedemption) (bool, error) {
			return insertPostgresInviteLink(ctx, q, value)
		},
		func(ctx context.Context) error {
			rows, incErr := q.IncrementInviteUse(ctx, postgres.IncrementInviteUseParams{ID: invite.ID, UpdatedAt: core.NormalizeTime(at)})
			return provisioningRows("increment reconciled invite use", rows, incErr)
		}, func(ctx context.Context) error {
			rows, doneErr := q.CompleteInviteProvisioningCleanup(ctx, postgres.CompleteInviteProvisioningCleanupParams{
				ID: failure.ID, LeaseToken: failure.LeaseToken,
			})
			return provisioningRows("complete invite provisioning policy", rows, doneErr)
		})
}

func (s *postgresInvites) RescheduleInviteProvisioningFailure(
	ctx context.Context, id, token, safeError string, at, next time.Time, terminal bool,
) error {
	if len(safeError) > core.MaxInviteProvisioningErrorBytes || at.IsZero() || next.IsZero() {
		return core.ErrInvalidArgument
	}
	rows, err := s.q.RescheduleInviteProvisioningFailure(ctx, postgres.RescheduleInviteProvisioningFailureParams{
		NextAttemptAt: core.NormalizeTime(next), LastError: safeError, Terminal: terminal,
		UpdatedAt: core.NormalizeTime(at), ID: id, LeaseToken: token,
	})
	return provisioningRows("reschedule invite provisioning failure", rows, err)
}

func (s *postgresInvites) ListInviteProvisioningFailures(
	ctx context.Context, after *core.InviteProvisioningFailureCursor, pageSize int,
) ([]core.InviteProvisioningFailure, error) {
	if err := validateProvisioningFailurePage(after, pageSize); err != nil {
		return nil, err
	}
	params := postgres.ListInviteProvisioningFailuresParams{PageSize: int32(pageSize)} //nolint:gosec // bounded above.
	if after != nil {
		params.HasCursor, params.AfterCreatedAt, params.AfterID = 1, core.NormalizeTime(after.CreatedAt), after.ID
	}
	rows, err := s.q.ListInviteProvisioningFailures(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("list invite provisioning failures: %w", err)
	}
	result := make([]core.InviteProvisioningFailure, 0, len(rows))
	for _, row := range rows {
		result = append(result, mapPostgresListedFailure(row))
	}
	return result, nil
}

func (s *postgresInvites) DismissInviteProvisioningFailure(ctx context.Context, id string, at time.Time) error {
	if !core.ValidID(id) || at.IsZero() {
		return core.ErrInvalidArgument
	}
	return withTransaction(ctx, s.pool, func(tx *sql.Tx) error {
		q := postgres.New(tx)
		rows, err := q.DismissInviteProvisioningFailure(ctx, postgres.DismissInviteProvisioningFailureParams{
			ID: id, DismissedAt: postgresNullableTime(&at),
		})
		if err != nil || rows == 1 {
			return provisioningRows("dismiss invite provisioning failure", rows, err)
		}
		exists, existsErr := q.InviteProvisioningFailureExists(ctx, id)
		return dismissalConflict(exists, existsErr)
	})
}

func (s *postgresInvites) InviteProvisioningFailureDepth(ctx context.Context) (int64, error) {
	return s.q.InviteProvisioningFailureDepth(ctx)
}

func mapPostgresClaimedFailure(row postgres.ClaimInviteProvisioningFailureRow) core.InviteProvisioningFailure {
	return postgresFailure(row.ID, row.InviteID, row.MediaServerID, row.MediaUserID, row.AccountID,
		row.MediaUserOwned, row.Username, row.Reason, row.Attempts, row.NextAttemptAt, row.LeaseToken,
		row.LeaseExpiresAt, row.LastError, row.Terminal, row.CreatedAt, row.UpdatedAt)
}

func mapPostgresStoredFailure(row postgres.GetClaimedInviteProvisioningFailureRow) core.InviteProvisioningFailure {
	return postgresFailure(row.ID, row.InviteID, row.MediaServerID, row.MediaUserID, row.AccountID,
		row.MediaUserOwned, row.Username, row.Reason, row.Attempts, row.NextAttemptAt, row.LeaseToken,
		row.LeaseExpiresAt, row.LastError, row.Terminal, row.CreatedAt, row.UpdatedAt)
}

func mapPostgresListedFailure(row postgres.ListInviteProvisioningFailuresRow) core.InviteProvisioningFailure {
	return core.InviteProvisioningFailure{
		ID: row.ID, InviteID: row.InviteID, MediaServerID: row.MediaServerID, MediaServerName: row.MediaServerName,
		MediaUserOwned: row.MediaUserOwned, Username: row.Username,
		Reason: core.InviteProvisioningFailureReason(row.Reason), Attempts: int(row.Attempts),
		NextAttemptAt: core.NormalizeTime(row.NextAttemptAt), LastError: row.LastError, Terminal: row.Terminal,
		CreatedAt: core.NormalizeTime(row.CreatedAt), UpdatedAt: core.NormalizeTime(row.UpdatedAt),
	}
}

func postgresFailure(
	id, inviteID, serverID string, userID, accountID sql.NullString, owned bool, username, reason string,
	attempts int32, next time.Time, token string, lease sql.NullTime, lastError string, terminal bool,
	created, updated time.Time,
) core.InviteProvisioningFailure {
	return core.InviteProvisioningFailure{
		ID: id, InviteID: inviteID, MediaServerID: serverID, MediaUserID: userID.String, AccountID: accountID.String,
		MediaUserOwned: owned, Username: username,
		Reason: core.InviteProvisioningFailureReason(reason), Attempts: int(attempts),
		NextAttemptAt: core.NormalizeTime(next), LeaseToken: token, LeaseExpiresAt: pointerTime(lease), LastError: lastError,
		Terminal: terminal, CreatedAt: core.NormalizeTime(created), UpdatedAt: core.NormalizeTime(updated),
	}
}
