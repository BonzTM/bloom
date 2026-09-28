package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/db/postgres"
)

type postgresAccountAdminReader struct{ q *postgres.Queries }

var _ core.AccountAdminReader = (*postgresAccountAdminReader)(nil)

func newPostgresAccountAdminReader(pool *sql.DB) *postgresAccountAdminReader {
	return &postgresAccountAdminReader{q: postgres.New(pool)}
}

func (s *postgresAccountAdminReader) ListAccounts(
	ctx context.Context, query core.AccountListQuery,
) (core.AccountPage, error) {
	if !query.Valid() {
		return core.AccountPage{}, core.ErrInvalidArgument
	}
	afterKey, afterID := "", ""
	if query.After != nil {
		afterKey, afterID = query.After.UsernameKey, query.After.ID
	}
	rows, err := s.q.ListAdminAccounts(ctx, postgres.ListAdminAccountsParams{
		SearchKey: query.SearchKey, AfterUsernameKey: afterKey, AfterID: afterID,
		PageSize: int32(query.Limit + 1), //nolint:gosec // query validation bounds the value at 101.
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
		account, mapErr := newAdminAccount(
			row.ID, row.Username, row.UsernameKey, core.NormalizeTime(row.CreatedAt), row.HasLocal, row.HasOidc,
		)
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

func (s *postgresAccountAdminReader) GetAdminAccount(ctx context.Context, id string) (core.AdminAccount, error) {
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
	account, err := newAdminAccount(
		row.ID, row.Username, row.UsernameKey, core.NormalizeTime(row.CreatedAt), row.HasLocal, row.HasOidc,
	)
	if err != nil {
		return core.AdminAccount{}, err
	}
	accounts := []core.AdminAccount{account}
	if err := s.enrich(ctx, accounts); err != nil {
		return core.AdminAccount{}, err
	}
	return accounts[0], nil
}

func (s *postgresAccountAdminReader) enrich(ctx context.Context, accounts []core.AdminAccount) error {
	if len(accounts) == 0 {
		return nil
	}
	encoded, err := accountIDsJSON(accountPageIDs(accounts))
	if err != nil {
		return fmt.Errorf("encode administrative account page: %w", err)
	}
	indexes := accountIndexes(accounts)
	if err := s.enrichRoles(ctx, accounts, indexes, json.RawMessage(encoded)); err != nil {
		return err
	}
	return s.enrichMediaUsers(ctx, accounts, indexes, json.RawMessage(encoded))
}

func (s *postgresAccountAdminReader) enrichRoles(
	ctx context.Context, accounts []core.AdminAccount, indexes map[string]int, encoded json.RawMessage,
) error {
	rowLimit, err := accountEnrichmentRowLimit(len(accounts), accountRoleEnrichment)
	if err != nil {
		return err
	}
	roles, err := s.q.ListAdminAccountRoles(ctx, postgres.ListAdminAccountRolesParams{
		AccountIdsJson: encoded,
		RowLimit:       int32(rowLimit), //nolint:gosec // page and per-account bounds cap this at 10001.
	})
	if err != nil {
		return fmt.Errorf("list administrative account roles: %w", err)
	}
	if err := checkAccountEnrichmentLookahead(len(roles), rowLimit, "role"); err != nil {
		return err
	}
	for _, role := range roles {
		if roleErr := appendAdminRole(accounts, indexes, role.AccountID, role.Name, role.Source); roleErr != nil {
			return roleErr
		}
	}
	return nil
}

func (s *postgresAccountAdminReader) enrichMediaUsers(
	ctx context.Context, accounts []core.AdminAccount, indexes map[string]int, encoded json.RawMessage,
) error {
	rowLimit, err := accountEnrichmentRowLimit(len(accounts), accountMediaUserEnrichment)
	if err != nil {
		return err
	}
	links, err := s.q.ListAdminAccountMediaUsers(ctx, postgres.ListAdminAccountMediaUsersParams{
		AccountIdsJson: encoded,
		RowLimit:       int32(rowLimit), //nolint:gosec // page and per-account bounds cap this at 10001.
	})
	if err != nil {
		return fmt.Errorf("list administrative account media users: %w", err)
	}
	if err := checkAccountEnrichmentLookahead(len(links), rowLimit, "media-user"); err != nil {
		return err
	}
	for _, row := range links {
		link := postgresAccountMediaUser(
			row.AccountID, row.MediaServerID, row.MediaServerName, row.MediaUserID, row.Username,
			row.Source, row.CreatedAt, row.UpdatedAt, row.SuppressedAt,
		)
		if err := appendAdminMediaUser(accounts, indexes, link); err != nil {
			return err
		}
	}
	return nil
}
