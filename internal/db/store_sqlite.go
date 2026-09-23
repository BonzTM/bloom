package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	sqlitelib "modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"

	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/db/sqlite"
)

// sqliteTimeLayout is the on-disk timestamp format for SQLite (see the
// created_at comment in migrations/sqlite/00001_create_accounts.sql): RFC 3339
// UTC with exactly six fractional digits, so values sort lexically and carry
// the same microsecond precision as PostgreSQL's TIMESTAMPTZ.
const sqliteTimeLayout = "2006-01-02T15:04:05.000000Z"

// sqliteAccounts adapts the sqlc-generated sqlite.Queries to
// core.AccountStore.
type sqliteAccounts struct {
	q *sqlite.Queries
}

// Compile-time proof that the adapter satisfies the consumer-defined contract.
var (
	_ core.AccountStore       = (*sqliteAccounts)(nil)
	_ core.LocalIdentityStore = (*sqliteAccounts)(nil)
)

func newSQLiteAccounts(pool *sql.DB) *sqliteAccounts {
	return &sqliteAccounts{q: sqlite.New(pool)}
}

// CreateAccount inserts an account. A primary-key or unique collision surfaces
// as core.ErrAlreadyExists by matching the driver's typed *sqlite.Error
// extended result code, never the error string.
func (s *sqliteAccounts) CreateAccount(ctx context.Context, a core.Account) error {
	username, err := core.CanonicalUsername(a.Username)
	if err != nil {
		return fmt.Errorf("insert account username: %w", errors.Join(core.ErrInvalidArgument, err))
	}
	key, err := core.UsernameKey(a.Username)
	if err != nil {
		return fmt.Errorf("insert account username key: %w", errors.Join(core.ErrInvalidArgument, err))
	}
	err = s.q.CreateAccount(ctx, sqlite.CreateAccountParams{
		ID:           a.ID,
		Username:     username,
		UsernameKey:  key,
		PasswordHash: nullableString(a.PasswordHash),
		Disabled:     boolToInt64(a.Disabled),
		CreatedAt:    formatSQLiteTime(a.CreatedAt),
	})
	if err != nil {
		if isSQLiteUnique(err) {
			return fmt.Errorf("insert account: %w", core.ErrAlreadyExists)
		}
		return fmt.Errorf("insert account: %w", err)
	}
	return nil
}

// GetAccount loads an account by id, mapping sql.ErrNoRows to core.ErrNotFound.
func (s *sqliteAccounts) GetAccount(ctx context.Context, id string) (core.Account, error) {
	row, err := s.q.GetAccount(ctx, id)
	return sqliteAccountFromRow(row.ID, row.Username, row.PasswordHash, row.Disabled, row.CreatedAt, err, "select account")
}

// GetAccountByUsername loads an account by username, mapping sql.ErrNoRows to
// core.ErrNotFound.
func (s *sqliteAccounts) GetAccountByUsername(ctx context.Context, username string) (core.Account, error) {
	key, err := core.UsernameKey(username)
	if err != nil {
		return core.Account{}, fmt.Errorf("select account by username: %w", errors.Join(core.ErrInvalidArgument, err))
	}
	row, err := s.q.GetAccountByUsername(ctx, key)
	return sqliteAccountFromRow(row.ID, row.Username, row.PasswordHash, row.Disabled, row.CreatedAt, err, "select account by username")
}

// UpdateAccountPasswordHash replaces the local credential after a successful legacy-profile login.
func (s *sqliteAccounts) UpdateAccountPasswordHash(ctx context.Context, id, hash string) error {
	rows, err := s.q.UpdateAccountPasswordHash(ctx, sqlite.UpdateAccountPasswordHashParams{PasswordHash: sql.NullString{String: hash, Valid: true}, ID: id})
	if err != nil {
		return fmt.Errorf("update account password hash: %w", err)
	}
	if rows != 1 {
		return fmt.Errorf("update account password hash: %w", core.ErrNotFound)
	}
	return nil
}

// sqliteAccountFromRow maps a generated row (and its query error) to the domain
// type. A malformed created_at is a storage corruption, reported as an error
// rather than silently zeroed.
func sqliteAccountFromRow(
	id, username string,
	passwordHash sql.NullString,
	disabled int64,
	created string,
	err error,
	op string,
) (core.Account, error) {
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return core.Account{}, core.ErrNotFound
	case err != nil:
		return core.Account{}, fmt.Errorf("%s: %w", op, err)
	}
	createdAt, err := parseSQLiteTime(created)
	if err != nil {
		return core.Account{}, fmt.Errorf("%s %q: %w", op, id, err)
	}
	return core.Account{
		ID: id, Username: username, PasswordHash: stringFromNull(passwordHash),
		Disabled: disabled != 0, CreatedAt: createdAt,
	}, nil
}

func nullableString(value *string) sql.NullString {
	if value == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: *value, Valid: true}
}

func stringFromNull(value sql.NullString) *string {
	if !value.Valid {
		return nil
	}
	return &value.String
}

func boolToInt64(value bool) int64 {
	if value {
		return 1
	}
	return 0
}

// formatSQLiteTime renders t in the on-disk layout after normalizing to UTC
// microseconds, so what is written is exactly what PostgreSQL would store.
func formatSQLiteTime(t time.Time) string {
	return core.NormalizeTime(t).Format(sqliteTimeLayout)
}

func sqliteNullableTime(value *time.Time) sql.NullString {
	if value == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: formatSQLiteTime(*value), Valid: true}
}

// parseSQLiteTime is the inverse of formatSQLiteTime. It accepts only the exact
// layout: any other shape means a foreign writer touched the column.
func parseSQLiteTime(s string) (time.Time, error) {
	t, err := time.Parse(sqliteTimeLayout, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse stored timestamp: %w", err)
	}
	return t.UTC(), nil
}

// isSQLiteUnique reports whether err is a SQLite UNIQUE or PRIMARY KEY
// constraint violation, keyed on the extended result code.
func isSQLiteUnique(err error) bool {
	var serr *sqlitelib.Error
	if !errors.As(err, &serr) {
		return false
	}
	code := serr.Code()
	return code == sqlite3.SQLITE_CONSTRAINT_UNIQUE || code == sqlite3.SQLITE_CONSTRAINT_PRIMARYKEY
}
