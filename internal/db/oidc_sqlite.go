package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/db/sqlite"
)

type sqliteOIDCStore struct {
	pool *sql.DB
}

var _ core.OIDCAccountStore = (*sqliteOIDCStore)(nil)

func newSQLiteOIDCStore(pool *sql.DB) *sqliteOIDCStore {
	return &sqliteOIDCStore{pool: pool}
}

func (s *sqliteOIDCStore) SignInOIDC(ctx context.Context, signIn core.OIDCSignIn) (core.OIDCSignInResult, error) {
	if err := validateOIDCSignIn(signIn); err != nil {
		return core.OIDCSignInResult{}, err
	}
	for collision := range maxOIDCUsernameTrials {
		var result core.OIDCSignInResult
		err := withSQLiteOIDCTransaction(ctx, s.pool, func(q *sqlite.Queries) error {
			resolved, txErr := s.signInTx(ctx, q, signIn, collision)
			result = resolved
			return txErr
		})
		if err == nil || !errors.Is(err, core.ErrAlreadyExists) {
			return result, err
		}
	}
	return core.OIDCSignInResult{}, fmt.Errorf("derive unique OIDC username: %w", core.ErrAlreadyExists)
}

func withSQLiteOIDCTransaction(
	ctx context.Context, pool *sql.DB, work func(*sqlite.Queries) error,
) (retErr error) {
	conn, err := pool.Conn(ctx)
	if err != nil {
		return fmt.Errorf("acquire SQLite OIDC connection: %w", err)
	}
	defer func() { retErr = errors.Join(retErr, conn.Close()) }()
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return fmt.Errorf("begin SQLite OIDC transaction: %w", err)
	}
	if err := work(sqlite.New(conn)); err != nil {
		return errors.Join(err, rollbackSQLiteOIDC(ctx, conn))
	}
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return errors.Join(fmt.Errorf("commit SQLite OIDC transaction: %w", err), rollbackSQLiteOIDC(ctx, conn))
	}
	return nil
}

func rollbackSQLiteOIDC(ctx context.Context, conn *sql.Conn) error {
	rollbackCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if _, err := conn.ExecContext(rollbackCtx, "ROLLBACK"); err != nil {
		return fmt.Errorf("roll back SQLite OIDC transaction: %w", err)
	}
	return nil
}

func (s *sqliteOIDCStore) signInTx(
	ctx context.Context, q *sqlite.Queries, signIn core.OIDCSignIn, collision int,
) (core.OIDCSignInResult, error) {
	row, err := q.GetAccountIdentity(ctx, sqlite.GetAccountIdentityParams{Issuer: signIn.Issuer, Subject: signIn.Subject})
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

func (s *sqliteOIDCStore) existingIdentity(
	ctx context.Context, q *sqlite.Queries, row sqlite.GetAccountIdentityRow, signIn core.OIDCSignIn,
) (core.OIDCSignInResult, error) {
	account, err := sqliteAccountFromRow(
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
	added, removed, err := syncSQLiteRoles(ctx, q, account.ID, oldRoles, roles)
	if err != nil {
		return core.OIDCSignInResult{}, err
	}
	if err := updateSQLiteIdentity(ctx, q, signIn, roles); err != nil {
		return core.OIDCSignInResult{}, err
	}
	return core.OIDCSignInResult{Account: account, AddedRoles: added, RemovedRoles: removed}, nil
}

func (s *sqliteOIDCStore) provisionIdentity(
	ctx context.Context, q *sqlite.Queries, signIn core.OIDCSignIn, collision int,
) (core.OIDCSignInResult, error) {
	accounts := &sqliteAccounts{q: q}
	account, err := createOIDCAccount(ctx, accounts, signIn, collision)
	if err != nil {
		return core.OIDCSignInResult{}, err
	}
	added, err := assignSQLiteRoles(ctx, q, account.ID, provisionRoles(signIn))
	if err != nil {
		return core.OIDCSignInResult{}, err
	}
	mapped, err := encodeMappedRoles(signIn.MappedRoles)
	if err != nil {
		return core.OIDCSignInResult{}, err
	}
	now := formatSQLiteTime(signIn.Now)
	err = q.CreateAccountIdentity(ctx, sqlite.CreateAccountIdentityParams{
		AccountID: account.ID, Provider: signIn.Provider, Issuer: signIn.Issuer, Subject: signIn.Subject,
		UsernameClaim: signIn.UsernameClaim, MappedRoles: mapped, CreatedAt: now, LastLoginAt: now,
	})
	if err != nil {
		if isSQLiteUnique(err) {
			return core.OIDCSignInResult{}, fmt.Errorf("create OIDC identity: %w", core.ErrAlreadyExists)
		}
		return core.OIDCSignInResult{}, fmt.Errorf("create OIDC identity: %w", err)
	}
	return core.OIDCSignInResult{Account: account, Provisioned: true, AddedRoles: added}, nil
}

func updateSQLiteIdentity(ctx context.Context, q *sqlite.Queries, signIn core.OIDCSignIn, roles []string) error {
	mapped, err := encodeMappedRoles(roles)
	if err != nil {
		return err
	}
	rows, err := q.UpdateAccountIdentityLogin(ctx, sqlite.UpdateAccountIdentityLoginParams{
		UsernameClaim: signIn.UsernameClaim, MappedRoles: mapped,
		LastLoginAt: formatSQLiteTime(signIn.Now), Issuer: signIn.Issuer, Subject: signIn.Subject,
	})
	if err != nil {
		return fmt.Errorf("update OIDC identity: %w", err)
	}
	if rows != 1 {
		return fmt.Errorf("update OIDC identity: %w", core.ErrNotFound)
	}
	return nil
}

func assignSQLiteRoles(ctx context.Context, q *sqlite.Queries, accountID string, roles []string) ([]string, error) {
	added := make([]string, 0, len(roles))
	for _, role := range roles {
		roleID, err := q.GetRoleIDByName(ctx, role)
		if err != nil {
			return nil, oidcRoleLookupError(role, err)
		}
		rows, err := q.AssignOIDCRoleIDToAccount(ctx, sqlite.AssignOIDCRoleIDToAccountParams{AccountID: accountID, RoleID: roleID})
		if err != nil {
			return nil, fmt.Errorf("assign OIDC role %q: %w", role, err)
		}
		if rows == 1 {
			added = append(added, role)
		}
	}
	return added, nil
}

func syncSQLiteRoles(
	ctx context.Context, q *sqlite.Queries, accountID string, oldRoles, newRoles []string,
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
		rows, err := q.RemoveRoleIDFromAccount(ctx, sqlite.RemoveRoleIDFromAccountParams{AccountID: accountID, RoleID: roleID})
		if err != nil {
			return nil, nil, fmt.Errorf("remove mapped OIDC role %q: %w", role, err)
		}
		if rows == 1 {
			removed = append(removed, role)
		}
	}
	added, err := assignSQLiteRoles(ctx, q, accountID, newRoles)
	return added, removed, err
}
