package core

import (
	"context"
	"errors"
	"time"
	"unicode/utf8"
)

const (
	// ImportBatchSize bounds source reads and atomic database writes.
	ImportBatchSize = 500
	// MaxImportCursorBytes bounds durable source checkpoints.
	MaxImportCursorBytes = 512
	// MaxImportRecordIDBytes bounds source-owned idempotency keys.
	MaxImportRecordIDBytes = 256
	// MaxImportErrorBytes bounds operator-visible terminal failure text.
	MaxImportErrorBytes = 512
	// MaxImportUploadBytes bounds one Bloom export upload.
	MaxImportUploadBytes = 256 << 20
	// ImportUploadChunkBytes is the fixed database chunk size.
	ImportUploadChunkBytes = 1 << 20
	// MaxImportUploadWriteChunks bounds one upload transaction.
	MaxImportUploadWriteChunks = 8
	// MaxOrphanImportUploadChunks bounds one worker cleanup transaction.
	MaxOrphanImportUploadChunks = 256
)

// ImportSource identifies a supported historical-watch source.
type ImportSource string

const (
	// ImportSourcePlaybackReporting reads the Jellyfin Playback Reporting plugin.
	ImportSourcePlaybackReporting ImportSource = "playback_reporting"
	// ImportSourceBloomExport reads Bloom's JSONL watch export.
	ImportSourceBloomExport ImportSource = "bloom_export"
)

// Valid reports whether the source is implemented by this slice.
func (s ImportSource) Valid() bool {
	return s == ImportSourcePlaybackReporting || s == ImportSourceBloomExport
}

// ImportState is the durable lifecycle state of one import job.
type ImportState string

const (
	// ImportPending is waiting for a worker lease.
	ImportPending ImportState = "pending"
	// ImportRunning has an active or recoverable worker lease.
	ImportRunning ImportState = "running"
	// ImportCompleted finished without a terminal source error.
	ImportCompleted ImportState = "completed"
	// ImportFailed stopped after a terminal source or persistence error.
	ImportFailed ImportState = "failed"
	// ImportCancelled was cancelled by an administrator.
	ImportCancelled ImportState = "cancelled"
)

// Terminal reports whether no worker may claim the job again.
func (s ImportState) Terminal() bool {
	return s == ImportCompleted || s == ImportFailed || s == ImportCancelled
}

// Valid reports whether the state belongs to the persisted set.
func (s ImportState) Valid() bool {
	return s == ImportPending || s == ImportRunning || s.Terminal()
}

// ImportJob records resumable progress through one source.
type ImportJob struct {
	ID, MediaServerID, RequestedBy string
	Source                         ImportSource
	State                          ImportState
	Cursor                         string
	Read, Imported, Skipped        int64
	Duplicate                      int64
	LastError, LeaseToken          string
	LeaseExpiresAt                 *time.Time
	CreatedAt, UpdatedAt           time.Time
	StartedAt, FinishedAt          *time.Time
}

// Valid reports whether a job can safely cross the persistence boundary.
func (j ImportJob) Valid() bool {
	if !ValidID(j.ID) || !ValidID(j.MediaServerID) || !ValidID(j.RequestedBy) ||
		!j.Source.Valid() || !j.State.Valid() || !validImportText(j.Cursor, MaxImportCursorBytes) ||
		!validImportText(j.LastError, MaxImportErrorBytes) || min(j.Read, j.Imported, j.Skipped, j.Duplicate) < 0 ||
		!validImportCounters(j) || j.CreatedAt.IsZero() || j.UpdatedAt.IsZero() {
		return false
	}
	if j.State == ImportPending && (j.StartedAt != nil || j.FinishedAt != nil ||
		j.LeaseToken != "" || j.LeaseExpiresAt != nil) {
		return false
	}
	if j.State == ImportRunning && (j.StartedAt == nil || j.FinishedAt != nil ||
		!validImportText(j.LeaseToken, 128) || j.LeaseToken == "" || j.LeaseExpiresAt == nil) {
		return false
	}
	return !j.State.Terminal() || (j.FinishedAt != nil && j.LeaseToken == "" && j.LeaseExpiresAt == nil)
}

func validImportCounters(job ImportJob) bool {
	if job.Imported > job.Read || job.Skipped > job.Read-job.Imported {
		return false
	}
	return job.Duplicate == job.Read-job.Imported-job.Skipped
}

// ImportCursor is the newest-first list position.
type ImportCursor struct {
	CreatedAt time.Time
	ID        string
}

// ImportListQuery describes one bounded job page.
type ImportListQuery struct {
	Before        *ImportCursor
	MediaServerID string
	PageSize      int
}

// ImportUploadChunk is one bounded piece of a database-staged upload.
type ImportUploadChunk struct {
	ID        string
	Index     int64
	Bytes     []byte
	CreatedAt time.Time
}

// ImportUploadInfo describes one complete database-staged upload.
type ImportUploadInfo struct {
	ID         string
	Size       int64
	ChunkCount int64
}

// ImportLease grants one worker temporary ownership of a job.
type ImportLease struct {
	Token     string
	ExpiresAt time.Time
}

// ImportedWatch is a source record normalized for atomic persistence.
type ImportedWatch struct {
	RecordID, MediaUserID, Username string
	DeviceID, DeviceName, Client    string
	ItemID, ItemName, ItemType      string
	SeriesName                      string
	LibraryID, LibraryName          string
	SeasonNumber, EpisodeNumber     *int32
	PlayMethod                      PlayMethod
	Stream                          *StreamDetails
	StartedAt                       time.Time
	EndedAt                         *time.Time
	Runtime                         *time.Duration
	Duration, LastPosition          time.Duration
}

// Valid reports whether the normalized source record fits the watch schema.
func (w ImportedWatch) Valid() bool {
	if !validImportText(w.RecordID, MaxImportRecordIDBytes) || w.RecordID == "" ||
		!validImportIdentity(w.MediaUserID) || !validImportIdentity(w.ItemID) ||
		!validImportText(w.DeviceID, 256) || !validImportValue(w.Username) ||
		!validImportValue(w.DeviceName) || !validImportValue(w.Client) ||
		!validImportValue(w.ItemName) || !validImportValue(w.ItemType) ||
		!validImportValue(w.SeriesName) || !w.PlayMethod.Valid() ||
		w.StartedAt.IsZero() || w.Duration < 0 || w.LastPosition < 0 ||
		(w.Runtime != nil && *w.Runtime < 0) {
		return false
	}
	if (w.LibraryID == "") != (w.LibraryName == "") ||
		(w.LibraryID != "" && !(Library{ID: w.LibraryID, Name: w.LibraryName}).Valid()) {
		return false
	}
	if w.EndedAt != nil && w.EndedAt.Before(w.StartedAt) {
		return false
	}
	return w.Stream == nil || w.Stream.Valid()
}

func validImportIdentity(value string) bool {
	return value != "" && len(value) <= 256 && validImportValue(value)
}

func validImportValue(value string) bool {
	return len(value) <= 500 && utf8.ValidString(value) && stringsIndexControl(value) < 0
}

func validImportText(value string, limit int) bool {
	return len(value) <= limit && utf8.ValidString(value) && stringsIndexControl(value) < 0
}

// ImportBatch is one atomic watch-and-checkpoint commit.
type ImportBatch struct {
	JobID, LeaseToken, Cursor string
	Source                    ImportSource
	MediaServerID             string
	Records                   []ImportedWatch
	Skipped                   int64
	ResumeWindow              time.Duration
	Now, LeaseExpiresAt       time.Time
}

// ImportBatchResult contains the cumulative durable counters.
type ImportBatchResult struct {
	Read, Imported, Skipped, Duplicate int64
}

// ImportStore is the durable job, lease, and atomic batch seam.
type ImportStore interface {
	CreateImport(context.Context, ImportJob) error
	CreateUploadedImport(context.Context, ImportJob, string) error
	ListImports(context.Context, ImportListQuery) ([]ImportJob, error)
	GetImport(context.Context, string) (ImportJob, error)
	CancelImport(context.Context, string, time.Time) (ImportJob, error)
	ClaimImport(context.Context, ImportLease, time.Time) (ImportJob, error)
	RenewImportLease(context.Context, string, ImportLease, time.Time) error
	CommitImportBatch(context.Context, ImportBatch) (ImportBatchResult, error)
	FinishImport(context.Context, string, string, ImportState, string, time.Time) error
	WriteImportUploadChunks(context.Context, []ImportUploadChunk) error
	ImportUploadInfo(context.Context, string) (ImportUploadInfo, error)
	ReadImportUploadChunk(context.Context, string, int64) ([]byte, error)
	DeleteImportUpload(context.Context, string) error
	DeleteOrphanImportUploads(context.Context, time.Time, int) (int64, error)
}

var (
	// ErrImportInProgress rejects a second active job for one server and source.
	ErrImportInProgress = errors.New("import in progress")
	// ErrImportLeaseLost reports cancellation or ownership loss between batches.
	ErrImportLeaseLost = errors.New("import lease lost")
	// ErrImportPluginMissing reports a terminal Playback Reporting absence.
	ErrImportPluginMissing = errors.New("playback reporting plugin is not installed")
	// ErrImportStore classifies persistence failures that workers should retry.
	ErrImportStore = errors.New("import store failure")
	// ErrImportRecordCountMismatch reports a truncated or inconsistent Bloom export.
	ErrImportRecordCountMismatch = errors.New("bloom export record count mismatch")
)

// SafeImportError returns bounded operator text without source bodies or secrets.
func SafeImportError(err error) string {
	switch {
	case errors.Is(err, ErrImportPluginMissing):
		return "Playback Reporting plugin is not installed"
	case errors.Is(err, ErrInvalidArgument):
		return "source data is invalid"
	case errors.Is(err, ErrImportRecordCountMismatch):
		return "Bloom export watch record count does not match summary"
	default:
		return "import failed; see server logs"
	}
}

// PlaybackReportingPage carries one bounded plugin page and its source cursor.
type PlaybackReportingPage struct {
	Records []ImportedWatch
	Cursor  int64
	Skipped int64
}

// PlaybackReportingReader reads one bounded plugin page.
type PlaybackReportingReader interface {
	PlaybackReporting(context.Context, int64, int) (PlaybackReportingPage, error)
}

// ImportSourceReader reads one normalized page and returns its next cursor.
type ImportSourceReader interface {
	ReadImportBatch(context.Context, ImportJob) ([]ImportedWatch, string, int64, error)
}
