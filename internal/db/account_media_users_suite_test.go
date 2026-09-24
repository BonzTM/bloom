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
	links, err := reader.ListAccountMediaUsers(ctx, link.AccountID, 8)
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
	if err := writer.DeleteAccountMediaUser(ctx, link.AccountID, link.MediaServerID); err != nil {
		t.Fatalf("DeleteAccountMediaUser: %v", err)
	}
	if _, err := reader.GetAccountMediaUser(ctx, link.AccountID, link.MediaServerID); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("deleted GetAccountMediaUser = %v", err)
	}
}
