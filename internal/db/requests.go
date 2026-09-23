package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/BonzTM/bloom/internal/config"
	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/db/postgres"
	"github.com/BonzTM/bloom/internal/db/sqlite"
)

const maxRequestQueryPageSize = 101

type requests struct {
	pool     *sql.DB
	driver   config.Driver
	sqlite   *sqlite.Queries
	postgres *postgres.Queries
}

var (
	_ core.RequestReader              = (*requests)(nil)
	_ core.RequestWriter              = (*requests)(nil)
	_ core.RequestQuotaReader         = (*requests)(nil)
	_ core.RequestQuotaWriter         = (*requests)(nil)
	_ core.AccountRequestQuotaDeleter = (*requests)(nil)
)

// NewRequestStores constructs request and quota stores for one engine.
func NewRequestStores(pool *sql.DB, driver config.Driver) (core.RequestReader, core.RequestWriter, core.RequestQuotaReader, core.RequestQuotaWriter, core.AccountRequestQuotaDeleter, error) {
	store := &requests{pool: pool, driver: driver}
	switch driver {
	case config.DriverSQLite:
		store.sqlite = sqlite.New(pool)
	case config.DriverPostgres:
		store.postgres = postgres.New(pool)
	default:
		return nil, nil, nil, nil, nil, fmt.Errorf("unsupported database driver %q", driver)
	}
	return store, store, store, store, store, nil
}

func (s *requests) CreateRequest(ctx context.Context, request core.MediaRequest, now time.Time, quotaExempt bool) error {
	if err := core.ValidateMediaRequest(request); err != nil {
		return err
	}
	if quotaExempt {
		return s.createWithoutQuota(ctx, request)
	}
	if s.sqlite != nil {
		return withSQLiteWriteTransaction(ctx, s.pool, func(conn *sql.Conn) error {
			q := sqlite.New(conn)
			if err := checkSQLiteQuota(ctx, q, request, now); err != nil {
				return err
			}
			return insertSQLiteRequest(ctx, q, request)
		})
	}
	return withTransaction(ctx, s.pool, func(tx *sql.Tx) error {
		q := s.postgres.WithTx(tx)
		if err := q.LockAccountRequestQuota(ctx, request.RequesterID); err != nil {
			return fmt.Errorf("lock request quota: %w", err)
		}
		if err := checkPostgresQuota(ctx, q, request, now); err != nil {
			return err
		}
		return insertPostgresRequest(ctx, q, request)
	})
}

func (s *requests) createWithoutQuota(ctx context.Context, request core.MediaRequest) error {
	if s.sqlite != nil {
		return withTransaction(ctx, s.pool, func(tx *sql.Tx) error { return insertSQLiteRequest(ctx, s.sqlite.WithTx(tx), request) })
	}
	return withTransaction(ctx, s.pool, func(tx *sql.Tx) error { return insertPostgresRequest(ctx, s.postgres.WithTx(tx), request) })
}

func insertSQLiteRequest(ctx context.Context, q *sqlite.Queries, request core.MediaRequest) error {
	err := q.CreateRequest(ctx, sqlite.CreateRequestParams{
		ID: request.ID, Kind: string(request.Kind), Provider: string(request.Provider),
		ProviderID: request.ProviderID, Title: request.Title, ReleaseYear: int64(request.Year), PosterPath: request.PosterPath,
		RequesterAccountID: request.RequesterID, ProfileID: request.ProfileID, Status: string(request.Status), DecisionReason: request.DecisionReason,
		DecidedByAccountID: nullableText(request.DecidedBy), DecidedAt: sqliteNullableTime(request.DecidedAt),
		CreatedAt: formatSQLiteTime(request.CreatedAt), UpdatedAt: formatSQLiteTime(request.UpdatedAt),
	})
	if err != nil {
		return requestInsertError(err)
	}
	for _, season := range request.Seasons {
		if err := q.CreateRequestSeason(ctx, sqlite.CreateRequestSeasonParams{RequestID: request.ID, SeasonNumber: int64(season.Number), Status: string(season.Status)}); err != nil {
			return fmt.Errorf("insert request season: %w", err)
		}
	}
	return nil
}

func insertPostgresRequest(ctx context.Context, q *postgres.Queries, request core.MediaRequest) error {
	releaseYear, err := checkedInt32(request.Year)
	if err != nil {
		return fmt.Errorf("convert request release year: %w", err)
	}
	err = q.CreateRequest(ctx, postgres.CreateRequestParams{
		ID: request.ID, Kind: string(request.Kind), Provider: string(request.Provider),
		ProviderID: request.ProviderID, Title: request.Title, ReleaseYear: releaseYear, PosterPath: request.PosterPath,
		RequesterAccountID: request.RequesterID, ProfileID: request.ProfileID, Status: string(request.Status), DecisionReason: request.DecisionReason,
		DecidedByAccountID: nullableText(request.DecidedBy), DecidedAt: postgresNullableTime(request.DecidedAt),
		CreatedAt: core.NormalizeTime(request.CreatedAt), UpdatedAt: core.NormalizeTime(request.UpdatedAt),
	})
	if err != nil {
		return requestInsertError(err)
	}
	for _, season := range request.Seasons {
		seasonNumber, conversionErr := checkedInt32(season.Number)
		if conversionErr != nil {
			return fmt.Errorf("convert request season number: %w", conversionErr)
		}
		if err := q.CreateRequestSeason(ctx, postgres.CreateRequestSeasonParams{RequestID: request.ID, SeasonNumber: seasonNumber, Status: string(season.Status)}); err != nil {
			return fmt.Errorf("insert request season: %w", err)
		}
	}
	return nil
}

func checkedInt32(value int) (int32, error) {
	if value < math.MinInt32 || value > math.MaxInt32 {
		return 0, core.ErrInvalidArgument
	}
	return int32(value), nil
}

func requestInsertError(err error) error {
	if isUniqueError(err) {
		return fmt.Errorf("insert request: %w", core.ErrAlreadyExists)
	}
	return fmt.Errorf("insert request: %w", err)
}

func (s *requests) GetRequest(ctx context.Context, id string) (core.MediaRequest, error) {
	if s.sqlite != nil {
		row, err := s.sqlite.GetRequest(ctx, id)
		if err != nil {
			return core.MediaRequest{}, mapNotFound("get request", err)
		}
		request, err := sqliteRequest(row)
		if err != nil {
			return core.MediaRequest{}, err
		}
		seasons, err := s.sqlite.ListRequestSeasons(ctx, id)
		if err != nil {
			return core.MediaRequest{}, fmt.Errorf("list request seasons: %w", err)
		}
		for _, season := range seasons {
			request.Seasons = append(request.Seasons, core.RequestSeason{Number: int(season.SeasonNumber), Status: core.SeasonStatus(season.Status)})
		}
		return request, nil
	}
	row, err := s.postgres.GetRequest(ctx, id)
	if err != nil {
		return core.MediaRequest{}, mapNotFound("get request", err)
	}
	request := postgresRequest(row)
	seasons, err := s.postgres.ListRequestSeasons(ctx, id)
	if err != nil {
		return core.MediaRequest{}, fmt.Errorf("list request seasons: %w", err)
	}
	for _, season := range seasons {
		request.Seasons = append(request.Seasons, core.RequestSeason{Number: int(season.SeasonNumber), Status: core.SeasonStatus(season.Status)})
	}
	return request, nil
}

func (s *requests) ListRequests(ctx context.Context, filter core.RequestListFilter) ([]core.MediaRequest, error) {
	if err := validateRequestFilter(filter); err != nil {
		return nil, err
	}
	if s.sqlite != nil {
		return s.listSQLiteRequests(ctx, filter)
	}
	return s.listPostgresRequests(ctx, filter)
}

func validateRequestFilter(filter core.RequestListFilter) error {
	if filter.PageSize < 1 || filter.PageSize > maxRequestQueryPageSize {
		return core.ErrInvalidArgument
	}
	if filter.Status != nil && !filter.Status.Valid() {
		return core.ErrInvalidArgument
	}
	if filter.After != nil && (!core.ValidID(filter.After.ID) || filter.After.CreatedAt.IsZero()) {
		return core.ErrInvalidArgument
	}
	return nil
}

func (s *requests) listSQLiteRequests(ctx context.Context, filter core.RequestListFilter) ([]core.MediaRequest, error) {
	params := sqlite.ListRequestsParams{RequesterID: filter.RequesterID, PageSize: int64(filter.PageSize)}
	if filter.RequesterID != "" {
		params.HasRequester = 1
	}
	if filter.Status != nil {
		params.HasStatus, params.StatusFilter = 1, string(*filter.Status)
	}
	if filter.After != nil {
		params.HasCursor, params.AfterCreatedAt, params.AfterID = 1, formatSQLiteTime(filter.After.CreatedAt), filter.After.ID
	}
	rows, err := s.sqlite.ListRequests(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("list requests: %w", err)
	}
	result := make([]core.MediaRequest, 0, len(rows))
	for _, row := range rows {
		request, mapErr := sqliteRequest(row)
		if mapErr != nil {
			return nil, mapErr
		}
		seasons, listErr := s.sqlite.ListRequestSeasons(ctx, request.ID)
		if listErr != nil {
			return nil, fmt.Errorf("list request seasons: %w", listErr)
		}
		for _, season := range seasons {
			request.Seasons = append(request.Seasons, core.RequestSeason{Number: int(season.SeasonNumber), Status: core.SeasonStatus(season.Status)})
		}
		result = append(result, request)
	}
	return result, nil
}

func (s *requests) listPostgresRequests(ctx context.Context, filter core.RequestListFilter) ([]core.MediaRequest, error) {
	pageSize, err := checkedInt32(filter.PageSize)
	if err != nil {
		return nil, fmt.Errorf("convert request page size: %w", err)
	}
	params := postgres.ListRequestsParams{RequesterID: filter.RequesterID, PageSize: pageSize}
	if filter.RequesterID != "" {
		params.HasRequester = 1
	}
	if filter.Status != nil {
		params.HasStatus, params.StatusFilter = 1, string(*filter.Status)
	}
	if filter.After != nil {
		params.HasCursor, params.AfterCreatedAt, params.AfterID = 1, core.NormalizeTime(filter.After.CreatedAt), filter.After.ID
	}
	rows, err := s.postgres.ListRequests(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("list requests: %w", err)
	}
	result := make([]core.MediaRequest, 0, len(rows))
	for _, row := range rows {
		request := postgresRequest(row)
		seasons, listErr := s.postgres.ListRequestSeasons(ctx, request.ID)
		if listErr != nil {
			return nil, fmt.Errorf("list request seasons: %w", listErr)
		}
		for _, season := range seasons {
			request.Seasons = append(request.Seasons, core.RequestSeason{Number: int(season.SeasonNumber), Status: core.SeasonStatus(season.Status)})
		}
		result = append(result, request)
	}
	return result, nil
}

func (s *requests) TransitionRequest(ctx context.Context, id string, from, to core.RequestStatus, actorID, reason string, decidedAt time.Time) (core.MediaRequest, error) {
	if _, err := core.TransitionPermission(from, to); err != nil {
		return core.MediaRequest{}, err
	}
	if !core.ValidID(id) || !core.ValidID(actorID) || core.ValidateDecisionReason(reason) != nil {
		return core.MediaRequest{}, core.ErrInvalidArgument
	}
	err := withTransaction(ctx, s.pool, func(tx *sql.Tx) error { return s.transition(ctx, tx, id, from, to, actorID, reason, decidedAt) })
	if err != nil {
		return core.MediaRequest{}, err
	}
	return s.GetRequest(ctx, id)
}

func (s *requests) transition(ctx context.Context, tx *sql.Tx, id string, from, to core.RequestStatus, actorID, reason string, at time.Time) error {
	var rows int64
	var err error
	if s.sqlite != nil {
		q := s.sqlite.WithTx(tx)
		normalized := formatSQLiteTime(at)
		rows, err = q.TransitionRequest(ctx, sqlite.TransitionRequestParams{
			ID: id, FromStatus: string(from), ToStatus: string(to), DecisionReason: reason,
			DecidedByAccountID: nullableText(actorID), DecidedAt: sql.NullString{String: normalized, Valid: true}, UpdatedAt: normalized,
		})
		if err == nil && rows == 1 {
			err = q.TransitionRequestSeasons(ctx, sqlite.TransitionRequestSeasonsParams{RequestID: id, ToStatus: string(to)})
		}
	} else {
		q := s.postgres.WithTx(tx)
		normalized := core.NormalizeTime(at)
		rows, err = q.TransitionRequest(ctx, postgres.TransitionRequestParams{
			ID: id, FromStatus: string(from), ToStatus: string(to), DecisionReason: reason,
			DecidedByAccountID: nullableText(actorID), DecidedAt: sql.NullTime{Time: normalized, Valid: true}, UpdatedAt: normalized,
		})
		if err == nil && rows == 1 {
			err = q.TransitionRequestSeasons(ctx, postgres.TransitionRequestSeasonsParams{RequestID: id, ToStatus: string(to)})
		}
	}
	if err != nil {
		return fmt.Errorf("transition request: %w", err)
	}
	if rows != 1 {
		return core.ErrInvalidTransition
	}
	return nil
}

func sqliteRequest(row sqlite.Request) (core.MediaRequest, error) {
	created, err := parseSQLiteTime(row.CreatedAt)
	if err != nil {
		return core.MediaRequest{}, err
	}
	updated, err := parseSQLiteTime(row.UpdatedAt)
	if err != nil {
		return core.MediaRequest{}, err
	}
	decided, err := parseNullableSQLiteTime(row.DecidedAt)
	if err != nil {
		return core.MediaRequest{}, err
	}
	return core.MediaRequest{
		ID: row.ID, Kind: core.MediaKind(row.Kind), Provider: core.MetadataProviderKind(row.Provider), ProviderID: row.ProviderID,
		Title: row.Title, Year: int(row.ReleaseYear), PosterPath: row.PosterPath, RequesterID: row.RequesterAccountID, ProfileID: row.ProfileID,
		Status: core.RequestStatus(row.Status), DecisionReason: row.DecisionReason, DecidedBy: row.DecidedByAccountID.String, DecidedAt: decided,
		CreatedAt: created, UpdatedAt: updated,
	}, nil
}

func postgresRequest(row postgres.Request) core.MediaRequest {
	var decided *time.Time
	if row.DecidedAt.Valid {
		value := core.NormalizeTime(row.DecidedAt.Time)
		decided = &value
	}
	return core.MediaRequest{
		ID: row.ID, Kind: core.MediaKind(row.Kind), Provider: core.MetadataProviderKind(row.Provider), ProviderID: row.ProviderID,
		Title: row.Title, Year: int(row.ReleaseYear), PosterPath: row.PosterPath, RequesterID: row.RequesterAccountID, ProfileID: row.ProfileID,
		Status: core.RequestStatus(row.Status), DecisionReason: row.DecisionReason, DecidedBy: row.DecidedByAccountID.String, DecidedAt: decided,
		CreatedAt: core.NormalizeTime(row.CreatedAt), UpdatedAt: core.NormalizeTime(row.UpdatedAt),
	}
}

func parseNullableSQLiteTime(value sql.NullString) (*time.Time, error) {
	if !value.Valid {
		return nil, nil
	}
	parsed, err := parseSQLiteTime(value.String)
	if err != nil {
		return nil, err
	}
	return &parsed, nil
}

func nullableText(value string) sql.NullString {
	return sql.NullString{String: value, Valid: value != ""}
}

func checkSQLiteQuota(ctx context.Context, q *sqlite.Queries, request core.MediaRequest, now time.Time) error {
	account, err := q.GetAccountRequestQuota(ctx, request.RequesterID)
	if err == nil {
		return sqliteQuotaAllows(ctx, q, request, sqliteQuota(account), now)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("get account request quota: %w", err)
	}
	rows, err := q.ListRoleRequestQuotasForAccount(ctx, request.RequesterID)
	if err != nil {
		return fmt.Errorf("list role request quotas: %w", err)
	}
	if len(rows) == 0 {
		return nil
	}
	for _, row := range rows {
		if sqliteQuotaAllows(ctx, q, request, sqliteRoleQuota(row), now) == nil {
			return nil
		}
	}
	return core.ErrQuotaExceeded
}

func checkPostgresQuota(ctx context.Context, q *postgres.Queries, request core.MediaRequest, now time.Time) error {
	account, err := q.GetAccountRequestQuota(ctx, request.RequesterID)
	if err == nil {
		return postgresQuotaAllows(ctx, q, request, postgresQuota(account), now)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("get account request quota: %w", err)
	}
	rows, err := q.ListRoleRequestQuotasForAccount(ctx, request.RequesterID)
	if err != nil {
		return fmt.Errorf("list role request quotas: %w", err)
	}
	if len(rows) == 0 {
		return nil
	}
	for _, row := range rows {
		if postgresQuotaAllows(ctx, q, request, postgresRoleQuota(row), now) == nil {
			return nil
		}
	}
	return core.ErrQuotaExceeded
}

func sqliteQuotaAllows(ctx context.Context, q *sqlite.Queries, request core.MediaRequest, quota core.RequestQuota, now time.Time) error {
	if request.Kind == core.MediaKindMovie {
		if quota.MovieLimit == 0 {
			return core.CheckRequestQuota(quota, request.Kind, 0, core.RequestQuotaUsage{})
		}
		count, err := q.CountRequestedMoviesSince(ctx, sqlite.CountRequestedMoviesSinceParams{AccountID: request.RequesterID, SinceTime: formatSQLiteTime(now.Add(-quota.MoviePeriod))})
		if err != nil {
			return err
		}
		return core.CheckRequestQuota(quota, request.Kind, 0, core.RequestQuotaUsage{Movies: count})
	}
	if quota.SeasonLimit == 0 {
		return core.CheckRequestQuota(quota, request.Kind, len(request.Seasons), core.RequestQuotaUsage{})
	}
	count, err := q.CountRequestedSeasonsSince(ctx, sqlite.CountRequestedSeasonsSinceParams{AccountID: request.RequesterID, SinceTime: formatSQLiteTime(now.Add(-quota.SeasonPeriod))})
	if err != nil {
		return err
	}
	return core.CheckRequestQuota(quota, request.Kind, len(request.Seasons), core.RequestQuotaUsage{Seasons: count})
}

func postgresQuotaAllows(ctx context.Context, q *postgres.Queries, request core.MediaRequest, quota core.RequestQuota, now time.Time) error {
	if request.Kind == core.MediaKindMovie {
		if quota.MovieLimit == 0 {
			return core.CheckRequestQuota(quota, request.Kind, 0, core.RequestQuotaUsage{})
		}
		count, err := q.CountRequestedMoviesSince(ctx, postgres.CountRequestedMoviesSinceParams{AccountID: request.RequesterID, SinceTime: core.NormalizeTime(now.Add(-quota.MoviePeriod))})
		if err != nil {
			return err
		}
		return core.CheckRequestQuota(quota, request.Kind, 0, core.RequestQuotaUsage{Movies: count})
	}
	if quota.SeasonLimit == 0 {
		return core.CheckRequestQuota(quota, request.Kind, len(request.Seasons), core.RequestQuotaUsage{})
	}
	count, err := q.CountRequestedSeasonsSince(ctx, postgres.CountRequestedSeasonsSinceParams{AccountID: request.RequesterID, SinceTime: core.NormalizeTime(now.Add(-quota.SeasonPeriod))})
	if err != nil {
		return err
	}
	return core.CheckRequestQuota(quota, request.Kind, len(request.Seasons), core.RequestQuotaUsage{Seasons: count})
}

func sqliteQuota(row sqlite.AccountRequestQuota) core.RequestQuota {
	return core.RequestQuota{MovieLimit: int(row.MovieLimit), MoviePeriod: time.Duration(row.MoviePeriodSeconds) * time.Second, SeasonLimit: int(row.SeasonLimit), SeasonPeriod: time.Duration(row.SeasonPeriodSeconds) * time.Second}
}

func sqliteRoleQuota(row sqlite.RoleRequestQuota) core.RequestQuota {
	return core.RequestQuota{MovieLimit: int(row.MovieLimit), MoviePeriod: time.Duration(row.MoviePeriodSeconds) * time.Second, SeasonLimit: int(row.SeasonLimit), SeasonPeriod: time.Duration(row.SeasonPeriodSeconds) * time.Second}
}

func postgresQuota(row postgres.AccountRequestQuota) core.RequestQuota {
	return core.RequestQuota{MovieLimit: int(row.MovieLimit), MoviePeriod: time.Duration(row.MoviePeriodSeconds) * time.Second, SeasonLimit: int(row.SeasonLimit), SeasonPeriod: time.Duration(row.SeasonPeriodSeconds) * time.Second}
}

func postgresRoleQuota(row postgres.RoleRequestQuota) core.RequestQuota {
	return core.RequestQuota{MovieLimit: int(row.MovieLimit), MoviePeriod: time.Duration(row.MoviePeriodSeconds) * time.Second, SeasonLimit: int(row.SeasonLimit), SeasonPeriod: time.Duration(row.SeasonPeriodSeconds) * time.Second}
}
