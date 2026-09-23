package db

import (
	"database/sql"
	"errors"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/BonzTM/bloom/internal/config"
)

type sqliteDSNCase struct {
	name      string
	dsn       string
	want      string
	wantAdded bool
}

func sqliteDSNCases() []sqliteDSNCase {
	return []sqliteDSNCase{
		{"plain path", "bloom.db", "bloom.db?_pragma=foreign_keys(1)", true},
		{"file URI without query", "file:bloom.db", "file:bloom.db?_pragma=foreign_keys(1)", true},
		{"memory file without query", "file::memory:", "file::memory:?_pragma=foreign_keys(1)", true},
		{"shared memory file with query", "file:path?mode=memory&cache=shared", "file:path?mode=memory&cache=shared&_pragma=foreign_keys(1)", true},
		{"explicitly enabled", "file:bloom.db?_pragma=foreign_keys(1)", "file:bloom.db?_pragma=foreign_keys(1)", false},
		{"explicitly disabled", "file:bloom.db?mode=rwc&_pragma=foreign_keys(0)", "file:bloom.db?mode=rwc&_pragma=foreign_keys(0)", false},
		{"mixed case parenthesized value", "file:bloom.db?_pragma=FoReIgN_KeYs(0)", "file:bloom.db?_pragma=FoReIgN_KeYs(0)", false},
		{"assignment value", "file:bloom.db?_pragma=foreign_keys=off", "file:bloom.db?_pragma=foreign_keys=off", false},
		{"encoded value", "file:bloom.db?_pragma=foreign_keys%280%29", "file:bloom.db?_pragma=foreign_keys%280%29", false},
		{"encoded key and value", "file:bloom.db?%5Fpragma=foreign_keys%3Doff", "file:bloom.db?%5Fpragma=foreign_keys%3Doff", false},
		{"foreign keys in later pragma", "file:bloom.db?_pragma=busy_timeout(1)&_pragma=FoReIgN_KeYs%3Doff", "file:bloom.db?_pragma=busy_timeout(1)&_pragma=FoReIgN_KeYs%3Doff", false},
		{"pragma key is case sensitive", "file:bloom.db?_Pragma=foreign_keys(0)", "file:bloom.db?_Pragma=foreign_keys(0)&_pragma=foreign_keys(1)", true},
		{"pragma text inside another value", "file:bloom.db?label=_pragma=foreign_keys(0)", "file:bloom.db?label=_pragma=foreign_keys(0)&_pragma=foreign_keys(1)", true},
		{"leading question mark belongs to filename", "?bloom.db", "file:%3Fbloom.db?_pragma=foreign_keys(1)", true},
		{"encoded question mark belongs to path", "file:bloom%3F.db", "file:bloom%3F.db?_pragma=foreign_keys(1)", true},
		{"fragment is preserved", "file:bloom.db?mode=rwc#section", "file:bloom.db?mode=rwc#section&_pragma=foreign_keys(1)", true},
		{"pragma before fragment is preserved", "file:bloom.db?_pragma=foreign_keys(0)&label=x#section", "file:bloom.db?_pragma=foreign_keys(0)&label=x#section", false},
		{"foreign keys shorthand", "file:bloom.db?_foreign_keys=on", "file:bloom.db?_foreign_keys=on", false},
		{"encoded foreign keys shorthand", "file:bloom.db?%5Fforeign%5Fkeys=off", "file:bloom.db?%5Fforeign%5Fkeys=off", false},
		{"empty foreign keys shorthand", "file:bloom.db?_foreign_keys=", "file:bloom.db?_foreign_keys=", false},
		{"foreign keys alias", "file:bloom.db?_fk=yes", "file:bloom.db?_fk=yes", false},
		{"encoded foreign keys alias", "file:bloom.db?%5Ffk=off", "file:bloom.db?%5Ffk=off", false},
		{"empty alias wins", "file:bloom.db?_foreign_keys=on&_fk=", "file:bloom.db?_foreign_keys=on&_fk=", false},
		{"conflicting alias wins", "file:bloom.db?_foreign_keys=off&_fk=on", "file:bloom.db?_foreign_keys=off&_fk=on", false},
	}
}

func TestSQLiteDSNWithForeignKeys(t *testing.T) {
	for _, test := range sqliteDSNCases() {
		t.Run(test.name, func(t *testing.T) {
			got, added := sqliteDSNWithForeignKeys(test.dsn)
			if got != test.want || added != test.wantAdded {
				t.Fatalf("sqliteDSNWithForeignKeys(%q) = %q, %t; want %q, %t", test.dsn, got, added, test.want, test.wantAdded)
			}
		})
	}
}

func TestOpenSQLiteLeadingQuestionMarkPathEnablesForeignKeys(t *testing.T) {
	t.Chdir(t.TempDir())
	pool, err := Open(t.Context(), sqliteTestConfig("?bloom.db"), discardTestLogger())
	if err != nil {
		t.Fatalf("Open SQLite leading-question-mark path: %v", err)
	}
	t.Cleanup(func() { _ = pool.Close() })
	assertSQLiteForeignKeys(t, pool, 1)
	if _, err := os.Stat("?bloom.db"); err != nil {
		t.Fatalf("stat original SQLite filename: %v", err)
	}
}

func TestOpenSQLiteForeignKeyShorthands(t *testing.T) {
	tests := []struct {
		name    string
		dsn     string
		enabled bool
	}{
		{"enabled", "file::memory:?_foreign_keys=on", true},
		{"disabled foreign keys", "file::memory:?_foreign_keys=false", false},
		{"disabled alias", "file::memory:?_fk=off", false},
		{"empty alias", "file::memory:?_fk=", false},
		{"encoded foreign keys", "file::memory:?%5Fforeign%5Fkeys=%66alse", false},
		{"encoded alias", "file::memory:?%5Ffk=%6Fff", false},
		{"empty alias overrides enabled foreign keys", "file::memory:?_foreign_keys=on&_fk=", false},
		{"conflicting alias disables", "file::memory:?_foreign_keys=on&_fk=off", false},
		{"conflicting alias enables", "file::memory:?_foreign_keys=off&_fk=on", true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) { testSQLiteForeignKeyShorthand(t, test.dsn, test.enabled) })
	}
}

func testSQLiteForeignKeyShorthand(t *testing.T, dsn string, enabled bool) {
	t.Helper()
	pool, err := Open(t.Context(), sqliteTestConfig(dsn), discardTestLogger())
	if !enabled {
		const want = "verify SQLite foreign keys: disabled; BLOOM_DB_DSN contains a foreign-key directive that disables foreign keys and must be enabled or removed"
		if err == nil || err.Error() != want {
			_ = pool.Close()
			t.Fatalf("Open with disabled foreign keys error = %v, want %q", err, want)
		}
		return
	}
	if err != nil {
		t.Fatalf("Open with foreign keys enabled: %v", err)
	}
	t.Cleanup(func() { _ = pool.Close() })
	assertSQLiteForeignKeys(t, pool, 1)
}

func TestVerifySQLiteForeignKeysReportsCompatibilityDefaultFailure(t *testing.T) {
	pool, err := sql.Open("sqlite", "file::memory:")
	if err != nil {
		t.Fatalf("open SQLite: %v", err)
	}
	t.Cleanup(func() { _ = pool.Close() })
	const want = "verify SQLite foreign keys: disabled after compatibility default was applied to BLOOM_DB_DSN without a foreign-key directive"
	if err := verifySQLiteForeignKeys(t.Context(), pool, true); err == nil || err.Error() != want {
		t.Fatalf("verify SQLite compatibility default error = %v, want %q", err, want)
	}
}

func TestOpenRejectsNilLogger(t *testing.T) {
	pool, err := Open(
		t.Context(),
		sqliteTestConfig("file::memory:?_pragma=foreign_keys(1)"),
		nil,
	)
	if !errors.Is(err, ErrDatabaseLoggerRequired) {
		t.Fatalf("Open with nil logger error = %v, want %v", err, ErrDatabaseLoggerRequired)
	}
	if pool != nil {
		t.Fatal("Open with nil logger returned a pool")
	}
}

func sqliteTestConfig(dsn string) config.DatabaseConfig {
	return config.DatabaseConfig{
		Driver:          config.DriverSQLite,
		DSN:             dsn,
		MaxOpenConns:    1,
		MaxIdleConns:    1,
		ConnMaxLifetime: time.Hour,
		ConnMaxIdleTime: time.Hour,
	}
}

func discardTestLogger() *slog.Logger {
	return slog.New(slog.DiscardHandler)
}

func assertSQLiteForeignKeys(t *testing.T, pool *sql.DB, want int) {
	t.Helper()
	var got int
	if err := pool.QueryRowContext(t.Context(), "PRAGMA foreign_keys").Scan(&got); err != nil {
		t.Fatalf("read PRAGMA foreign_keys: %v", err)
	}
	if got != want {
		t.Fatalf("PRAGMA foreign_keys = %d, want %d", got, want)
	}
}
