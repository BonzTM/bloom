package db

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/db/sqlite"
)

type sqliteAuthorization struct {
	pool *sql.DB
	q    *sqlite.Queries
}

var (
	_ core.Authorizer        = (*sqliteAuthorization)(nil)
	_ core.RoleReader        = (*sqliteAuthorization)(nil)
	_ core.AdminAccountStore = (*sqliteAuthorization)(nil)
)

func newSQLiteAuthorization(pool *sql.DB) *sqliteAuthorization {
	return &sqliteAuthorization{pool: pool, q: sqlite.New(pool)}
}

func (s *sqliteAuthorization) Permissions(ctx context.Context, accountID string) ([]core.Permission, error) {
	values, err := s.q.ListAccountPermissions(ctx, accountID)
	if err != nil {
		return nil, fmt.Errorf("list account permissions: %w", err)
	}
	return permissionsFromStrings(values)
}

func (s *sqliteAuthorization) Snapshot(ctx context.Context, accountID string) (core.AuthorizationSnapshot, error) {
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

func (s *sqliteAuthorization) ListRoles(ctx context.Context, afterName string, pageSize int) ([]core.Role, error) {
	if pageSize < 1 || pageSize > maxRoleQueryPageSize {
		return nil, fmt.Errorf("list roles: %w", core.ErrInvalidArgument)
	}
	rows, err := s.q.ListRolesWithPermissions(ctx, sqlite.ListRolesWithPermissionsParams{
		AfterName: afterName, PageSize: int64(pageSize),
	})
	if err != nil {
		return nil, fmt.Errorf("list roles: %w", err)
	}
	roles := make([]core.Role, 0, 2)
	for _, row := range rows {
		roles, err = appendRolePermission(roles, roleRow{
			id: row.ID, name: row.Name, description: row.Description,
			builtIn: row.BuiltIn != 0, createdAt: row.CreatedAt, permission: row.Permission,
		}, sqliteRole)
		if err != nil {
			return nil, err
		}
	}
	return roles, nil
}

func sqliteRole(value any) (core.Role, error) {
	created, ok := value.(string)
	if !ok {
		return core.Role{}, fmt.Errorf("SQLite role timestamp has type %T", value)
	}
	createdAt, err := parseSQLiteTime(created)
	if err != nil {
		return core.Role{}, fmt.Errorf("parse role timestamp: %w", err)
	}
	return core.Role{CreatedAt: createdAt}, nil
}

func (s *sqliteAuthorization) CreateAccountWithRole(ctx context.Context, account core.Account, roleName string) error {
	if account.ID == "" || roleName == "" {
		return fmt.Errorf("create account with role: %w", core.ErrInvalidArgument)
	}
	return withTransaction(ctx, s.pool, func(tx *sql.Tx) error {
		q := s.q.WithTx(tx)
		roleID, err := q.GetRoleIDByName(ctx, roleName)
		if err != nil {
			return roleNotFound(roleName, err)
		}
		createErr := (&sqliteAccounts{q: q}).CreateAccount(ctx, account)
		if createErr != nil {
			return createErr
		}
		rows, err := q.AssignRoleIDToAccount(ctx, sqlite.AssignRoleIDToAccountParams{AccountID: account.ID, RoleID: roleID})
		if err != nil {
			return fmt.Errorf("assign initial role %q: %w", roleName, err)
		}
		if rows != 1 {
			return fmt.Errorf("assign initial role %q: affected %d rows", roleName, rows)
		}
		return nil
	})
}

func (s *sqliteAuthorization) GrantRole(ctx context.Context, accountID, roleName string) (bool, error) {
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
		rows, err := q.AssignRoleIDToAccount(ctx, sqlite.AssignRoleIDToAccountParams{AccountID: accountID, RoleID: roleID})
		if err != nil {
			return fmt.Errorf("grant role %q: %w", roleName, err)
		}
		assigned = rows == 1
		return nil
	})
	return assigned, err
}
