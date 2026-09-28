package db

import (
	"errors"
	"slices"
	"testing"

	"github.com/BonzTM/bloom/internal/core"
)

func TestImportRowsErrorPreservesDatabaseErrorClassification(t *testing.T) {
	databaseErr := errors.New("database failure")
	err := importRowsError("claim import", 0, databaseErr, core.ErrNotFound)
	if !errors.Is(err, databaseErr) || !errors.Is(err, core.ErrImportStore) ||
		errors.Is(err, core.ErrNotFound) {
		t.Fatalf("database error classification = %v", err)
	}
	err = importRowsError("claim import", 0, nil, core.ErrNotFound)
	if !errors.Is(err, core.ErrNotFound) || errors.Is(err, core.ErrImportStore) {
		t.Fatalf("zero-row classification = %v", err)
	}
}

func TestPostgresImportLockKeysShareCrossSourceActivityKey(t *testing.T) {
	serverID := "11111111-1111-4111-8111-111111111111"
	reporting := core.ImportBatch{
		MediaServerID: serverID, Source: core.ImportSourcePlaybackReporting,
		Records: []core.ImportedWatch{{RecordID: "77", MediaUserID: "reporting-user", ItemID: "reporting-item"}},
	}
	jellystat := core.ImportBatch{
		MediaServerID: serverID, Source: core.ImportSourceJellystat,
		Records: []core.ImportedWatch{{
			RecordID: "jellystat-activity", OriginRecordID: "77",
			MediaUserID: "jellystat-user", ItemID: "jellystat-item",
		}},
	}
	reportingKeys := postgresImportLockKeys(reporting)
	jellystatKeys := postgresImportLockKeys(jellystat)
	shared := 0
	for _, key := range reportingKeys {
		if slices.Contains(jellystatKeys, key) {
			shared++
		}
	}
	if shared != 1 || !slices.IsSorted(reportingKeys) || !slices.IsSorted(jellystatKeys) {
		t.Fatalf("lock keys = %v and %v, shared = %d", reportingKeys, jellystatKeys, shared)
	}
}
