package db_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/BonzTM/bloom/internal/config"
	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/db"
)

// runEngineSuite is the ONE parity suite both engines must pass (ADR 0004
// item 6): migrate up, down to zero, up again, then exercise every
// core.AccountStore method against the freshly migrated schema. The SQLite
// test runs it in-memory on every `make verify`; the PostgreSQL test runs it
// under the integration build tag against a live server.
func runEngineSuite(t *testing.T, pool *sql.DB, driver config.Driver) {
	t.Helper()
	ctx := context.Background()

	// up / down / up: forward, reverse, and re-apply all succeed.
	if err := db.Migrate(ctx, pool, driver); err != nil {
		t.Fatalf("Migrate (first up): %v", err)
	}
	if err := db.MigrateDownAll(ctx, pool, driver); err != nil {
		t.Fatalf("MigrateDownAll: %v", err)
	}
	if err := db.Migrate(ctx, pool, driver); err != nil {
		t.Fatalf("Migrate (second up): %v", err)
	}

	store, err := db.NewAccountStore(pool, driver)
	if err != nil {
		t.Fatalf("NewAccountStore: %v", err)
	}

	t.Run("create and get round-trip", func(t *testing.T) { testCreateGet(t, store) })
	t.Run("duplicate id", func(t *testing.T) { testDuplicateID(t, store) })
	t.Run("duplicate username", func(t *testing.T) { testDuplicateUsername(t, store) })
	t.Run("not found", func(t *testing.T) { testNotFound(t, store) })
	t.Run("timestamp normalized to utc microseconds", func(t *testing.T) { testTimestampNormalization(t, store) })
}

func mustID(t *testing.T) string {
	t.Helper()
	id, err := core.NewID()
	if err != nil {
		t.Fatalf("NewID: %v", err)
	}
	return id
}

func testCreateGet(t *testing.T, store core.AccountStore) {
	t.Helper()
	ctx := context.Background()
	want := core.Account{
		ID:        mustID(t),
		Username:  "alice-" + mustID(t),
		CreatedAt: time.Date(2026, 9, 22, 10, 30, 0, 123456000, time.UTC),
	}
	if err := store.CreateAccount(ctx, want); err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}

	got, err := store.GetAccount(ctx, want.ID)
	if err != nil {
		t.Fatalf("GetAccount: %v", err)
	}
	assertAccount(t, got, want)

	got, err = store.GetAccountByUsername(ctx, want.Username)
	if err != nil {
		t.Fatalf("GetAccountByUsername: %v", err)
	}
	assertAccount(t, got, want)
}

func testDuplicateID(t *testing.T, store core.AccountStore) {
	t.Helper()
	ctx := context.Background()
	id := mustID(t)
	first := core.Account{ID: id, Username: "first-" + id, CreatedAt: time.Now()}
	if err := store.CreateAccount(ctx, first); err != nil {
		t.Fatalf("first CreateAccount: %v", err)
	}
	dup := core.Account{ID: id, Username: "second-" + id, CreatedAt: time.Now()}
	if err := store.CreateAccount(ctx, dup); !errors.Is(err, core.ErrAlreadyExists) {
		t.Fatalf("duplicate id CreateAccount = %v, want ErrAlreadyExists", err)
	}
	// The original row is untouched.
	got, err := store.GetAccount(ctx, id)
	if err != nil || got.Username != first.Username {
		t.Fatalf("GetAccount after duplicate = %+v, %v; want username %q", got, err, first.Username)
	}
}

func testDuplicateUsername(t *testing.T, store core.AccountStore) {
	t.Helper()
	ctx := context.Background()
	username := "shared-" + mustID(t)
	if err := store.CreateAccount(ctx, core.Account{ID: mustID(t), Username: username, CreatedAt: time.Now()}); err != nil {
		t.Fatalf("first CreateAccount: %v", err)
	}
	err := store.CreateAccount(ctx, core.Account{ID: mustID(t), Username: username, CreatedAt: time.Now()})
	if !errors.Is(err, core.ErrAlreadyExists) {
		t.Fatalf("duplicate username CreateAccount = %v, want ErrAlreadyExists", err)
	}
}

func testNotFound(t *testing.T, store core.AccountStore) {
	t.Helper()
	ctx := context.Background()
	if _, err := store.GetAccount(ctx, mustID(t)); !errors.Is(err, core.ErrNotFound) {
		t.Errorf("GetAccount(missing) = %v, want ErrNotFound", err)
	}
	if _, err := store.GetAccountByUsername(ctx, "nobody-"+mustID(t)); !errors.Is(err, core.ErrNotFound) {
		t.Errorf("GetAccountByUsername(missing) = %v, want ErrNotFound", err)
	}
}

// testTimestampNormalization proves ADR 0004 item 5 on this engine: a
// non-UTC, nanosecond-precision input reads back as the same instant in UTC at
// microsecond precision, so a value written on either engine compares Equal.
func testTimestampNormalization(t *testing.T, store core.AccountStore) {
	t.Helper()
	ctx := context.Background()
	loc := time.FixedZone("minus7", -7*60*60)
	in := time.Date(2026, 1, 2, 3, 4, 5, 987654321, loc)
	a := core.Account{ID: mustID(t), Username: "tz-" + mustID(t), CreatedAt: in}
	if err := store.CreateAccount(ctx, a); err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	got, err := store.GetAccount(ctx, a.ID)
	if err != nil {
		t.Fatalf("GetAccount: %v", err)
	}
	want := core.NormalizeTime(in)
	if !got.CreatedAt.Equal(want) {
		t.Errorf("CreatedAt = %v, want %v", got.CreatedAt, want)
	}
	if got.CreatedAt.Location() != time.UTC {
		t.Errorf("CreatedAt location = %v, want UTC", got.CreatedAt.Location())
	}
	if got.CreatedAt.Nanosecond() != 987654000 {
		t.Errorf("CreatedAt nanos = %d, want 987654000 (microsecond truncation)", got.CreatedAt.Nanosecond())
	}
}

func assertAccount(t *testing.T, got, want core.Account) {
	t.Helper()
	if got.ID != want.ID || got.Username != want.Username {
		t.Errorf("account = %+v, want id %q username %q", got, want.ID, want.Username)
	}
	if !got.CreatedAt.Equal(want.CreatedAt) {
		t.Errorf("CreatedAt = %v, want %v", got.CreatedAt, want.CreatedAt)
	}
}
