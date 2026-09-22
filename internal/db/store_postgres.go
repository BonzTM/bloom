package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/db/postgres"
)

// pgUniqueViolation is the PostgreSQL SQLSTATE for unique_violation (a
// duplicate key). The adapter matches it on the driver's typed *pgconn.PgError,
// never on the error string, so a message change in the driver or database
// locale cannot silently break the mapping.
const pgUniqueViolation = "23505"

// postgresAccounts adapts the sqlc-generated postgres.Queries to
// core.AccountStore.
type postgresAccounts struct {
	q *postgres.Queries
}

// Compile-time proof that the adapter satisfies the consumer-defined contract.
var _ core.AccountStore = (*postgresAccounts)(nil)

func newPostgresAccounts(pool *sql.DB) *postgresAccounts {
	return &postgresAccounts{q: postgres.New(pool)}
}

// CreateAccount inserts an account. A primary-key or unique collision surfaces
// as core.ErrAlreadyExists by matching SQLSTATE 23505 on the typed driver error.
func (s *postgresAccounts) CreateAccount(ctx context.Context, a core.Account) error {
	err := s.q.CreateAccount(ctx, postgres.CreateAccountParams{
		ID:        a.ID,
		Username:  a.Username,
		CreatedAt: core.NormalizeTime(a.CreatedAt),
	})
	if err != nil {
		if isPostgresUnique(err) {
			return fmt.Errorf("insert account: %w", core.ErrAlreadyExists)
		}
		return fmt.Errorf("insert account: %w", err)
	}
	return nil
}

// GetAccount loads an account by id, mapping sql.ErrNoRows to core.ErrNotFound.
func (s *postgresAccounts) GetAccount(ctx context.Context, id string) (core.Account, error) {
	row, err := s.q.GetAccount(ctx, id)
	return postgresAccountFromRow(row, err, "select account")
}

// GetAccountByUsername loads an account by username, mapping sql.ErrNoRows to
// core.ErrNotFound.
func (s *postgresAccounts) GetAccountByUsername(ctx context.Context, username string) (core.Account, error) {
	row, err := s.q.GetAccountByUsername(ctx, username)
	return postgresAccountFromRow(row, err, "select account by username")
}

// postgresAccountFromRow maps a generated row (and its query error) to the
// domain type. The driver returns TIMESTAMPTZ in the session zone; it is
// normalized back to UTC so both engines yield identical values.
func postgresAccountFromRow(row postgres.Account, err error, op string) (core.Account, error) {
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return core.Account{}, core.ErrNotFound
	case err != nil:
		return core.Account{}, fmt.Errorf("%s: %w", op, err)
	}
	return core.Account{ID: row.ID, Username: row.Username, CreatedAt: core.NormalizeTime(row.CreatedAt)}, nil
}

// isPostgresUnique reports whether err is a unique_violation, keyed on SQLSTATE.
func isPostgresUnique(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == pgUniqueViolation
}
