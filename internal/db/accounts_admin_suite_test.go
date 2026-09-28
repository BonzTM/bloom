package db_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/BonzTM/bloom/internal/config"
	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/db"
)

const (
	adminPasswordHashCanary = "db-password-hash-canary-42f7"
	adminOIDCIssuerCanary   = "https://db-issuer-canary-8c19.example.test"
	adminOIDCSubjectCanary  = "db-subject-canary-a603"
	adminOIDCClaimCanary    = "db-claim-canary-d85e"
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
	assertAdminAccountCanariesAbsent(t, "first page", first)
	assertAdminAccountCanariesAbsent(t, "second page", second)
	assertAccountAdminDetail(t, reader, fixture)
	testAccountAdminUnicodePaging(t, reader, accounts)
	testAccountAdminUnicodeRoleOrdering(t, pool, driver, reader, accounts)
	testAccountAdminEnrichmentBounds(t, pool, driver, reader, accounts)
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
	hash := adminPasswordHashCanary
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
	insertAccountAdminIdentity(t, pool, driver, fixture.oidc, now,
		"https://issuer-"+fixture.oidc.ID+".example.test", fixture.oidc.ID, fixture.oidc.Username)
	insertAccountAdminIdentity(t, pool, driver, fixture.both, now,
		adminOIDCIssuerCanary, adminOIDCSubjectCanary, adminOIDCClaimCanary)
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
	issuer, subject, claim string,
) {
	t.Helper()
	storedTime := any(now)
	if driver == config.DriverSQLite {
		storedTime = now.Format("2006-01-02T15:04:05.000000Z")
	}
	execTestSQL(t, pool, `INSERT INTO account_identities
        (account_id, provider, issuer, subject, username_claim, mapped_roles, created_at, last_login_at)
        VALUES ($1, 'oidc', $2, $3, $4, '[]', $5, $5)`,
		account.ID, issuer, subject, claim, storedTime)
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
	assertAdminAccountCanariesAbsent(t, "account detail", detail)
	if _, err := reader.GetAdminAccount(context.Background(), mustID(t)); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("missing GetAdminAccount = %v, want ErrNotFound", err)
	}
}

func assertAdminAccountCanariesAbsent(t *testing.T, location string, value any) {
	t.Helper()
	serialized, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal %s: %v", location, err)
	}
	for _, canary := range [...]string{
		adminPasswordHashCanary, adminOIDCIssuerCanary, adminOIDCSubjectCanary, adminOIDCClaimCanary,
	} {
		if strings.Contains(string(serialized), canary) {
			t.Fatalf("%s contains credential canary %q", location, canary)
		}
	}
}

func testAccountAdminUnicodePaging(
	t *testing.T, reader core.AccountAdminReader, accounts core.AccountStore,
) {
	t.Helper()
	want := [...]string{
		"zzadminunicode-a", "zzadminunicode-é", "zzadminunicode-ω", "zzadminunicode-中",
	}
	now := core.NormalizeTime(time.Date(2026, 9, 28, 13, 0, 0, 0, time.UTC))
	for _, username := range want {
		hash := "unicode-ordering-hash"
		account := core.Account{ID: mustID(t), Username: username, PasswordHash: &hash, CreatedAt: now}
		if err := accounts.CreateAccount(t.Context(), account); err != nil {
			t.Fatalf("create Unicode account %q: %v", username, err)
		}
	}
	searchKey, err := core.AccountSearchKey("zzadminunicode-")
	if err != nil {
		t.Fatalf("Unicode AccountSearchKey: %v", err)
	}
	var after *core.AccountListPosition
	got := make([]string, 0, len(want))
	for pageIndex := range want {
		page, pageErr := reader.ListAccounts(t.Context(), core.AccountListQuery{
			SearchKey: searchKey, After: after, Limit: 1,
		})
		if pageErr != nil || len(page.Items) != 1 || page.HasMore != (pageIndex < len(want)-1) {
			t.Fatalf("Unicode account page %d = %+v, %v", pageIndex, page, pageErr)
		}
		account := page.Items[0]
		got = append(got, account.Username)
		after = &core.AccountListPosition{UsernameKey: account.UsernameKey, ID: account.ID}
	}
	if !slices.Equal(got, want[:]) {
		t.Fatalf("Unicode account order = %q, want %q", got, want)
	}
}

func testAccountAdminUnicodeRoleOrdering(
	t *testing.T, pool *sql.DB, driver config.Driver, reader core.AccountAdminReader, accounts core.AccountStore,
) {
	t.Helper()
	want := [...]string{"zzrole-a", "zzrole-é", "zzrole-ω", "zzrole-中"}
	account := createAccountAdminBoundFixture(t, accounts, "zzrole-order")
	now := migrationCreatedAt(driver)
	for _, name := range want {
		roleID := mustID(t)
		execTestSQL(t, pool, `INSERT INTO roles (id,name,description,built_in,created_at)
            VALUES ($1,$2,'Unicode ordering role',FALSE,$3)`, roleID, name, now)
		execTestSQL(t, pool, `INSERT INTO account_roles (account_id,role_id,source)
            VALUES ($1,$2,'manual')`, account.ID, roleID)
	}
	detail, err := reader.GetAdminAccount(t.Context(), account.ID)
	if err != nil {
		t.Fatalf("Unicode role detail: %v", err)
	}
	got := make([]string, 0, len(detail.Roles))
	for _, role := range detail.Roles {
		got = append(got, role.Name)
	}
	if !slices.Equal(got, want[:]) {
		t.Fatalf("Unicode role order = %q, want %q", got, want)
	}
}

func testAccountAdminEnrichmentBounds(
	t *testing.T, pool *sql.DB, driver config.Driver, reader core.AccountAdminReader, accounts core.AccountStore,
) {
	t.Helper()
	maxRoles := createAccountAdminBoundFixture(t, accounts, "zzbound-roles-max")
	seedAccountAdminRoles(t, pool, driver, maxRoles.ID, core.MaxAccountRoleAssignments)
	detail, err := reader.GetAdminAccount(t.Context(), maxRoles.ID)
	if err != nil || len(detail.Roles) != core.MaxAccountRoleAssignments {
		t.Fatalf("maximum role enrichment = %d, %v", len(detail.Roles), err)
	}
	overflowRoles := createAccountAdminBoundFixture(t, accounts, "zzbound-roles-overflow")
	seedAccountAdminRoles(t, pool, driver, overflowRoles.ID, core.MaxAccountRoleAssignments+1)
	if _, overflowErr := reader.GetAdminAccount(t.Context(), overflowRoles.ID); overflowErr == nil || errors.Is(overflowErr, core.ErrInvalidArgument) {
		t.Fatalf("overflow role enrichment error = %v, want internal error", overflowErr)
	}

	maxMedia := createAccountAdminBoundFixture(t, accounts, "zzbound-media-max")
	seedAccountAdminMediaUsers(t, pool, driver, maxMedia.ID, core.MaxAccountLinkedMediaUsers)
	detail, err = reader.GetAdminAccount(t.Context(), maxMedia.ID)
	if err != nil || len(detail.MediaUsers) != core.MaxAccountLinkedMediaUsers {
		t.Fatalf("maximum media-user enrichment = %d, %v", len(detail.MediaUsers), err)
	}
	overflowMedia := createAccountAdminBoundFixture(t, accounts, "zzbound-media-overflow")
	seedAccountAdminMediaUsers(t, pool, driver, overflowMedia.ID, core.MaxAccountLinkedMediaUsers+1)
	if _, overflowErr := reader.GetAdminAccount(t.Context(), overflowMedia.ID); overflowErr == nil || errors.Is(overflowErr, core.ErrInvalidArgument) {
		t.Fatalf("overflow media-user enrichment error = %v, want internal error", overflowErr)
	}
}

func createAccountAdminBoundFixture(t *testing.T, accounts core.AccountStore, prefix string) core.Account {
	t.Helper()
	hash := "bounded-enrichment-hash"
	account := core.Account{
		ID: mustID(t), Username: prefix + "-" + mustID(t), PasswordHash: &hash,
		CreatedAt: core.NormalizeTime(time.Date(2026, 9, 28, 14, 0, 0, 0, time.UTC)),
	}
	if err := accounts.CreateAccount(t.Context(), account); err != nil {
		t.Fatalf("create bounded account fixture: %v", err)
	}
	return account
}

func seedAccountAdminRoles(
	t *testing.T, pool *sql.DB, driver config.Driver, accountID string, count int,
) {
	t.Helper()
	if count < 0 || count > core.MaxAccountRoleAssignments+1 {
		t.Fatalf("role fixture count = %d", count)
	}
	for index := range core.MaxAccountRoleAssignments + 1 {
		if index >= count {
			break
		}
		roleID := mustID(t)
		name := fmt.Sprintf("zzbound-role-%03d-%s", index, roleID[:8])
		execTestSQL(t, pool, `INSERT INTO roles (id,name,description,built_in,created_at)
            VALUES ($1,$2,'Bounded role',FALSE,$3)`, roleID, name, migrationCreatedAt(driver))
		execTestSQL(t, pool, `INSERT INTO account_roles (account_id,role_id,source)
            VALUES ($1,$2,'manual')`, accountID, roleID)
	}
}

func seedAccountAdminMediaUsers(
	t *testing.T, pool *sql.DB, driver config.Driver, accountID string, count int,
) {
	t.Helper()
	if count < 0 || count > core.MaxAccountLinkedMediaUsers+1 {
		t.Fatalf("media-user fixture count = %d", count)
	}
	for index := range core.MaxAccountLinkedMediaUsers + 1 {
		if index >= count {
			break
		}
		serverID := mustID(t)
		name := fmt.Sprintf("Bounded server %03d %s", index, serverID[:8])
		execTestSQL(t, pool, `INSERT INTO media_servers
            (id,kind,name,name_key,base_url,credential_ciphertext,allow_insecure,created_at,updated_at)
            VALUES ($1,'jellyfin',$2,$2,$3,$4,FALSE,$5,$5)`, serverID, name,
			"https://"+serverID+".example.test", []byte("ciphertext"), migrationCreatedAt(driver))
		execTestSQL(t, pool, `INSERT INTO account_media_users
            (account_id,media_server_id,media_user_id,username,source,created_at,updated_at)
            VALUES ($1,$2,$3,$4,'admin',$5,$5)`, accountID, serverID,
			fmt.Sprintf("bounded-user-%03d", index), fmt.Sprintf("Bounded user %03d", index), migrationCreatedAt(driver))
	}
}
