package db

import (
	"context"
	"database/sql"
	"encoding/json"
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
	_ core.RequestDispatchWriter      = (*requests)(nil)
	_ core.RequestAvailabilityClaimer = (*requests)(nil)
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

func (s *requests) CreateRequest(
	ctx context.Context, request core.MediaRequest, now time.Time, quotaExempt bool, events ...core.RequestEvent,
) error {
	events, err := requestCreationEvents(request, events)
	if err != nil {
		return err
	}
	if s.sqlite != nil {
		return s.createSQLiteRequest(ctx, request, now, quotaExempt, events)
	}
	return s.createPostgresRequest(ctx, request, now, quotaExempt, events)
}

func (s *requests) createSQLiteRequest(
	ctx context.Context, request core.MediaRequest, now time.Time, quotaExempt bool, events []core.RequestEvent,
) error {
	return withSQLiteWriteTransaction(ctx, s.pool, func(conn *sql.Conn) error {
		q := sqlite.New(conn)
		if !quotaExempt {
			if err := checkSQLiteQuota(ctx, q, request, now); err != nil {
				return err
			}
		}
		if err := checkSQLiteSeasonOverlap(ctx, q, request); err != nil {
			return err
		}
		if err := insertSQLiteRequest(ctx, q, request); err != nil {
			return err
		}
		return insertSQLiteNotificationEvents(ctx, q, events)
	})
}

func (s *requests) createPostgresRequest(
	ctx context.Context, request core.MediaRequest, now time.Time, quotaExempt bool, events []core.RequestEvent,
) error {
	return withTransaction(ctx, s.pool, func(tx *sql.Tx) error {
		q := s.postgres.WithTx(tx)
		if err := q.LockRequestTitle(ctx, requestTitleLockKey(request)); err != nil {
			return fmt.Errorf("lock request title: %w", err)
		}
		if !quotaExempt {
			if err := q.LockAccountRequestQuota(ctx, request.RequesterID); err != nil {
				return fmt.Errorf("lock request quota: %w", err)
			}
			if err := checkPostgresQuota(ctx, q, request, now); err != nil {
				return err
			}
		}
		if err := checkPostgresSeasonOverlap(ctx, q, request); err != nil {
			return err
		}
		if err := insertPostgresRequest(ctx, q, request); err != nil {
			return err
		}
		return insertPostgresNotificationEvents(ctx, q, events)
	})
}

func requestTitleLockKey(request core.MediaRequest) string {
	return string(request.Provider) + ":" + request.ProviderID + ":" + request.ProfileID
}

func checkSQLiteSeasonOverlap(ctx context.Context, q *sqlite.Queries, request core.MediaRequest) error {
	if request.Kind != core.MediaKindSeries {
		return nil
	}
	for _, season := range request.Seasons {
		count, err := q.CountActiveRequestSeason(ctx, sqlite.CountActiveRequestSeasonParams{
			Provider: string(request.Provider), ProviderID: request.ProviderID,
			ProfileID: request.ProfileID, SeasonNumber: int64(season.Number),
		})
		if err != nil {
			return fmt.Errorf("check active request season: %w", err)
		}
		if count != 0 {
			return fmt.Errorf("active request season overlaps: %w", core.ErrAlreadyExists)
		}
	}
	return nil
}

func checkPostgresSeasonOverlap(ctx context.Context, q *postgres.Queries, request core.MediaRequest) error {
	if request.Kind != core.MediaKindSeries {
		return nil
	}
	for _, season := range request.Seasons {
		seasonNumber, err := checkedInt32(season.Number)
		if err != nil {
			return fmt.Errorf("convert request season number: %w", err)
		}
		count, err := q.CountActiveRequestSeason(ctx, postgres.CountActiveRequestSeasonParams{
			Provider: string(request.Provider), ProviderID: request.ProviderID,
			ProfileID: request.ProfileID, SeasonNumber: seasonNumber,
		})
		if err != nil {
			return fmt.Errorf("check active request season: %w", err)
		}
		if count != 0 {
			return fmt.Errorf("active request season overlaps: %w", core.ErrAlreadyExists)
		}
	}
	return nil
}

func insertSQLiteRequest(ctx context.Context, q *sqlite.Queries, request core.MediaRequest) error {
	err := q.CreateRequest(ctx, sqlite.CreateRequestParams{
		ID: request.ID, Kind: string(request.Kind), Provider: string(request.Provider),
		ProviderID: request.ProviderID, Title: request.Title, ReleaseYear: int64(request.Year), PosterPath: request.PosterPath,
		RequesterAccountID: request.RequesterID, ProfileID: request.ProfileID, Status: string(request.Status), DecisionReason: request.DecisionReason,
		DecidedByAccountID: nullableText(request.DecidedBy), DecidedAt: sqliteNullableTime(request.DecidedAt),
		DownloadManagerItemID: request.DownloadManagerItemID, FailureReason: request.FailureReason,
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
		DownloadManagerItemID: request.DownloadManagerItemID, FailureReason: request.FailureReason,
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
	request, err := postgresRequest(row)
	if err != nil {
		return core.MediaRequest{}, err
	}
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
		request, mapErr := postgresRequest(row)
		if mapErr != nil {
			return nil, mapErr
		}
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

func (s *requests) TransitionRequest(
	ctx context.Context, id string, from, to core.RequestStatus, actorID, reason string, decidedAt time.Time,
	events ...core.RequestEvent,
) (core.MediaRequest, error) {
	permission, err := core.TransitionPermission(from, to)
	if err != nil {
		return core.MediaRequest{}, err
	}
	actorValid := core.ValidID(actorID) || permission == core.PermissionAdminSettings && actorID == ""
	if !core.ValidID(id) || !actorValid || core.ValidateDecisionReason(reason) != nil || len(events) > 1 {
		return core.MediaRequest{}, core.ErrInvalidArgument
	}
	err = withTransaction(ctx, s.pool, func(tx *sql.Tx) error {
		if transitionErr := s.transition(ctx, tx, id, from, to, actorID, reason, decidedAt); transitionErr != nil {
			return transitionErr
		}
		event, eventErr := s.requestTransitionEvent(ctx, tx, id, to, actorID, reason, decidedAt, events)
		if eventErr != nil {
			return eventErr
		}
		return s.insertNotificationEvent(ctx, tx, event)
	})
	if err != nil {
		return core.MediaRequest{}, err
	}
	return s.GetRequest(ctx, id)
}

func (s *requests) RecordRequestDispatch(
	ctx context.Context, id, leaseToken, managerItemID string, at time.Time, events ...core.RequestEvent,
) (core.MediaRequest, error) {
	if !core.ValidID(id) || !core.ValidID(leaseToken) || managerItemID == "" || len(managerItemID) > 100 || len(events) > 1 {
		return core.MediaRequest{}, core.ErrInvalidArgument
	}
	err := withTransaction(ctx, s.pool, func(tx *sql.Tx) error {
		if dispatchErr := s.recordDispatch(ctx, tx, id, leaseToken, managerItemID, at); dispatchErr != nil {
			return dispatchErr
		}
		event, eventErr := s.requestTransitionEvent(
			ctx, tx, id, core.RequestProcessing, "system", "", at, events,
		)
		if eventErr != nil {
			return eventErr
		}
		return s.insertNotificationEvent(ctx, tx, event)
	})
	if err != nil {
		return core.MediaRequest{}, err
	}
	return s.GetRequest(ctx, id)
}

func (s *requests) recordDispatch(
	ctx context.Context, tx *sql.Tx, id, leaseToken, managerItemID string, at time.Time,
) error {
	if s.sqlite != nil {
		q := s.sqlite.WithTx(tx)
		rows, err := q.RecordRequestDispatch(ctx, sqlite.RecordRequestDispatchParams{
			ManagerItemID: managerItemID, UpdatedAt: formatSQLiteTime(at), ID: id,
			DispatchLeaseToken: leaseToken,
		})
		if resultErr := dispatchTransitionResult("record request dispatch", rows, err); resultErr != nil {
			return resultErr
		}
		if err := q.TransitionRequestSeasons(ctx, sqlite.TransitionRequestSeasonsParams{RequestID: id, ToStatus: string(core.RequestProcessing)}); err != nil {
			return fmt.Errorf("record request dispatch seasons: %w", err)
		}
		return nil
	}
	q := s.postgres.WithTx(tx)
	rows, err := q.RecordRequestDispatch(ctx, postgres.RecordRequestDispatchParams{
		ManagerItemID: managerItemID, UpdatedAt: core.NormalizeTime(at), ID: id,
		DispatchLeaseToken: leaseToken,
	})
	if resultErr := dispatchTransitionResult("record request dispatch", rows, err); resultErr != nil {
		return resultErr
	}
	if err := q.TransitionRequestSeasons(ctx, postgres.TransitionRequestSeasonsParams{RequestID: id, ToStatus: string(core.RequestProcessing)}); err != nil {
		return fmt.Errorf("record request dispatch seasons: %w", err)
	}
	return nil
}

func dispatchTransitionResult(operation string, rows int64, err error) error {
	if err != nil {
		return fmt.Errorf("%s: %w", operation, err)
	}
	if rows != 1 {
		return core.ErrInvalidTransition
	}
	return nil
}

func (s *requests) ClaimRequestDispatch(
	ctx context.Context, id string, snapshot core.RequestDispatchSnapshot, lease core.RequestDispatchLease, at time.Time,
) (core.MediaRequest, error) {
	if !core.ValidID(id) || core.ValidateRequestDispatchSnapshot(snapshot) != nil ||
		core.ValidateRequestDispatchLease(lease, at) != nil {
		return core.MediaRequest{}, core.ErrInvalidArgument
	}
	tags, err := json.Marshal(snapshot.Tags)
	if err != nil {
		return core.MediaRequest{}, fmt.Errorf("encode dispatch tags: %w", err)
	}
	rows, err := s.claimRequestDispatch(ctx, id, snapshot, lease, string(tags), at)
	if err != nil {
		return core.MediaRequest{}, err
	}
	if rows != 1 {
		return core.MediaRequest{}, core.ErrInvalidTransition
	}
	return s.GetRequest(ctx, id)
}

func (s *requests) claimRequestDispatch(
	ctx context.Context, id string, snapshot core.RequestDispatchSnapshot,
	lease core.RequestDispatchLease, tags string, at time.Time,
) (int64, error) {
	if s.sqlite != nil {
		rows, err := s.sqlite.ClaimRequestDispatch(ctx, sqlite.ClaimRequestDispatchParams{
			DownloadManagerID: snapshot.DownloadManagerID, DispatchQualityProfile: snapshot.QualityProfile,
			DispatchRootFolder: snapshot.RootFolder, DispatchTags: tags, DispatchLeaseToken: lease.Token,
			DispatchLeaseExpiresAt: sqliteNullableTime(&lease.ExpiresAt), UpdatedAt: formatSQLiteTime(at), ID: id,
		})
		return rows, wrapRequestClaimError(err)
	}
	rows, err := s.postgres.ClaimRequestDispatch(ctx, postgres.ClaimRequestDispatchParams{
		DownloadManagerID: snapshot.DownloadManagerID, DispatchQualityProfile: snapshot.QualityProfile,
		DispatchRootFolder: snapshot.RootFolder, DispatchTags: tags, DispatchLeaseToken: lease.Token,
		DispatchLeaseExpiresAt: postgresNullableTime(&lease.ExpiresAt), UpdatedAt: core.NormalizeTime(at), ID: id,
	})
	return rows, wrapRequestClaimError(err)
}

func (s *requests) FailRequestDispatch(
	ctx context.Context, id, leaseToken, reason string, at time.Time, events ...core.RequestEvent,
) (core.MediaRequest, error) {
	if !core.ValidID(id) || !core.ValidID(leaseToken) || core.ValidateDecisionReason(reason) != nil || len(events) > 1 {
		return core.MediaRequest{}, core.ErrInvalidArgument
	}
	err := withTransaction(ctx, s.pool, func(tx *sql.Tx) error {
		if dispatchErr := s.failRequestDispatch(ctx, tx, id, leaseToken, reason, at); dispatchErr != nil {
			return dispatchErr
		}
		event, eventErr := s.requestTransitionEvent(ctx, tx, id, core.RequestFailed, "system", reason, at, events)
		if eventErr != nil {
			return eventErr
		}
		return s.insertNotificationEvent(ctx, tx, event)
	})
	if err != nil {
		return core.MediaRequest{}, err
	}
	return s.GetRequest(ctx, id)
}

func (s *requests) failRequestDispatch(
	ctx context.Context, tx *sql.Tx, id, leaseToken, reason string, at time.Time,
) error {
	if s.sqlite != nil {
		q := s.sqlite.WithTx(tx)
		rows, err := q.FailRequestDispatch(ctx, sqlite.FailRequestDispatchParams{
			FailureReason: reason, UpdatedAt: formatSQLiteTime(at), ID: id, DispatchLeaseToken: leaseToken,
		})
		return s.finishFailedDispatch(ctx, rows, err, q, nil, id)
	}
	q := s.postgres.WithTx(tx)
	rows, err := q.FailRequestDispatch(ctx, postgres.FailRequestDispatchParams{
		FailureReason: reason, UpdatedAt: core.NormalizeTime(at), ID: id, DispatchLeaseToken: leaseToken,
	})
	return s.finishFailedDispatch(ctx, rows, err, nil, q, id)
}

func (s *requests) finishFailedDispatch(
	ctx context.Context, rows int64, err error, sq *sqlite.Queries, pg *postgres.Queries, id string,
) error {
	if resultErr := dispatchTransitionResult("fail request dispatch", rows, err); resultErr != nil {
		return resultErr
	}
	if sq != nil {
		err = sq.TransitionRequestSeasons(ctx, sqlite.TransitionRequestSeasonsParams{RequestID: id, ToStatus: string(core.RequestFailed)})
	} else {
		err = pg.TransitionRequestSeasons(ctx, postgres.TransitionRequestSeasonsParams{RequestID: id, ToStatus: string(core.RequestFailed)})
	}
	if err != nil {
		return fmt.Errorf("fail request dispatch seasons: %w", err)
	}
	return nil
}

func wrapRequestClaimError(err error) error {
	if err != nil {
		return fmt.Errorf("claim request dispatch: %w", err)
	}
	return nil
}

func (s *requests) ClaimRequestsForAvailability(
	ctx context.Context, pageSize int, at time.Time,
) ([]core.MediaRequest, error) {
	if pageSize < 1 || pageSize > maxRequestQueryPageSize {
		return nil, core.ErrInvalidArgument
	}
	var result []core.MediaRequest
	if s.sqlite != nil {
		err := withSQLiteWriteTransaction(ctx, s.pool, func(conn *sql.Conn) error {
			var claimErr error
			result, claimErr = claimSQLiteAvailability(ctx, sqlite.New(conn), pageSize, at)
			return claimErr
		})
		return result, err
	}
	err := withTransaction(ctx, s.pool, func(tx *sql.Tx) error {
		var claimErr error
		result, claimErr = claimPostgresAvailability(ctx, s.postgres.WithTx(tx), pageSize, at)
		return claimErr
	})
	return result, err
}

func claimSQLiteAvailability(
	ctx context.Context, q *sqlite.Queries, pageSize int, at time.Time,
) ([]core.MediaRequest, error) {
	rows, err := q.ListRequestsForAvailability(ctx, int64(pageSize))
	if err != nil {
		return nil, fmt.Errorf("list requests for availability: %w", err)
	}
	result := make([]core.MediaRequest, 0, len(rows))
	checkedAt := formatSQLiteTime(at)
	for _, row := range rows {
		request, mapErr := sqliteRequestWithSeasons(ctx, q, row)
		if mapErr != nil {
			return nil, mapErr
		}
		count, stampErr := q.StampRequestAvailabilityCheck(ctx, sqlite.StampRequestAvailabilityCheckParams{
			CheckedAt: sql.NullString{String: checkedAt, Valid: true}, ID: row.ID,
		})
		if stampErr != nil || count != 1 {
			return nil, availabilityStampError(stampErr)
		}
		request.LastAvailabilityCheckAt = new(core.NormalizeTime(at))
		result = append(result, request)
	}
	return result, nil
}

func claimPostgresAvailability(
	ctx context.Context, q *postgres.Queries, pageSize int, at time.Time,
) ([]core.MediaRequest, error) {
	limit, err := checkedInt32(pageSize)
	if err != nil {
		return nil, err
	}
	rows, err := q.ListRequestsForAvailability(ctx, limit)
	if err != nil {
		return nil, fmt.Errorf("list requests for availability: %w", err)
	}
	result := make([]core.MediaRequest, 0, len(rows))
	checkedAt := core.NormalizeTime(at)
	for _, row := range rows {
		request, mapErr := postgresRequestWithSeasons(ctx, q, row)
		if mapErr != nil {
			return nil, mapErr
		}
		count, stampErr := q.StampRequestAvailabilityCheck(ctx, postgres.StampRequestAvailabilityCheckParams{
			CheckedAt: sql.NullTime{Time: checkedAt, Valid: true}, ID: row.ID,
		})
		if stampErr != nil || count != 1 {
			return nil, availabilityStampError(stampErr)
		}
		request.LastAvailabilityCheckAt = new(checkedAt)
		result = append(result, request)
	}
	return result, nil
}

func availabilityStampError(err error) error {
	if err != nil {
		return fmt.Errorf("stamp request availability check: %w", err)
	}
	return core.ErrInvalidTransition
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

func sqliteRequestWithSeasons(
	ctx context.Context, q *sqlite.Queries, row sqlite.Request,
) (core.MediaRequest, error) {
	request, err := sqliteRequest(row)
	if err != nil {
		return core.MediaRequest{}, err
	}
	seasons, err := q.ListRequestSeasons(ctx, row.ID)
	if err != nil {
		return core.MediaRequest{}, fmt.Errorf("list request seasons: %w", err)
	}
	for _, season := range seasons {
		request.Seasons = append(request.Seasons, core.RequestSeason{
			Number: int(season.SeasonNumber), Status: core.SeasonStatus(season.Status),
		})
	}
	return request, nil
}

func postgresRequestWithSeasons(
	ctx context.Context, q *postgres.Queries, row postgres.Request,
) (core.MediaRequest, error) {
	request, err := postgresRequest(row)
	if err != nil {
		return core.MediaRequest{}, err
	}
	seasons, err := q.ListRequestSeasons(ctx, row.ID)
	if err != nil {
		return core.MediaRequest{}, fmt.Errorf("list request seasons: %w", err)
	}
	for _, season := range seasons {
		request.Seasons = append(request.Seasons, core.RequestSeason{
			Number: int(season.SeasonNumber), Status: core.SeasonStatus(season.Status),
		})
	}
	return request, nil
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
	lastCheck, err := parseNullableSQLiteTime(row.LastAvailabilityCheckAt)
	if err != nil {
		return core.MediaRequest{}, err
	}
	leaseExpiry, err := parseNullableSQLiteTime(row.DispatchLeaseExpiresAt)
	if err != nil {
		return core.MediaRequest{}, err
	}
	tags, err := decodeDispatchTags(row.DispatchTags)
	if err != nil {
		return core.MediaRequest{}, err
	}
	return core.MediaRequest{
		ID: row.ID, Kind: core.MediaKind(row.Kind), Provider: core.MetadataProviderKind(row.Provider), ProviderID: row.ProviderID,
		Title: row.Title, Year: int(row.ReleaseYear), PosterPath: row.PosterPath, RequesterID: row.RequesterAccountID, ProfileID: row.ProfileID,
		Status: core.RequestStatus(row.Status), DecisionReason: row.DecisionReason, FailureReason: row.FailureReason,
		DownloadManagerID: row.DownloadManagerID, DownloadManagerItemID: row.DownloadManagerItemID,
		DispatchQualityProfile: row.DispatchQualityProfile, DispatchRootFolder: row.DispatchRootFolder, DispatchTags: tags,
		DispatchLeaseToken: row.DispatchLeaseToken, DispatchLeaseExpiresAt: leaseExpiry,
		LastAvailabilityCheckAt: lastCheck, DecidedBy: row.DecidedByAccountID.String, DecidedAt: decided,
		CreatedAt: created, UpdatedAt: updated,
	}, nil
}

func postgresRequest(row postgres.Request) (core.MediaRequest, error) {
	var decided *time.Time
	if row.DecidedAt.Valid {
		value := core.NormalizeTime(row.DecidedAt.Time)
		decided = &value
	}
	var lastCheck *time.Time
	if row.LastAvailabilityCheckAt.Valid {
		value := core.NormalizeTime(row.LastAvailabilityCheckAt.Time)
		lastCheck = &value
	}
	var leaseExpiry *time.Time
	if row.DispatchLeaseExpiresAt.Valid {
		value := core.NormalizeTime(row.DispatchLeaseExpiresAt.Time)
		leaseExpiry = &value
	}
	tags, err := decodeDispatchTags(row.DispatchTags)
	if err != nil {
		return core.MediaRequest{}, err
	}
	return core.MediaRequest{
		ID: row.ID, Kind: core.MediaKind(row.Kind), Provider: core.MetadataProviderKind(row.Provider), ProviderID: row.ProviderID,
		Title: row.Title, Year: int(row.ReleaseYear), PosterPath: row.PosterPath, RequesterID: row.RequesterAccountID, ProfileID: row.ProfileID,
		Status: core.RequestStatus(row.Status), DecisionReason: row.DecisionReason, FailureReason: row.FailureReason,
		DownloadManagerID: row.DownloadManagerID, DownloadManagerItemID: row.DownloadManagerItemID,
		DispatchQualityProfile: row.DispatchQualityProfile, DispatchRootFolder: row.DispatchRootFolder, DispatchTags: tags,
		DispatchLeaseToken: row.DispatchLeaseToken, DispatchLeaseExpiresAt: leaseExpiry,
		LastAvailabilityCheckAt: lastCheck, DecidedBy: row.DecidedByAccountID.String, DecidedAt: decided,
		CreatedAt: core.NormalizeTime(row.CreatedAt), UpdatedAt: core.NormalizeTime(row.UpdatedAt),
	}, nil
}

func decodeDispatchTags(value string) ([]string, error) {
	var tags []string
	if err := json.Unmarshal([]byte(value), &tags); err != nil {
		return nil, fmt.Errorf("decode request dispatch tags: %w", err)
	}
	if tags == nil {
		tags = []string{}
	}
	return tags, nil
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
