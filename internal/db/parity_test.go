package db_test

import (
	"context"
	"database/sql"
	"io/fs"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/BonzTM/bloom/internal/config"
	"github.com/BonzTM/bloom/internal/db"
	"github.com/BonzTM/bloom/internal/db/postgres"
	"github.com/BonzTM/bloom/internal/db/sqlite"
)

// TestMigrationDirectoriesMatch enforces ADR 0004 item 2 for SQL migrations:
// each schema step ships once per engine with the SAME name. Engine-neutral Go
// data migrations are registered separately through the provider.
func TestMigrationDirectoriesMatch(t *testing.T) {
	list := func(dir string) []string {
		entries, err := fs.ReadDir(db.MigrationsFS(), "migrations/"+dir)
		if err != nil {
			t.Fatalf("read %s migrations: %v", dir, err)
		}
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		slices.Sort(names)
		return names
	}
	sqliteFiles, postgresFiles := list("sqlite"), list("postgres")
	if len(sqliteFiles) == 0 {
		t.Fatal("no sqlite migrations embedded")
	}
	if !slices.Equal(sqliteFiles, postgresFiles) {
		t.Fatalf("migration file lists differ:\n  sqlite:   %v\n  postgres: %v", sqliteFiles, postgresFiles)
	}
}

// TestGeneratedQuerierParity enforces ADR 0004 item 3: a query name that exists
// for one engine and not the other fails the build. The sqlc Querier
// interfaces are compared by method name.
func TestGeneratedQuerierParity(t *testing.T) {
	methods := func(typ reflect.Type) []string {
		names := make([]string, 0, typ.NumMethod())
		for m := range typ.Methods() {
			names = append(names, m.Name)
		}
		slices.Sort(names)
		return names
	}
	sq := methods(reflect.TypeFor[sqlite.Querier]())
	pg := methods(reflect.TypeFor[postgres.Querier]())
	if len(sq) == 0 {
		t.Fatal("sqlite Querier has no methods")
	}
	if !slices.Equal(sq, pg) {
		t.Fatalf("generated query sets differ:\n  sqlite:   %v\n  postgres: %v", sq, pg)
	}
}

// TestSQLiteEngineSuite runs the shared parity suite against an in-memory
// SQLite database on every `make verify`. MaxOpenConns is 1 and the lifetime is
// long because an in-memory database lives and dies with its connection.
func TestSQLiteEngineSuite(t *testing.T) {
	pool := openSQLiteMemory(t)
	runEngineSuite(t, pool, config.DriverSQLite)
	assertColumns(t, pool, sqliteColumns, expectedAccountColumns)
	assertColumns(t, pool, sqliteSessionColumns, expectedSessionColumns)
}

// expectedAccountColumns is the column set ADR 0004 item 2 requires both
// engines to expose for the accounts table.
var (
	expectedAccountColumns = []string{"created_at", "disabled", "id", "password_hash", "username", "username_key"}
	expectedSessionColumns = []string{"data", "expiry", "token"}
)

func openSQLiteMemory(t *testing.T) *sql.DB {
	t.Helper()
	pool, err := db.Open(context.Background(), config.DatabaseConfig{
		Driver:          config.DriverSQLite,
		DSN:             "file::memory:",
		MaxOpenConns:    1,
		MaxIdleConns:    1,
		ConnMaxLifetime: time.Hour,
		ConnMaxIdleTime: time.Hour,
	})
	if err != nil {
		t.Fatalf("Open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = pool.Close() })
	return pool
}

// sqliteColumns lists the accounts columns via PRAGMA table_info.
func sqliteColumns(ctx context.Context, pool *sql.DB) ([]string, error) {
	rows, err := pool.QueryContext(ctx, "SELECT name FROM pragma_table_info('accounts') ORDER BY name")
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanStrings(rows)
}

func sqliteSessionColumns(ctx context.Context, pool *sql.DB) ([]string, error) {
	rows, err := pool.QueryContext(ctx, "SELECT name FROM pragma_table_info('sessions') ORDER BY name")
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanStrings(rows)
}

func scanStrings(rows *sql.Rows) ([]string, error) {
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

type columnLister func(ctx context.Context, pool *sql.DB) ([]string, error)

func assertColumns(t *testing.T, pool *sql.DB, list columnLister, want []string) {
	t.Helper()
	got, err := list(context.Background(), pool)
	if err != nil {
		t.Fatalf("list columns: %v", err)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("accounts columns = %v, want %v", got, want)
	}
}

func TestOpenRejectsUnknownDriver(t *testing.T) {
	_, err := db.Open(context.Background(), config.DatabaseConfig{Driver: "mysql", DSN: "x", MaxOpenConns: 1})
	if err == nil {
		t.Fatal("Open(mysql) succeeded, want error")
	}
	if _, _, err := db.NewAccountStores(nil, "mysql"); err == nil {
		t.Fatal("NewAccountStores(mysql) succeeded, want error")
	}
}
