package db

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/db/postgres"
)

type postgresAuthorization struct {
	pool *sql.DB
	q    *postgres.Queries
}

var (
	_ core.Authorizer        = (*postgresAuthorization)(nil)
	_ core.RoleReader        = (*postgresAuthorization)(nil)
	_ core.AdminAccountStore = (*postgresAuthorization)(nil)
)

func newPostgresAuthorization(pool *sql.DB) *postgresAuthorization {
	return &postgresAuthorization{pool: pool, q: postgres.New(pool)}
}

func (s *postgresAuthorization) Permissions(ctx context.Context, accountID string) ([]core.Permission, error) {
	values, err := s.q.ListAccountPermissions(ctx, accountID)
	if err != nil {
		return nil, fmt.Errorf("list account permissions: %w", err)
	}
	return permissionsFromStrings(values)
}

func (s *postgresAuthorization) Snapshot(ctx context.Context, accountID string) (core.AuthorizationSnapshot, error) {
	rows, err := s.q.GetAuthorizationSnapshot(ctx, accountID)
	if err != nil {
		return core.AuthorizationSnapshot{}, fmt.Errorf("get authorization snapshot: %w", err)
	}
	values := make([]authorizationRow, 0, len(rows))
	for _, row := range rows {
		values = append(values, authorizationRow{roleName: row.RoleName, permission: row.Permission})
	}
	return authorizationSnapshot(values)
}

func (s *postgresAuthorization) ListRoles(ctx context.Context, afterName string, pageSize int) ([]core.Role, error) {
	if pageSize < 1 || pageSize > maxRoleQueryPageSize {
		return nil, fmt.Errorf("list roles: %w", core.ErrInvalidArgument)
	}
	rows, err := s.q.ListRolesWithPermissions(ctx, postgres.ListRolesWithPermissionsParams{
		AfterName: afterName, PageSize: int32(pageSize), // page size is transport-bounded well below int32.
	})
	if err != nil {
		return nil, fmt.Errorf("list roles: %w", err)
	}
	roles := make([]core.Role, 0, 2)
	for _, row := range rows {
		roles, err = appendRolePermission(roles, roleRow{
			id: row.ID, name: row.Name, description: row.Description,
			builtIn: row.BuiltIn, createdAt: row.CreatedAt, permission: row.Permission,
		}, postgresRole)
		if err != nil {
			return nil, err
		}
	}
	return roles, nil
}

func postgresRole(value any) (core.Role, error) {
	createdAt, ok := value.(time.Time)
	if !ok {
		return core.Role{}, fmt.Errorf("PostgreSQL role timestamp has type %T", value)
	}
	return core.Role{CreatedAt: core.NormalizeTime(createdAt)}, nil
}

func (s *postgresAuthorization) CreateAccountWithRole(ctx context.Context, account core.Account, roleName string) error {
	if account.ID == "" || roleName == "" {
		return fmt.Errorf("create account with role: %w", core.ErrInvalidArgument)
	}
	return withTransaction(ctx, s.pool, func(tx *sql.Tx) error {
		q := s.q.WithTx(tx)
		roleID, err := q.GetRoleIDByName(ctx, roleName)
		if err != nil {
			return roleNotFound(roleName, err)
		}
		createErr := (&postgresAccounts{q: q}).CreateAccount(ctx, account)
		if createErr != nil {
			return createErr
		}
		rows, err := q.AssignRoleIDToAccount(ctx, postgres.AssignRoleIDToAccountParams{AccountID: account.ID, RoleID: roleID})
		if err != nil {
			return fmt.Errorf("assign initial role %q: %w", roleName, err)
		}
		if rows != 1 {
			return fmt.Errorf("assign initial role %q: affected %d rows", roleName, rows)
		}
		return nil
	})
}

func (s *postgresAuthorization) GrantRole(ctx context.Context, accountID, roleName string) (bool, error) {
	if accountID == "" || roleName == "" {
		return false, fmt.Errorf("grant role: %w", core.ErrInvalidArgument)
	}
	var assigned bool
	err := withTransaction(ctx, s.pool, func(tx *sql.Tx) error {
		q := s.q.WithTx(tx)
		if _, err := q.GetAccount(ctx, accountID); err != nil {
			return accountLookupError(accountID, err)
		}
		roleID, err := q.GetRoleIDByName(ctx, roleName)
		if err != nil {
			return roleNotFound(roleName, err)
		}
		rows, err := q.AssignRoleIDToAccount(ctx, postgres.AssignRoleIDToAccountParams{AccountID: accountID, RoleID: roleID})
		if err != nil {
			return fmt.Errorf("grant role %q: %w", roleName, err)
		}
		assigned = rows == 1
		return nil
	})
	return assigned, err
}
