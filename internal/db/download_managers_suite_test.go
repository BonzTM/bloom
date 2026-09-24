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
	t.Run("binary name ordering and pagination", func(t *testing.T) {
		testDownloadManagerBinaryPagination(t, reader, writer, now)
	})
	t.Run("round trip", func(t *testing.T) {
		testDownloadManagerRoundTrip(t, reader, writer, now)
	})
	t.Run("referenced manager", func(t *testing.T) {
		testReferencedDownloadManager(t, pool, driver, writer, now)
	})
}

func testDownloadManagerRoundTrip(
	t *testing.T, reader core.DownloadManagerReader, writer core.DownloadManagerWriter, now time.Time,
) {
	t.Helper()
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
}

func testReferencedDownloadManager(
	t *testing.T, pool *sql.DB, driver config.Driver, writer core.DownloadManagerWriter, now time.Time,
) {
	t.Helper()
	record := core.DownloadManagerRecord{DownloadManager: core.DownloadManager{
		ID: mustID(t), Kind: core.DownloadManagerKindRadarr, Name: "Profile Radarr",
		BaseURL: "https://radarr.example", CreatedAt: now, UpdatedAt: now,
	}, CredentialCiphertext: []byte("ciphertext"), KeyID: "key-1"}
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

func testDownloadManagerBinaryPagination(
	t *testing.T, reader core.DownloadManagerReader, writer core.DownloadManagerWriter, now time.Time,
) {
	t.Helper()
	names := []string{"a", "B", "c"}
	ids := make([]string, 0, len(names))
	for _, name := range names {
		record := core.DownloadManagerRecord{DownloadManager: core.DownloadManager{
			ID: mustID(t), Kind: core.DownloadManagerKindRadarr, Name: name,
			BaseURL: "https://pagination.example", CreatedAt: now, UpdatedAt: now,
		}, CredentialCiphertext: []byte("ciphertext"), KeyID: "key-1"}
		if err := writer.CreateDownloadManager(t.Context(), record); err != nil {
			t.Fatalf("create %q: %v", name, err)
		}
		ids = append(ids, record.ID)
	}
	first, err := reader.ListDownloadManagers(t.Context(), "", 2)
	if err != nil || len(first) != 2 || first[0].Name != "a" || first[1].Name != "B" {
		t.Fatalf("first binary page = %+v, %v", first, err)
	}
	second, err := reader.ListDownloadManagers(t.Context(), core.MediaServerNameKey(first[1].Name), 2)
	if err != nil || len(second) != 1 || second[0].Name != "c" {
		t.Fatalf("second binary page = %+v, %v", second, err)
	}
	for _, id := range ids {
		if err := writer.DeleteDownloadManager(t.Context(), id); err != nil {
			t.Fatalf("delete pagination fixture: %v", err)
		}
	}
}
