package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/db/sqlite"
)

type sqliteMediaServers struct{ q *sqlite.Queries }

var (
	_ core.MediaServerReader = (*sqliteMediaServers)(nil)
	_ core.MediaServerWriter = (*sqliteMediaServers)(nil)
)

func newSQLiteMediaServers(pool *sql.DB) *sqliteMediaServers {
	return &sqliteMediaServers{q: sqlite.New(pool)}
}

func (s *sqliteMediaServers) CreateMediaServer(ctx context.Context, record core.MediaServerRecord) error {
	if err := validateMediaServerRecord(record); err != nil {
		return fmt.Errorf("insert media server: %w", err)
	}
	err := s.q.CreateMediaServer(ctx, sqlite.CreateMediaServerParams{
		ID: record.ID, Kind: string(record.Kind), Name: record.Name,
		NameKey: core.MediaServerNameKey(record.Name), BaseUrl: record.BaseURL,
		AllowInsecure:        boolToInt64(record.AllowInsecure),
		CredentialCiphertext: record.CredentialCiphertext,
		CreatedAt:            formatSQLiteTime(record.CreatedAt), UpdatedAt: formatSQLiteTime(record.UpdatedAt),
	})
	if isSQLiteUnique(err) {
		return fmt.Errorf("insert media server: %w", core.ErrAlreadyExists)
	}
	if err != nil {
		return fmt.Errorf("insert media server: %w", err)
	}
	return nil
}

func (s *sqliteMediaServers) GetMediaServer(ctx context.Context, id string) (core.MediaServerRecord, error) {
	row, err := s.q.GetMediaServer(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return core.MediaServerRecord{}, core.ErrNotFound
	}
	if err != nil {
		return core.MediaServerRecord{}, fmt.Errorf("select media server: %w", err)
	}
	createdAt, err := parseSQLiteTime(row.CreatedAt)
	if err != nil {
		return core.MediaServerRecord{}, fmt.Errorf("parse media server created_at: %w", err)
	}
	updatedAt, err := parseSQLiteTime(row.UpdatedAt)
	if err != nil {
		return core.MediaServerRecord{}, fmt.Errorf("parse media server updated_at: %w", err)
	}
	return core.MediaServerRecord{MediaServer: core.MediaServer{
		ID: row.ID, Kind: core.MediaServerKind(row.Kind), Name: row.Name,
		BaseURL: row.BaseUrl, AllowInsecure: row.AllowInsecure != 0,
		CreatedAt: createdAt, UpdatedAt: updatedAt,
	}, CredentialCiphertext: row.CredentialCiphertext}, nil
}

func (s *sqliteMediaServers) ListMediaServers(ctx context.Context, afterNameKey string, pageSize int) ([]core.MediaServer, error) {
	if err := validateMediaServerPageSize(pageSize); err != nil {
		return nil, fmt.Errorf("list media servers: %w", err)
	}
	rows, err := s.q.ListMediaServers(ctx, sqlite.ListMediaServersParams{
		AfterNameKey: afterNameKey, PageSize: int64(pageSize),
	})
	if err != nil {
		return nil, fmt.Errorf("list media servers: %w", err)
	}
	servers := make([]core.MediaServer, 0, len(rows))
	for _, row := range rows {
		createdAt, parseErr := parseSQLiteTime(row.CreatedAt)
		if parseErr != nil {
			return nil, fmt.Errorf("parse media server created_at: %w", parseErr)
		}
		updatedAt, parseErr := parseSQLiteTime(row.UpdatedAt)
		if parseErr != nil {
			return nil, fmt.Errorf("parse media server updated_at: %w", parseErr)
		}
		servers = append(servers, core.MediaServer{
			ID: row.ID, Kind: core.MediaServerKind(row.Kind), Name: row.Name,
			BaseURL: row.BaseUrl, AllowInsecure: row.AllowInsecure != 0,
			CreatedAt: createdAt, UpdatedAt: updatedAt,
		})
	}
	return servers, nil
}

func (s *sqliteMediaServers) DeleteMediaServer(ctx context.Context, id string) error {
	rows, err := s.q.DeleteMediaServer(ctx, id)
	if err != nil {
		return fmt.Errorf("delete media server: %w", err)
	}
	if rows != 1 {
		return fmt.Errorf("delete media server: %w", core.ErrNotFound)
	}
	return nil
}
