package db

import (
	"errors"
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
