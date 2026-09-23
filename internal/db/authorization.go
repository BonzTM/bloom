package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"

	"github.com/BonzTM/bloom/internal/config"
	"github.com/BonzTM/bloom/internal/core"
)

const maxRoleQueryPageSize = 101

// NewAuthorizationStores returns the authorization and role-reading seams for
// the configured engine.
func NewAuthorizationStores(pool *sql.DB, driver config.Driver) (core.Authorizer, core.RoleReader, error) {
	switch driver {
	case config.DriverSQLite:
		adapter := newSQLiteAuthorization(pool)
		return adapter, adapter, nil
	case config.DriverPostgres:
		adapter := newPostgresAuthorization(pool)
		return adapter, adapter, nil
	default:
		return nil, nil, fmt.Errorf("unsupported database driver %q", driver)
	}
}

// NewAdminAccountStore returns the atomic account-and-role bootstrap seam.
func NewAdminAccountStore(pool *sql.DB, driver config.Driver) (core.AdminAccountStore, error) {
	switch driver {
	case config.DriverSQLite:
		return newSQLiteAuthorization(pool), nil
	case config.DriverPostgres:
		return newPostgresAuthorization(pool), nil
	default:
		return nil, fmt.Errorf("unsupported database driver %q", driver)
	}
}

func permissionsFromStrings(values []string) ([]core.Permission, error) {
	permissions := make([]core.Permission, 0, len(values))
	for _, value := range values {
		permission := core.Permission(value)
		if !permission.Valid() {
			return nil, fmt.Errorf("unknown stored permission %q", value)
		}
		permissions = append(permissions, permission)
	}
	slices.Sort(permissions)
	return slices.Compact(permissions), nil
}

type authorizationRow struct {
	roleName   string
	permission sql.NullString
}

func authorizationSnapshot(rows []authorizationRow) (core.AuthorizationSnapshot, error) {
	roles := make([]string, 0)
	permissionValues := make([]string, 0)
	for _, row := range rows {
		if !core.ValidRoleName(row.roleName) {
			return core.AuthorizationSnapshot{}, fmt.Errorf("invalid stored role name %q", row.roleName)
		}
		if len(roles) == 0 || roles[len(roles)-1] != row.roleName {
			roles = append(roles, row.roleName)
		}
		if row.permission.Valid {
			permissionValues = append(permissionValues, row.permission.String)
		}
	}
	permissions, err := permissionsFromStrings(permissionValues)
	if err != nil {
		return core.AuthorizationSnapshot{}, err
	}
	return core.AuthorizationSnapshot{RoleNames: roles, Permissions: permissions}, nil
}

type roleRow struct {
	id, name, description string
	builtIn               bool
	createdAt             any
	permission            sql.NullString
}

func appendRolePermission(roles []core.Role, row roleRow, createdAt func(any) (core.Role, error)) ([]core.Role, error) {
	if len(roles) == 0 || roles[len(roles)-1].ID != row.id {
		role, err := createdAt(row.createdAt)
		if err != nil {
			return nil, err
		}
		role.ID, role.Name = row.id, row.name
		role.Description, role.BuiltIn = row.description, row.builtIn
		role.Permissions = make([]core.Permission, 0)
		roles = append(roles, role)
	}
	if row.permission.Valid {
		permission := core.Permission(row.permission.String)
		if !permission.Valid() {
			return nil, fmt.Errorf("role %q has unknown permission %q", row.name, permission)
		}
		roles[len(roles)-1].Permissions = append(roles[len(roles)-1].Permissions, permission)
	}
	return roles, nil
}

func withTransaction(ctx context.Context, pool *sql.DB, work func(*sql.Tx) error) (retErr error) {
	if pool == nil || work == nil {
		return fmt.Errorf("database transaction: %w", core.ErrInvalidArgument)
	}
	tx, err := pool.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin database transaction: %w", err)
	}
	defer func() {
		if rollbackErr := tx.Rollback(); rollbackErr != nil && !errors.Is(rollbackErr, sql.ErrTxDone) {
			retErr = errors.Join(retErr, fmt.Errorf("roll back database transaction: %w", rollbackErr))
		}
	}()
	if err := work(tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit database transaction: %w", err)
	}
	return nil
}

func roleNotFound(roleName string, err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("find role %q: %w", roleName, core.ErrNotFound)
	}
	return fmt.Errorf("find role %q: %w", roleName, err)
}

func accountLookupError(accountID string, err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("find account %q: %w", accountID, core.ErrNotFound)
	}
	return fmt.Errorf("find account %q: %w", accountID, err)
}
