package db_test

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/BonzTM/bloom/internal/config"
	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/db"
)

func testAccountAdminReads(
	t *testing.T,
	pool *sql.DB,
	driver config.Driver,
	accounts core.AccountStore,
	adminStore core.AdminAccountStore,
) {
	t.Helper()
	reader, err := db.NewAccountAdminReader(pool, driver)
	if err != nil {
		t.Fatalf("NewAccountAdminReader: %v", err)
	}
	fixture := createAccountAdminFixture(t, pool, driver, accounts, adminStore)
	searchKey, err := core.AccountSearchKey("ZZADMINREAD")
	if err != nil {
		t.Fatalf("AccountSearchKey: %v", err)
	}
	first, err := reader.ListAccounts(t.Context(), core.AccountListQuery{SearchKey: searchKey, Limit: 1})
	if err != nil || len(first.Items) != 1 || !first.HasMore || first.Items[0].Username != fixture.both.Username {
		t.Fatalf("first account page = %+v, %v", first, err)
	}
	second, err := reader.ListAccounts(t.Context(), core.AccountListQuery{
		SearchKey: searchKey,
		After: &core.AccountListPosition{
			UsernameKey: first.Items[0].UsernameKey,
			ID:          first.Items[0].ID,
		},
		Limit: 2,
	})
	if err != nil || len(second.Items) != 2 || second.HasMore ||
		second.Items[0].Username != fixture.local.Username || second.Items[1].Username != fixture.oidc.Username {
		t.Fatalf("second account page = %+v, %v", second, err)
	}
	if second.Items[0].SignInMethod != core.AccountSignInLocal ||
		second.Items[1].SignInMethod != core.AccountSignInOIDC {
		t.Fatalf("second account page sign-in methods = %q, %q",
			second.Items[0].SignInMethod, second.Items[1].SignInMethod)
	}
	assertAccountAdminDetail(t, reader, fixture)
}

type accountAdminFixture struct {
	local, oidc, both core.Account
	link              core.AccountMediaUser
}

func createAccountAdminFixture(
	t *testing.T,
	pool *sql.DB,
	driver config.Driver,
	accounts core.AccountStore,
	adminStore core.AdminAccountStore,
) accountAdminFixture {
	t.Helper()
	now := core.NormalizeTime(time.Date(2026, 9, 28, 12, 0, 0, 123456789, time.UTC))
	hash := "local-hash"
	fixture := accountAdminFixture{
		local: core.Account{ID: mustID(t), Username: "zzadminread-local-" + mustID(t), PasswordHash: &hash, CreatedAt: now},
		oidc:  core.Account{ID: mustID(t), Username: "zzadminread-oidc-" + mustID(t), CreatedAt: now},
		both:  core.Account{ID: mustID(t), Username: "zzadminread-both-" + mustID(t), PasswordHash: &hash, CreatedAt: now},
	}
	for _, account := range []core.Account{fixture.local, fixture.oidc, fixture.both} {
		if err := accounts.CreateAccount(t.Context(), account); err != nil {
			t.Fatalf("create account admin fixture: %v", err)
		}
	}
	insertAccountAdminIdentity(t, pool, driver, fixture.oidc, now)
	insertAccountAdminIdentity(t, pool, driver, fixture.both, now)
	if _, err := adminStore.GrantRole(t.Context(), fixture.both.ID, "owner"); err != nil {
		t.Fatalf("grant manual account admin role: %v", err)
	}
	execTestSQL(t, pool, `INSERT INTO account_roles (account_id, role_id, source)
        VALUES ($1, '00000000-0000-4000-8000-000000000001', 'oidc')`, fixture.both.ID)
	fixture.link = createAccountAdminMediaLink(t, pool, driver, fixture.both, now)
	return fixture
}

func insertAccountAdminIdentity(
	t *testing.T, pool *sql.DB, driver config.Driver, account core.Account, now time.Time,
) {
	t.Helper()
	storedTime := any(now)
	if driver == config.DriverSQLite {
		storedTime = now.Format("2006-01-02T15:04:05.000000Z")
	}
	execTestSQL(t, pool, `INSERT INTO account_identities
        (account_id, provider, issuer, subject, username_claim, mapped_roles, created_at, last_login_at)
        VALUES ($1, 'oidc', $2, $3, $4, '[]', $5, $5)`,
		account.ID, "https://issuer-"+account.ID+".example.test", account.ID, account.Username, storedTime)
}

func createAccountAdminMediaLink(
	t *testing.T, pool *sql.DB, driver config.Driver, account core.Account, now time.Time,
) core.AccountMediaUser {
	t.Helper()
	server := mediaServerRecord(t, "Account admin server "+mustID(t), "https://account-admin.example.test", now)
	_, serverWriter, err := db.NewMediaServerStores(pool, driver)
	if err != nil {
		t.Fatalf("NewMediaServerStores: %v", err)
	}
	if createErr := serverWriter.CreateMediaServer(t.Context(), server); createErr != nil {
		t.Fatalf("create account admin media server: %v", createErr)
	}
	_, linkWriter, err := db.NewAccountMediaUserStores(pool, driver)
	if err != nil {
		t.Fatalf("NewAccountMediaUserStores: %v", err)
	}
	link := core.AccountMediaUser{
		AccountID: account.ID, MediaServerID: server.ID, MediaUserID: "account-admin-user",
		Username: "linked-admin", Source: core.AccountMediaUserSourceAdmin, CreatedAt: now, UpdatedAt: now,
	}
	if err := linkWriter.SetAccountMediaUser(t.Context(), link); err != nil {
		t.Fatalf("set account admin media user: %v", err)
	}
	suppressedAt := now.Add(time.Second)
	if err := linkWriter.SuppressAccountMediaUser(t.Context(), account.ID, server.ID, suppressedAt); err != nil {
		t.Fatalf("suppress account admin media user: %v", err)
	}
	link.MediaServerName = server.Name
	link.UpdatedAt, link.SuppressedAt = suppressedAt, &suppressedAt
	return link
}

func assertAccountAdminDetail(t *testing.T, reader core.AccountAdminReader, fixture accountAdminFixture) {
	t.Helper()
	detail, err := reader.GetAdminAccount(context.Background(), fixture.both.ID)
	if err != nil {
		t.Fatalf("GetAdminAccount: %v", err)
	}
	wantRoles := []core.AccountRoleAssignment{
		{Name: "owner", Source: core.AccountRoleSourceManual},
		{Name: "owner", Source: core.AccountRoleSourceOIDC},
	}
	if detail.SignInMethod != core.AccountSignInBoth || !slices.Equal(detail.Roles, wantRoles) ||
		len(detail.MediaUsers) != 1 || detail.MediaUsers[0].SuppressedAt == nil ||
		detail.MediaUsers[0].MediaServerID != fixture.link.MediaServerID {
		t.Fatalf("account detail = %+v", detail)
	}
	if _, err := reader.GetAdminAccount(context.Background(), mustID(t)); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("missing GetAdminAccount = %v, want ErrNotFound", err)
	}
}
