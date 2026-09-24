package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

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
		row.MediaUserID, row.Username, row.Source, row.CreatedAt, row.UpdatedAt, row.SuppressedAt)
}

func (s *sqliteAccountMediaUsers) ListAccountMediaUsers(
	ctx context.Context, accountID string, includeSuppressed bool, limit int,
) ([]core.AccountMediaUser, error) {
	if err := validateAccountMediaUserList(accountID, limit); err != nil {
		return nil, err
	}
	rows, err := s.q.ListAccountMediaUsers(ctx, sqlite.ListAccountMediaUsersParams{
		AccountID: accountID, IncludeSuppressed: boolInt64(includeSuppressed), PageSize: int64(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("list account media users: %w", err)
	}
	links := make([]core.AccountMediaUser, 0, len(rows))
	for _, row := range rows {
		link, mapErr := sqliteAccountMediaUser(row.AccountID, row.MediaServerID, row.MediaServerName,
			row.MediaUserID, row.Username, row.Source, row.CreatedAt, row.UpdatedAt, row.SuppressedAt)
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

func (s *sqliteAccountMediaUsers) SuppressAccountMediaUser(
	ctx context.Context, accountID, serverID string, suppressedAt time.Time,
) error {
	if err := validateAccountMediaUserQuery(accountID, serverID); err != nil {
		return err
	}
	if suppressedAt.IsZero() {
		return core.ErrInvalidArgument
	}
	value := formatSQLiteTime(core.NormalizeTime(suppressedAt))
	rows, err := s.q.SuppressAccountMediaUser(ctx, sqlite.SuppressAccountMediaUserParams{
		SuppressedAt: sql.NullString{String: value, Valid: true}, UpdatedAt: value,
		AccountID: accountID, MediaServerID: serverID,
	})
	if err != nil {
		return fmt.Errorf("suppress account media user: %w", err)
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
	suppressed sql.NullString,
) (core.AccountMediaUser, error) {
	createdAt, err := parseSQLiteTime(created)
	if err != nil {
		return core.AccountMediaUser{}, fmt.Errorf("parse account media user created_at: %w", err)
	}
	updatedAt, err := parseSQLiteTime(updated)
	if err != nil {
		return core.AccountMediaUser{}, fmt.Errorf("parse account media user updated_at: %w", err)
	}
	suppressedAt, err := parseSQLiteNullableTime(suppressed)
	if err != nil {
		return core.AccountMediaUser{}, fmt.Errorf("parse account media user suppressed_at: %w", err)
	}
	return core.AccountMediaUser{
		AccountID: accountID, MediaServerID: serverID, MediaServerName: serverName,
		MediaUserID: mediaUserID, Username: username, Source: core.AccountMediaUserSource(source),
		CreatedAt: createdAt, UpdatedAt: updatedAt, SuppressedAt: suppressedAt,
	}, nil
}

func boolInt64(value bool) int64 {
	if value {
		return 1
	}
	return 0
}
