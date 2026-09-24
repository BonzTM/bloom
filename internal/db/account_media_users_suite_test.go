package db_test

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/BonzTM/bloom/internal/accountmedia"
	"github.com/BonzTM/bloom/internal/config"
	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/db"
	"github.com/BonzTM/bloom/internal/testutil"
)

func runAccountMediaUserEngineTests(
	t *testing.T, pool *sql.DB, driver config.Driver, accounts core.AccountStore,
) {
	t.Helper()
	ctx := context.Background()
	now := core.NormalizeTime(time.Date(2026, 9, 24, 12, 0, 0, 123456789, time.UTC))
	account := core.Account{ID: mustID(t), Username: "linked-" + mustID(t), CreatedAt: now}
	other := core.Account{ID: mustID(t), Username: "other-" + mustID(t), CreatedAt: now}
	if err := accounts.CreateAccount(ctx, account); err != nil {
		t.Fatalf("create linked account: %v", err)
	}
	if err := accounts.CreateAccount(ctx, other); err != nil {
		t.Fatalf("create other account: %v", err)
	}
	server := mediaServerRecord(t, "Linked server", "https://linked.example.test", now)
	_, mediaWriter, err := db.NewMediaServerStores(pool, driver)
	if err != nil {
		t.Fatalf("NewMediaServerStores: %v", err)
	}
	if createErr := mediaWriter.CreateMediaServer(ctx, server); createErr != nil {
		t.Fatalf("create linked server: %v", createErr)
	}
	reader, writer, err := db.NewAccountMediaUserStores(pool, driver)
	if err != nil {
		t.Fatalf("NewAccountMediaUserStores: %v", err)
	}
	link := core.AccountMediaUser{
		AccountID: account.ID, MediaServerID: server.ID, MediaUserID: "media-user-1",
		Username: "linked-user", Source: core.AccountMediaUserSourceMatch, CreatedAt: now, UpdatedAt: now,
	}
	testAccountMediaUserCRUD(t, reader, writer, link, other.ID, now)
	testSuppressedLinkIsNotEnsured(t, reader, writer, accounts, account, server, now)
}

type accountMediaMatchServer struct {
	connection core.MediaServerConnection
	user       core.MediaUser
}

func (s accountMediaMatchServer) List(context.Context, string, int) ([]core.MediaServerConnection, error) {
	return []core.MediaServerConnection{s.connection}, nil
}

func (s accountMediaMatchServer) Get(_ context.Context, id string) (core.MediaServerConnection, error) {
	if id != s.connection.Server.ID {
		return core.MediaServerConnection{}, core.ErrNotFound
	}
	return s.connection, nil
}

func (s accountMediaMatchServer) FindUserByName(context.Context, string, string) (core.MediaUser, bool, bool, error) {
	return s.user, true, true, nil
}

func (s accountMediaMatchServer) FindUserByID(context.Context, string, string) (core.MediaUser, bool, bool, error) {
	return s.user, true, true, nil
}

type accountMediaMatchMetrics struct{}

func (accountMediaMatchMetrics) IncMediaUserMatch(string) {}

func testSuppressedLinkIsNotEnsured(
	t *testing.T, reader core.AccountMediaUserReader, writer core.AccountMediaUserWriter,
	accounts core.AccountStore, account core.Account, server core.MediaServerRecord, now time.Time,
) {
	t.Helper()
	service, err := accountmedia.NewService(accountmedia.Dependencies{
		Reader: reader, Writer: writer, Accounts: accounts,
		Servers: accountMediaMatchServer{
			connection: core.MediaServerConnection{Server: server.MediaServer},
			user:       core.MediaUser{ID: "media-user-2", Name: account.Username},
		},
		Clock:  testutil.NewFakeClock(now.Add(5 * time.Second)),
		Logger: slog.New(slog.DiscardHandler), Metrics: accountMediaMatchMetrics{},
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	links, err := service.EnsureLinks(t.Context(), account, server.ID)
	if err != nil || len(links) != 0 {
		t.Fatalf("EnsureLinks after suppression = %+v, %v", links, err)
	}
}

func testAccountMediaUserCRUD(
	t *testing.T, reader core.AccountMediaUserReader, writer core.AccountMediaUserWriter,
	link core.AccountMediaUser, otherAccountID string, now time.Time,
) {
	t.Helper()
	ctx := t.Context()
	created, err := writer.CreateAccountMediaUserIfAbsent(ctx, link)
	if err != nil || !created {
		t.Fatalf("CreateAccountMediaUserIfAbsent = %t, %v", created, err)
	}
	created, err = writer.CreateAccountMediaUserIfAbsent(ctx, link)
	if err != nil || created {
		t.Fatalf("duplicate CreateAccountMediaUserIfAbsent = %t, %v", created, err)
	}
	got, err := reader.GetAccountMediaUser(ctx, link.AccountID, link.MediaServerID)
	if err != nil || got.AccountID != link.AccountID || got.MediaUserID != link.MediaUserID ||
		got.Source != link.Source || got.MediaServerName != "Linked server" {
		t.Fatalf("GetAccountMediaUser = %+v, %v", got, err)
	}
	links, err := reader.ListAccountMediaUsers(ctx, link.AccountID, false, 8)
	if err != nil || len(links) != 1 || links[0].MediaUserID != link.MediaUserID {
		t.Fatalf("ListAccountMediaUsers = %+v, %v", links, err)
	}
	replacement := link
	replacement.MediaUserID, replacement.Username = "media-user-2", "renamed"
	replacement.Source, replacement.UpdatedAt = core.AccountMediaUserSourceAdmin, now.Add(time.Second)
	if err := writer.SetAccountMediaUser(ctx, replacement); err != nil {
		t.Fatalf("SetAccountMediaUser: %v", err)
	}
	conflict := link
	conflict.AccountID = otherAccountID
	conflict.MediaUserID = replacement.MediaUserID
	if err := writer.SetAccountMediaUser(ctx, conflict); !errors.Is(err, core.ErrAlreadyExists) {
		t.Fatalf("unique media user conflict = %v", err)
	}
	suppressedAt := now.Add(2 * time.Second)
	if err := writer.SuppressAccountMediaUser(ctx, link.AccountID, link.MediaServerID, suppressedAt); err != nil {
		t.Fatalf("SuppressAccountMediaUser: %v", err)
	}
	if _, err := reader.GetAccountMediaUser(ctx, link.AccountID, link.MediaServerID); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("suppressed GetAccountMediaUser = %v", err)
	}
	assertSuppressedLinkCannotRematch(t, reader, writer, replacement, suppressedAt)
	replacement.UpdatedAt = now.Add(3 * time.Second)
	if err := writer.SetAccountMediaUser(ctx, replacement); err != nil {
		t.Fatalf("SetAccountMediaUser after suppression: %v", err)
	}
	if got, err := reader.GetAccountMediaUser(ctx, link.AccountID, link.MediaServerID); err != nil || got.SuppressedAt != nil {
		t.Fatalf("relinked GetAccountMediaUser = %+v, %v", got, err)
	}
	assertConcurrentSuppressionWins(t, reader, writer, replacement, now.Add(4*time.Second))
}

func assertSuppressedLinkCannotRematch(
	t *testing.T, reader core.AccountMediaUserReader, writer core.AccountMediaUserWriter,
	link core.AccountMediaUser, suppressedAt time.Time,
) {
	t.Helper()
	created, err := writer.CreateAccountMediaUserIfAbsent(t.Context(), link)
	if err != nil || created {
		t.Fatalf("match after suppression = %t, %v", created, err)
	}
	visible, err := reader.ListAccountMediaUsers(t.Context(), link.AccountID, false, 8)
	if err != nil || len(visible) != 0 {
		t.Fatalf("visible suppressed links = %+v, %v", visible, err)
	}
	all, err := reader.ListAccountMediaUsers(t.Context(), link.AccountID, true, 8)
	if err != nil || len(all) != 1 || all[0].SuppressedAt == nil || !all[0].SuppressedAt.Equal(suppressedAt) {
		t.Fatalf("all suppressed links = %+v, %v", all, err)
	}
}

func assertConcurrentSuppressionWins(
	t *testing.T, reader core.AccountMediaUserReader, writer core.AccountMediaUserWriter,
	link core.AccountMediaUser, suppressedAt time.Time,
) {
	t.Helper()
	start := make(chan struct{})
	suppressed := make(chan error, 1)
	matched := make(chan bool, 1)
	matchErr := make(chan error, 1)
	go func() {
		<-start
		suppressed <- writer.SuppressAccountMediaUser(t.Context(), link.AccountID, link.MediaServerID, suppressedAt)
	}()
	go func() {
		<-start
		created, err := writer.CreateAccountMediaUserIfAbsent(t.Context(), link)
		matched <- created
		matchErr <- err
	}()
	close(start)
	if err := <-suppressed; err != nil {
		t.Fatalf("concurrent suppression: %v", err)
	}
	if created, err := <-matched, <-matchErr; err != nil || created {
		t.Fatalf("concurrent match = %t, %v", created, err)
	}
	all, err := reader.ListAccountMediaUsers(t.Context(), link.AccountID, true, 8)
	if err != nil || len(all) != 1 || all[0].SuppressedAt == nil {
		t.Fatalf("concurrent final links = %+v, %v", all, err)
	}
}
