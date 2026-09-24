package db

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/db/sqlite"
)

var _ core.InviteProvisioningFailureStore = (*sqliteInvites)(nil)

func (s *sqliteInvites) ClaimInviteProvisioningFailure(
	ctx context.Context, lease core.InviteProvisioningLease, at time.Time,
) (core.InviteProvisioningFailure, error) {
	if err := validateProvisioningLease(lease, at); err != nil {
		return core.InviteProvisioningFailure{}, err
	}
	var failure core.InviteProvisioningFailure
	err := withSQLiteWriteTransaction(ctx, s.pool, func(conn *sql.Conn) error {
		q := sqlite.New(conn)
		stamp := formatSQLiteTime(at)
		id, err := q.LockInviteProvisioningFailureForClaim(ctx, stamp)
		if err != nil {
			return mapNotFound("lock invite provisioning failure", err)
		}
		row, err := q.ClaimInviteProvisioningFailure(ctx, sqlite.ClaimInviteProvisioningFailureParams{
			LeaseToken: lease.Token, LeaseExpiresAt: sqliteNullableTime(&lease.ExpiresAt),
			UpdatedAt: stamp, ID: id, DueAt: stamp,
		})
		if err != nil {
			return mapNotFound("claim invite provisioning failure", err)
		}
		failure, err = mapSQLiteClaimedFailure(row)
		if err != nil {
			return err
		}
		failure.LibraryIDs, err = q.ListInviteLibraries(ctx, failure.InviteID)
		return err
	})
	return failure, err
}

func (s *sqliteInvites) CompleteInviteProvisioningCleanup(ctx context.Context, id, token string) error {
	rows, err := s.q.CompleteInviteProvisioningCleanup(ctx, sqlite.CompleteInviteProvisioningCleanupParams{
		ID: id, LeaseToken: token,
	})
	return provisioningRows("complete invite provisioning cleanup", rows, err)
}

func (s *sqliteInvites) CompleteInviteProvisioningPolicy(
	ctx context.Context, failure core.InviteProvisioningFailure, redemption core.InviteRedemption, at time.Time,
) error {
	if at.IsZero() {
		return core.ErrInvalidArgument
	}
	return withSQLiteWriteTransaction(ctx, s.pool, func(conn *sql.Conn) error {
		q := sqlite.New(conn)
		stored, err := q.GetClaimedInviteProvisioningFailure(ctx, sqlite.GetClaimedInviteProvisioningFailureParams{
			ID: failure.ID, LeaseToken: failure.LeaseToken,
		})
		if err != nil {
			return mapNotFound("get claimed invite provisioning failure", err)
		}
		persisted, err := mapSQLiteStoredFailure(stored)
		if err != nil {
			return err
		}
		return s.completeSQLitePolicy(ctx, q, persisted, redemption, at)
	})
}

func (s *sqliteInvites) completeSQLitePolicy(
	ctx context.Context, q *sqlite.Queries, failure core.InviteProvisioningFailure,
	redemption core.InviteRedemption, at time.Time,
) error {
	row, err := q.LockInviteByID(ctx, failure.InviteID)
	if err != nil {
		return mapNotFound("lock invite for provisioning completion", err)
	}
	invite, err := sqliteInvite(row.ID, row.MediaServerID, row.CreatedByAccountID, row.Label,
		row.ExpiresAt, row.MaxUses, row.UseCount, row.RevokedAt, row.CreatedAt, row.UpdatedAt)
	if err != nil {
		return err
	}
	return completeProvisioningPolicy(ctx, failure, redemption, invite,
		func(ctx context.Context, value core.InviteRedemption) error {
			return insertSQLiteRedemption(ctx, q, value)
		},
		func(ctx context.Context, value core.InviteRedemption) (bool, error) {
			return insertSQLiteInviteLink(ctx, q, value)
		},
		func(ctx context.Context) error {
			rows, incErr := q.IncrementInviteUse(ctx, sqlite.IncrementInviteUseParams{ID: invite.ID, UpdatedAt: formatSQLiteTime(at)})
			return provisioningRows("increment reconciled invite use", rows, incErr)
		}, func(ctx context.Context) error {
			rows, doneErr := q.CompleteInviteProvisioningCleanup(ctx, sqlite.CompleteInviteProvisioningCleanupParams{
				ID: failure.ID, LeaseToken: failure.LeaseToken,
			})
			return provisioningRows("complete invite provisioning policy", rows, doneErr)
		})
}

func (s *sqliteInvites) RescheduleInviteProvisioningFailure(
	ctx context.Context, id, token, safeError string, at, next time.Time, terminal bool,
) error {
	if len(safeError) > core.MaxInviteProvisioningErrorBytes || at.IsZero() || next.IsZero() {
		return core.ErrInvalidArgument
	}
	rows, err := s.q.RescheduleInviteProvisioningFailure(ctx, sqlite.RescheduleInviteProvisioningFailureParams{
		NextAttemptAt: formatSQLiteTime(next), LastError: safeError, Terminal: boolInt64(terminal),
		UpdatedAt: formatSQLiteTime(at), ID: id, LeaseToken: token,
	})
	return provisioningRows("reschedule invite provisioning failure", rows, err)
}

func (s *sqliteInvites) ListInviteProvisioningFailures(
	ctx context.Context, after *core.InviteProvisioningFailureCursor, pageSize int,
) ([]core.InviteProvisioningFailure, error) {
	if err := validateProvisioningFailurePage(after, pageSize); err != nil {
		return nil, err
	}
	params := sqlite.ListInviteProvisioningFailuresParams{PageSize: int64(pageSize)}
	if after != nil {
		params.HasCursor, params.AfterCreatedAt, params.AfterID = 1, formatSQLiteTime(after.CreatedAt), after.ID
	}
	rows, err := s.q.ListInviteProvisioningFailures(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("list invite provisioning failures: %w", err)
	}
	result := make([]core.InviteProvisioningFailure, 0, len(rows))
	for _, row := range rows {
		value, mapErr := mapSQLiteListedFailure(row)
		if mapErr != nil {
			return nil, mapErr
		}
		result = append(result, value)
	}
	return result, nil
}

func (s *sqliteInvites) DismissInviteProvisioningFailure(ctx context.Context, id string, at time.Time) error {
	if !core.ValidID(id) || at.IsZero() {
		return core.ErrInvalidArgument
	}
	return withSQLiteWriteTransaction(ctx, s.pool, func(conn *sql.Conn) error {
		q := sqlite.New(conn)
		rows, err := q.DismissInviteProvisioningFailure(ctx, sqlite.DismissInviteProvisioningFailureParams{
			ID: id, DismissedAt: sqliteNullableTime(&at),
		})
		if err != nil || rows == 1 {
			return provisioningRows("dismiss invite provisioning failure", rows, err)
		}
		exists, existsErr := q.InviteProvisioningFailureExists(ctx, id)
		return dismissalConflict(exists, existsErr)
	})
}

func (s *sqliteInvites) InviteProvisioningFailureDepth(ctx context.Context) (int64, error) {
	return s.q.InviteProvisioningFailureDepth(ctx)
}

func provisioningRows(operation string, rows int64, err error) error {
	if err != nil {
		return fmt.Errorf("%s: %w", operation, err)
	}
	if rows != 1 {
		return core.ErrNotFound
	}
	return nil
}

func mapSQLiteClaimedFailure(row sqlite.ClaimInviteProvisioningFailureRow) (core.InviteProvisioningFailure, error) {
	return sqliteFailure(row.ID, row.InviteID, row.MediaServerID, row.MediaUserID, row.AccountID,
		row.MediaUserOwned, row.Username, row.Reason, row.Attempts, row.NextAttemptAt, row.LeaseToken,
		row.LeaseExpiresAt, row.LastError, row.Terminal, row.CreatedAt, row.UpdatedAt)
}

func mapSQLiteStoredFailure(row sqlite.GetClaimedInviteProvisioningFailureRow) (core.InviteProvisioningFailure, error) {
	return sqliteFailure(row.ID, row.InviteID, row.MediaServerID, row.MediaUserID, row.AccountID,
		row.MediaUserOwned, row.Username, row.Reason, row.Attempts, row.NextAttemptAt, row.LeaseToken,
		row.LeaseExpiresAt, row.LastError, row.Terminal, row.CreatedAt, row.UpdatedAt)
}

func mapSQLiteListedFailure(row sqlite.ListInviteProvisioningFailuresRow) (core.InviteProvisioningFailure, error) {
	next, err := parseSQLiteTime(row.NextAttemptAt)
	if err != nil {
		return core.InviteProvisioningFailure{}, fmt.Errorf("parse provisioning next attempt: %w", err)
	}
	created, err := parseSQLiteTime(row.CreatedAt)
	if err != nil {
		return core.InviteProvisioningFailure{}, fmt.Errorf("parse provisioning created_at: %w", err)
	}
	updated, err := parseSQLiteTime(row.UpdatedAt)
	if err != nil {
		return core.InviteProvisioningFailure{}, fmt.Errorf("parse provisioning updated_at: %w", err)
	}
	return core.InviteProvisioningFailure{
		ID: row.ID, InviteID: row.InviteID, MediaServerID: row.MediaServerID, MediaServerName: row.MediaServerName,
		MediaUserOwned: row.MediaUserOwned != 0, Username: row.Username,
		Reason: core.InviteProvisioningFailureReason(row.Reason), Attempts: int(row.Attempts),
		NextAttemptAt: next, LastError: row.LastError, Terminal: row.Terminal != 0, CreatedAt: created, UpdatedAt: updated,
	}, nil
}

func sqliteFailure(
	id, inviteID, serverID string, userID, accountID sql.NullString, owned int64, username, reason string,
	attempts int64, next, token string, lease sql.NullString, lastError string, terminal int64,
	created, updated string,
) (core.InviteProvisioningFailure, error) {
	nextAt, err := parseSQLiteTime(next)
	if err != nil {
		return core.InviteProvisioningFailure{}, err
	}
	leaseAt, err := parseSQLiteNullableTime(lease)
	if err != nil {
		return core.InviteProvisioningFailure{}, err
	}
	createdAt, err := parseSQLiteTime(created)
	if err != nil {
		return core.InviteProvisioningFailure{}, err
	}
	updatedAt, err := parseSQLiteTime(updated)
	if err != nil {
		return core.InviteProvisioningFailure{}, err
	}
	return core.InviteProvisioningFailure{
		ID: id, InviteID: inviteID, MediaServerID: serverID, MediaUserID: userID.String, AccountID: accountID.String,
		MediaUserOwned: owned != 0, Username: username,
		Reason: core.InviteProvisioningFailureReason(reason), Attempts: int(attempts),
		NextAttemptAt: nextAt, LeaseToken: token, LeaseExpiresAt: leaseAt, LastError: lastError,
		Terminal: terminal != 0, CreatedAt: createdAt, UpdatedAt: updatedAt,
	}, nil
}
