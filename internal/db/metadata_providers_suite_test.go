package db_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/BonzTM/bloom/internal/config"
	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/db"
	"github.com/BonzTM/bloom/internal/metadata"
	"github.com/BonzTM/bloom/internal/secrets"
	"github.com/BonzTM/bloom/internal/testutil"
)

func runMetadataProviderEngineTests(t *testing.T, pool *sql.DB, driver config.Driver) {
	t.Helper()
	reader, writer, err := db.NewMetadataProviderStores(pool, driver)
	if err != nil {
		t.Fatalf("NewMetadataProviderStores: %v", err)
	}
	cipher, err := secrets.New([]byte("metadata-store-test-master-key"))
	if err != nil {
		t.Fatalf("new metadata cipher: %v", err)
	}
	const legacyKey = "0123456789abcdef0123456789abcdef"
	ciphertext, err := cipher.Encrypt([]byte(legacyKey), metadataCredentialContext())
	if err != nil {
		t.Fatalf("encrypt legacy metadata key: %v", err)
	}
	now := core.NormalizeTime(time.Date(2026, 9, 27, 12, 0, 0, 123456789, time.UTC))
	record := core.MetadataProviderRecord{
		Kind: core.MetadataProviderTMDB, CredentialCiphertext: ciphertext, KeyID: cipher.KeyID(),
		CreatedAt: now, UpdatedAt: now,
	}
	if err := writer.UpsertMetadataProvider(t.Context(), record); err != nil {
		t.Fatalf("seed legacy metadata key: %v", err)
	}
	t.Run("stored v3 key is not configured", func(t *testing.T) {
		testStoredV3MetadataKey(t, reader, writer, cipher, legacyKey, now)
	})
}

func testStoredV3MetadataKey(
	t *testing.T, reader core.MetadataProviderReader, writer core.MetadataProviderWriter,
	cipher *secrets.Cipher, legacyKey string, now time.Time,
) {
	t.Helper()
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	service, err := metadata.NewService(
		reader, writer, metadataEngineStates{}, cipher, metadataEngineFactory{}, testutil.NewFakeClock(now), logger,
	)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	for range 2 {
		configured, keyErr := service.HasKey(t.Context(), core.MetadataProviderTMDB)
		if keyErr != nil || configured {
			t.Fatalf("HasKey = %t, %v; want false, nil", configured, keyErr)
		}
		_, searchErr := service.Search(t.Context(), core.MetadataSearch{Query: "movie"})
		if !errors.Is(searchErr, core.ErrMetadataNotConfigured) {
			t.Fatalf("Search error = %v, want %v", searchErr, core.ErrMetadataNotConfigured)
		}
	}
	if strings.Count(logs.String(), "stored TMDB credential is not an API Read Access Token") != 1 {
		t.Fatalf("warning was not logged once: %s", logs.String())
	}
	if strings.Contains(logs.String(), legacyKey) {
		t.Fatalf("warning exposed legacy key: %s", logs.String())
	}
}

func metadataCredentialContext() secrets.Context {
	return secrets.Context{
		Purpose: "metadata-provider-api-key", RecordID: "tmdb", Kind: "tmdb",
		BaseURL: "https://api.themoviedb.org",
	}
}

type metadataEngineFactory struct{}

func (metadataEngineFactory) Probe(context.Context, core.MetadataProviderKind, string) error {
	return nil
}

func (metadataEngineFactory) New(core.MetadataProviderKind, string) (core.MetadataProvider, error) {
	return metadataEngineProvider{}, nil
}

type metadataEngineProvider struct{}

type metadataEngineStates struct{}

func (metadataEngineStates) MetadataRequestStates(
	context.Context, string, []core.MetadataTitle,
) (map[core.MetadataTitleKey]core.RequestStatus, error) {
	return map[core.MetadataTitleKey]core.RequestStatus{}, nil
}

func (metadataEngineProvider) Search(context.Context, core.MetadataSearch) ([]core.MetadataTitle, error) {
	return nil, nil
}

func (metadataEngineProvider) Movie(context.Context, string) (core.MetadataTitle, error) {
	return core.MetadataTitle{}, nil
}

func (metadataEngineProvider) Series(context.Context, string, bool) (core.MetadataSeries, error) {
	return core.MetadataSeries{}, nil
}
