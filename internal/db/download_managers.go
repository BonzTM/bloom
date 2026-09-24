package db

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/BonzTM/bloom/internal/config"
	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/db/postgres"
	"github.com/BonzTM/bloom/internal/db/sqlite"
)

const maxDownloadManagerPageSize = 101

type downloadManagers struct {
	sqlite   *sqlite.Queries
	postgres *postgres.Queries
}

var (
	_ core.DownloadManagerReader = (*downloadManagers)(nil)
	_ core.DownloadManagerWriter = (*downloadManagers)(nil)
)

// NewDownloadManagerStores constructs the registered-manager stores for one engine.
func NewDownloadManagerStores(
	pool *sql.DB, driver config.Driver,
) (core.DownloadManagerReader, core.DownloadManagerWriter, error) {
	store := &downloadManagers{}
	switch driver {
	case config.DriverSQLite:
		store.sqlite = sqlite.New(pool)
	case config.DriverPostgres:
		store.postgres = postgres.New(pool)
	default:
		return nil, nil, fmt.Errorf("unsupported database driver %q", driver)
	}
	return store, store, nil
}

func (s *downloadManagers) CreateDownloadManager(ctx context.Context, value core.DownloadManagerRecord) error {
	if err := core.ValidateDownloadManager(value.DownloadManager); err != nil || len(value.CredentialCiphertext) == 0 || value.KeyID == "" {
		return core.ErrInvalidArgument
	}
	var err error
	if s.sqlite != nil {
		err = s.sqlite.CreateDownloadManager(ctx, sqlite.CreateDownloadManagerParams{
			ID: value.ID, Kind: string(value.Kind), Name: value.Name,
			NameKey: core.MediaServerNameKey(value.Name), BaseUrl: value.BaseURL,
			AllowInsecure: boolToInt64(value.AllowInsecure), CredentialCiphertext: value.CredentialCiphertext,
			KeyID: value.KeyID, CreatedAt: formatSQLiteTime(value.CreatedAt), UpdatedAt: formatSQLiteTime(value.UpdatedAt),
		})
	} else {
		err = s.postgres.CreateDownloadManager(ctx, postgres.CreateDownloadManagerParams{
			ID: value.ID, Kind: string(value.Kind), Name: value.Name,
			NameKey: core.MediaServerNameKey(value.Name), BaseUrl: value.BaseURL,
			AllowInsecure: value.AllowInsecure, CredentialCiphertext: value.CredentialCiphertext,
			KeyID: value.KeyID, CreatedAt: core.NormalizeTime(value.CreatedAt), UpdatedAt: core.NormalizeTime(value.UpdatedAt),
		})
	}
	if isUniqueError(err) {
		return fmt.Errorf("create download manager: %w", core.ErrAlreadyExists)
	}
	if err != nil {
		return fmt.Errorf("create download manager: %w", err)
	}
	return nil
}

func (s *downloadManagers) GetDownloadManager(ctx context.Context, id string) (core.DownloadManagerRecord, error) {
	if s.sqlite != nil {
		row, err := s.sqlite.GetDownloadManager(ctx, id)
		if err != nil {
			return core.DownloadManagerRecord{}, mapNotFound("get download manager", err)
		}
		return sqliteDownloadManagerRecord(row)
	}
	row, err := s.postgres.GetDownloadManager(ctx, id)
	if err != nil {
		return core.DownloadManagerRecord{}, mapNotFound("get download manager", err)
	}
	return postgresDownloadManagerRecord(row), nil
}

func (s *downloadManagers) GetDownloadManagerByName(ctx context.Context, name string) (core.DownloadManagerRecord, error) {
	key := core.MediaServerNameKey(name)
	if s.sqlite != nil {
		row, err := s.sqlite.GetDownloadManagerByName(ctx, key)
		if err != nil {
			return core.DownloadManagerRecord{}, mapNotFound("get download manager by name", err)
		}
		return sqliteDownloadManagerRecord(sqlite.GetDownloadManagerRow(row))
	}
	row, err := s.postgres.GetDownloadManagerByName(ctx, key)
	if err != nil {
		return core.DownloadManagerRecord{}, mapNotFound("get download manager by name", err)
	}
	return postgresDownloadManagerRecord(postgres.GetDownloadManagerRow(row)), nil
}

func (s *downloadManagers) ListDownloadManagers(
	ctx context.Context, afterNameKey string, pageSize int,
) ([]core.DownloadManager, error) {
	if pageSize < 1 || pageSize > maxDownloadManagerPageSize {
		return nil, core.ErrInvalidArgument
	}
	if s.sqlite != nil {
		rows, err := s.sqlite.ListDownloadManagers(ctx, sqlite.ListDownloadManagersParams{AfterNameKey: afterNameKey, PageSize: int64(pageSize)})
		if err != nil {
			return nil, fmt.Errorf("list download managers: %w", err)
		}
		return mapSQLiteDownloadManagers(rows)
	}
	rows, err := s.postgres.ListDownloadManagers(ctx, postgres.ListDownloadManagersParams{AfterNameKey: afterNameKey, PageSize: int32(pageSize)})
	if err != nil {
		return nil, fmt.Errorf("list download managers: %w", err)
	}
	result := make([]core.DownloadManager, 0, len(rows))
	for _, row := range rows {
		result = append(result, core.DownloadManager{
			ID: row.ID, Kind: core.DownloadManagerKind(row.Kind), Name: row.Name, BaseURL: row.BaseUrl,
			AllowInsecure: row.AllowInsecure, CreatedAt: core.NormalizeTime(row.CreatedAt), UpdatedAt: core.NormalizeTime(row.UpdatedAt),
		})
	}
	return result, nil
}

func (s *downloadManagers) DeleteDownloadManager(ctx context.Context, id string) error {
	record, err := s.GetDownloadManager(ctx, id)
	if err != nil {
		return err
	}
	count, err := s.profileCount(ctx, record)
	if err != nil {
		return err
	}
	if count != 0 {
		return core.ErrDownloadManagerInUse
	}
	var rows int64
	if s.sqlite != nil {
		rows, err = s.sqlite.DeleteDownloadManager(ctx, id)
	} else {
		rows, err = s.postgres.DeleteDownloadManager(ctx, id)
	}
	if err != nil {
		return fmt.Errorf("delete download manager: %w", err)
	}
	if rows != 1 {
		return core.ErrNotFound
	}
	return nil
}

func (s *downloadManagers) profileCount(ctx context.Context, value core.DownloadManagerRecord) (int64, error) {
	var count int64
	var err error
	if s.sqlite != nil {
		count, err = s.sqlite.CountRequestProfilesForDownloadManager(ctx, sqlite.CountRequestProfilesForDownloadManagerParams{Kind: string(value.Kind), Name: value.Name})
	} else {
		count, err = s.postgres.CountRequestProfilesForDownloadManager(ctx, postgres.CountRequestProfilesForDownloadManagerParams{Kind: string(value.Kind), Name: value.Name})
	}
	if err != nil {
		return 0, fmt.Errorf("count download manager profiles: %w", err)
	}
	return count, nil
}

func sqliteDownloadManagerRecord(row sqlite.GetDownloadManagerRow) (core.DownloadManagerRecord, error) {
	created, err := parseSQLiteTime(row.CreatedAt)
	if err != nil {
		return core.DownloadManagerRecord{}, err
	}
	updated, err := parseSQLiteTime(row.UpdatedAt)
	if err != nil {
		return core.DownloadManagerRecord{}, err
	}
	return core.DownloadManagerRecord{DownloadManager: core.DownloadManager{
		ID: row.ID, Kind: core.DownloadManagerKind(row.Kind), Name: row.Name, BaseURL: row.BaseUrl,
		AllowInsecure: row.AllowInsecure != 0, CreatedAt: created, UpdatedAt: updated,
	}, CredentialCiphertext: row.CredentialCiphertext, KeyID: row.KeyID}, nil
}

func postgresDownloadManagerRecord(row postgres.GetDownloadManagerRow) core.DownloadManagerRecord {
	return core.DownloadManagerRecord{DownloadManager: core.DownloadManager{
		ID: row.ID, Kind: core.DownloadManagerKind(row.Kind), Name: row.Name, BaseURL: row.BaseUrl,
		AllowInsecure: row.AllowInsecure, CreatedAt: core.NormalizeTime(row.CreatedAt), UpdatedAt: core.NormalizeTime(row.UpdatedAt),
	}, CredentialCiphertext: row.CredentialCiphertext, KeyID: row.KeyID}
}

func mapSQLiteDownloadManagers(rows []sqlite.ListDownloadManagersRow) ([]core.DownloadManager, error) {
	result := make([]core.DownloadManager, 0, len(rows))
	for _, row := range rows {
		created, err := parseSQLiteTime(row.CreatedAt)
		if err != nil {
			return nil, err
		}
		updated, err := parseSQLiteTime(row.UpdatedAt)
		if err != nil {
			return nil, err
		}
		result = append(result, core.DownloadManager{
			ID: row.ID, Kind: core.DownloadManagerKind(row.Kind), Name: row.Name, BaseURL: row.BaseUrl,
			AllowInsecure: row.AllowInsecure != 0, CreatedAt: created, UpdatedAt: updated,
		})
	}
	return result, nil
}
