package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/BonzTM/bloom/internal/config"
	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/db/postgres"
	"github.com/BonzTM/bloom/internal/db/sqlite"
)

const maxRequestProfileQueryPageSize = 101

type requestProfiles struct {
	pool     *sql.DB
	driver   config.Driver
	sqlite   *sqlite.Queries
	postgres *postgres.Queries
}

var (
	_ core.RequestProfileReader = (*requestProfiles)(nil)
	_ core.RequestProfileWriter = (*requestProfiles)(nil)
)

// NewRequestProfileStores constructs request profile stores for one engine.
func NewRequestProfileStores(pool *sql.DB, driver config.Driver) (core.RequestProfileReader, core.RequestProfileWriter, error) {
	store := &requestProfiles{pool: pool, driver: driver}
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

func (s *requestProfiles) CreateRequestProfile(ctx context.Context, profile core.RequestProfile) error {
	if err := core.ValidateRequestProfile(profile); err != nil {
		return err
	}
	return s.writeProfile(ctx, profile, true)
}

func (s *requestProfiles) UpdateRequestProfile(ctx context.Context, profile core.RequestProfile) error {
	if err := core.ValidateRequestProfile(profile); err != nil {
		return err
	}
	return s.writeProfile(ctx, profile, false)
}

func (s *requestProfiles) writeProfile(ctx context.Context, profile core.RequestProfile, create bool) (retErr error) {
	tx, err := s.pool.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin request profile write: %w", err)
	}
	defer func() {
		if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
			retErr = errors.Join(retErr, fmt.Errorf("roll back request profile: %w", err))
		}
	}()
	if err := s.writeProfileRow(ctx, tx, profile, create); err != nil {
		return err
	}
	if err := s.writeProfileTags(ctx, tx, profile); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit request profile: %w", err)
	}
	return nil
}

func (s *requestProfiles) writeProfileRow(ctx context.Context, tx *sql.Tx, profile core.RequestProfile, create bool) error {
	movies, series := acceptedKinds(profile.Kinds)
	var err error
	var rows int64 = 1
	if s.driver == config.DriverSQLite {
		q := s.sqlite.WithTx(tx)
		params := sqliteProfileParams(profile, movies, series)
		if create {
			err = q.CreateRequestProfile(ctx, params.create)
		} else {
			rows, err = q.UpdateRequestProfile(ctx, params.update)
		}
	} else {
		q := s.postgres.WithTx(tx)
		params := postgresProfileParams(profile, movies, series)
		if create {
			err = q.CreateRequestProfile(ctx, params.create)
		} else {
			rows, err = q.UpdateRequestProfile(ctx, params.update)
		}
	}
	if isUniqueError(err) {
		return fmt.Errorf("write request profile: %w", core.ErrAlreadyExists)
	}
	if err != nil {
		return fmt.Errorf("write request profile: %w", err)
	}
	if rows != 1 {
		return core.ErrNotFound
	}
	return nil
}

func (s *requestProfiles) writeProfileTags(ctx context.Context, tx *sql.Tx, profile core.RequestProfile) error {
	if s.driver == config.DriverSQLite {
		q := s.sqlite.WithTx(tx)
		if err := q.DeleteRequestProfileTags(ctx, profile.ID); err != nil {
			return fmt.Errorf("delete request profile tags: %w", err)
		}
		for index, tag := range profile.Tags {
			if err := q.CreateRequestProfileTag(ctx, sqlite.CreateRequestProfileTagParams{ProfileID: profile.ID, Position: int64(index), Tag: tag}); err != nil {
				return fmt.Errorf("insert request profile tag: %w", err)
			}
		}
		return nil
	}
	q := s.postgres.WithTx(tx)
	if err := q.DeleteRequestProfileTags(ctx, profile.ID); err != nil {
		return fmt.Errorf("delete request profile tags: %w", err)
	}
	for index, tag := range profile.Tags {
		if err := q.CreateRequestProfileTag(ctx, postgres.CreateRequestProfileTagParams{ProfileID: profile.ID, Position: int32(index), Tag: tag}); err != nil {
			return fmt.Errorf("insert request profile tag: %w", err)
		}
	}
	return nil
}

func (s *requestProfiles) GetRequestProfile(ctx context.Context, id string) (core.RequestProfile, error) {
	if s.sqlite != nil {
		row, err := s.sqlite.GetRequestProfile(ctx, id)
		if err != nil {
			return core.RequestProfile{}, mapNotFound("get request profile", err)
		}
		profile, err := sqliteProfile(row)
		if err != nil {
			return core.RequestProfile{}, err
		}
		profile.Tags, err = s.sqlite.ListRequestProfileTags(ctx, id)
		return profile, wrapProfileRead(profile, err)
	}
	row, err := s.postgres.GetRequestProfile(ctx, id)
	if err != nil {
		return core.RequestProfile{}, mapNotFound("get request profile", err)
	}
	profile := postgresProfile(row)
	profile.Tags, err = s.postgres.ListRequestProfileTags(ctx, id)
	return profile, wrapProfileRead(profile, err)
}

func (s *requestProfiles) ListRequestProfiles(ctx context.Context, afterName string, pageSize int) ([]core.RequestProfile, error) {
	if pageSize < 1 || pageSize > maxRequestProfileQueryPageSize {
		return nil, core.ErrInvalidArgument
	}
	if s.sqlite != nil {
		rows, err := s.sqlite.ListRequestProfiles(ctx, sqlite.ListRequestProfilesParams{AfterName: afterName, PageSize: int64(pageSize)})
		if err != nil {
			return nil, fmt.Errorf("list request profiles: %w", err)
		}
		profiles := make([]core.RequestProfile, 0, len(rows))
		for _, row := range rows {
			profile, mapErr := sqliteProfile(row)
			if mapErr != nil {
				return nil, mapErr
			}
			profile.Tags, mapErr = s.sqlite.ListRequestProfileTags(ctx, profile.ID)
			if mapErr != nil {
				return nil, fmt.Errorf("list request profile tags: %w", mapErr)
			}
			profiles = append(profiles, profile)
		}
		return profiles, nil
	}
	rows, err := s.postgres.ListRequestProfiles(ctx, postgres.ListRequestProfilesParams{AfterName: afterName, PageSize: int32(pageSize)})
	if err != nil {
		return nil, fmt.Errorf("list request profiles: %w", err)
	}
	profiles := make([]core.RequestProfile, 0, len(rows))
	for _, row := range rows {
		profile := postgresProfile(row)
		profile.Tags, err = s.postgres.ListRequestProfileTags(ctx, profile.ID)
		if err != nil {
			return nil, fmt.Errorf("list request profile tags: %w", err)
		}
		profiles = append(profiles, profile)
	}
	return profiles, nil
}

func (s *requestProfiles) DeleteRequestProfile(ctx context.Context, id string) error {
	var rows int64
	var err error
	if s.sqlite != nil {
		rows, err = s.sqlite.DeleteRequestProfile(ctx, id)
	} else {
		rows, err = s.postgres.DeleteRequestProfile(ctx, id)
	}
	if isForeignKeyError(err) {
		return core.ErrProfileInUse
	}
	if err != nil {
		return fmt.Errorf("delete request profile: %w", err)
	}
	if rows != 1 {
		return core.ErrNotFound
	}
	return nil
}

type (
	sqliteProfilePair struct {
		create sqlite.CreateRequestProfileParams
		update sqlite.UpdateRequestProfileParams
	}
	postgresProfilePair struct {
		create postgres.CreateRequestProfileParams
		update postgres.UpdateRequestProfileParams
	}
)

func sqliteProfileParams(p core.RequestProfile, movies, series bool) sqliteProfilePair {
	create := sqlite.CreateRequestProfileParams{
		ID: p.ID, Name: p.Name, AcceptsMovies: boolToInt64(movies), AcceptsSeries: boolToInt64(series),
		DownloadManagerKind: p.DownloadManagerKind, DownloadManagerInstance: p.DownloadManagerInstance, QualityProfile: p.QualityProfile,
		RootFolder: p.RootFolder, CreatedAt: formatSQLiteTime(p.CreatedAt), UpdatedAt: formatSQLiteTime(p.UpdatedAt),
	}
	return sqliteProfilePair{create: create, update: sqlite.UpdateRequestProfileParams{
		ID: p.ID, Name: p.Name, AcceptsMovies: create.AcceptsMovies,
		AcceptsSeries: create.AcceptsSeries, DownloadManagerKind: p.DownloadManagerKind, DownloadManagerInstance: p.DownloadManagerInstance,
		QualityProfile: p.QualityProfile, RootFolder: p.RootFolder, UpdatedAt: create.UpdatedAt,
	}}
}

func postgresProfileParams(p core.RequestProfile, movies, series bool) postgresProfilePair {
	create := postgres.CreateRequestProfileParams{
		ID: p.ID, Name: p.Name, AcceptsMovies: movies, AcceptsSeries: series,
		DownloadManagerKind: p.DownloadManagerKind, DownloadManagerInstance: p.DownloadManagerInstance, QualityProfile: p.QualityProfile,
		RootFolder: p.RootFolder, CreatedAt: core.NormalizeTime(p.CreatedAt), UpdatedAt: core.NormalizeTime(p.UpdatedAt),
	}
	return postgresProfilePair{create: create, update: postgres.UpdateRequestProfileParams{
		ID: p.ID, Name: p.Name, AcceptsMovies: movies,
		AcceptsSeries: series, DownloadManagerKind: p.DownloadManagerKind, DownloadManagerInstance: p.DownloadManagerInstance,
		QualityProfile: p.QualityProfile, RootFolder: p.RootFolder, UpdatedAt: create.UpdatedAt,
	}}
}

func sqliteProfile(row sqlite.RequestProfile) (core.RequestProfile, error) {
	created, err := parseSQLiteTime(row.CreatedAt)
	if err != nil {
		return core.RequestProfile{}, err
	}
	updated, err := parseSQLiteTime(row.UpdatedAt)
	if err != nil {
		return core.RequestProfile{}, err
	}
	return core.RequestProfile{
		ID: row.ID, Name: row.Name, Kinds: acceptedKindList(row.AcceptsMovies != 0, row.AcceptsSeries != 0),
		DownloadManagerKind: row.DownloadManagerKind, DownloadManagerInstance: row.DownloadManagerInstance,
		QualityProfile: row.QualityProfile, RootFolder: row.RootFolder, CreatedAt: created, UpdatedAt: updated,
	}, nil
}

func postgresProfile(row postgres.RequestProfile) core.RequestProfile {
	return core.RequestProfile{
		ID: row.ID, Name: row.Name, Kinds: acceptedKindList(row.AcceptsMovies, row.AcceptsSeries),
		DownloadManagerKind: row.DownloadManagerKind, DownloadManagerInstance: row.DownloadManagerInstance,
		QualityProfile: row.QualityProfile, RootFolder: row.RootFolder, CreatedAt: core.NormalizeTime(row.CreatedAt), UpdatedAt: core.NormalizeTime(row.UpdatedAt),
	}
}

func acceptedKinds(kinds []core.MediaKind) (bool, bool) {
	var movies, series bool
	for _, kind := range kinds {
		movies = movies || kind == core.MediaKindMovie
		series = series || kind == core.MediaKindSeries
	}
	return movies, series
}

func acceptedKindList(movies, series bool) []core.MediaKind {
	kinds := make([]core.MediaKind, 0, 2)
	if movies {
		kinds = append(kinds, core.MediaKindMovie)
	}
	if series {
		kinds = append(kinds, core.MediaKindSeries)
	}
	return kinds
}

func wrapProfileRead(profile core.RequestProfile, err error) error {
	if err != nil {
		return fmt.Errorf("read request profile tags: %w", err)
	}
	return core.ValidateRequestProfile(profile)
}

func isUniqueError(err error) bool { return isSQLiteUnique(err) || isPostgresUnique(err) }

func isForeignKeyError(err error) bool {
	var pgErr *pgconn.PgError
	return isSQLiteForeignKey(err) || errors.As(err, &pgErr) && pgErr.Code == "23503"
}
