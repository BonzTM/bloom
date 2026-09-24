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

type postgresAccountMediaUsers struct{ q *postgres.Queries }

var (
	_ core.AccountMediaUserReader = (*postgresAccountMediaUsers)(nil)
	_ core.AccountMediaUserWriter = (*postgresAccountMediaUsers)(nil)
)

func newPostgresAccountMediaUsers(pool *sql.DB) *postgresAccountMediaUsers {
	return &postgresAccountMediaUsers{q: postgres.New(pool)}
}

func (s *postgresAccountMediaUsers) GetAccountMediaUser(
	ctx context.Context, accountID, serverID string,
) (core.AccountMediaUser, error) {
	if err := validateAccountMediaUserQuery(accountID, serverID); err != nil {
		return core.AccountMediaUser{}, err
	}
	row, err := s.q.GetAccountMediaUser(ctx, postgres.GetAccountMediaUserParams{
		AccountID: accountID, MediaServerID: serverID,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return core.AccountMediaUser{}, core.ErrNotFound
	}
	if err != nil {
		return core.AccountMediaUser{}, fmt.Errorf("select account media user: %w", err)
	}
	return postgresAccountMediaUser(row.AccountID, row.MediaServerID, row.MediaServerName,
		row.MediaUserID, row.Username, row.Source, row.CreatedAt, row.UpdatedAt), nil
}

func (s *postgresAccountMediaUsers) ListAccountMediaUsers(
	ctx context.Context, accountID string, limit int,
) ([]core.AccountMediaUser, error) {
	if err := validateAccountMediaUserList(accountID, limit); err != nil {
		return nil, err
	}
	rows, err := s.q.ListAccountMediaUsers(ctx, postgres.ListAccountMediaUsersParams{
		AccountID: accountID, PageSize: int32(limit), //nolint:gosec // validated at 100.
	})
	if err != nil {
		return nil, fmt.Errorf("list account media users: %w", err)
	}
	links := make([]core.AccountMediaUser, 0, len(rows))
	for _, row := range rows {
		links = append(links, postgresAccountMediaUser(row.AccountID, row.MediaServerID, row.MediaServerName,
			row.MediaUserID, row.Username, row.Source, row.CreatedAt, row.UpdatedAt))
	}
	return links, nil
}

func (s *postgresAccountMediaUsers) SetAccountMediaUser(ctx context.Context, link core.AccountMediaUser) error {
	if err := core.ValidateAccountMediaUser(link); err != nil {
		return err
	}
	err := s.q.SetAccountMediaUser(ctx, postgresAccountMediaUserParams(link))
	if isPostgresUnique(err) {
		return fmt.Errorf("set account media user: %w", core.ErrAlreadyExists)
	}
	if err != nil {
		return fmt.Errorf("set account media user: %w", err)
	}
	return nil
}

func (s *postgresAccountMediaUsers) CreateAccountMediaUserIfAbsent(
	ctx context.Context, link core.AccountMediaUser,
) (bool, error) {
	if err := core.ValidateAccountMediaUser(link); err != nil {
		return false, err
	}
	rows, err := s.q.CreateAccountMediaUserIfAbsent(ctx, postgres.CreateAccountMediaUserIfAbsentParams(postgresAccountMediaUserParams(link)))
	if err != nil {
		return false, fmt.Errorf("insert account media user: %w", err)
	}
	return rows == 1, nil
}

func (s *postgresAccountMediaUsers) DeleteAccountMediaUser(ctx context.Context, accountID, serverID string) error {
	if err := validateAccountMediaUserQuery(accountID, serverID); err != nil {
		return err
	}
	rows, err := s.q.DeleteAccountMediaUser(ctx, postgres.DeleteAccountMediaUserParams{
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

func postgresAccountMediaUserParams(link core.AccountMediaUser) postgres.SetAccountMediaUserParams {
	return postgres.SetAccountMediaUserParams{
		AccountID: link.AccountID, MediaServerID: link.MediaServerID, MediaUserID: link.MediaUserID,
		Username: link.Username, Source: string(link.Source), CreatedAt: core.NormalizeTime(link.CreatedAt),
		UpdatedAt: core.NormalizeTime(link.UpdatedAt),
	}
}

func postgresAccountMediaUser(
	accountID, serverID, serverName, mediaUserID, username, source string,
	createdAt, updatedAt time.Time,
) core.AccountMediaUser {
	return core.AccountMediaUser{
		AccountID: accountID, MediaServerID: serverID, MediaServerName: serverName,
		MediaUserID: mediaUserID, Username: username, Source: core.AccountMediaUserSource(source),
		CreatedAt: core.NormalizeTime(createdAt), UpdatedAt: core.NormalizeTime(updatedAt),
	}
}
