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
	"math"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/BonzTM/bloom/internal/core"
)

func TestWatchJSONLRoundTripUsesOriginalWatchID(t *testing.T) {
	started := core.NormalizeTime(time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC))
	ended := started.Add(95 * time.Second)
	runtime := 42 * time.Minute
	season, episode, direct := int32(2), int32(7), true
	watch := core.PlaybackWatch{
		ID:            "11111111-1111-4111-8111-111111111111",
		MediaServerID: "22222222-2222-4222-8222-222222222222", MediaServerName: "Jellyfin",
		MediaUserID: "user-1", Username: "Alice", DeviceID: "device-1", DeviceName: "TV",
		Client: "Web", ItemID: "item-1", ItemName: "Episode", ItemType: "Episode",
		SeriesName: "Series", LibraryID: "library-1", LibraryName: "Shows",
		SeasonNumber: &season, EpisodeNumber: &episode, LastPosition: 45 * time.Second,
		PlayMethod: core.PlayMethodDirectPlay, State: core.WatchStopped,
		Stream: &core.StreamDetails{
			Container: "mkv", VideoCodec: "h264", AudioCodec: "aac", Bitrate: 1234,
			Width: 1920, Height: 1080, Framerate: 24, AudioChannels: 2,
			IsVideoDirect: &direct, IsAudioDirect: &direct, TranscodeReasons: []string{"ContainerBitrateExceedsLimit"},
		},
		Runtime: &runtime, Source: core.WatchSourceImport, ImportSource: core.ImportSourceJellystat,
		ImportRecordID: "activity-77", ImportOriginRecordID: "77",
		ActiveTime: 90 * time.Second, StartedAt: started, EndedAt: &ended,
	}
	var output bytes.Buffer
	if err := EncodeWatchJSONL(&output, watch); err != nil {
		t.Fatalf("EncodeWatchJSONL: %v", err)
	}
	record, err := DecodeWatchJSONL(output.Bytes())
	if err != nil {
		t.Fatalf("decodeJSONLRecord: %v", err)
	}
	if record.RecordID != watch.ID || record.Duration != watch.ActiveTime || record.Username != watch.Username ||
		record.DeviceID != watch.DeviceID || record.SeriesName != watch.SeriesName ||
		record.LibraryID != watch.LibraryID || record.LibraryName != watch.LibraryName ||
		record.SeasonNumber == nil || *record.SeasonNumber != season ||
		record.EpisodeNumber == nil || *record.EpisodeNumber != episode ||
		record.LastPosition != watch.LastPosition || !reflect.DeepEqual(record.Stream, watch.Stream) ||
		record.Runtime == nil || *record.Runtime != runtime ||
		record.EndedAt == nil || !record.EndedAt.Equal(ended) || record.OriginRecordID != "77" {
		t.Fatalf("round trip = %+v", record)
	}
	var wire bloomExportRecord
	if err := json.Unmarshal(output.Bytes(), &wire); err != nil || wire.RuntimeMS == nil || *wire.RuntimeMS != 2520000 {
		t.Fatalf("exported runtime = %v, %v", wire.RuntimeMS, err)
	}
}

func TestWatchJSONLRejectsSchemaDriftAndControlCharacters(t *testing.T) {
	base := validJSONLFixture()
	for _, line := range []string{
		strings.Replace(base, `"item_name":"Film"`, `"item_name":"bad\nname"`, 1),
		strings.TrimSuffix(base, "}") + `,"extra":true}`,
		base + ` {}`,
		strings.Replace(base, `"ended_at":"2026-09-25T12:01:00Z"`, `"ended_at":null`, 1),
		strings.Replace(base, `"position_ms":0`, `"position_ms":9223372036854775807`, 1),
		strings.Replace(base, `"runtime_ms":null`, `"runtime_ms":9223372036855`, 1),
		strings.TrimSuffix(base, "}") + `,"import_origin_record_id":"` +
			strings.Repeat("x", core.MaxImportOriginRecordIDBytes+1) + `"}`,
	} {
		if _, err := DecodeWatchJSONL([]byte(line)); err == nil {
			t.Errorf("DecodeWatchJSONL accepted %s", line)
		}
	}
}

func TestReadJSONLLinesCountsBlankLinesWithoutEndingBatch(t *testing.T) {
	input := validJSONLFixture() + "\n\n" + validJSONLFixture() + "\n"
	reader := bufio.NewReaderSize(strings.NewReader(input), maxJSONLLineBytes+1)
	records, offset, skipped, _, err := readJSONLLines(context.Background(), reader, 0)
	if err != nil || len(records) != 2 || skipped != 1 || offset != int64(len(input)) {
		t.Fatalf("readJSONLLines = %d records, offset %d, skipped %d, %v", len(records), offset, skipped, err)
	}
}

func TestBloomUploadImportsValidThreeEntryExport(t *testing.T) {
	staging := newJSONLTestStaging(t)
	payload := zipFixture(t, map[string]string{
		"manifest.json": `{}`,
		"watches.jsonl": validJSONLFixture() + "\n",
		"summary.json":  `{"watch_records":1,"import_records":0}`,
	})
	id, err := staging.stage(t.Context(), bytes.NewReader(payload), staticClock{time.Now()})
	if err != nil {
		t.Fatalf("stage zip: %v", err)
	}
	cursor, err := encodeFileCursor(fileCursor{ID: id})
	if err != nil {
		t.Fatalf("encode cursor: %v", err)
	}
	records, _, _, err := (&jsonlReader{staging: staging}).ReadImportBatch(t.Context(), core.ImportJob{Cursor: cursor})
	if err != nil || len(records) != 1 || records[0].RecordID == "" {
		t.Fatalf("ReadImportBatch = %+v, %v", records, err)
	}
}

func TestBloomUploadRejectsUnderdeclaredCentralDirectory(t *testing.T) {
	if err := openStagedWatch(t, underdeclaredCentralDirectoryFixture()); err == nil {
		t.Fatal("openWatchUpload accepted an underdeclared central directory")
	}
}

func TestBloomUploadRejectsUnsafeZipShapes(t *testing.T) {
	tests := map[string][]byte{
		"missing watches": zipFixture(t, map[string]string{"manifest.json": `{}`}),
		"missing manifest": zipFixture(t, map[string]string{
			"watches.jsonl": validJSONLFixture() + "\n", "summary.json": `{"watch_records":1}`,
		}),
		"missing summary": zipFixture(t, map[string]string{
			"manifest.json": `{}`, "watches.jsonl": validJSONLFixture() + "\n",
		}),
		"nested archive": zipFixture(t, map[string]string{
			"watches.jsonl": validJSONLFixture() + "\n",
			"nested.bin":    string(zipFixture(t, map[string]string{"inside": "data"})),
		}),
		"empty nested archive": zipFixture(t, map[string]string{
			"watches.jsonl": validJSONLFixture() + "\n",
			"nested.bin":    string(zipFixture(t, map[string]string{})),
		}),
		"too many entries": tooManyZipEntries(t),
		"oversized entry":  oversizedZipEntry(t),
	}
	for name, payload := range tests {
		t.Run(name, func(t *testing.T) {
			staging := newJSONLTestStaging(t)
			id, err := staging.stage(t.Context(), bytes.NewReader(payload), staticClock{time.Now()})
			if err != nil {
				t.Fatalf("stage zip: %v", err)
			}
			cursor, err := encodeFileCursor(fileCursor{ID: id})
			if err != nil {
				t.Fatalf("encode cursor: %v", err)
			}
			if _, _, _, err := (&jsonlReader{staging: staging}).ReadImportBatch(
				t.Context(), core.ImportJob{Cursor: cursor},
			); err == nil {
				t.Fatal("ReadImportBatch accepted unsafe zip")
			}
		})
	}
}

func TestBloomUploadBoundsCentralDirectoryBeforeZipReader(t *testing.T) {
	fixture := zipFixture(t, map[string]string{
		"manifest.json": `{}`, "watches.jsonl": validJSONLFixture() + "\n",
		"summary.json": `{"watch_records":1}`,
	})
	tests := []struct {
		name          string
		records       uint16
		directorySize uint32
	}{
		{name: "huge declared entry count", records: 60_000},
		{name: "oversized declared directory", directorySize: maxImportCentralDirectoryBytes + 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			payload := bytes.Clone(fixture)
			end := bytes.LastIndex(payload, []byte{'P', 'K', 0x05, 0x06})
			if end < 0 {
				t.Fatal("fixture does not contain an end-of-central-directory record")
			}
			if test.records != 0 {
				binary.LittleEndian.PutUint16(payload[end+8:end+10], test.records)
				binary.LittleEndian.PutUint16(payload[end+10:end+12], test.records)
			}
			if test.directorySize != 0 {
				binary.LittleEndian.PutUint32(payload[end+12:end+16], test.directorySize)
			}
			if err := openStagedWatch(t, payload); !errors.Is(err, errImportArchiveDirectoryBounds) {
				t.Fatalf("openWatchUpload = %v, want directory bound", err)
			}
		})
	}
}

func openStagedWatch(t *testing.T, payload []byte) error {
	t.Helper()
	staging := newJSONLTestStaging(t)
	id, err := staging.stage(t.Context(), bytes.NewReader(payload), staticClock{time.Now()})
	if err != nil {
		t.Fatalf("stage zip: %v", err)
	}
	upload, err := staging.open(t.Context(), id)
	if err != nil {
		t.Fatalf("open staged zip: %v", err)
	}
	_, err = openWatchUpload(upload, 0)
	return err
}

func TestBloomUploadRejectsMalformedAndOversizedZip64Records(t *testing.T) {
	tests := map[string][]byte{
		"missing locator":          zip64EndFixture(t, false, 44),
		"oversized record":         zip64EndFixture(t, true, maxImportCentralDirectoryBytes+1),
		"bogus directory interval": bogusZip64DirectoryFixture(t),
	}
	for name, payload := range tests {
		t.Run(name, func(t *testing.T) {
			if err := validateZipDirectory(payloadReaderAt(payload), int64(len(payload))); err == nil {
				t.Fatal("validateZipDirectory accepted invalid ZIP64 metadata")
			}
		})
	}
}

func underdeclaredCentralDirectoryFixture() []byte {
	const (
		localHeaderBytes = 30
		headerCount      = math.MaxUint16 + 2
	)
	payload := make([]byte, localHeaderBytes+headerCount*zipDirectoryHeaderBytes+zipEndRecordBytes)
	binary.LittleEndian.PutUint32(payload[0:4], 0x04034b50)
	for index := range headerCount {
		offset := localHeaderBytes + index*zipDirectoryHeaderBytes
		binary.LittleEndian.PutUint32(payload[offset:offset+4], zipDirectoryHeaderSignature)
	}
	end := len(payload) - zipEndRecordBytes
	binary.LittleEndian.PutUint32(payload[end:end+4], zipEndSignature)
	binary.LittleEndian.PutUint16(payload[end+8:end+10], 1)
	binary.LittleEndian.PutUint16(payload[end+10:end+12], 1)
	binary.LittleEndian.PutUint32(payload[end+12:end+16], zipDirectoryHeaderBytes)
	binary.LittleEndian.PutUint32(payload[end+16:end+20], localHeaderBytes)
	return payload
}

func bogusZip64DirectoryFixture(t *testing.T) []byte {
	t.Helper()
	payload := zipFixture(t, map[string]string{
		"manifest.json": `{}`, "watches.jsonl": validJSONLFixture() + "\n",
		"summary.json": `{"watch_records":1}`,
	})
	end := bytes.LastIndex(payload, []byte{'P', 'K', 0x05, 0x06})
	if end < 0 {
		t.Fatal("fixture does not contain an end-of-central-directory record")
	}
	directorySize := binary.LittleEndian.Uint32(payload[end+12 : end+16])
	directoryOffset := binary.LittleEndian.Uint32(payload[end+16 : end+20])
	record := make([]byte, zip64EndRecordBytes)
	binary.LittleEndian.PutUint32(record[0:4], zip64EndSignature)
	binary.LittleEndian.PutUint64(record[4:12], zip64EndRecordBytes-12)
	binary.LittleEndian.PutUint64(record[24:32], 3)
	binary.LittleEndian.PutUint64(record[32:40], 3)
	binary.LittleEndian.PutUint64(record[40:48], uint64(directorySize-1))
	binary.LittleEndian.PutUint64(record[48:56], uint64(directoryOffset))
	locator := make([]byte, zip64LocatorBytes)
	binary.LittleEndian.PutUint32(locator[0:4], zip64LocatorSignature)
	binary.LittleEndian.PutUint64(locator[8:16], uint64(end))
	binary.LittleEndian.PutUint32(locator[16:20], 1)
	result := append(bytes.Clone(payload[:end]), record...)
	result = append(result, locator...)
	legacyEnd := bytes.Clone(payload[end:])
	binary.LittleEndian.PutUint16(legacyEnd[8:10], math.MaxUint16)
	binary.LittleEndian.PutUint16(legacyEnd[10:12], math.MaxUint16)
	binary.LittleEndian.PutUint32(legacyEnd[12:16], math.MaxUint32)
	binary.LittleEndian.PutUint32(legacyEnd[16:20], math.MaxUint32)
	return append(result, legacyEnd...)
}

type payloadReaderAt []byte

func (p payloadReaderAt) ReadAt(buffer []byte, offset int64) (int, error) {
	return bytes.NewReader(p).ReadAt(buffer, offset)
}

func zip64EndFixture(t *testing.T, locator bool, recordSize uint64) []byte {
	t.Helper()
	payload := make([]byte, zip64EndRecordBytes)
	binary.LittleEndian.PutUint32(payload[0:4], zip64EndSignature)
	binary.LittleEndian.PutUint64(payload[4:12], recordSize)
	if locator {
		locatorRecord := make([]byte, zip64LocatorBytes)
		binary.LittleEndian.PutUint32(locatorRecord[0:4], zip64LocatorSignature)
		binary.LittleEndian.PutUint32(locatorRecord[16:20], 1)
		payload = append(payload, locatorRecord...)
	}
	end := make([]byte, zipEndRecordBytes)
	binary.LittleEndian.PutUint32(end[0:4], zipEndSignature)
	binary.LittleEndian.PutUint16(end[8:10], math.MaxUint16)
	binary.LittleEndian.PutUint16(end[10:12], math.MaxUint16)
	binary.LittleEndian.PutUint32(end[12:16], math.MaxUint32)
	binary.LittleEndian.PutUint32(end[16:20], math.MaxUint32)
	return append(payload, end...)
}

func tooManyZipEntries(t *testing.T) []byte {
	t.Helper()
	entries := make(map[string]string, maxImportArchiveEntries+1)
	entries["watches.jsonl"] = validJSONLFixture() + "\n"
	for index := range maxImportArchiveEntries {
		entries[fmt.Sprintf("entry-%d", index)] = "value"
	}
	return zipFixture(t, entries)
}

func oversizedZipEntry(t *testing.T) []byte {
	t.Helper()
	var output bytes.Buffer
	writer := zip.NewWriter(&output)
	header := &zip.FileHeader{Name: "watches.jsonl", Method: zip.Store}
	header.UncompressedSize64 = core.MaxImportUploadBytes + 1
	header.CompressedSize64 = header.UncompressedSize64
	if _, err := writer.CreateRaw(header); err != nil {
		t.Fatalf("create oversized raw entry: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close oversized archive: %v", err)
	}
	return output.Bytes()
}

func newJSONLTestStaging(t *testing.T) *Staging {
	t.Helper()
	staging, err := NewStaging(newMemoryUploadStore(), 10*time.Minute)
	if err != nil {
		t.Fatalf("NewStaging: %v", err)
	}
	return staging
}

func zipFixture(t *testing.T, entries map[string]string) []byte {
	t.Helper()
	var output bytes.Buffer
	writer := zip.NewWriter(&output)
	for name, value := range entries {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatalf("create zip entry: %v", err)
		}
		if _, err := entry.Write([]byte(value)); err != nil {
			t.Fatalf("write zip entry: %v", err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	return output.Bytes()
}

func validJSONLFixture() string {
	return `{"id":"11111111-1111-4111-8111-111111111111",` +
		`"media_server_id":"22222222-2222-4222-8222-222222222222","media_server_name":"Jellyfin",` +
		`"media_user_id":"user","username":"Alice","device_id":"","device_name":"TV",` +
		`"client":"Web","item_id":"item","item_name":"Film","item_type":"Movie","series_name":"",` +
		`"library_id":"","library_name":"","season_number":null,"episode_number":null,` +
		`"position_ms":0,"runtime_ms":null,"paused":false,"play_method":"direct_play","source":"poll",` +
		`"active_seconds":60,"started_at":"2026-09-25T12:00:00Z","ended_at":"2026-09-25T12:01:00Z"}`
}
