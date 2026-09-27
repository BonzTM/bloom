package db

import (
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/BonzTM/bloom/internal/config"
	"github.com/BonzTM/bloom/internal/core"
)

const maxImportPageSize = 501

// NewImportStore returns the import persistence seam for the configured engine.
func NewImportStore(pool *sql.DB, driver config.Driver) (core.ImportStore, error) {
	switch driver {
	case config.DriverSQLite:
		return newSQLiteImportStore(pool), nil
	case config.DriverPostgres:
		return newPostgresImportStore(pool), nil
	default:
		return nil, fmt.Errorf("unsupported database driver %q", driver)
	}
}

func validateImportList(query core.ImportListQuery) error {
	if query.PageSize < 1 || query.PageSize > maxImportPageSize {
		return core.ErrInvalidArgument
	}
	if query.MediaServerID != "" && !core.ValidID(query.MediaServerID) {
		return core.ErrInvalidArgument
	}
	if query.Before != nil && (query.Before.CreatedAt.IsZero() || !core.ValidID(query.Before.ID)) {
		return core.ErrInvalidArgument
	}
	return nil
}

func validateUploadChunks(chunks []core.ImportUploadChunk) error {
	if len(chunks) < 1 || len(chunks) > core.MaxImportUploadWriteChunks {
		return core.ErrInvalidArgument
	}
	for _, chunk := range chunks {
		if !core.ValidID(chunk.ID) || chunk.Index < 0 || len(chunk.Bytes) > core.ImportUploadChunkBytes ||
			chunk.CreatedAt.IsZero() {
			return core.ErrInvalidArgument
		}
	}
	return nil
}

func validateImportBatch(batch core.ImportBatch) error {
	if !core.ValidID(batch.JobID) || !core.ValidID(batch.MediaServerID) || batch.LeaseToken == "" ||
		!batch.Source.Valid() || len(batch.Cursor) > core.MaxImportCursorBytes ||
		len(batch.Records) > core.ImportBatchSize || batch.Skipped < 0 || batch.ResumeWindow <= 0 ||
		batch.Now.IsZero() || !batch.LeaseExpiresAt.After(batch.Now) {
		return core.ErrInvalidArgument
	}
	for _, record := range batch.Records {
		if !record.Valid() {
			return core.ErrInvalidArgument
		}
	}
	return nil
}

func importStoreError(operation string, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, core.ErrNotFound) || errors.Is(err, core.ErrImportLeaseLost) ||
		errors.Is(err, core.ErrImportInProgress) || errors.Is(err, core.ErrInvalidArgument) {
		return fmt.Errorf("%s: %w", operation, err)
	}
	return fmt.Errorf("%s: %w", operation, errors.Join(core.ErrImportStore, err))
}

func importRowsError(operation string, rows int64, queryErr, zeroRowsErr error) error {
	if queryErr != nil {
		return importStoreError(operation, queryErr)
	}
	if rows != 1 {
		return zeroRowsErr
	}
	return nil
}

func watchDedupKey(serverID, userID, itemID string) string {
	var key strings.Builder
	for _, value := range []string{serverID, userID, itemID} {
		key.WriteString(strconv.Itoa(len(value)))
		key.WriteByte(':')
		key.WriteString(value)
	}
	return key.String()
}

func importEndedAt(record core.ImportedWatch) time.Time {
	if record.EndedAt != nil {
		return core.NormalizeTime(*record.EndedAt)
	}
	return core.NormalizeTime(record.StartedAt.Add(record.Duration))
}

func importNotFound(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return core.ErrNotFound
	}
	return err
}

func importListCursor(query core.ImportListQuery) (time.Time, string) {
	if query.Before != nil {
		return core.NormalizeTime(query.Before.CreatedAt), query.Before.ID
	}
	return time.Date(9999, 12, 31, 23, 59, 59, 999999000, time.UTC), "\uffff"
}

func rollbackImport(tx *sql.Tx, result *error) {
	if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
		*result = errors.Join(*result, importStoreError("rollback import transaction", err))
	}
}
