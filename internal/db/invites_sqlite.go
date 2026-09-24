package db

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/db/sqlite"
)

type sqliteInvites struct {
	pool *sql.DB
	q    *sqlite.Queries
}

var (
	_ core.InviteReader = (*sqliteInvites)(nil)
	_ core.InviteStore  = (*sqliteInvites)(nil)
)

func newSQLiteInvites(pool *sql.DB) *sqliteInvites {
	return &sqliteInvites{pool: pool, q: sqlite.New(pool)}
}

func (s *sqliteInvites) CreateInvite(ctx context.Context, invite core.Invite, hash [sha256.Size]byte) error {
	if err := validateInviteForStorage(invite); err != nil {
		return fmt.Errorf("insert invite: %w", err)
	}
	return withTransaction(ctx, s.pool, func(tx *sql.Tx) error {
		return insertSQLiteInvite(ctx, sqlite.New(tx), invite, hash)
	})
}

func insertSQLiteInvite(
	ctx context.Context, q *sqlite.Queries, invite core.Invite, hash [sha256.Size]byte,
) error {
	if err := q.CreateInvite(ctx, sqliteCreateParams(invite, hash)); err != nil {
		return fmt.Errorf("insert invite: %w", err)
	}
	for _, libraryID := range invite.LibraryIDs {
		if err := q.CreateInviteLibrary(ctx, sqlite.CreateInviteLibraryParams{InviteID: invite.ID, LibraryID: libraryID}); err != nil {
			return fmt.Errorf("insert invite library: %w", err)
		}
	}
	return nil
}

func sqliteCreateParams(invite core.Invite, hash [sha256.Size]byte) sqlite.CreateInviteParams {
	return sqlite.CreateInviteParams{
		ID: invite.ID, MediaServerID: invite.MediaServerID, CreatedByAccountID: invite.CreatedBy,
		CodeHash: hash[:], Label: invite.Label, ExpiresAt: sqliteNullableTime(invite.ExpiresAt),
		MaxUses: nullableInt(invite.MaxUses), CreatedAt: formatSQLiteTime(invite.CreatedAt),
		UpdatedAt: formatSQLiteTime(invite.UpdatedAt),
	}
}

func (s *sqliteInvites) GetInvite(ctx context.Context, id string) (core.Invite, error) {
	row, err := s.q.GetInvite(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return core.Invite{}, core.ErrNotFound
	}
	if err != nil {
		return core.Invite{}, fmt.Errorf("select invite: %w", err)
	}
	invite, err := sqliteInvite(row.ID, row.MediaServerID, row.CreatedByAccountID, row.Label,
		row.ExpiresAt, row.MaxUses, row.UseCount, row.RevokedAt, row.CreatedAt, row.UpdatedAt)
	if err != nil {
		return core.Invite{}, err
	}
	return s.withLibraries(ctx, s.q, invite)
}

func (s *sqliteInvites) GetInviteByCodeHash(ctx context.Context, hash [sha256.Size]byte) (core.Invite, error) {
	row, err := s.q.GetInviteByCodeHash(ctx, hash[:])
	if errors.Is(err, sql.ErrNoRows) {
		return core.Invite{}, core.ErrInviteUnavailable
	}
	if err != nil {
		return core.Invite{}, fmt.Errorf("select invite by code: %w", err)
	}
	invite, err := sqliteInvite(row.ID, row.MediaServerID, row.CreatedByAccountID, row.Label,
		row.ExpiresAt, row.MaxUses, row.UseCount, row.RevokedAt, row.CreatedAt, row.UpdatedAt)
	if err != nil {
		return core.Invite{}, err
	}
	return s.withLibraries(ctx, s.q, invite)
}

func (s *sqliteInvites) ListInvites(ctx context.Context, after *core.InviteCursor, pageSize int) ([]core.Invite, error) {
	if err := validateInvitePage(after, pageSize); err != nil {
		return nil, fmt.Errorf("list invites: %w", err)
	}
	params := sqlite.ListInvitesParams{PageSize: int64(pageSize)}
	if after != nil {
		params.HasCursor, params.AfterCreatedAt, params.AfterID = 1, formatSQLiteTime(after.CreatedAt), after.ID
	}
	rows, err := s.q.ListInvites(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("list invites: %w", err)
	}
	invites := make([]core.Invite, 0, len(rows))
	for _, row := range rows {
		invite, mapErr := sqliteInvite(row.ID, row.MediaServerID, row.CreatedByAccountID, row.Label,
			row.ExpiresAt, row.MaxUses, row.UseCount, row.RevokedAt, row.CreatedAt, row.UpdatedAt)
		if mapErr != nil {
			return nil, mapErr
		}
		invite, mapErr = s.withLibraries(ctx, s.q, invite)
		if mapErr != nil {
			return nil, mapErr
		}
		invites = append(invites, invite)
	}
	return invites, nil
}

func (s *sqliteInvites) RevokeInvite(ctx context.Context, id string, revokedAt time.Time) (core.Invite, error) {
	now := core.NormalizeTime(revokedAt)
	row, err := s.q.RevokeInvite(ctx, sqlite.RevokeInviteParams{
		ID: id, RevokedAt: sql.NullString{String: formatSQLiteTime(now), Valid: true}, UpdatedAt: formatSQLiteTime(now),
	})
	if errors.Is(err, sql.ErrNoRows) {
		return core.Invite{}, core.ErrNotFound
	}
	if err != nil {
		return core.Invite{}, fmt.Errorf("revoke invite: %w", err)
	}
	invite, err := sqliteInvite(row.ID, row.MediaServerID, row.CreatedByAccountID, row.Label,
		row.ExpiresAt, row.MaxUses, row.UseCount, row.RevokedAt, row.CreatedAt, row.UpdatedAt)
	if err != nil {
		return core.Invite{}, err
	}
	return s.withLibraries(ctx, s.q, invite)
}

func (s *sqliteInvites) RedeemInvite(
	ctx context.Context, hash [sha256.Size]byte, clock core.Clock, redeem core.InviteRedeemFunc,
) (bool, error) {
	if clock == nil || redeem == nil {
		return false, core.ErrInvalidArgument
	}
	var provisioningErr error
	var linkCreated bool
	txErr := withSQLiteWriteTransaction(ctx, s.pool, func(conn *sql.Conn) error {
		q := sqlite.New(conn)
		created, redeemErr := s.redeemLocked(ctx, q, hash, clock, redeem)
		linkCreated = created
		pending, ok := errors.AsType[*core.InviteProvisioningError](redeemErr)
		if !ok || pending == nil {
			return redeemErr
		}
		provisioningErr = redeemErr
		return insertSQLiteProvisioningFailure(ctx, q, pending.Failure)
	})
	return linkCreated, errors.Join(provisioningErr, txErr)
}

func (s *sqliteInvites) redeemLocked(
	ctx context.Context, q *sqlite.Queries, hash [sha256.Size]byte, clock core.Clock, redeem core.InviteRedeemFunc,
) (bool, error) {
	invite, err := s.lockedInvite(ctx, q, hash)
	if err != nil {
		return false, err
	}
	now := core.NormalizeTime(clock.Now())
	if availabilityErr := inviteAvailable(invite, now); availabilityErr != nil {
		return false, availabilityErr
	}
	redemption, err := redeem(ctx, invite)
	if err != nil {
		return false, err
	}
	if validationErr := validateRedemption(invite, redemption); validationErr != nil {
		return false, fmt.Errorf("validate invite redemption: %w", validationErr)
	}
	if insertErr := insertSQLiteRedemption(ctx, q, redemption); insertErr != nil {
		return false, insertErr
	}
	linkCreated, err := insertSQLiteInviteLink(ctx, q, redemption)
	if err != nil {
		return false, err
	}
	rows, err := q.IncrementInviteUse(ctx, sqlite.IncrementInviteUseParams{ID: invite.ID, UpdatedAt: formatSQLiteTime(now)})
	if err != nil || rows != 1 {
		return false, fmt.Errorf("increment invite use: %w", errors.Join(err, core.ErrNotFound))
	}
	return linkCreated, nil
}

func insertSQLiteInviteLink(
	ctx context.Context, q *sqlite.Queries, redemption core.InviteRedemption,
) (bool, error) {
	if redemption.AccountID == "" {
		return false, nil
	}
	link := core.AccountMediaUser{
		AccountID: redemption.AccountID, MediaServerID: redemption.MediaServerID,
		MediaUserID: redemption.MediaUserID, Username: redemption.Username,
		Source: core.AccountMediaUserSourceInvite, CreatedAt: redemption.RedeemedAt, UpdatedAt: redemption.RedeemedAt,
	}
	if err := core.ValidateAccountMediaUser(link); err != nil {
		return false, fmt.Errorf("validate invite account media user: %w", err)
	}
	rows, err := q.CreateAccountMediaUserIfAbsent(ctx,
		sqlite.CreateAccountMediaUserIfAbsentParams(sqliteAccountMediaUserParams(link)))
	if err != nil {
		return false, fmt.Errorf("insert invite account media user: %w", err)
	}
	return rows == 1, nil
}

func (s *sqliteInvites) RecordInviteProvisioningFailure(
	ctx context.Context, failure core.InviteProvisioningFailure,
) error {
	return insertSQLiteProvisioningFailure(ctx, s.q, failure)
}

func insertSQLiteProvisioningFailure(
	ctx context.Context, q *sqlite.Queries, failure core.InviteProvisioningFailure,
) error {
	if err := validateProvisioningFailure(failure); err != nil {
		return provisioningFailureRecordError(err)
	}
	err := q.InsertInviteProvisioningFailure(ctx, sqlite.InsertInviteProvisioningFailureParams{
		ID: failure.ID, InviteID: failure.InviteID, MediaServerID: failure.MediaServerID,
		MediaUserID: nullableInviteMediaUserID(failure.MediaUserID), Username: failure.Username, Reason: string(failure.Reason),
		CreatedAt: formatSQLiteTime(failure.CreatedAt), UpdatedAt: formatSQLiteTime(failure.UpdatedAt),
	})
	if err != nil {
		return provisioningFailureRecordError(err)
	}
	return nil
}

func (s *sqliteInvites) lockedInvite(
	ctx context.Context, q *sqlite.Queries, hash [sha256.Size]byte,
) (core.Invite, error) {
	row, err := q.LockInviteByCodeHash(ctx, hash[:])
	if errors.Is(err, sql.ErrNoRows) {
		return core.Invite{}, core.ErrInviteUnavailable
	}
	if err != nil {
		return core.Invite{}, fmt.Errorf("lock invite: %w", err)
	}
	hasFailure, err := q.InviteHasProvisioningFailure(ctx, row.ID)
	if err != nil {
		return core.Invite{}, fmt.Errorf("check invite provisioning failure: %w", err)
	}
	if hasFailure {
		return core.Invite{}, core.ErrInviteUnavailable
	}
	invite, err := sqliteInvite(row.ID, row.MediaServerID, row.CreatedByAccountID, row.Label,
		row.ExpiresAt, row.MaxUses, row.UseCount, row.RevokedAt, row.CreatedAt, row.UpdatedAt)
	if err != nil {
		return core.Invite{}, err
	}
	return s.withLibraries(ctx, q, invite)
}

func (s *sqliteInvites) withLibraries(ctx context.Context, q *sqlite.Queries, invite core.Invite) (core.Invite, error) {
	libraries, err := q.ListInviteLibraries(ctx, invite.ID)
	if err != nil {
		return core.Invite{}, fmt.Errorf("list invite libraries: %w", err)
	}
	invite.LibraryIDs = libraries
	return invite, nil
}

func insertSQLiteRedemption(ctx context.Context, q *sqlite.Queries, value core.InviteRedemption) error {
	err := q.InsertInviteRedemption(ctx, sqlite.InsertInviteRedemptionParams{
		ID: value.ID, InviteID: value.InviteID, MediaServerID: value.MediaServerID,
		MediaUserID: value.MediaUserID, Username: value.Username, RedeemedAt: formatSQLiteTime(value.RedeemedAt),
	})
	if err != nil {
		return fmt.Errorf("insert invite redemption: %w", err)
	}
	return nil
}

func sqliteInvite(
	id, serverID, createdBy, label string, expires sql.NullString, maxUses sql.NullInt64,
	useCount int64, revoked sql.NullString, created, updated string,
) (core.Invite, error) {
	createdAt, err := parseSQLiteTime(created)
	if err != nil {
		return core.Invite{}, fmt.Errorf("parse invite created_at: %w", err)
	}
	updatedAt, err := parseSQLiteTime(updated)
	if err != nil {
		return core.Invite{}, fmt.Errorf("parse invite updated_at: %w", err)
	}
	expiresAt, err := parseSQLiteNullableTime(expires)
	if err != nil {
		return core.Invite{}, fmt.Errorf("parse invite expires_at: %w", err)
	}
	revokedAt, err := parseSQLiteNullableTime(revoked)
	if err != nil {
		return core.Invite{}, fmt.Errorf("parse invite revoked_at: %w", err)
	}
	return core.Invite{
		ID: id, MediaServerID: serverID, CreatedBy: createdBy, Label: label,
		ExpiresAt: expiresAt, MaxUses: pointerInt(maxUses), UseCount: int(useCount), RevokedAt: revokedAt,
		CreatedAt: createdAt, UpdatedAt: updatedAt,
	}, nil
}

func parseSQLiteNullableTime(value sql.NullString) (*time.Time, error) {
	if !value.Valid {
		return nil, nil
	}
	parsed, err := parseSQLiteTime(value.String)
	return &parsed, err
}
