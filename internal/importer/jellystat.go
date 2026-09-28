package importer

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/BonzTM/bloom/internal/core"
)

const (
	maxJellystatLookupEntries = 250_000
	maxJellystatLookupBytes   = 64 << 20
	// maxJellystatRows supports backups with up to one million table markers
	// and rows independently of the upload byte limit.
	maxJellystatRows = 1_000_000
)

type jellystatLine struct {
	Type  string          `json:"type"`
	Table string          `json:"table"`
	Data  json.RawMessage `json:"data,omitempty"`
}

type jellystatItem struct {
	Name    string
	Type    string
	Runtime *time.Duration
}

type jellystatEpisode struct {
	Name, SeriesID, SeasonID, SeriesName, SeasonName string
	SeasonNumber, EpisodeNumber                      *int32
	Runtime                                          *time.Duration
}

type jellystatLookups struct {
	users       map[string]string
	items       map[string]jellystatItem
	episodes    map[string]jellystatEpisode
	userRows    map[string]string
	itemRows    map[string]string
	episodeRows map[string]string
	entries     int
	bytes       int
}

type jellystatReader struct {
	staging       *Staging
	store         core.ImportStore
	clock         core.Clock
	leaseDuration time.Duration
	storeTimeout  time.Duration
	uploadID      string
	offset        int64
	stream        io.ReadCloser
	buffered      *bufio.Reader
	lookups       jellystatLookups
}

func (r *jellystatReader) ReadImportBatch(
	ctx context.Context, job core.ImportJob,
) ([]core.ImportedWatch, string, int64, error) {
	cursor, err := decodeFileCursor(job.Cursor)
	if err != nil {
		return nil, "", 0, err
	}
	if r.stream == nil {
		if openErr := r.open(ctx, job, cursor); openErr != nil {
			return nil, "", 0, openErr
		}
	} else if cursor.ID != r.uploadID || cursor.Offset != r.offset {
		return nil, "", 0, fmt.Errorf("validate Jellystat cursor: %w", core.ErrInvalidArgument)
	}
	records, offset, skipped, err := readJellystatLines(ctx, r.buffered, r.offset, r.lookups)
	if err != nil {
		return nil, "", 0, err
	}
	r.offset = offset
	next, err := encodeFileCursor(fileCursor{ID: cursor.ID, Offset: offset})
	return records, next, skipped, err
}

func (r *jellystatReader) open(ctx context.Context, job core.ImportJob, cursor fileCursor) error {
	upload, err := r.staging.openWithTimeout(ctx, cursor.ID, r.storeTimeout)
	if err != nil {
		return fmt.Errorf("open Jellystat upload: %w", err)
	}
	if cursor.Offset > upload.Size() {
		return fmt.Errorf("seek Jellystat upload: %w", core.ErrInvalidArgument)
	}
	lookups, err := loadJellystatLookups(ctx, upload, r.lookupLeaseRenewer(job))
	if err != nil {
		return err
	}
	r.uploadID, r.offset, r.lookups = cursor.ID, cursor.Offset, lookups
	r.stream = io.NopCloser(io.NewSectionReader(upload, cursor.Offset, upload.Size()-cursor.Offset))
	r.buffered = bufio.NewReaderSize(r.stream, maxJSONLLineBytes+1)
	return nil
}

type jellystatLeaseRenewer struct {
	store                  core.ImportStore
	clock                  core.Clock
	jobID, token           string
	duration, storeTimeout time.Duration
	expiresAt              time.Time
}

func (r *jellystatReader) lookupLeaseRenewer(job core.ImportJob) func(context.Context) error {
	if r.store == nil || r.clock == nil || r.leaseDuration <= 0 || job.LeaseExpiresAt == nil {
		return nil
	}
	renewer := &jellystatLeaseRenewer{
		store: r.store, clock: r.clock, jobID: job.ID, token: job.LeaseToken,
		duration: r.leaseDuration, storeTimeout: r.storeTimeout, expiresAt: *job.LeaseExpiresAt,
	}
	return renewer.renew
}

func (r *jellystatLeaseRenewer) renew(ctx context.Context) error {
	now := core.NormalizeTime(r.clock.Now())
	if now.Add(r.duration / 2).Before(r.expiresAt) {
		return nil
	}
	lease := core.ImportLease{Token: r.token, ExpiresAt: now.Add(r.duration)}
	storeCtx, cancel := storeCallContext(ctx, r.storeTimeout)
	defer cancel()
	if err := r.store.RenewImportLease(storeCtx, r.jobID, lease, now); err != nil {
		return fmt.Errorf("renew Jellystat lookup lease: %w", err)
	}
	r.expiresAt = lease.ExpiresAt
	return nil
}

func (r *jellystatReader) Close() error {
	if r.stream == nil {
		return nil
	}
	err := r.stream.Close()
	r.stream, r.buffered = nil, nil
	return err
}

func validateJellystatUpload(
	ctx context.Context, staging *Staging, id string, storeTimeout time.Duration,
) error {
	upload, err := staging.openWithTimeout(ctx, id, storeTimeout)
	if err != nil {
		return fmt.Errorf("open Jellystat upload: %w", err)
	}
	reader := bufio.NewReaderSize(io.NewSectionReader(upload, 0, upload.Size()), maxJSONLLineBytes+1)
	line, _, err := readJellystatLine(reader)
	if err != nil {
		if errors.Is(err, core.ErrImportStore) || errors.Is(err, context.Canceled) ||
			errors.Is(err, context.DeadlineExceeded) {
			return err
		}
		return invalidJellystatBackup()
	}
	envelope, err := decodeJellystatLine(line)
	if err != nil || envelope.Type != "table" || envelope.Table == "" || len(envelope.Data) != 0 {
		return invalidJellystatBackup()
	}
	return nil
}

func invalidJellystatBackup() error {
	return &core.InvalidArgumentError{
		Field: "file", Code: "invalid", Message: "must be a Jellystat JSONL backup",
	}
}

func loadJellystatLookups(
	ctx context.Context, upload *UploadReader, renew func(context.Context) error,
) (jellystatLookups, error) {
	reader := bufio.NewReaderSize(io.NewSectionReader(upload, 0, upload.Size()), maxJSONLLineBytes+1)
	return scanJellystatLookups(ctx, reader, maxJellystatRows, renew)
}

func scanJellystatLookups(
	ctx context.Context, reader *bufio.Reader, rowLimit int, renew func(context.Context) error,
) (jellystatLookups, error) {
	if rowLimit < 1 || rowLimit > maxJellystatRows {
		return jellystatLookups{}, core.ErrInvalidArgument
	}
	lookups := newJellystatLookups()
	sawPlaybackActivity := false
	rows := 0
	for range rowLimit + 1 {
		if err := ctx.Err(); err != nil {
			return jellystatLookups{}, err
		}
		line, complete, err := readJellystatLine(reader)
		if err != nil {
			return jellystatLookups{}, err
		}
		if renew != nil {
			if err := renew(ctx); err != nil {
				return jellystatLookups{}, err
			}
		}
		if len(line) > 0 {
			if rows == rowLimit {
				return jellystatLookups{}, fmt.Errorf("jellystat backup row limit exceeded: %w", core.ErrInvalidArgument)
			}
			rows++
			if err := collectJellystatLookupLine(line, &lookups, &sawPlaybackActivity); err != nil {
				return jellystatLookups{}, err
			}
		}
		if complete {
			if !sawPlaybackActivity {
				return jellystatLookups{}, invalidJellystatBackup()
			}
			return lookups, nil
		}
	}
	return jellystatLookups{}, fmt.Errorf("jellystat backup row limit exceeded: %w", core.ErrInvalidArgument)
}

func newJellystatLookups() jellystatLookups {
	return jellystatLookups{
		users: make(map[string]string), items: make(map[string]jellystatItem),
		episodes: make(map[string]jellystatEpisode), userRows: make(map[string]string),
		itemRows: make(map[string]string), episodeRows: make(map[string]string),
	}
}

func collectJellystatLookupLine(
	line []byte, lookups *jellystatLookups, sawPlaybackActivity *bool,
) error {
	envelope, ok := decodedJellystatLine(line)
	if !ok {
		return nil
	}
	if envelope.Type == "table" {
		if envelope.Table == "jf_playback_activity" {
			*sawPlaybackActivity = true
		}
		return nil
	}
	if envelope.Type != "row" || len(envelope.Data) == 0 {
		return nil
	}
	switch envelope.Table {
	case "jf_users":
		return collectJellystatUser(envelope.Data, lookups)
	case "jf_library_items":
		return collectJellystatItem(envelope.Data, lookups)
	case "jf_library_episodes":
		return collectJellystatEpisode(envelope.Data, lookups)
	}
	return nil
}

func collectJellystatUser(data []byte, lookups *jellystatLookups) error {
	var row jellystatUserRow
	if !decodedJellystatData(data, &row) || !validImportLookup(row.ID, 256) ||
		!validImportLookup(row.Name, 500) {
		return nil
	}
	unique, err := lookups.reserveUnique(lookups.userRows, row.ID, data, row.ID, row.Name)
	if err != nil || !unique {
		return err
	}
	lookups.users[row.ID] = row.Name
	return nil
}

func collectJellystatItem(data []byte, lookups *jellystatLookups) error {
	var row jellystatItemRow
	if !decodedJellystatData(data, &row) || !validImportLookup(row.ID, 256) ||
		!validImportLookup(row.Name, 500) || !validImportLookup(row.Type, 500) {
		return nil
	}
	runtime, ok := jellystatRuntime(row.RunTimeTicks)
	if !ok {
		return nil
	}
	unique, err := lookups.reserveUnique(lookups.itemRows, row.ID, data, row.ID, row.Name, row.Type)
	if err != nil || !unique {
		return err
	}
	lookups.items[row.ID] = jellystatItem{Name: row.Name, Type: row.Type, Runtime: runtime}
	return nil
}

func collectJellystatEpisode(data []byte, lookups *jellystatLookups) error {
	var row jellystatEpisodeRow
	if !decodedJellystatData(data, &row) || !validJellystatEpisode(row) {
		return nil
	}
	runtime, ok := jellystatRuntime(row.RunTimeTicks)
	if !ok {
		return nil
	}
	unique, err := lookups.reserveUnique(
		lookups.episodeRows, row.EpisodeID, data,
		row.EpisodeID, row.Name, row.SeriesID, row.SeasonID, row.SeriesName, row.SeasonName,
	)
	if err != nil || !unique {
		return err
	}
	lookups.episodes[row.EpisodeID] = jellystatEpisode{
		Name: row.Name, SeriesID: row.SeriesID, SeasonID: row.SeasonID,
		SeriesName: row.SeriesName, SeasonName: row.SeasonName,
		SeasonNumber: row.ParentIndexNumber, EpisodeNumber: row.IndexNumber, Runtime: runtime,
	}
	return nil
}

func (l *jellystatLookups) reserveUnique(
	rows map[string]string, id string, data []byte, values ...string,
) (bool, error) {
	raw := string(data)
	if existing, ok := rows[id]; ok {
		if existing == raw {
			return false, nil
		}
		return false, fmt.Errorf("conflicting duplicate Jellystat lookup id: %w", core.ErrInvalidArgument)
	}
	if err := l.reserve(append(values, raw)...); err != nil {
		return false, err
	}
	rows[id] = raw
	return true, nil
}

func (l *jellystatLookups) reserve(values ...string) error {
	if l.entries >= maxJellystatLookupEntries {
		return fmt.Errorf("jellystat lookup entry limit exceeded: %w", core.ErrInvalidArgument)
	}
	size := 0
	for _, value := range values {
		size += len(value)
	}
	if size > maxJellystatLookupBytes-l.bytes {
		return fmt.Errorf("jellystat lookup byte limit exceeded: %w", core.ErrInvalidArgument)
	}
	l.entries++
	l.bytes += size
	return nil
}

func readJellystatLines(
	ctx context.Context, reader *bufio.Reader, offset int64, lookups jellystatLookups,
) ([]core.ImportedWatch, int64, int64, error) {
	records := make([]core.ImportedWatch, 0, core.ImportBatchSize)
	var skipped int64
	for range core.ImportBatchSize {
		if err := ctx.Err(); err != nil {
			return nil, offset, skipped, err
		}
		line, complete, err := readJellystatLine(reader)
		offset += int64(len(line))
		if err != nil {
			return nil, offset, skipped, err
		}
		record, ok := decodeJellystatActivity(line, lookups)
		if ok {
			records = append(records, record)
		} else if len(line) > 0 {
			skipped++
		}
		if complete {
			return records, offset, skipped, nil
		}
	}
	return records, offset, skipped, nil
}

func readJellystatLine(reader *bufio.Reader) ([]byte, bool, error) {
	line, err := reader.ReadSlice('\n')
	payloadBytes := len(line)
	if payloadBytes > 0 && line[payloadBytes-1] == '\n' {
		payloadBytes--
	}
	if payloadBytes > maxJSONLLineBytes || errors.Is(err, bufio.ErrBufferFull) {
		return line, false, errors.New("JSONL line exceeds size limit")
	}
	if errors.Is(err, io.EOF) {
		return line, true, nil
	}
	if err != nil {
		return line, false, fmt.Errorf("read Jellystat upload: %w", err)
	}
	return line, false, nil
}

func decodeJellystatActivity(line []byte, lookups jellystatLookups) (core.ImportedWatch, bool) {
	envelope, err := decodeJellystatLine(line)
	if err != nil || envelope.Type != "row" || envelope.Table != "jf_playback_activity" {
		return core.ImportedWatch{}, false
	}
	var row jellystatActivityRow
	if decodeJellystatData(envelope.Data, &row) != nil {
		return core.ImportedWatch{}, false
	}
	record, err := mapJellystatActivity(row, lookups)
	return record, err == nil
}

func mapJellystatActivity(row jellystatActivityRow, lookups jellystatLookups) (core.ImportedWatch, error) {
	if row.ID == "" || row.UserID == "" || row.PlaybackDuration.Value < 0 ||
		!row.PlaybackDuration.Valid || row.ActivityDateInserted.IsZero() ||
		row.PlaybackDuration.Value > core.MaxImportPlaybackSeconds || !validJellystatActivityNames(row) {
		return core.ImportedWatch{}, core.ErrInvalidArgument
	}
	duration := time.Duration(row.PlaybackDuration.Value) * time.Second
	started, ended, ok := jellystatActivityTimes(row.ActivityDateInserted, duration)
	if !ok {
		return core.ImportedWatch{}, core.ErrInvalidArgument
	}
	record := core.ImportedWatch{
		RecordID: row.ID, MediaUserID: row.UserID,
		Username: jellystatUsername(row.UserID, valueOrEmpty(row.UserName), lookups),
		DeviceID: valueOrEmpty(row.DeviceID), DeviceName: valueOrEmpty(row.DeviceName),
		Client: valueOrEmpty(row.Client), PlayMethod: jellystatPlayMethod(valueOrEmpty(row.PlayMethod)),
		Duration: duration, StartedAt: started, EndedAt: &ended,
	}
	mapJellystatItem(&record, row, lookups)
	if !plausibleJellystatDuration(duration, record.Runtime) {
		return core.ImportedWatch{}, core.ErrInvalidArgument
	}
	// Jellystat allows longer activity ids than the origin column holds;
	// keep the activity and drop only its provenance, as migration 00026 does.
	if row.Imported && len(row.ID) <= core.MaxImportOriginRecordIDBytes {
		record.OriginRecordID = row.ID
	}
	if !record.Valid() {
		return core.ImportedWatch{}, core.ErrInvalidArgument
	}
	return record, nil
}

func plausibleJellystatDuration(duration time.Duration, runtime *time.Duration) bool {
	if runtime == nil || *runtime <= 0 {
		return true
	}
	maximumRuntime := (time.Duration(math.MaxInt64) - time.Hour) / 3
	if *runtime > maximumRuntime {
		return true
	}
	return duration <= 3*(*runtime)+time.Hour
}

func validJellystatActivityNames(row jellystatActivityRow) bool {
	for _, value := range []*string{row.UserName, row.NowPlayingItemName, row.SeriesName} {
		if value != nil && !validJSONLText(*value, 500, false) {
			return false
		}
	}
	return true
}

func jellystatActivityTimes(value time.Time, duration time.Duration) (time.Time, time.Time, bool) {
	ended := core.NormalizeTime(value)
	started := core.NormalizeTime(ended.Add(-duration))
	// Imported timestamps must remain in the four-digit RFC 3339 year range
	// that both persistence adapters can round-trip.
	minimum := time.Date(1, 1, 1, 0, 0, 0, 0, time.UTC)
	maximum := time.Date(10_000, 1, 1, 0, 0, 0, 0, time.UTC)
	valid := !started.Before(minimum) && started.Before(maximum) &&
		!ended.Before(minimum) && ended.Before(maximum)
	return started, ended, valid
}

func mapJellystatItem(record *core.ImportedWatch, row jellystatActivityRow, lookups jellystatLookups) {
	if episodeID := valueOrEmpty(row.EpisodeID); episodeID != "" {
		record.ItemID, record.ItemName, record.ItemType = episodeID,
			jellystatFallbackName(valueOrEmpty(row.NowPlayingItemName), episodeID), "Episode"
		record.SeriesName = valueOrEmpty(row.SeriesName)
		if episode, ok := lookups.episodes[episodeID]; ok {
			record.ItemName = episode.Name
			record.SeasonNumber, record.EpisodeNumber, record.Runtime = episode.SeasonNumber, episode.EpisodeNumber, episode.Runtime
			if core.ValidCatalogID(episode.SeriesID) {
				record.SeriesID = episode.SeriesID
			}
			if episode.SeriesName != "" {
				record.SeriesName = episode.SeriesName
			} else if series, found := lookups.items[episode.SeriesID]; found {
				record.SeriesName = series.Name
			}
		}
		return
	}
	itemID := valueOrEmpty(row.NowPlayingItemID)
	record.ItemID = itemID
	record.ItemName = jellystatFallbackName(valueOrEmpty(row.NowPlayingItemName), itemID)
	if item, ok := lookups.items[itemID]; ok {
		record.ItemName, record.ItemType, record.Runtime = item.Name, item.Type, item.Runtime
	}
}

func jellystatUsername(id, fallback string, lookups jellystatLookups) string {
	if name, ok := lookups.users[id]; ok {
		return name
	}
	return jellystatFallbackName(fallback, id)
}

func jellystatFallbackName(name, id string) string {
	if name != "" {
		return name
	}
	return id
}

func jellystatPlayMethod(value string) core.PlayMethod {
	switch value {
	case "DirectPlay":
		return core.PlayMethodDirectPlay
	case "DirectStream":
		return core.PlayMethodDirectStream
	case "Transcode":
		return core.PlayMethodTranscode
	default:
		return core.PlayMethodUnknown
	}
}

func jellystatRuntime(ticks jellystatInt64) (*time.Duration, bool) {
	if !ticks.Valid {
		return nil, true
	}
	if ticks.Value < 0 || ticks.Value > math.MaxInt64/100 {
		return nil, false
	}
	runtime := time.Duration(ticks.Value * 100)
	return &runtime, true
}

func decodeJellystatLine(line []byte) (jellystatLine, error) {
	if !utf8.Valid(line) || len(bytes.TrimSpace(line)) == 0 {
		return jellystatLine{}, core.ErrInvalidArgument
	}
	var value jellystatLine
	if err := decodeSingleJSON(line, &value); err != nil || value.Type == "" || value.Table == "" {
		return jellystatLine{}, core.ErrInvalidArgument
	}
	return value, nil
}

func decodeJellystatData(data []byte, destination any) error {
	if len(data) == 0 || bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return core.ErrInvalidArgument
	}
	return decodeSingleJSON(data, destination)
}

func decodedJellystatLine(line []byte) (jellystatLine, bool) {
	envelope, err := decodeJellystatLine(line)
	return envelope, err == nil
}

func decodedJellystatData(data []byte, destination any) bool {
	return decodeJellystatData(data, destination) == nil
}

func decodeSingleJSON(data []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("JSON value contains trailing data")
	}
	return nil
}

func validImportLookup(value string, maxBytes int) bool {
	return value != "" && validJSONLText(value, maxBytes, true)
}

func validJellystatEpisode(row jellystatEpisodeRow) bool {
	values := []struct {
		value string
		limit int
	}{
		{row.EpisodeID, 256},
		{row.Name, 500},
		{row.SeriesID, 256},
		{row.SeasonID, 256},
		{row.SeriesName, 500},
		{row.SeasonName, 500},
	}
	for _, value := range values {
		if value.value != "" && !validJSONLText(value.value, value.limit, true) {
			return false
		}
	}
	return row.EpisodeID != "" && row.Name != ""
}

func valueOrEmpty(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

type jellystatInt64 struct {
	Value int64
	Valid bool
}

func (v *jellystatInt64) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if bytes.Equal(trimmed, []byte("null")) {
		return nil
	}
	if len(trimmed) >= 2 && trimmed[0] == '"' && trimmed[len(trimmed)-1] == '"' {
		trimmed = trimmed[1 : len(trimmed)-1]
	}
	value, err := parseJSONInt64(trimmed)
	if err != nil {
		return err
	}
	v.Value, v.Valid = value, true
	return nil
}

func parseJSONInt64(data []byte) (int64, error) {
	value, err := strconv.ParseInt(string(data), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse Jellystat integer: %w", err)
	}
	return value, nil
}

type jellystatUserRow struct {
	ID               string          `json:"Id"`
	Name             string          `json:"Name"`
	PrimaryImageTag  json.RawMessage `json:"PrimaryImageTag"`
	LastLoginDate    json.RawMessage `json:"LastLoginDate"`
	LastActivityDate json.RawMessage `json:"LastActivityDate"`
	IsAdministrator  bool            `json:"IsAdministrator"`
}

type jellystatItemRow struct {
	ID, Name, ServerID, Type, ParentID string
	PremiereDate, EndDate, Status      json.RawMessage
	ImageTags, ImageTagsPrimary        json.RawMessage
	ImageTagsBackdrop, ImageTagsLogo   json.RawMessage
	ImageTagsThumb, PrimaryImageTag    json.RawMessage
	BackdropImageTags                  json.RawMessage
	CommunityRating                    *float64
	RunTimeTicks                       jellystatInt64
	ProductionYear                     *int32
	IsFolder                           *bool
	PrimaryImageHash, Genres           json.RawMessage
	Archived                           bool
}

type jellystatEpisodeRow struct {
	ID, EpisodeID, Name, ServerID, Type            string
	SeriesID, SeasonID, SeasonName, SeriesName     string
	PremiereDate, OfficialRating                   json.RawMessage
	CommunityRating                                *float64
	RunTimeTicks                                   jellystatInt64
	ProductionYear, IndexNumber, ParentIndexNumber *int32
	Archived                                       bool
	PrimaryImageHash                               json.RawMessage
}

type jellystatActivityRow struct {
	ID, UserID                 string
	IsPaused                   json.RawMessage
	UserName, Client           *string
	DeviceName, DeviceID       *string
	ApplicationVersion         *string
	NowPlayingItemID           *string
	NowPlayingItemName         *string
	SeasonID, SeriesName       *string
	EpisodeID                  *string
	PlaybackDuration           jellystatInt64
	ActivityDateInserted       time.Time
	PlayMethod                 *string
	MediaStreams               json.RawMessage
	TranscodingInfo, PlayState json.RawMessage
	OriginalContainer          *string
	RemoteEndPoint, ServerID   *string
	Imported                   bool `json:"imported"`
}
