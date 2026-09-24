package db_test

import (
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/BonzTM/bloom/internal/config"
	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/db"
)

func runDownloadManagerEngineTests(t *testing.T, pool *sql.DB, driver config.Driver) {
	t.Helper()
	reader, writer, setupErr := db.NewDownloadManagerStores(pool, driver)
	if setupErr != nil {
		t.Fatalf("NewDownloadManagerStores: %v", setupErr)
	}
	now := core.NormalizeTime(time.Date(2026, 9, 23, 20, 0, 0, 123456789, time.UTC))
	record := core.DownloadManagerRecord{DownloadManager: core.DownloadManager{
		ID: mustID(t), Kind: core.DownloadManagerKindRadarr, Name: "Main Radarr",
		BaseURL: "https://radarr.example", CreatedAt: now, UpdatedAt: now,
	}, CredentialCiphertext: []byte("ciphertext"), KeyID: "key-1"}
	if err := writer.CreateDownloadManager(t.Context(), record); err != nil {
		t.Fatalf("CreateDownloadManager: %v", err)
	}
	loaded, loadErr := reader.GetDownloadManagerByName(t.Context(), "main radarr")
	if loadErr != nil || loaded.ID != record.ID || string(loaded.CredentialCiphertext) != "ciphertext" {
		t.Fatalf("GetDownloadManagerByName = %+v, %v", loaded, loadErr)
	}
	listed, listErr := reader.ListDownloadManagers(t.Context(), "", 10)
	if listErr != nil || len(listed) != 1 || listed[0].Name != record.Name {
		t.Fatalf("ListDownloadManagers = %+v, %v", listed, listErr)
	}
	if err := writer.CreateDownloadManager(t.Context(), record); !errors.Is(err, core.ErrAlreadyExists) {
		t.Fatalf("duplicate registration error = %v", err)
	}
	if err := writer.DeleteDownloadManager(t.Context(), record.ID); err != nil {
		t.Fatalf("DeleteDownloadManager: %v", err)
	}
	if _, err := reader.GetDownloadManager(t.Context(), record.ID); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("deleted manager error = %v", err)
	}

	record.ID, record.Name = mustID(t), "Profile Radarr"
	if err := writer.CreateDownloadManager(t.Context(), record); err != nil {
		t.Fatalf("create referenced manager: %v", err)
	}
	_, profileWriter, profileStoreErr := db.NewRequestProfileStores(pool, driver)
	if profileStoreErr != nil {
		t.Fatalf("NewRequestProfileStores: %v", profileStoreErr)
	}
	profile := core.RequestProfile{
		ID: mustID(t), Name: "Manager reference", Kinds: []core.MediaKind{core.MediaKindMovie},
		DownloadManagerKind: "radarr", DownloadManagerInstance: record.Name,
		QualityProfile: "1", RootFolder: "/movies", CreatedAt: now, UpdatedAt: now,
	}
	if err := profileWriter.CreateRequestProfile(t.Context(), profile); err != nil {
		t.Fatalf("create manager profile: %v", err)
	}
	if err := writer.DeleteDownloadManager(t.Context(), record.ID); !errors.Is(err, core.ErrDownloadManagerInUse) {
		t.Fatalf("referenced manager deletion error = %v", err)
	}
	if err := profileWriter.DeleteRequestProfile(t.Context(), profile.ID); err != nil {
		t.Fatalf("delete manager profile: %v", err)
	}
	if err := writer.DeleteDownloadManager(t.Context(), record.ID); err != nil {
		t.Fatalf("delete unreferenced manager: %v", err)
	}
}
