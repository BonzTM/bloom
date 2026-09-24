package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/db/sqlite"
)

type sqliteAccountMediaUsers struct{ q *sqlite.Queries }

var (
	_ core.AccountMediaUserReader = (*sqliteAccountMediaUsers)(nil)
	_ core.AccountMediaUserWriter = (*sqliteAccountMediaUsers)(nil)
)

func newSQLiteAccountMediaUsers(pool *sql.DB) *sqliteAccountMediaUsers {
	return &sqliteAccountMediaUsers{q: sqlite.New(pool)}
}

func (s *sqliteAccountMediaUsers) GetAccountMediaUser(
	ctx context.Context, accountID, serverID string,
) (core.AccountMediaUser, error) {
	if err := validateAccountMediaUserQuery(accountID, serverID); err != nil {
		return core.AccountMediaUser{}, err
	}
	row, err := s.q.GetAccountMediaUser(ctx, sqlite.GetAccountMediaUserParams{
		AccountID: accountID, MediaServerID: serverID,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return core.AccountMediaUser{}, core.ErrNotFound
	}
	if err != nil {
		return core.AccountMediaUser{}, fmt.Errorf("select account media user: %w", err)
	}
	return sqliteAccountMediaUser(row.AccountID, row.MediaServerID, row.MediaServerName,
		row.MediaUserID, row.Username, row.Source, row.CreatedAt, row.UpdatedAt)
}

func (s *sqliteAccountMediaUsers) ListAccountMediaUsers(
	ctx context.Context, accountID string, limit int,
) ([]core.AccountMediaUser, error) {
	if err := validateAccountMediaUserList(accountID, limit); err != nil {
		return nil, err
	}
	rows, err := s.q.ListAccountMediaUsers(ctx, sqlite.ListAccountMediaUsersParams{
		AccountID: accountID, PageSize: int64(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("list account media users: %w", err)
	}
	links := make([]core.AccountMediaUser, 0, len(rows))
	for _, row := range rows {
		link, mapErr := sqliteAccountMediaUser(row.AccountID, row.MediaServerID, row.MediaServerName,
			row.MediaUserID, row.Username, row.Source, row.CreatedAt, row.UpdatedAt)
		if mapErr != nil {
			return nil, mapErr
		}
		links = append(links, link)
	}
	return links, nil
}

func (s *sqliteAccountMediaUsers) SetAccountMediaUser(ctx context.Context, link core.AccountMediaUser) error {
	if err := core.ValidateAccountMediaUser(link); err != nil {
		return err
	}
	err := s.q.SetAccountMediaUser(ctx, sqliteAccountMediaUserParams(link))
	if isSQLiteUnique(err) {
		return fmt.Errorf("set account media user: %w", core.ErrAlreadyExists)
	}
	if err != nil {
		return fmt.Errorf("set account media user: %w", err)
	}
	return nil
}

func (s *sqliteAccountMediaUsers) CreateAccountMediaUserIfAbsent(
	ctx context.Context, link core.AccountMediaUser,
) (bool, error) {
	if err := core.ValidateAccountMediaUser(link); err != nil {
		return false, err
	}
	rows, err := s.q.CreateAccountMediaUserIfAbsent(ctx, sqlite.CreateAccountMediaUserIfAbsentParams(sqliteAccountMediaUserParams(link)))
	if err != nil {
		return false, fmt.Errorf("insert account media user: %w", err)
	}
	return rows == 1, nil
}

func (s *sqliteAccountMediaUsers) DeleteAccountMediaUser(ctx context.Context, accountID, serverID string) error {
	if err := validateAccountMediaUserQuery(accountID, serverID); err != nil {
		return err
	}
	rows, err := s.q.DeleteAccountMediaUser(ctx, sqlite.DeleteAccountMediaUserParams{
		AccountID: accountID, MediaServerID: serverID,
	})
	if err != nil {
		return fmt.Errorf("delete account media user: %w", err)
	}
	if rows != 1 {
		return core.ErrNotFound
	}
	return nil
}

func sqliteAccountMediaUserParams(link core.AccountMediaUser) sqlite.SetAccountMediaUserParams {
	return sqlite.SetAccountMediaUserParams{
		AccountID: link.AccountID, MediaServerID: link.MediaServerID, MediaUserID: link.MediaUserID,
		Username: link.Username, Source: string(link.Source), CreatedAt: formatSQLiteTime(link.CreatedAt),
		UpdatedAt: formatSQLiteTime(link.UpdatedAt),
	}
}

func sqliteAccountMediaUser(
	accountID, serverID, serverName, mediaUserID, username, source, created, updated string,
) (core.AccountMediaUser, error) {
	createdAt, err := parseSQLiteTime(created)
	if err != nil {
		return core.AccountMediaUser{}, fmt.Errorf("parse account media user created_at: %w", err)
	}
	updatedAt, err := parseSQLiteTime(updated)
	if err != nil {
		return core.AccountMediaUser{}, fmt.Errorf("parse account media user updated_at: %w", err)
	}
	return core.AccountMediaUser{
		AccountID: accountID, MediaServerID: serverID, MediaServerName: serverName,
		MediaUserID: mediaUserID, Username: username, Source: core.AccountMediaUserSource(source),
		CreatedAt: createdAt, UpdatedAt: updatedAt,
	}, nil
}
