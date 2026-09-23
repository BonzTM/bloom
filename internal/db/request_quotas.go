package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/db/postgres"
	"github.com/BonzTM/bloom/internal/db/sqlite"
)

func (s *requests) GetRoleRequestQuota(ctx context.Context, roleID string) (core.RoleRequestQuota, error) {
	if s.sqlite != nil {
		row, err := s.sqlite.GetRoleRequestQuota(ctx, roleID)
		if err != nil {
			return core.RoleRequestQuota{}, mapNotFound("get role request quota", err)
		}
		return core.RoleRequestQuota{RoleID: row.RoleID, Quota: sqliteRoleQuota(row)}, nil
	}
	row, err := s.postgres.GetRoleRequestQuota(ctx, roleID)
	if err != nil {
		return core.RoleRequestQuota{}, mapNotFound("get role request quota", err)
	}
	return core.RoleRequestQuota{RoleID: row.RoleID, Quota: postgresRoleQuota(row)}, nil
}

func (s *requests) GetAccountRequestQuota(ctx context.Context, accountID string) (core.AccountRequestQuota, error) {
	if s.sqlite != nil {
		row, err := s.sqlite.GetAccountRequestQuota(ctx, accountID)
		if err != nil {
			return core.AccountRequestQuota{}, mapNotFound("get account request quota", err)
		}
		return core.AccountRequestQuota{AccountID: row.AccountID, Quota: sqliteQuota(row)}, nil
	}
	row, err := s.postgres.GetAccountRequestQuota(ctx, accountID)
	if err != nil {
		return core.AccountRequestQuota{}, mapNotFound("get account request quota", err)
	}
	return core.AccountRequestQuota{AccountID: row.AccountID, Quota: postgresQuota(row)}, nil
}

func (s *requests) SetRoleRequestQuota(ctx context.Context, value core.RoleRequestQuota) error {
	if !core.ValidID(value.RoleID) || core.ValidateRequestQuota(value.Quota) != nil {
		return core.ErrInvalidArgument
	}
	q := value.Quota
	movieLimit, err := checkedInt32(q.MovieLimit)
	if err != nil {
		return err
	}
	seasonLimit, err := checkedInt32(q.SeasonLimit)
	if err != nil {
		return err
	}
	if s.sqlite != nil {
		return wrapDB("upsert role request quota", s.sqlite.UpsertRoleRequestQuota(ctx, sqlite.UpsertRoleRequestQuotaParams{RoleID: value.RoleID, MovieLimit: int64(q.MovieLimit), MoviePeriodSeconds: int64(q.MoviePeriod.Seconds()), SeasonLimit: int64(q.SeasonLimit), SeasonPeriodSeconds: int64(q.SeasonPeriod.Seconds())}))
	}
	return wrapDB("upsert role request quota", s.postgres.UpsertRoleRequestQuota(ctx, postgres.UpsertRoleRequestQuotaParams{RoleID: value.RoleID, MovieLimit: movieLimit, MoviePeriodSeconds: int64(q.MoviePeriod.Seconds()), SeasonLimit: seasonLimit, SeasonPeriodSeconds: int64(q.SeasonPeriod.Seconds())}))
}

func (s *requests) SetAccountRequestQuota(ctx context.Context, value core.AccountRequestQuota) error {
	if !core.ValidID(value.AccountID) || core.ValidateRequestQuota(value.Quota) != nil {
		return core.ErrInvalidArgument
	}
	q := value.Quota
	movieLimit, err := checkedInt32(q.MovieLimit)
	if err != nil {
		return err
	}
	seasonLimit, err := checkedInt32(q.SeasonLimit)
	if err != nil {
		return err
	}
	if s.sqlite != nil {
		return wrapDB("upsert account request quota", s.sqlite.UpsertAccountRequestQuota(ctx, sqlite.UpsertAccountRequestQuotaParams{AccountID: value.AccountID, MovieLimit: int64(q.MovieLimit), MoviePeriodSeconds: int64(q.MoviePeriod.Seconds()), SeasonLimit: int64(q.SeasonLimit), SeasonPeriodSeconds: int64(q.SeasonPeriod.Seconds())}))
	}
	return wrapDB("upsert account request quota", s.postgres.UpsertAccountRequestQuota(ctx, postgres.UpsertAccountRequestQuotaParams{AccountID: value.AccountID, MovieLimit: movieLimit, MoviePeriodSeconds: int64(q.MoviePeriod.Seconds()), SeasonLimit: seasonLimit, SeasonPeriodSeconds: int64(q.SeasonPeriod.Seconds())}))
}

func (s *requests) DeleteRoleRequestQuota(ctx context.Context, roleID string) error {
	return deleteQuota(func() (int64, error) {
		if s.sqlite != nil {
			return s.sqlite.DeleteRoleRequestQuota(ctx, roleID)
		}
		return s.postgres.DeleteRoleRequestQuota(ctx, roleID)
	})
}

func (s *requests) DeleteAccountRequestQuota(ctx context.Context, accountID string) error {
	return deleteQuota(func() (int64, error) {
		if s.sqlite != nil {
			return s.sqlite.DeleteAccountRequestQuota(ctx, accountID)
		}
		return s.postgres.DeleteAccountRequestQuota(ctx, accountID)
	})
}

func deleteQuota(operation func() (int64, error)) error {
	rows, err := operation()
	if errors.Is(err, sql.ErrNoRows) || err == nil && rows == 0 {
		return core.ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("delete request quota: %w", err)
	}
	return nil
}
