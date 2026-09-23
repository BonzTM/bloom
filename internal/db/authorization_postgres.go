package db

import (
	"context"
	"database/sql"
	"errors"
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
	_ core.Authorizer            = (*postgresAuthorization)(nil)
	_ core.RoleReader            = (*postgresAuthorization)(nil)
	_ core.AdminAccountStore     = (*postgresAuthorization)(nil)
	_ core.BootstrapAccountStore = (*postgresAuthorization)(nil)
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

func (s *postgresAuthorization) RoleExists(ctx context.Context, name string) (bool, error) {
	if !core.ValidRoleName(name) {
		return false, fmt.Errorf("find role: %w", core.ErrInvalidArgument)
	}
	_, err := s.q.GetRoleIDByName(ctx, name)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return false, fmt.Errorf("find role %q: %w", name, err)
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
		return createPostgresAccountWithRole(ctx, s.q.WithTx(tx), account, roleName)
	})
}

func (s *postgresAuthorization) CreateFirstAccountWithRole(
	ctx context.Context, account core.Account, roleName string,
) (bool, error) {
	if account.ID == "" || roleName == "" {
		return false, fmt.Errorf("create first account with role: %w", core.ErrInvalidArgument)
	}
	created := false
	err := withTransaction(ctx, s.pool, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, "LOCK TABLE accounts IN SHARE ROW EXCLUSIVE MODE"); err != nil {
			return fmt.Errorf("lock accounts for first-account creation: %w", err)
		}
		q := s.q.WithTx(tx)
		count, err := q.CountAccounts(ctx)
		if err != nil {
			return fmt.Errorf("count accounts: %w", err)
		}
		if count > 0 {
			return nil
		}
		if err := createPostgresAccountWithRole(ctx, q, account, roleName); err != nil {
			return err
		}
		created = true
		return nil
	})
	return created, err
}

func createPostgresAccountWithRole(
	ctx context.Context, q *postgres.Queries, account core.Account, roleName string,
) error {
	if err := (&postgresAccounts{q: q}).CreateAccount(ctx, account); err != nil {
		return err
	}
	roleID, err := q.GetRoleIDByName(ctx, roleName)
	if err != nil {
		return roleNotFound(roleName, err)
	}
	rows, err := q.AssignRoleIDToAccount(ctx, postgres.AssignRoleIDToAccountParams{
		AccountID: account.ID, RoleID: roleID,
	})
	if err != nil {
		return fmt.Errorf("assign initial role %q: %w", roleName, err)
	}
	if rows != 1 {
		return fmt.Errorf("assign initial role %q: affected %d rows", roleName, rows)
	}
	return nil
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
