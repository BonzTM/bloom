package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"

	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/db/postgres"
)

type postgresOIDCStore struct {
	pool *sql.DB
	q    *postgres.Queries
}

var _ core.OIDCAccountStore = (*postgresOIDCStore)(nil)

func newPostgresOIDCStore(pool *sql.DB) *postgresOIDCStore {
	return &postgresOIDCStore{pool: pool, q: postgres.New(pool)}
}

func (s *postgresOIDCStore) SignInOIDC(ctx context.Context, signIn core.OIDCSignIn) (core.OIDCSignInResult, error) {
	if err := validateOIDCSignIn(signIn); err != nil {
		return core.OIDCSignInResult{}, err
	}
	for collision := range maxOIDCUsernameTrials {
		var result core.OIDCSignInResult
		err := withTransaction(ctx, s.pool, func(tx *sql.Tx) error {
			resolved, txErr := s.signInTx(ctx, tx, s.q.WithTx(tx), signIn, collision)
			result = resolved
			return txErr
		})
		if err == nil || !errors.Is(err, core.ErrAlreadyExists) {
			return result, err
		}
	}
	return core.OIDCSignInResult{}, fmt.Errorf("derive unique OIDC username: %w", core.ErrAlreadyExists)
}

func (s *postgresOIDCStore) signInTx(
	ctx context.Context, tx *sql.Tx, q *postgres.Queries, signIn core.OIDCSignIn, collision int,
) (core.OIDCSignInResult, error) {
	if err := lockPostgresIdentity(ctx, tx, signIn.Issuer, signIn.Subject); err != nil {
		return core.OIDCSignInResult{}, err
	}
	row, err := q.GetAccountIdentity(ctx, postgres.GetAccountIdentityParams{Issuer: signIn.Issuer, Subject: signIn.Subject})
	if err == nil {
		return s.existingIdentity(ctx, q, row, signIn)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return core.OIDCSignInResult{}, fmt.Errorf("find OIDC identity: %w", err)
	}
	if !signIn.AutoProvision {
		return core.OIDCSignInResult{}, core.ErrOIDCProvisioningDisabled
	}
	return s.provisionIdentity(ctx, q, signIn, collision)
}

func lockPostgresIdentity(ctx context.Context, tx *sql.Tx, issuer, subject string) error {
	var marker int
	err := tx.QueryRowContext(ctx,
		"SELECT 1 FROM account_identities WHERE issuer = $1 AND subject = $2 FOR UPDATE",
		issuer, subject,
	).Scan(&marker)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("lock OIDC identity: %w", err)
	}
	return nil
}

func (s *postgresOIDCStore) existingIdentity(
	ctx context.Context, q *postgres.Queries, row postgres.GetAccountIdentityRow, signIn core.OIDCSignIn,
) (core.OIDCSignInResult, error) {
	account, err := postgresAccountFromRow(
		row.AccountID, row.Username, row.PasswordHash, row.Disabled,
		row.AccountCreatedAt, nil, "select OIDC account",
	)
	if err != nil {
		return core.OIDCSignInResult{}, err
	}
	if account.Disabled {
		return core.OIDCSignInResult{}, &core.CredentialFailure{Reason: core.CredentialDisabled}
	}
	oldRoles, err := decodeMappedRoles(row.MappedRoles)
	if err != nil {
		return core.OIDCSignInResult{}, err
	}
	roles := signIn.MappedRoles
	added, removed, err := syncPostgresRoles(ctx, q, account.ID, oldRoles, roles)
	if err != nil {
		return core.OIDCSignInResult{}, err
	}
	if err := updatePostgresIdentity(ctx, q, signIn, roles); err != nil {
		return core.OIDCSignInResult{}, err
	}
	return core.OIDCSignInResult{Account: account, AddedRoles: added, RemovedRoles: removed}, nil
}

func (s *postgresOIDCStore) provisionIdentity(
	ctx context.Context, q *postgres.Queries, signIn core.OIDCSignIn, collision int,
) (core.OIDCSignInResult, error) {
	accounts := &postgresAccounts{q: q}
	account, err := createOIDCAccount(ctx, accounts, signIn, collision)
	if err != nil {
		return core.OIDCSignInResult{}, err
	}
	added, err := assignPostgresRoles(ctx, q, account.ID, provisionRoles(signIn))
	if err != nil {
		return core.OIDCSignInResult{}, err
	}
	mapped, err := encodeMappedRoles(signIn.MappedRoles)
	if err != nil {
		return core.OIDCSignInResult{}, err
	}
	now := core.NormalizeTime(signIn.Now)
	err = q.CreateAccountIdentity(ctx, postgres.CreateAccountIdentityParams{
		AccountID: account.ID, Provider: signIn.Provider, Issuer: signIn.Issuer, Subject: signIn.Subject,
		UsernameClaim: signIn.UsernameClaim, MappedRoles: mapped, CreatedAt: now, LastLoginAt: now,
	})
	if err != nil {
		if isPostgresUnique(err) {
			return core.OIDCSignInResult{}, fmt.Errorf("create OIDC identity: %w", core.ErrAlreadyExists)
		}
		return core.OIDCSignInResult{}, fmt.Errorf("create OIDC identity: %w", err)
	}
	return core.OIDCSignInResult{Account: account, Provisioned: true, AddedRoles: added}, nil
}

func updatePostgresIdentity(ctx context.Context, q *postgres.Queries, signIn core.OIDCSignIn, roles []string) error {
	mapped, err := encodeMappedRoles(roles)
	if err != nil {
		return err
	}
	rows, err := q.UpdateAccountIdentityLogin(ctx, postgres.UpdateAccountIdentityLoginParams{
		UsernameClaim: signIn.UsernameClaim, MappedRoles: mapped,
		LastLoginAt: core.NormalizeTime(signIn.Now), Issuer: signIn.Issuer, Subject: signIn.Subject,
	})
	if err != nil {
		return fmt.Errorf("update OIDC identity: %w", err)
	}
	if rows != 1 {
		return fmt.Errorf("update OIDC identity: %w", core.ErrNotFound)
	}
	return nil
}

func assignPostgresRoles(ctx context.Context, q *postgres.Queries, accountID string, roles []string) ([]string, error) {
	added := make([]string, 0, len(roles))
	for _, role := range roles {
		roleID, err := q.GetRoleIDByName(ctx, role)
		if err != nil {
			return nil, oidcRoleLookupError(role, err)
		}
		rows, err := q.AssignOIDCRoleIDToAccount(ctx, postgres.AssignOIDCRoleIDToAccountParams{AccountID: accountID, RoleID: roleID})
		if err != nil {
			return nil, fmt.Errorf("assign OIDC role %q: %w", role, err)
		}
		if rows == 1 {
			added = append(added, role)
		}
	}
	return added, nil
}

func syncPostgresRoles(
	ctx context.Context, q *postgres.Queries, accountID string, oldRoles, newRoles []string,
) ([]string, []string, error) {
	removed := make([]string, 0, len(oldRoles))
	for _, role := range oldRoles {
		if slices.Contains(newRoles, role) {
			continue
		}
		roleID, err := q.GetRoleIDByName(ctx, role)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, nil, roleNotFound(role, err)
		}
		rows, err := q.RemoveRoleIDFromAccount(ctx, postgres.RemoveRoleIDFromAccountParams{AccountID: accountID, RoleID: roleID})
		if err != nil {
			return nil, nil, fmt.Errorf("remove mapped OIDC role %q: %w", role, err)
		}
		if rows == 1 {
			removed = append(removed, role)
		}
	}
	added, err := assignPostgresRoles(ctx, q, accountID, newRoles)
	return added, removed, err
}
