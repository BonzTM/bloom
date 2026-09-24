package db

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/db/postgres"
)

type postgresInvites struct {
	pool *sql.DB
	q    *postgres.Queries
}

var (
	_ core.InviteReader = (*postgresInvites)(nil)
	_ core.InviteStore  = (*postgresInvites)(nil)
)

func newPostgresInvites(pool *sql.DB) *postgresInvites {
	return &postgresInvites{pool: pool, q: postgres.New(pool)}
}

func (s *postgresInvites) CreateInvite(ctx context.Context, invite core.Invite, hash [sha256.Size]byte) error {
	if err := validateInviteForStorage(invite); err != nil {
		return fmt.Errorf("insert invite: %w", err)
	}
	return withTransaction(ctx, s.pool, func(tx *sql.Tx) error {
		return insertPostgresInvite(ctx, postgres.New(tx), invite, hash)
	})
}

func insertPostgresInvite(
	ctx context.Context, q *postgres.Queries, invite core.Invite, hash [sha256.Size]byte,
) error {
	if err := q.CreateInvite(ctx, postgresCreateParams(invite, hash)); err != nil {
		return fmt.Errorf("insert invite: %w", err)
	}
	for _, libraryID := range invite.LibraryIDs {
		if err := q.CreateInviteLibrary(ctx, postgres.CreateInviteLibraryParams{InviteID: invite.ID, LibraryID: libraryID}); err != nil {
			return fmt.Errorf("insert invite library: %w", err)
		}
	}
	return nil
}

func postgresCreateParams(invite core.Invite, hash [sha256.Size]byte) postgres.CreateInviteParams {
	return postgres.CreateInviteParams{
		ID: invite.ID, MediaServerID: invite.MediaServerID, CreatedByAccountID: invite.CreatedBy,
		CodeHash: hash[:], Label: invite.Label, ExpiresAt: postgresNullableTime(invite.ExpiresAt),
		MaxUses: postgresNullableInt(invite.MaxUses), CreatedAt: core.NormalizeTime(invite.CreatedAt),
		UpdatedAt: core.NormalizeTime(invite.UpdatedAt),
	}
}

func (s *postgresInvites) GetInvite(ctx context.Context, id string) (core.Invite, error) {
	row, err := s.q.GetInvite(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return core.Invite{}, core.ErrNotFound
	}
	if err != nil {
		return core.Invite{}, fmt.Errorf("select invite: %w", err)
	}
	invite := postgresInvite(row.ID, row.MediaServerID, row.CreatedByAccountID, row.Label,
		row.ExpiresAt, row.MaxUses, row.UseCount, row.RevokedAt, row.CreatedAt, row.UpdatedAt)
	return s.withLibraries(ctx, s.q, invite)
}

func (s *postgresInvites) GetInviteByCodeHash(ctx context.Context, hash [sha256.Size]byte) (core.Invite, error) {
	row, err := s.q.GetInviteByCodeHash(ctx, hash[:])
	if errors.Is(err, sql.ErrNoRows) {
		return core.Invite{}, core.ErrInviteUnavailable
	}
	if err != nil {
		return core.Invite{}, fmt.Errorf("select invite by code: %w", err)
	}
	invite := postgresInvite(row.ID, row.MediaServerID, row.CreatedByAccountID, row.Label,
		row.ExpiresAt, row.MaxUses, row.UseCount, row.RevokedAt, row.CreatedAt, row.UpdatedAt)
	return s.withLibraries(ctx, s.q, invite)
}

func (s *postgresInvites) ListInvites(ctx context.Context, after *core.InviteCursor, pageSize int) ([]core.Invite, error) {
	if err := validateInvitePage(after, pageSize); err != nil {
		return nil, fmt.Errorf("list invites: %w", err)
	}
	params := postgres.ListInvitesParams{PageSize: int32(pageSize)} //nolint:gosec // bounded above.
	if after != nil {
		params.HasCursor, params.AfterCreatedAt, params.AfterID = 1, core.NormalizeTime(after.CreatedAt), after.ID
	}
	rows, err := s.q.ListInvites(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("list invites: %w", err)
	}
	invites := make([]core.Invite, 0, len(rows))
	for _, row := range rows {
		invite := postgresInvite(row.ID, row.MediaServerID, row.CreatedByAccountID, row.Label,
			row.ExpiresAt, row.MaxUses, row.UseCount, row.RevokedAt, row.CreatedAt, row.UpdatedAt)
		invite, err = s.withLibraries(ctx, s.q, invite)
		if err != nil {
			return nil, err
		}
		invites = append(invites, invite)
	}
	return invites, nil
}

func (s *postgresInvites) RevokeInvite(ctx context.Context, id string, revokedAt time.Time) (core.Invite, error) {
	now := core.NormalizeTime(revokedAt)
	row, err := s.q.RevokeInvite(ctx, postgres.RevokeInviteParams{
		ID: id, RevokedAt: sql.NullTime{Time: now, Valid: true}, UpdatedAt: now,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return core.Invite{}, core.ErrNotFound
	}
	if err != nil {
		return core.Invite{}, fmt.Errorf("revoke invite: %w", err)
	}
	invite := postgresInvite(row.ID, row.MediaServerID, row.CreatedByAccountID, row.Label,
		row.ExpiresAt, row.MaxUses, row.UseCount, row.RevokedAt, row.CreatedAt, row.UpdatedAt)
	return s.withLibraries(ctx, s.q, invite)
}

func (s *postgresInvites) RedeemInvite(
	ctx context.Context, hash [sha256.Size]byte, clock core.Clock, redeem core.InviteRedeemFunc,
) (bool, error) {
	if clock == nil || redeem == nil {
		return false, core.ErrInvalidArgument
	}
	var provisioningErr error
	var linkCreated bool
	txErr := withTransaction(ctx, s.pool, func(tx *sql.Tx) error {
		q := postgres.New(tx)
		created, redeemErr := s.redeemLocked(ctx, q, hash, clock, redeem)
		linkCreated = created
		pending, ok := errors.AsType[*core.InviteProvisioningError](redeemErr)
		if !ok || pending == nil {
			return redeemErr
		}
		provisioningErr = redeemErr
		return insertPostgresProvisioningFailure(ctx, q, pending.Failure)
	})
	return linkCreated, errors.Join(provisioningErr, txErr)
}

func (s *postgresInvites) redeemLocked(
	ctx context.Context, q *postgres.Queries, hash [sha256.Size]byte, clock core.Clock, redeem core.InviteRedeemFunc,
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
	if insertErr := insertPostgresRedemption(ctx, q, redemption); insertErr != nil {
		return false, insertErr
	}
	linkCreated, err := insertPostgresInviteLink(ctx, q, redemption)
	if err != nil {
		return false, err
	}
	rows, err := q.IncrementInviteUse(ctx, postgres.IncrementInviteUseParams{ID: invite.ID, UpdatedAt: core.NormalizeTime(now)})
	if err != nil || rows != 1 {
		return false, fmt.Errorf("increment invite use: %w", errors.Join(err, core.ErrNotFound))
	}
	return linkCreated, nil
}

func insertPostgresInviteLink(
	ctx context.Context, q *postgres.Queries, redemption core.InviteRedemption,
) (bool, error) {
	if redemption.AccountID == "" {
		return false, nil
	}
	link := core.AccountMediaUser{
		AccountID: redemption.AccountID, MediaServerID: redemption.MediaServerID,
		MediaUserID: redemption.MediaUserID, Username: redemption.Username,
		Source: core.AccountMediaUserSourceInvite, CreatedAt: redemption.RedeemedAt, UpdatedAt: redemption.RedeemedAt,
	}
	rows, err := q.CreateAccountMediaUserIfAbsent(ctx,
		postgres.CreateAccountMediaUserIfAbsentParams(postgresAccountMediaUserParams(link)))
	if err != nil {
		return false, fmt.Errorf("insert invite account media user: %w", err)
	}
	return rows == 1, nil
}

func (s *postgresInvites) RecordInviteProvisioningFailure(
	ctx context.Context, failure core.InviteProvisioningFailure,
) error {
	return insertPostgresProvisioningFailure(ctx, s.q, failure)
}

func insertPostgresProvisioningFailure(
	ctx context.Context, q *postgres.Queries, failure core.InviteProvisioningFailure,
) error {
	if err := validateProvisioningFailure(failure); err != nil {
		return provisioningFailureRecordError(err)
	}
	err := q.InsertInviteProvisioningFailure(ctx, postgres.InsertInviteProvisioningFailureParams{
		ID: failure.ID, InviteID: failure.InviteID, MediaServerID: failure.MediaServerID,
		MediaUserID: nullableInviteMediaUserID(failure.MediaUserID), Username: failure.Username, Reason: string(failure.Reason),
		CreatedAt: core.NormalizeTime(failure.CreatedAt), UpdatedAt: core.NormalizeTime(failure.UpdatedAt),
	})
	if err != nil {
		return provisioningFailureRecordError(err)
	}
	return nil
}

func (s *postgresInvites) lockedInvite(
	ctx context.Context, q *postgres.Queries, hash [sha256.Size]byte,
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
	invite := postgresInvite(row.ID, row.MediaServerID, row.CreatedByAccountID, row.Label,
		row.ExpiresAt, row.MaxUses, row.UseCount, row.RevokedAt, row.CreatedAt, row.UpdatedAt)
	return s.withLibraries(ctx, q, invite)
}

func (s *postgresInvites) withLibraries(ctx context.Context, q *postgres.Queries, invite core.Invite) (core.Invite, error) {
	libraries, err := q.ListInviteLibraries(ctx, invite.ID)
	if err != nil {
		return core.Invite{}, fmt.Errorf("list invite libraries: %w", err)
	}
	invite.LibraryIDs = libraries
	return invite, nil
}

func insertPostgresRedemption(ctx context.Context, q *postgres.Queries, value core.InviteRedemption) error {
	err := q.InsertInviteRedemption(ctx, postgres.InsertInviteRedemptionParams{
		ID: value.ID, InviteID: value.InviteID, MediaServerID: value.MediaServerID,
		MediaUserID: value.MediaUserID, Username: value.Username, RedeemedAt: core.NormalizeTime(value.RedeemedAt),
	})
	if err != nil {
		return fmt.Errorf("insert invite redemption: %w", err)
	}
	return nil
}

func postgresInvite(
	id, serverID, createdBy, label string, expires sql.NullTime, maxUses sql.NullInt32,
	useCount int32, revoked sql.NullTime, created, updated time.Time,
) core.Invite {
	return core.Invite{
		ID: id, MediaServerID: serverID, CreatedBy: createdBy, Label: label,
		ExpiresAt: pointerTime(expires), MaxUses: pointerInt32(maxUses), UseCount: int(useCount),
		RevokedAt: pointerTime(revoked), CreatedAt: core.NormalizeTime(created), UpdatedAt: core.NormalizeTime(updated),
	}
}

func postgresNullableTime(value *time.Time) sql.NullTime {
	if value == nil {
		return sql.NullTime{}
	}
	return sql.NullTime{Time: core.NormalizeTime(*value), Valid: true}
}

func postgresNullableInt(value *int) sql.NullInt32 {
	if value == nil {
		return sql.NullInt32{}
	}
	return sql.NullInt32{Int32: int32(*value), Valid: true} //nolint:gosec // validated as at most 1000.
}

func pointerTime(value sql.NullTime) *time.Time {
	if !value.Valid {
		return nil
	}
	result := core.NormalizeTime(value.Time)
	return &result
}

func pointerInt32(value sql.NullInt32) *int {
	if !value.Valid {
		return nil
	}
	result := int(value.Int32)
	return &result
}
