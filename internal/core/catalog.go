package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	// CatalogPageSize is the fixed Jellyfin full-walk page size.
	CatalogPageSize = 200
	// CatalogUserDataBatchSize bounds one Jellyfin ids query.
	CatalogUserDataBatchSize = 100
	// MaxCatalogPagesPerLibrary bounds a single upstream full walk.
	MaxCatalogPagesPerLibrary = 100000
	// MaxCatalogGenres bounds genre fan-out on one item.
	MaxCatalogGenres = 64
	// MaxCatalogGenresJSONBytes bounds the persisted JSON representation.
	MaxCatalogGenresJSONBytes = 8192
	// MaxCatalogTextBytes bounds upstream display and type fields.
	MaxCatalogTextBytes = 500
	// MaxCatalogImageTagBytes bounds one opaque Jellyfin image tag.
	MaxCatalogImageTagBytes = 256
	// MaxCatalogCursorBytes bounds the durable sync checkpoint.
	MaxCatalogCursorBytes = 512
	// MaxCatalogErrorBytes bounds operator-visible sync errors.
	MaxCatalogErrorBytes = 512
	// MaxCatalogReadPageSize bounds item and history response pages.
	MaxCatalogReadPageSize = 100
	// MaxCatalogReadCursorBytes bounds an opaque public catalog cursor.
	MaxCatalogReadCursorBytes = 2048
	// MaxCatalogTMDBID bounds a stored TMDB movie or series ID.
	MaxCatalogTMDBID = 1_000_000_000
)

// LibraryItem is one type-agnostic media-server catalog row. TMDBID is the
// movie or series TMDB ID from the media server's provider IDs, or zero when
// it is unknown or the item is neither.
type LibraryItem struct {
	MediaServerID, ItemID, LibraryID   string
	ParentID, ItemType, Name           string
	SeriesID, SeriesName, SeasonID     string
	SeasonNumber, IndexNumber          *int32
	Runtime                            *time.Duration
	PremiereDate                       *time.Time
	ProductionYear                     *int32
	CommunityRating                    *float64
	Genres                             []string
	PrimaryImageTag                    string
	DateCreated                        *time.Time
	TMDBID                             int64
	Archived                           bool
	FirstSeenAt, LastSeenAt, UpdatedAt time.Time
}

// Valid reports whether an item fits both supported persistence engines.
func (i LibraryItem) Valid() bool {
	return ValidID(i.MediaServerID) && i.ValidUpstream() &&
		!i.FirstSeenAt.IsZero() && !i.LastSeenAt.IsZero() && !i.UpdatedAt.IsZero()
}

// ValidUpstream reports whether fields supplied by the media server are safe.
func (i LibraryItem) ValidUpstream() bool { return i.UpstreamProblem() == "" }

// UpstreamProblem names the first media-server field Bloom cannot store, or
// returns "" when every field is safe. The text is bounded and safe to log.
func (i LibraryItem) UpstreamProblem() string {
	if problem := i.idProblem(); problem != "" {
		return problem
	}
	if problem := i.textProblem(); problem != "" {
		return problem
	}
	return i.numberProblem()
}

func (i LibraryItem) idProblem() string {
	switch {
	case !ValidCatalogID(i.ItemID):
		return "item id is invalid"
	case !ValidLibraryID(i.LibraryID):
		return "library id is invalid"
	case !validCatalogOptionalID(i.ParentID):
		return "parent id is invalid"
	case !validCatalogOptionalID(i.SeriesID):
		return "series id is invalid"
	case !validCatalogOptionalID(i.SeasonID):
		return "season id is invalid"
	}
	return ""
}

func (i LibraryItem) textProblem() string {
	fields := [...]struct {
		name, value string
		limit       int
		required    bool
	}{
		{"type", i.ItemType, MaxCatalogTextBytes, true},
		{"name", i.Name, MaxCatalogTextBytes, true},
		{"series name", i.SeriesName, MaxCatalogTextBytes, false},
		{"primary image tag", i.PrimaryImageTag, MaxCatalogImageTagBytes, false},
	}
	for _, field := range fields {
		if problem := catalogTextProblem(field.name, field.value, field.limit, field.required); problem != "" {
			return problem
		}
	}
	return catalogGenresProblem(i.Genres)
}

func (i LibraryItem) numberProblem() string {
	switch {
	case i.TMDBID < 0 || i.TMDBID > MaxCatalogTMDBID:
		return "tmdb id is out of range"
	case i.TMDBID != 0 && i.ItemType != "Movie" && i.ItemType != "Series":
		return "tmdb id is set on an item that is not a movie or series"
	case i.Runtime != nil && *i.Runtime < 0:
		return "runtime is negative"
	case i.ProductionYear != nil && (*i.ProductionYear < 0 || *i.ProductionYear > 9999):
		return "production year is out of range"
	case i.CommunityRating != nil && !validCommunityRating(*i.CommunityRating):
		return "community rating is out of range"
	}
	return ""
}

func validCommunityRating(value float64) bool {
	return value >= 0 && value <= 100 && !math.IsNaN(value) && !math.IsInf(value, 0)
}

func catalogGenresProblem(genres []string) string {
	if !validCatalogGenres(genres) {
		return "genres exceed " + strconv.Itoa(MaxCatalogGenres) + " entries or " +
			strconv.Itoa(MaxCatalogGenresJSONBytes) + " bytes"
	}
	for _, genre := range genres {
		if problem := catalogTextProblem("genre", genre, MaxCatalogTextBytes, true); problem != "" {
			return problem
		}
	}
	return ""
}

// catalogTextProblem applies validCatalogTextLimit's rules and says which one failed.
func catalogTextProblem(field, value string, limit int, required bool) string {
	switch {
	case required && value == "":
		return field + " is empty"
	case len(value) > limit:
		return field + " exceeds " + strconv.Itoa(limit) + " bytes"
	case !utf8.ValidString(value):
		return field + " is not valid UTF-8"
	}
	if index := stringsIndexControl(value); index >= 0 {
		char, _ := utf8.DecodeRuneInString(value[index:])
		return fmt.Sprintf("%s contains control character %U at byte %d", field, char, index)
	}
	return ""
}

func validCatalogGenres(genres []string) bool {
	if len(genres) > MaxCatalogGenres {
		return false
	}
	encoded, err := json.Marshal(genres)
	return err == nil && len(encoded) <= MaxCatalogGenresJSONBytes
}

// ValidCatalogID reports whether an opaque upstream item id is safe to persist.
func ValidCatalogID(value string) bool { return ValidLibraryID(value) }

func validCatalogOptionalID(value string) bool { return value == "" || ValidCatalogID(value) }

func validCatalogText(value string, required bool) bool {
	return validCatalogTextLimit(value, MaxCatalogTextBytes, required)
}

func validCatalogTextLimit(value string, limit int, required bool) bool {
	return (!required || value != "") && len(value) <= limit && utf8.ValidString(value) && stringsIndexControl(value) < 0
}

// LibrarySyncState is the durable lifecycle of a catalog walk.
type LibrarySyncState string

const (
	// LibrarySyncPending waits for a worker lease.
	LibrarySyncPending LibrarySyncState = "pending"
	// LibrarySyncRunning owns a live worker lease.
	LibrarySyncRunning LibrarySyncState = "running"
	// LibrarySyncCompleted records a successful full walk.
	LibrarySyncCompleted LibrarySyncState = "completed"
	// LibrarySyncFailed records a failed full walk.
	LibrarySyncFailed LibrarySyncState = "failed"
)

// Valid reports whether the state is persisted.
func (s LibrarySyncState) Valid() bool {
	return s == LibrarySyncPending || s == LibrarySyncRunning || s == LibrarySyncCompleted || s == LibrarySyncFailed
}

// LibrarySync records resumable progress for one media server.
type LibrarySync struct {
	MediaServerID                         string
	State                                 LibrarySyncState
	Cursor                                string
	Seen, Upserted, Archived              int64
	LastError, LeaseToken                 string
	LeaseExpiresAt, StartedAt, FinishedAt *time.Time
}

// Valid reports whether a sync row is internally consistent.
func (s LibrarySync) Valid() bool {
	if !ValidID(s.MediaServerID) || !s.State.Valid() || !validCatalogTextLimit(s.Cursor, MaxCatalogCursorBytes, false) ||
		!validCatalogTextLimit(s.LastError, MaxCatalogErrorBytes, false) || s.Seen < 0 || s.Upserted < 0 ||
		s.Archived < 0 || s.Upserted > s.Seen {
		return false
	}
	if s.State == LibrarySyncRunning {
		return s.StartedAt != nil && s.FinishedAt == nil && s.LeaseToken != "" && s.LeaseExpiresAt != nil
	}
	return s.LeaseToken == "" && s.LeaseExpiresAt == nil
}

// LibrarySyncLease grants temporary ownership of a catalog walk.
type LibrarySyncLease struct {
	Token     string
	ExpiresAt time.Time
}

// LibraryCatalogPage is one bounded upstream page.
type LibraryCatalogPage struct {
	Items []LibraryItem
	// Rejected holds the items on this page Bloom could not store. They still
	// occupy upstream positions, so the walk advances past them.
	Rejected          []LibraryItemRejection
	StartIndex, Total int
}

// Consumed returns how many upstream positions the page covers.
func (p LibraryCatalogPage) Consumed() int { return len(p.Items) + len(p.Rejected) }

// MaxCatalogRejectionTextBytes bounds each identifying field of a rejection.
const MaxCatalogRejectionTextBytes = 200

// LibraryItemRejection identifies one upstream catalog item Bloom skipped and
// says why, so an operator can find and fix it on the media server.
type LibraryItemRejection struct {
	ItemID, ItemType, Name, SeriesName, Reason string
}

// RejectLibraryItem builds a bounded rejection from whatever the media server
// sent. The identifying fields may themselves be the problem, so each is cut
// to MaxCatalogRejectionTextBytes and made valid UTF-8.
func RejectLibraryItem(item LibraryItem, reason string) LibraryItemRejection {
	return LibraryItemRejection{
		ItemID: boundedRejectionText(item.ItemID), ItemType: boundedRejectionText(item.ItemType),
		Name: boundedRejectionText(item.Name), SeriesName: boundedRejectionText(item.SeriesName),
		Reason: boundedRejectionText(reason),
	}
}

func boundedRejectionText(value string) string {
	if len(value) > MaxCatalogRejectionTextBytes {
		value = value[:MaxCatalogRejectionTextBytes]
	}
	return strings.ToValidUTF8(value, "\uFFFD")
}

// LibraryUserData is per-user play state for one catalog item.
type LibraryUserData struct {
	ItemID           string
	PlayCount        int32
	LastPlayedAt     *time.Time
	PlaybackPosition time.Duration
}

// CatalogImportItem carries catalog fields needed by the user-data source.
type CatalogImportItem struct {
	ItemID, ItemName, ItemType, SeriesID, SeriesName string
	LibraryID                                        string
	SeasonNumber, IndexNumber                        *int32
	Runtime                                          *time.Duration
}

// CatalogUser is one bounded media-server user snapshot used by an import.
type CatalogUser struct{ MediaUserID, Username string }

// CatalogItemPosition is the decoded keyset position for an item page.
type CatalogItemPosition struct {
	TextValue   string
	TimeValue   *time.Time
	NumberValue int64
	ItemID      string
}

// CatalogSort selects a stable item-list order.
type CatalogSort string

const (
	// CatalogSortName sorts by case-folded item name.
	CatalogSortName CatalogSort = "name"
	// CatalogSortDateAdded sorts by Jellyfin creation date.
	CatalogSortDateAdded CatalogSort = "date_added"
	// CatalogSortPremiereDate sorts by premiere date.
	CatalogSortPremiereDate CatalogSort = "premiere_date"
	// CatalogSortPlays sorts by recorded play count.
	CatalogSortPlays CatalogSort = "plays"
	// CatalogSortWatchTime sorts by recorded watch time.
	CatalogSortWatchTime CatalogSort = "watch_time"
	// CatalogSortLastPlayed sorts by the latest recorded play.
	CatalogSortLastPlayed CatalogSort = "last_played"
)

// Valid reports whether the item sort is supported.
func (s CatalogSort) Valid() bool {
	return s == CatalogSortName || s == CatalogSortDateAdded || s == CatalogSortPremiereDate ||
		s == CatalogSortPlays || s == CatalogSortWatchTime || s == CatalogSortLastPlayed
}

// SortOrder selects ascending or descending order.
type SortOrder string

const (
	// SortAscending selects ascending order.
	SortAscending SortOrder = "asc"
	// SortDescending selects descending order.
	SortDescending SortOrder = "desc"
)

// Valid reports whether the order is supported.
func (o SortOrder) Valid() bool { return o == SortAscending || o == SortDescending }

// CatalogWindow optionally constrains watch aggregates.
type CatalogWindow struct {
	Start, End time.Time
	Enabled    bool
}

// Valid reports whether the optional window is well formed.
func (w CatalogWindow) Valid() bool { return !w.Enabled || (!w.Start.IsZero() && w.End.After(w.Start)) }

// CatalogItemQuery describes one bounded library item page.
type CatalogItemQuery struct {
	MediaServerID, LibraryID, ItemType string
	Sort                               CatalogSort
	Order                              SortOrder
	Archived                           *bool
	Limit                              int
	After                              *CatalogItemPosition
	Window                             CatalogWindow
}

// Valid reports whether the list query is bounded and safe.
func (q CatalogItemQuery) Valid() bool {
	return ValidID(q.MediaServerID) && ValidLibraryID(q.LibraryID) &&
		(q.ItemType == "" || validCatalogText(q.ItemType, true)) && q.Sort.Valid() && q.Order.Valid() &&
		q.Limit >= 1 && q.Limit <= MaxCatalogReadPageSize+1 && validCatalogItemPosition(q.Sort, q.After) && q.Window.Valid()
}

func validCatalogItemPosition(sort CatalogSort, value *CatalogItemPosition) bool {
	if value == nil {
		return true
	}
	if !ValidCatalogID(value.ItemID) || value.NumberValue < 0 ||
		(value.TimeValue != nil && value.TimeValue.IsZero()) {
		return false
	}
	return sort != CatalogSortName || validCatalogText(value.TextValue, true)
}

// CatalogItemStats decorates an item with play aggregates.
type CatalogItemStats struct {
	Item                             LibraryItem
	Plays, WatchSeconds, UniqueUsers int64
	FirstPlayedAt, LastPlayedAt      *time.Time
	// PagePosition retains the indexed all-time sort key when a window changes displayed aggregates.
	PagePosition CatalogItemPosition
}

// CatalogTypeCount is one per-library type count and play summary.
type CatalogTypeCount struct {
	ItemType                   string
	Items, Plays, WatchSeconds int64
}

// CatalogLibrarySummary contains active catalog totals for one library.
type CatalogLibrarySummary struct {
	LibraryID string
	Types     []CatalogTypeCount
}

// CatalogChildSummary describes direct or linked descendants by type.
type CatalogChildSummary struct {
	ItemType string
	Items    int64
}

// CatalogItemDetail combines an item, descendants, and its play summary.
type CatalogItemDetail struct {
	Item     CatalogItemStats
	Children []CatalogChildSummary
}

// CatalogGenreSummary contains active item and watch totals for one genre.
type CatalogGenreSummary struct {
	Genre                      string
	Items, Plays, WatchSeconds int64
}

// CatalogHistoryQuery describes one item-or-descendant watch page.
type CatalogHistoryQuery struct {
	MediaServerID, ItemID string
	Limit                 int
	After                 *CatalogHistoryPosition
}

// CatalogHistoryPosition is the decoded keyset position for a history page.
type CatalogHistoryPosition struct {
	StartedAt time.Time
	ID        string
}

// Valid reports whether the history query is bounded.
func (q CatalogHistoryQuery) Valid() bool {
	return ValidID(q.MediaServerID) && ValidCatalogID(q.ItemID) && q.Limit >= 1 &&
		q.Limit <= MaxCatalogReadPageSize+1 && (q.After == nil || !q.After.StartedAt.IsZero() && ValidID(q.After.ID))
}

// CatalogStaleQuery describes one bounded stale-media page.
type CatalogStaleQuery struct {
	MediaServerID, LibraryID string
	Before                   time.Time
	Limit                    int
	After                    *CatalogStalePosition
}

// CatalogStalePosition is the decoded keyset position for a stale page.
type CatalogStalePosition struct {
	LastPlayedAt *time.Time
	Name, ItemID string
}

// Valid reports whether the stale query is bounded.
func (q CatalogStaleQuery) Valid() bool {
	return ValidID(q.MediaServerID) && ValidLibraryID(q.LibraryID) && !q.Before.IsZero() &&
		q.Limit >= 1 && q.Limit <= MaxCatalogReadPageSize+1 && validCatalogStalePosition(q.After)
}

func validCatalogStalePosition(value *CatalogStalePosition) bool {
	return value == nil || validCatalogText(value.Name, true) && ValidCatalogID(value.ItemID) &&
		(value.LastPlayedAt == nil || !value.LastPlayedAt.IsZero())
}

// LibraryCatalogSyncer is the optional adapter capability used by full sync and user data import.
type LibraryCatalogSyncer interface {
	CatalogItems(context.Context, string, int, int) (LibraryCatalogPage, error)
	CatalogItemIDs(context.Context, []string) ([]string, error)
	CatalogUserData(context.Context, string, []string) ([]LibraryUserData, error)
}

// LibraryCatalogStore owns durable sync mutation and catalog reads.
type LibraryCatalogStore interface {
	RequestLibrarySync(context.Context, string) (LibrarySync, error)
	ClaimLibrarySync(context.Context, LibrarySyncLease, time.Time, time.Time) (LibrarySync, error)
	CommitLibrarySyncPage(context.Context, LibrarySync, []LibraryItem, string, time.Time) (LibrarySync, error)
	ListLibrarySyncMissingIDs(context.Context, LibrarySync, string, int) ([]string, error)
	CommitLibrarySyncArchives(context.Context, LibrarySync, []string, string, time.Time) (LibrarySync, error)
	FinishLibrarySync(context.Context, LibrarySync, time.Time) (LibrarySync, error)
	FailLibrarySync(context.Context, LibrarySync, string, time.Time) error
	ListCatalogLibraries(context.Context, string, CatalogWindow) ([]CatalogLibrarySummary, error)
	ListCatalogItems(context.Context, CatalogItemQuery) ([]CatalogItemStats, error)
	GetCatalogItem(context.Context, string, string) (CatalogItemDetail, error)
	ListCatalogHistory(context.Context, CatalogHistoryQuery) ([]PlaybackWatch, error)
	ListRecentCatalogItems(context.Context, string, string, int) ([]CatalogItemStats, error)
	ListCatalogGenres(context.Context, string, string, CatalogWindow) ([]CatalogGenreSummary, error)
	ListStaleCatalogItems(context.Context, CatalogStaleQuery) ([]CatalogItemStats, error)
	ListCatalogImportItems(context.Context, string, string, int) ([]CatalogImportItem, error)
}

var (
	// ErrLibrarySyncInProgress rejects overlapping scheduled and manual walks.
	ErrLibrarySyncInProgress = errors.New("library sync in progress")
	// ErrLibrarySyncLeaseLost reports ownership loss between pages.
	ErrLibrarySyncLeaseLost = errors.New("library sync lease lost")
)
