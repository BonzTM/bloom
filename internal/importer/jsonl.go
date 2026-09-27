// Package importer owns resumable historical-watch import policy and workers.
package importer

import (
	"archive/zip"
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/BonzTM/bloom/internal/core"
)

const maxJSONLLineBytes = 64 << 10

const (
	maxImportArchiveEntries = 16
	maxImportMetadataBytes  = 1 << 20
)

var zipSignature = []byte{'P', 'K', 0x03, 0x04}

var nestedZipSignatures = [3][4]byte{
	{'P', 'K', 0x03, 0x04},
	{'P', 'K', 0x05, 0x06},
	{'P', 'K', 0x07, 0x08},
}

type bloomExportRecord struct {
	ID              string            `json:"id"`
	MediaServerID   string            `json:"media_server_id"`
	MediaServerName string            `json:"media_server_name"`
	MediaUserID     string            `json:"media_user_id"`
	Username        string            `json:"username"`
	DeviceID        string            `json:"device_id"`
	DeviceName      string            `json:"device_name"`
	Client          string            `json:"client"`
	ItemID          string            `json:"item_id"`
	ItemName        string            `json:"item_name"`
	ItemType        string            `json:"item_type"`
	SeriesName      string            `json:"series_name"`
	LibraryID       string            `json:"library_id"`
	LibraryName     string            `json:"library_name"`
	SeasonNumber    *int32            `json:"season_number"`
	EpisodeNumber   *int32            `json:"episode_number"`
	PositionMS      int64             `json:"position_ms"`
	RuntimeMS       *int64            `json:"runtime_ms"`
	Paused          bool              `json:"paused"`
	PlayMethod      core.PlayMethod   `json:"play_method"`
	Stream          *streamJSONLWire  `json:"stream,omitempty"`
	Source          core.WatchSource  `json:"source"`
	ImportSource    core.ImportSource `json:"import_source,omitempty"`
	ImportRecordID  string            `json:"import_record_id,omitempty"`
	ActiveSeconds   int64             `json:"active_seconds"`
	StartedAt       time.Time         `json:"started_at"`
	EndedAt         *time.Time        `json:"ended_at,omitempty"`
}

type streamJSONLWire struct {
	Container        string   `json:"container,omitempty"`
	VideoCodec       string   `json:"video_codec,omitempty"`
	AudioCodec       string   `json:"audio_codec,omitempty"`
	Bitrate          int64    `json:"bitrate,omitempty"`
	Width            int32    `json:"width,omitempty"`
	Height           int32    `json:"height,omitempty"`
	Framerate        float64  `json:"framerate,omitempty"`
	AudioChannels    int32    `json:"audio_channels,omitempty"`
	IsVideoDirect    *bool    `json:"is_video_direct,omitempty"`
	IsAudioDirect    *bool    `json:"is_audio_direct,omitempty"`
	TranscodeReasons []string `json:"transcode_reasons,omitempty"`
}

// EncodeWatchJSONL writes one contract record and a newline.
func EncodeWatchJSONL(writer io.Writer, watch core.PlaybackWatch) error {
	wire := watchWire(watch)
	if err := json.NewEncoder(writer).Encode(wire); err != nil {
		return fmt.Errorf("encode watch JSONL: %w", err)
	}
	return nil
}

func watchWire(watch core.PlaybackWatch) bloomExportRecord {
	return bloomExportRecord{
		ID: watch.ID, MediaServerID: watch.MediaServerID, MediaServerName: watch.MediaServerName,
		MediaUserID: watch.MediaUserID, Username: watch.Username, DeviceID: watch.DeviceID,
		DeviceName: watch.DeviceName, Client: watch.Client, ItemID: watch.ItemID,
		ItemName: watch.ItemName, ItemType: watch.ItemType, SeriesName: watch.SeriesName,
		LibraryID: watch.LibraryID, LibraryName: watch.LibraryName, SeasonNumber: watch.SeasonNumber,
		EpisodeNumber: watch.EpisodeNumber, PositionMS: int64(watch.LastPosition / time.Millisecond),
		RuntimeMS: runtimeMilliseconds(watch.Runtime),
		Paused:    watch.State == core.WatchPaused, PlayMethod: watch.PlayMethod, Stream: streamWire(watch.Stream),
		Source: watch.Source, ImportSource: watch.ImportSource, ImportRecordID: watch.ImportRecordID,
		ActiveSeconds: int64(watch.ActiveTime / time.Second), StartedAt: watch.StartedAt, EndedAt: watch.EndedAt,
	}
}

func runtimeMilliseconds(runtime *time.Duration) *int64 {
	if runtime == nil {
		return nil
	}
	value := int64(*runtime / time.Millisecond)
	return &value
}

func streamWire(stream *core.StreamDetails) *streamJSONLWire {
	if stream == nil {
		return nil
	}
	return &streamJSONLWire{
		Container: stream.Container, VideoCodec: stream.VideoCodec, AudioCodec: stream.AudioCodec,
		Bitrate: stream.Bitrate, Width: stream.Width, Height: stream.Height, Framerate: stream.Framerate,
		AudioChannels: stream.AudioChannels, IsVideoDirect: stream.IsVideoDirect,
		IsAudioDirect: stream.IsAudioDirect, TranscodeReasons: stream.TranscodeReasons,
	}
}

type fileCursor struct {
	ID     string `json:"id"`
	Offset int64  `json:"offset"`
}

func encodeFileCursor(cursor fileCursor) (string, error) {
	data, err := json.Marshal(cursor)
	if err != nil || len(data) > core.MaxImportCursorBytes {
		return "", fmt.Errorf("encode import file cursor: %w", errors.Join(err, core.ErrInvalidArgument))
	}
	return string(data), nil
}

func decodeFileCursor(value string) (fileCursor, error) {
	var cursor fileCursor
	decoder := json.NewDecoder(bytes.NewBufferString(value))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cursor); err != nil || !core.ValidID(cursor.ID) || cursor.Offset < 0 {
		return fileCursor{}, fmt.Errorf("decode import file cursor: %w", core.ErrInvalidArgument)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return fileCursor{}, fmt.Errorf("decode import file cursor: %w", core.ErrInvalidArgument)
	}
	return cursor, nil
}

type jsonlReader struct{ staging *Staging }

func (s jsonlReader) ReadImportBatch(ctx context.Context, job core.ImportJob) ([]core.ImportedWatch, string, int64, error) {
	cursor, err := decodeFileCursor(job.Cursor)
	if err != nil {
		return nil, "", 0, err
	}
	upload, err := s.staging.open(ctx, cursor.ID)
	if err != nil {
		return nil, "", 0, fmt.Errorf("open import upload: %w", err)
	}
	reader, err := openWatchUpload(upload, cursor.Offset)
	if err != nil {
		return nil, "", 0, err
	}
	records, offset, skipped, readErr := readJSONLLines(ctx, reader, cursor.Offset)
	closeErr := reader.Close()
	if readErr != nil || closeErr != nil {
		return nil, "", 0, errors.Join(readErr, closeErr)
	}
	next, err := encodeFileCursor(fileCursor{ID: cursor.ID, Offset: offset})
	return records, next, skipped, err
}

func openWatchUpload(upload *UploadReader, offset int64) (io.ReadCloser, error) {
	header := make([]byte, len(zipSignature))
	_, err := upload.ReadAt(header, 0)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("detect import upload: %w", err)
	}
	if !bytes.Equal(header, zipSignature) {
		if offset > upload.Size() {
			return nil, fmt.Errorf("seek import upload: %w", core.ErrInvalidArgument)
		}
		return io.NopCloser(io.NewSectionReader(upload, offset, upload.Size()-offset)), nil
	}
	archive, err := zip.NewReader(upload, upload.Size())
	if err != nil {
		return nil, fmt.Errorf("open import archive: %w", err)
	}
	watches, err := validateImportArchive(archive)
	if err != nil {
		return nil, err
	}
	watchesSize := int64(watches.UncompressedSize64) //nolint:gosec // Archive validation caps this below MaxInt64.
	if offset > watchesSize {
		return nil, fmt.Errorf("seek import upload: %w", core.ErrInvalidArgument)
	}
	if watches.Method == zip.Store {
		dataOffset, offsetErr := watches.DataOffset()
		if offsetErr != nil {
			return nil, fmt.Errorf("locate watches.jsonl: %w", offsetErr)
		}
		length := watchesSize - offset
		return io.NopCloser(io.NewSectionReader(upload, dataOffset+offset, length)), nil
	}
	reader, err := watches.Open()
	if err != nil {
		return nil, fmt.Errorf("open watches.jsonl: %w", err)
	}
	if err := discardUploadPrefix(reader, offset); err != nil {
		return nil, errors.Join(err, reader.Close())
	}
	return reader, nil
}

func validateImportArchive(archive *zip.Reader) (*zip.File, error) {
	if len(archive.File) == 0 || len(archive.File) > maxImportArchiveEntries {
		return nil, errors.New("import archive has an invalid entry count")
	}
	var watches *zip.File
	for _, entry := range archive.File {
		if err := validateImportArchiveEntry(entry); err != nil {
			return nil, err
		}
		if entry.Name == "watches.jsonl" {
			if watches != nil {
				return nil, errors.New("import archive repeats watches.jsonl")
			}
			watches = entry
		}
	}
	if watches == nil {
		return nil, errors.New("import archive does not contain watches.jsonl")
	}
	return watches, nil
}

func validateImportArchiveEntry(entry *zip.File) error {
	if entry.UncompressedSize64 > importArchiveEntryLimit(entry.Name) {
		return errors.New("import archive entry exceeds size limit")
	}
	reader, err := entry.Open()
	if err != nil {
		return fmt.Errorf("inspect import archive entry: %w", err)
	}
	header := make([]byte, len(zipSignature))
	_, readErr := io.ReadFull(reader, header)
	closeErr := reader.Close()
	if readErr != nil && !errors.Is(readErr, io.EOF) && !errors.Is(readErr, io.ErrUnexpectedEOF) {
		return fmt.Errorf("inspect import archive entry: %w", errors.Join(readErr, closeErr))
	}
	if closeErr != nil {
		return fmt.Errorf("close import archive entry: %w", closeErr)
	}
	if hasNestedZipSignature(header) {
		return errors.New("nested import archives are not allowed")
	}
	return nil
}

func hasNestedZipSignature(header []byte) bool {
	if len(header) != len(zipSignature) {
		return false
	}
	for _, signature := range nestedZipSignatures {
		if bytes.Equal(header, signature[:]) {
			return true
		}
	}
	return false
}

func importArchiveEntryLimit(name string) uint64 {
	if name == "watches.jsonl" || name == "imports.jsonl" {
		return core.MaxImportUploadBytes
	}
	return maxImportMetadataBytes
}

func discardUploadPrefix(reader io.Reader, offset int64) error {
	if offset == 0 {
		return nil
	}
	written, err := io.CopyN(io.Discard, reader, offset)
	if err != nil || written != offset {
		return fmt.Errorf("seek import upload: %w", errors.Join(err, core.ErrInvalidArgument))
	}
	return nil
}

func readJSONLLines(
	ctx context.Context, reader io.Reader, offset int64,
) ([]core.ImportedWatch, int64, int64, error) {
	buffered := bufio.NewReaderSize(reader, maxJSONLLineBytes+1)
	records := make([]core.ImportedWatch, 0, core.ImportBatchSize)
	var skipped int64
	for range core.ImportBatchSize {
		if err := ctx.Err(); err != nil {
			return nil, offset, skipped, err
		}
		line, err := buffered.ReadSlice('\n')
		offset += int64(len(line))
		if len(line) > maxJSONLLineBytes {
			return nil, offset, skipped, errors.New("JSONL line exceeds size limit")
		}
		if len(bytes.TrimSpace(line)) > 0 {
			record, decodeErr := DecodeWatchJSONL(line)
			if decodeErr != nil {
				return nil, offset, skipped, decodeErr
			}
			records = append(records, record)
		} else if len(line) > 0 {
			skipped++
		}
		if errors.Is(err, io.EOF) {
			return records, offset, skipped, nil
		}
		if err != nil {
			return nil, offset, skipped, fmt.Errorf("read import upload: %w", err)
		}
	}
	return records, offset, skipped, nil
}

// DecodeWatchJSONL validates and normalizes one Bloom watch-export record.
func DecodeWatchJSONL(line []byte) (core.ImportedWatch, error) {
	if !utf8.Valid(line) {
		return core.ImportedWatch{}, errors.New("JSONL line is not valid UTF-8")
	}
	var wire bloomExportRecord
	decoder := json.NewDecoder(bytes.NewReader(line))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&wire); err != nil {
		return core.ImportedWatch{}, fmt.Errorf("decode JSONL watch: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return core.ImportedWatch{}, errors.New("decode JSONL watch: trailing data")
	}
	if !validWatchWire(wire) || wire.ActiveSeconds > math.MaxInt64/int64(time.Second) ||
		wire.PositionMS > math.MaxInt64/int64(time.Millisecond) {
		return core.ImportedWatch{}, fmt.Errorf("validate JSONL watch: %w", core.ErrInvalidArgument)
	}
	record := core.ImportedWatch{
		RecordID: wire.ID, MediaUserID: wire.MediaUserID, Username: wire.Username,
		DeviceID: wire.DeviceID, DeviceName: wire.DeviceName, Client: wire.Client,
		ItemID: wire.ItemID, ItemName: wire.ItemName, ItemType: wire.ItemType,
		SeriesName: wire.SeriesName, LibraryID: wire.LibraryID, LibraryName: wire.LibraryName,
		SeasonNumber: wire.SeasonNumber, EpisodeNumber: wire.EpisodeNumber,
		PlayMethod: wire.PlayMethod, Stream: importedStream(wire.Stream),
		StartedAt: core.NormalizeTime(wire.StartedAt), EndedAt: normalizedTime(wire.EndedAt),
		Runtime:      millisecondsDuration(wire.RuntimeMS),
		Duration:     time.Duration(wire.ActiveSeconds) * time.Second,
		LastPosition: time.Duration(wire.PositionMS) * time.Millisecond,
	}
	if !record.Valid() {
		return core.ImportedWatch{}, fmt.Errorf("validate imported watch: %w", core.ErrInvalidArgument)
	}
	return record, nil
}

func millisecondsDuration(value *int64) *time.Duration {
	if value == nil {
		return nil
	}
	duration := time.Duration(*value) * time.Millisecond
	return &duration
}

func validWatchWire(wire bloomExportRecord) bool {
	if !core.ValidID(wire.ID) || !core.ValidID(wire.MediaServerID) || wire.PositionMS < 0 ||
		wire.ActiveSeconds < 0 || !wire.PlayMethod.Valid() || !wire.Source.Valid() || wire.StartedAt.IsZero() ||
		wire.EndedAt == nil || wire.Paused || !validJSONLText(wire.MediaServerName, 500, true) ||
		!validJSONLText(wire.DeviceID, 256, false) || !validJSONLText(wire.SeriesName, 500, false) {
		return false
	}
	if wire.RuntimeMS != nil && (*wire.RuntimeMS < 0 || *wire.RuntimeMS > math.MaxInt64/int64(time.Millisecond)) {
		return false
	}
	if (wire.LibraryID == "") != (wire.LibraryName == "") ||
		(wire.LibraryID != "" && !(core.Library{ID: wire.LibraryID, Name: wire.LibraryName}).Valid()) {
		return false
	}
	if wire.EndedAt != nil && wire.EndedAt.Before(wire.StartedAt) {
		return false
	}
	if wire.Stream != nil && !wireStream(wire.Stream).Valid() {
		return false
	}
	provenance := wire.ImportSource.Valid() && validJSONLText(wire.ImportRecordID, core.MaxImportRecordIDBytes, true)
	return (wire.Source == core.WatchSourceImport) == provenance
}

func validJSONLText(value string, maxBytes int, required bool) bool {
	if (required && value == "") || len(value) > maxBytes || !utf8.ValidString(value) {
		return false
	}
	for _, char := range value {
		if unicode.IsControl(char) {
			return false
		}
	}
	return true
}

func wireStream(wire *streamJSONLWire) core.StreamDetails {
	return core.StreamDetails{
		Container: wire.Container, VideoCodec: wire.VideoCodec, AudioCodec: wire.AudioCodec,
		Bitrate: wire.Bitrate, Width: wire.Width, Height: wire.Height, Framerate: wire.Framerate,
		AudioChannels: wire.AudioChannels, IsVideoDirect: wire.IsVideoDirect,
		IsAudioDirect: wire.IsAudioDirect, TranscodeReasons: wire.TranscodeReasons,
	}
}

func importedStream(wire *streamJSONLWire) *core.StreamDetails {
	if wire == nil {
		return nil
	}
	stream := wireStream(wire)
	return &stream
}

func normalizedTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	normalized := core.NormalizeTime(*value)
	return &normalized
}
