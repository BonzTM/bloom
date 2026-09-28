package db

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/BonzTM/bloom/internal/config"
	"github.com/BonzTM/bloom/internal/core"
)

// NewAccountAdminReader returns secret-free administrative account reads.
func NewAccountAdminReader(pool *sql.DB, driver config.Driver) (core.AccountAdminReader, error) {
	if pool == nil {
		return nil, fmt.Errorf("account administration reader: %w", core.ErrInvalidArgument)
	}
	switch driver {
	case config.DriverSQLite:
		return newSQLiteAccountAdminReader(pool), nil
	case config.DriverPostgres:
		return newPostgresAccountAdminReader(pool), nil
	default:
		return nil, fmt.Errorf("unsupported database driver %q", driver)
	}
}

func newAdminAccount(
	id, username, usernameKey string,
	createdAt time.Time,
	hasLocal, hasOIDC bool,
) (core.AdminAccount, error) {
	key, keyErr := core.UsernameKey(username)
	if !core.ValidID(id) || keyErr != nil || key != usernameKey || createdAt.IsZero() {
		return core.AdminAccount{}, fmt.Errorf("account %q has invalid administrative fields", id)
	}
	method, err := core.SignInMethod(hasLocal, hasOIDC)
	if err != nil {
		return core.AdminAccount{}, fmt.Errorf("account %q has no sign-in method: %w", id, err)
	}
	return core.AdminAccount{
		ID: id, Username: username, UsernameKey: usernameKey, CreatedAt: createdAt,
		SignInMethod: method, Roles: []core.AccountRoleAssignment{}, MediaUsers: []core.AccountMediaUser{},
	}, nil
}

func accountPageIDs(accounts []core.AdminAccount) []string {
	ids := make([]string, 0, len(accounts))
	for _, account := range accounts {
		ids = append(ids, account.ID)
	}
	return ids
}

func accountIndexes(accounts []core.AdminAccount) map[string]int {
	indexes := make(map[string]int, len(accounts))
	for index := range accounts {
		indexes[accounts[index].ID] = index
	}
	return indexes
}

type accountEnrichmentKind uint8

const (
	accountRoleEnrichment accountEnrichmentKind = iota + 1
	accountMediaUserEnrichment
)

func accountEnrichmentRowLimit(accountCount int, kind accountEnrichmentKind) (int, error) {
	if accountCount < 1 || accountCount > core.MaxAccountListPageSize {
		return 0, errors.New("invalid administrative account enrichment bounds")
	}
	var perAccountBound int
	switch kind {
	case accountRoleEnrichment:
		perAccountBound = core.MaxAccountRoleAssignments
	case accountMediaUserEnrichment:
		perAccountBound = core.MaxAccountLinkedMediaUsers
	default:
		return 0, errors.New("invalid administrative account enrichment kind")
	}
	return accountCount*perAccountBound + 1, nil
}

func checkAccountEnrichmentLookahead(rowCount, rowLimit int, kind string) error {
	if rowCount < rowLimit {
		return nil
	}
	return fmt.Errorf("administrative account %s enrichment exceeds its enforced bound", kind)
}

func appendAdminRole(accounts []core.AdminAccount, indexes map[string]int, accountID, name, source string) error {
	index, ok := indexes[accountID]
	roleSource := core.AccountRoleSource(source)
	if !ok || !core.ValidRoleName(name) || !roleSource.Valid() {
		return fmt.Errorf("invalid stored account role for %q", accountID)
	}
	if len(accounts[index].Roles) >= core.MaxAccountRoleAssignments {
		return errors.New("administrative account role enrichment exceeds its enforced per-account bound")
	}
	accounts[index].Roles = append(accounts[index].Roles, core.AccountRoleAssignment{Name: name, Source: roleSource})
	return nil
}

func appendAdminMediaUser(
	accounts []core.AdminAccount, indexes map[string]int, link core.AccountMediaUser,
) error {
	index, ok := indexes[link.AccountID]
	if !ok {
		return fmt.Errorf("invalid stored account media user for %q", link.AccountID)
	}
	if len(accounts[index].MediaUsers) >= core.MaxAccountLinkedMediaUsers {
		return errors.New("administrative account media-user enrichment exceeds its enforced per-account bound")
	}
	accounts[index].MediaUsers = append(accounts[index].MediaUsers, link)
	return nil
}
