// Package importer owns resumable historical-watch import policy and workers.
package importer

import (
	"archive/zip"
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
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
	maxImportArchiveEntries        = 16
	maxImportMetadataBytes         = 1 << 20
	maxImportCentralDirectoryBytes = 64 << 10
	zipEndRecordBytes              = 22
	zip64LocatorBytes              = 20
	zip64EndRecordBytes            = 56
)

const (
	zipEndSignature       = 0x06054b50
	zip64LocatorSignature = 0x07064b50
	zip64EndSignature     = 0x06064b50
)

var errImportArchiveDirectoryBounds = errors.New("import archive central directory exceeds limits")

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

type bloomExportSummary struct {
	WatchRecords  *int64 `json:"watch_records"`
	ImportRecords int64  `json:"import_records"`
	WatchesBytes  int64  `json:"watches_bytes"`
	ImportsBytes  int64  `json:"imports_bytes"`
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

type watchOpener func(*UploadReader, int64) (io.ReadCloser, error)

type jsonlReader struct {
	staging         *Staging
	storeTimeout    time.Duration
	openWatch       watchOpener
	uploadID        string
	offset          int64
	stream          io.ReadCloser
	buffered        *bufio.Reader
	expectedRecords int64
	recordsRead     int64
	validateSummary bool
}

func (s *jsonlReader) ReadImportBatch(
	ctx context.Context, job core.ImportJob,
) ([]core.ImportedWatch, string, int64, error) {
	cursor, err := decodeFileCursor(job.Cursor)
	if err != nil {
		return nil, "", 0, err
	}
	if s.stream == nil {
		if openErr := s.open(ctx, cursor); openErr != nil {
			return nil, "", 0, openErr
		}
		s.recordsRead = job.Read - job.Skipped
		if countErr := s.validateRecordCount(false); countErr != nil {
			return nil, "", 0, countErr
		}
	} else if cursor.ID != s.uploadID || cursor.Offset != s.offset {
		return nil, "", 0, fmt.Errorf("validate import cursor: %w", core.ErrInvalidArgument)
	}
	records, offset, skipped, complete, err := readJSONLLines(ctx, s.buffered, s.offset)
	if err != nil {
		return nil, "", 0, err
	}
	s.recordsRead += int64(len(records))
	if countErr := s.validateRecordCount(complete); countErr != nil {
		return nil, "", 0, countErr
	}
	s.offset = offset
	next, err := encodeFileCursor(fileCursor{ID: cursor.ID, Offset: offset})
	return records, next, skipped, err
}

func (s *jsonlReader) validateRecordCount(complete bool) error {
	if !s.validateSummary || (s.recordsRead <= s.expectedRecords && (!complete || s.recordsRead == s.expectedRecords)) {
		return nil
	}
	return fmt.Errorf(
		"validate Bloom export summary: read %d watch records, summary declares %d: %w",
		s.recordsRead, s.expectedRecords, core.ErrImportRecordCountMismatch,
	)
}

func (s *jsonlReader) open(ctx context.Context, cursor fileCursor) error {
	upload, err := s.staging.openWithTimeout(ctx, cursor.ID, s.storeTimeout)
	if err != nil {
		return fmt.Errorf("open import upload: %w", err)
	}
	opener := s.openWatch
	if opener == nil {
		opener = openWatchUpload
	}
	stream, err := opener(upload, cursor.Offset)
	if err != nil {
		return err
	}
	s.uploadID, s.offset, s.stream = cursor.ID, cursor.Offset, stream
	if bloomStream, ok := stream.(*bloomWatchStream); ok {
		s.expectedRecords, s.validateSummary = bloomStream.expectedRecords, true
	}
	s.buffered = bufio.NewReaderSize(stream, maxJSONLLineBytes+1)
	return nil
}

func (s *jsonlReader) Close() error {
	if s.stream == nil {
		return nil
	}
	err := s.stream.Close()
	s.stream, s.buffered = nil, nil
	return err
}

func openWatchUpload(upload *UploadReader, offset int64) (io.ReadCloser, error) {
	return openWatchUploadWith(upload, offset, func(entry *zip.File) (io.ReadCloser, error) {
		return entry.Open()
	})
}

func openWatchUploadWith(
	upload *UploadReader, offset int64, openEntry func(*zip.File) (io.ReadCloser, error),
) (io.ReadCloser, error) {
	header := make([]byte, len(zipSignature))
	_, readErr := upload.ReadAt(header, 0)
	if readErr != nil && !errors.Is(readErr, io.EOF) {
		return nil, fmt.Errorf("detect import upload: %w", readErr)
	}
	if !bytes.Equal(header, zipSignature) {
		if offset > upload.Size() {
			return nil, fmt.Errorf("seek import upload: %w", core.ErrInvalidArgument)
		}
		return io.NopCloser(io.NewSectionReader(upload, offset, upload.Size()-offset)), nil
	}
	if validationErr := validateZipDirectory(upload, upload.Size()); validationErr != nil {
		return nil, fmt.Errorf("validate import archive directory: %w", validationErr)
	}
	archive, err := zip.NewReader(upload, upload.Size())
	if err != nil {
		return nil, fmt.Errorf("open import archive: %w", err)
	}
	watches, expectedRecords, err := validateImportArchive(archive)
	if err != nil {
		return nil, err
	}
	watchesSize := int64(watches.UncompressedSize64) //nolint:gosec // Archive validation caps this below MaxInt64.
	if offset > watchesSize {
		return nil, fmt.Errorf("seek import upload: %w", core.ErrInvalidArgument)
	}
	if watches.Method == zip.Store {
		reader, openErr := openStoredWatchEntry(upload, watches, offset, watchesSize)
		return wrapBloomWatchStream(reader, expectedRecords, openErr)
	}
	reader, err := openEntry(watches)
	if err != nil {
		return nil, fmt.Errorf("open watches.jsonl: %w", err)
	}
	validated, err := validateOpenWatchEntry(reader)
	if err != nil {
		return nil, err
	}
	if err := discardUploadPrefix(validated, offset); err != nil {
		return nil, errors.Join(err, validated.Close())
	}
	return &bloomWatchStream{ReadCloser: validated, expectedRecords: expectedRecords}, nil
}

type bloomWatchStream struct {
	io.ReadCloser
	expectedRecords int64
}

func wrapBloomWatchStream(reader io.ReadCloser, expectedRecords int64, err error) (io.ReadCloser, error) {
	if err != nil {
		return nil, err
	}
	return &bloomWatchStream{ReadCloser: reader, expectedRecords: expectedRecords}, nil
}

func openStoredWatchEntry(
	upload *UploadReader, watches *zip.File, offset, size int64,
) (io.ReadCloser, error) {
	dataOffset, err := watches.DataOffset()
	if err != nil {
		return nil, fmt.Errorf("locate watches.jsonl: %w", err)
	}
	header := make([]byte, len(zipSignature))
	entry := io.NewSectionReader(upload, dataOffset, size)
	count, readErr := io.ReadFull(entry, header)
	if readErr != nil && !errors.Is(readErr, io.EOF) && !errors.Is(readErr, io.ErrUnexpectedEOF) {
		return nil, fmt.Errorf("inspect watches.jsonl: %w", readErr)
	}
	header = header[:count]
	if hasNestedZipSignature(header) {
		return nil, errors.New("nested import archives are not allowed")
	}
	return io.NopCloser(io.NewSectionReader(upload, dataOffset+offset, size-offset)), nil
}

type prefixedReadCloser struct {
	io.Reader
	closer io.Closer
}

func (r prefixedReadCloser) Close() error { return r.closer.Close() }

func validateOpenWatchEntry(reader io.ReadCloser) (io.ReadCloser, error) {
	header := make([]byte, len(zipSignature))
	count, err := io.ReadFull(reader, header)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return nil, errors.Join(fmt.Errorf("inspect watches.jsonl: %w", err), reader.Close())
	}
	header = header[:count]
	if hasNestedZipSignature(header) {
		return nil, errors.Join(errors.New("nested import archives are not allowed"), reader.Close())
	}
	return prefixedReadCloser{Reader: io.MultiReader(bytes.NewReader(header), reader), closer: reader}, nil
}

type zipDirectoryEnd struct {
	offset     int64
	records    uint64
	size       uint64
	needsZip64 bool
}

func validateZipDirectory(reader io.ReaderAt, size int64) error {
	end, err := readZipDirectoryEnd(reader, size)
	if err != nil {
		return err
	}
	zip64End, found, err := readZip64DirectoryEnd(reader, end.offset)
	if err != nil {
		return err
	}
	if end.needsZip64 != found {
		return errors.New("import archive has malformed ZIP64 metadata")
	}
	if found {
		end.records, end.size = zip64End.records, zip64End.size
	}
	if end.records == 0 || end.records > maxImportArchiveEntries || end.size > maxImportCentralDirectoryBytes {
		return errImportArchiveDirectoryBounds
	}
	return nil
}

func readZipDirectoryEnd(reader io.ReaderAt, size int64) (zipDirectoryEnd, error) {
	if size < zipEndRecordBytes {
		return zipDirectoryEnd{}, errors.New("import archive is missing its end record")
	}
	tailSize := min(size, int64(zipEndRecordBytes+math.MaxUint16))
	tail := make([]byte, int(tailSize))
	if err := readZipBytesAt(reader, tail, size-tailSize); err != nil {
		return zipDirectoryEnd{}, err
	}
	index := findZipEndRecord(tail)
	if index < 0 {
		return zipDirectoryEnd{}, errors.New("import archive has a malformed end record")
	}
	record := tail[index : index+zipEndRecordBytes]
	if binary.LittleEndian.Uint16(record[4:6]) != 0 || binary.LittleEndian.Uint16(record[6:8]) != 0 {
		return zipDirectoryEnd{}, errors.New("multi-disk import archives are not supported")
	}
	recordsThisDisk := binary.LittleEndian.Uint16(record[8:10])
	records := binary.LittleEndian.Uint16(record[10:12])
	if recordsThisDisk != records {
		return zipDirectoryEnd{}, errors.New("import archive has inconsistent entry counts")
	}
	directorySize := binary.LittleEndian.Uint32(record[12:16])
	directoryOffset := binary.LittleEndian.Uint32(record[16:20])
	return zipDirectoryEnd{
		offset: size - tailSize + int64(index), records: uint64(records), size: uint64(directorySize),
		needsZip64: records == math.MaxUint16 || directorySize == math.MaxUint32 || directoryOffset == math.MaxUint32,
	}, nil
}

func findZipEndRecord(tail []byte) int {
	for index := len(tail) - zipEndRecordBytes; index >= 0; index-- {
		if binary.LittleEndian.Uint32(tail[index:index+4]) != zipEndSignature {
			continue
		}
		commentSize := int(binary.LittleEndian.Uint16(tail[index+20 : index+22]))
		if index+zipEndRecordBytes+commentSize == len(tail) {
			return index
		}
	}
	return -1
}

func readZip64DirectoryEnd(
	reader io.ReaderAt, endOffset int64,
) (zipDirectoryEnd, bool, error) {
	locatorOffset := endOffset - zip64LocatorBytes
	if locatorOffset < 0 {
		return zipDirectoryEnd{}, false, nil
	}
	locator := make([]byte, zip64LocatorBytes)
	if err := readZipBytesAt(reader, locator, locatorOffset); err != nil {
		return zipDirectoryEnd{}, false, err
	}
	if binary.LittleEndian.Uint32(locator[0:4]) != zip64LocatorSignature {
		return zipDirectoryEnd{}, false, nil
	}
	if binary.LittleEndian.Uint32(locator[4:8]) != 0 || binary.LittleEndian.Uint32(locator[16:20]) != 1 {
		return zipDirectoryEnd{}, false, errors.New("import archive has malformed ZIP64 locator")
	}
	recordOffset := binary.LittleEndian.Uint64(locator[8:16])
	return parseZip64DirectoryEnd(reader, recordOffset, uint64(locatorOffset))
}

func parseZip64DirectoryEnd(
	reader io.ReaderAt, recordOffset, locatorOffset uint64,
) (zipDirectoryEnd, bool, error) {
	if recordOffset > math.MaxInt64 {
		return zipDirectoryEnd{}, false, errors.New("import archive has invalid ZIP64 record offset")
	}
	record := make([]byte, zip64EndRecordBytes)
	if err := readZipBytesAt(reader, record, int64(recordOffset)); err != nil {
		return zipDirectoryEnd{}, false, err
	}
	if binary.LittleEndian.Uint32(record[0:4]) != zip64EndSignature {
		return zipDirectoryEnd{}, false, errors.New("import archive has malformed ZIP64 end record")
	}
	recordSize := binary.LittleEndian.Uint64(record[4:12])
	if recordSize < zip64EndRecordBytes-12 || recordSize > maxImportCentralDirectoryBytes {
		return zipDirectoryEnd{}, false, errors.New("import archive has invalid ZIP64 record size")
	}
	if recordOffset+12+recordSize != locatorOffset {
		return zipDirectoryEnd{}, false, errors.New("import archive has misplaced ZIP64 end record")
	}
	if binary.LittleEndian.Uint32(record[16:20]) != 0 || binary.LittleEndian.Uint32(record[20:24]) != 0 {
		return zipDirectoryEnd{}, false, errors.New("multi-disk ZIP64 import archives are not supported")
	}
	recordsThisDisk := binary.LittleEndian.Uint64(record[24:32])
	records := binary.LittleEndian.Uint64(record[32:40])
	if recordsThisDisk != records {
		return zipDirectoryEnd{}, false, errors.New("import archive has inconsistent ZIP64 entry counts")
	}
	return zipDirectoryEnd{records: records, size: binary.LittleEndian.Uint64(record[40:48])}, true, nil
}

func readZipBytesAt(reader io.ReaderAt, destination []byte, offset int64) error {
	read, err := reader.ReadAt(destination, offset)
	if err != nil && (!errors.Is(err, io.EOF) || read != len(destination)) {
		return fmt.Errorf("read import archive metadata: %w", err)
	}
	if read != len(destination) {
		return errors.New("import archive metadata is truncated")
	}
	return nil
}

func validateImportArchive(archive *zip.Reader) (*zip.File, int64, error) {
	if len(archive.File) == 0 || len(archive.File) > maxImportArchiveEntries {
		return nil, 0, errors.New("import archive has an invalid entry count")
	}
	var watches, manifest, summary *zip.File
	for _, entry := range archive.File {
		if entry.UncompressedSize64 > importArchiveEntryLimit(entry.Name) {
			return nil, 0, errors.New("import archive entry exceeds size limit")
		}
		if entry.Name != "watches.jsonl" {
			if err := validateImportArchiveEntry(entry); err != nil {
				return nil, 0, err
			}
		}
		switch entry.Name {
		case "manifest.json":
			if manifest != nil {
				return nil, 0, errors.New("import archive repeats manifest.json")
			}
			manifest = entry
		case "summary.json":
			if summary != nil {
				return nil, 0, errors.New("import archive repeats summary.json")
			}
			summary = entry
		case "watches.jsonl":
			if watches != nil {
				return nil, 0, errors.New("import archive repeats watches.jsonl")
			}
			watches = entry
		}
	}
	if watches == nil || manifest == nil || summary == nil {
		return nil, 0, errors.New("import archive requires manifest.json, watches.jsonl, and summary.json")
	}
	expectedRecords, err := readBloomExportSummary(summary)
	return watches, expectedRecords, err
}

func readBloomExportSummary(entry *zip.File) (records int64, result error) {
	reader, err := entry.Open()
	if err != nil {
		return 0, fmt.Errorf("open summary.json: %w", err)
	}
	defer func() { result = errors.Join(result, reader.Close()) }()
	var summary bloomExportSummary
	decoder := json.NewDecoder(io.LimitReader(reader, maxImportMetadataBytes+1))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&summary); err != nil {
		return 0, fmt.Errorf("decode summary.json: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return 0, errors.New("decode summary.json: trailing data")
	}
	if summary.WatchRecords == nil || *summary.WatchRecords < 0 || summary.ImportRecords < 0 ||
		summary.WatchesBytes < 0 || summary.ImportsBytes < 0 {
		return 0, errors.New("summary.json contains missing or negative counters")
	}
	return *summary.WatchRecords, nil
}

func validateImportArchiveEntry(entry *zip.File) error {
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
	ctx context.Context, reader *bufio.Reader, offset int64,
) ([]core.ImportedWatch, int64, int64, bool, error) {
	records := make([]core.ImportedWatch, 0, core.ImportBatchSize)
	var skipped int64
	for range core.ImportBatchSize {
		if err := ctx.Err(); err != nil {
			return nil, offset, skipped, false, err
		}
		line, err := reader.ReadSlice('\n')
		offset += int64(len(line))
		if len(line) > maxJSONLLineBytes {
			return nil, offset, skipped, false, errors.New("JSONL line exceeds size limit")
		}
		if len(bytes.TrimSpace(line)) > 0 {
			record, decodeErr := DecodeWatchJSONL(line)
			if decodeErr != nil {
				return nil, offset, skipped, false, decodeErr
			}
			records = append(records, record)
		} else if len(line) > 0 {
			skipped++
		}
		if errors.Is(err, io.EOF) {
			return records, offset, skipped, true, nil
		}
		if err != nil {
			return nil, offset, skipped, false, fmt.Errorf("read import upload: %w", err)
		}
	}
	return records, offset, skipped, false, nil
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
