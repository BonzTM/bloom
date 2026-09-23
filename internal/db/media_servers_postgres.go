package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/db/postgres"
)

type postgresMediaServers struct{ q *postgres.Queries }

var (
	_ core.MediaServerReader = (*postgresMediaServers)(nil)
	_ core.MediaServerWriter = (*postgresMediaServers)(nil)
)

func newPostgresMediaServers(pool *sql.DB) *postgresMediaServers {
	return &postgresMediaServers{q: postgres.New(pool)}
}

func (s *postgresMediaServers) CreateMediaServer(ctx context.Context, record core.MediaServerRecord) error {
	if err := validateMediaServerRecord(record); err != nil {
		return fmt.Errorf("insert media server: %w", err)
	}
	err := s.q.CreateMediaServer(ctx, postgres.CreateMediaServerParams{
		ID: record.ID, Kind: string(record.Kind), Name: record.Name,
		NameKey: core.MediaServerNameKey(record.Name), BaseUrl: record.BaseURL,
		AllowInsecure:        record.AllowInsecure,
		CredentialCiphertext: record.CredentialCiphertext,
		CreatedAt:            core.NormalizeTime(record.CreatedAt), UpdatedAt: core.NormalizeTime(record.UpdatedAt),
	})
	if isPostgresUnique(err) {
		return fmt.Errorf("insert media server: %w", core.ErrAlreadyExists)
	}
	if err != nil {
		return fmt.Errorf("insert media server: %w", err)
	}
	return nil
}

func (s *postgresMediaServers) GetMediaServer(ctx context.Context, id string) (core.MediaServerRecord, error) {
	row, err := s.q.GetMediaServer(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return core.MediaServerRecord{}, core.ErrNotFound
	}
	if err != nil {
		return core.MediaServerRecord{}, fmt.Errorf("select media server: %w", err)
	}
	return core.MediaServerRecord{MediaServer: core.MediaServer{
		ID: row.ID, Kind: core.MediaServerKind(row.Kind), Name: row.Name, BaseURL: row.BaseUrl, AllowInsecure: row.AllowInsecure,
		CreatedAt: core.NormalizeTime(row.CreatedAt), UpdatedAt: core.NormalizeTime(row.UpdatedAt),
	}, CredentialCiphertext: row.CredentialCiphertext}, nil
}

func (s *postgresMediaServers) ListMediaServers(ctx context.Context, afterNameKey string, pageSize int) ([]core.MediaServer, error) {
	if err := validateMediaServerPageSize(pageSize); err != nil {
		return nil, fmt.Errorf("list media servers: %w", err)
	}
	boundedPageSize := int32(pageSize) //nolint:gosec // validated as 1 through 101 above.
	rows, err := s.q.ListMediaServers(ctx, postgres.ListMediaServersParams{
		AfterNameKey: afterNameKey, PageSize: boundedPageSize,
	})
	if err != nil {
		return nil, fmt.Errorf("list media servers: %w", err)
	}
	servers := make([]core.MediaServer, 0, len(rows))
	for _, row := range rows {
		servers = append(servers, core.MediaServer{
			ID: row.ID, Kind: core.MediaServerKind(row.Kind), Name: row.Name, BaseURL: row.BaseUrl,
			AllowInsecure: row.AllowInsecure,
			CreatedAt:     core.NormalizeTime(row.CreatedAt), UpdatedAt: core.NormalizeTime(row.UpdatedAt),
		})
	}
	return servers, nil
}

func (s *postgresMediaServers) DeleteMediaServer(ctx context.Context, id string) error {
	rows, err := s.q.DeleteMediaServer(ctx, id)
	if err != nil {
		return fmt.Errorf("delete media server: %w", err)
	}
	if rows != 1 {
		return fmt.Errorf("delete media server: %w", core.ErrNotFound)
	}
	return nil
}
