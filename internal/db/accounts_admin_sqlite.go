package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/db/sqlite"
)

type sqliteAccountAdminReader struct{ q *sqlite.Queries }

var _ core.AccountAdminReader = (*sqliteAccountAdminReader)(nil)

func newSQLiteAccountAdminReader(pool *sql.DB) *sqliteAccountAdminReader {
	return &sqliteAccountAdminReader{q: sqlite.New(pool)}
}

func (s *sqliteAccountAdminReader) ListAccounts(
	ctx context.Context, query core.AccountListQuery,
) (core.AccountPage, error) {
	if !query.Valid() {
		return core.AccountPage{}, core.ErrInvalidArgument
	}
	afterKey, afterID := "", ""
	if query.After != nil {
		afterKey, afterID = query.After.UsernameKey, query.After.ID
	}
	rows, err := s.q.ListAdminAccounts(ctx, sqlite.ListAdminAccountsParams{
		SearchKey: query.SearchKey, AfterUsernameKey: afterKey, AfterID: afterID,
		PageSize: int64(query.Limit + 1),
	})
	if err != nil {
		return core.AccountPage{}, fmt.Errorf("list administrative accounts: %w", err)
	}
	hasMore := len(rows) > query.Limit
	if hasMore {
		rows = rows[:query.Limit]
	}
	accounts := make([]core.AdminAccount, 0, len(rows))
	for _, row := range rows {
		createdAt, parseErr := parseSQLiteTime(row.CreatedAt)
		if parseErr != nil {
			return core.AccountPage{}, fmt.Errorf("parse account timestamp %q: %w", row.ID, parseErr)
		}
		account, mapErr := newAdminAccount(row.ID, row.Username, row.UsernameKey, createdAt, row.HasLocal != 0, row.HasOidc)
		if mapErr != nil {
			return core.AccountPage{}, mapErr
		}
		accounts = append(accounts, account)
	}
	if err := s.enrich(ctx, accounts); err != nil {
		return core.AccountPage{}, err
	}
	return core.AccountPage{Items: accounts, HasMore: hasMore}, nil
}

func (s *sqliteAccountAdminReader) GetAdminAccount(ctx context.Context, id string) (core.AdminAccount, error) {
	if !core.ValidID(id) {
		return core.AdminAccount{}, core.ErrInvalidArgument
	}
	row, err := s.q.GetAdminAccount(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return core.AdminAccount{}, core.ErrNotFound
	}
	if err != nil {
		return core.AdminAccount{}, fmt.Errorf("get administrative account: %w", err)
	}
	createdAt, err := parseSQLiteTime(row.CreatedAt)
	if err != nil {
		return core.AdminAccount{}, fmt.Errorf("parse account timestamp %q: %w", row.ID, err)
	}
	account, err := newAdminAccount(row.ID, row.Username, row.UsernameKey, createdAt, row.HasLocal != 0, row.HasOidc)
	if err != nil {
		return core.AdminAccount{}, err
	}
	accounts := []core.AdminAccount{account}
	if err := s.enrich(ctx, accounts); err != nil {
		return core.AdminAccount{}, err
	}
	return accounts[0], nil
}

func (s *sqliteAccountAdminReader) enrich(ctx context.Context, accounts []core.AdminAccount) error {
	if len(accounts) == 0 {
		return nil
	}
	encoded, err := accountIDsJSON(accountPageIDs(accounts))
	if err != nil {
		return fmt.Errorf("encode administrative account page: %w", err)
	}
	indexes := accountIndexes(accounts)
	roles, err := s.q.ListAdminAccountRoles(ctx, encoded)
	if err != nil {
		return fmt.Errorf("list administrative account roles: %w", err)
	}
	for _, role := range roles {
		if roleErr := appendAdminRole(accounts, indexes, role.AccountID, role.Name, role.Source); roleErr != nil {
			return roleErr
		}
	}
	links, err := s.q.ListAdminAccountMediaUsers(ctx, encoded)
	if err != nil {
		return fmt.Errorf("list administrative account media users: %w", err)
	}
	for _, row := range links {
		link, mapErr := sqliteAccountMediaUser(
			row.AccountID, row.MediaServerID, row.MediaServerName, row.MediaUserID, row.Username,
			row.Source, row.CreatedAt, row.UpdatedAt, row.SuppressedAt,
		)
		if mapErr != nil {
			return mapErr
		}
		if err := appendAdminMediaUser(accounts, indexes, link); err != nil {
			return err
		}
	}
	return nil
}
