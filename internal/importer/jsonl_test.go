package importer

import (
	"bytes"
	"context"
	"encoding/json"
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
		Runtime: &runtime, Source: core.WatchSourcePoll, ActiveTime: 90 * time.Second, StartedAt: started, EndedAt: &ended,
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
		record.EndedAt == nil || !record.EndedAt.Equal(ended) {
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
	} {
		if _, err := DecodeWatchJSONL([]byte(line)); err == nil {
			t.Errorf("DecodeWatchJSONL accepted %s", line)
		}
	}
}

func TestReadJSONLLinesCountsBlankLinesWithoutEndingBatch(t *testing.T) {
	input := validJSONLFixture() + "\n\n" + validJSONLFixture() + "\n"
	records, offset, skipped, err := readJSONLLines(context.Background(), strings.NewReader(input), 0)
	if err != nil || len(records) != 2 || skipped != 1 || offset != int64(len(input)) {
		t.Fatalf("readJSONLLines = %d records, offset %d, skipped %d, %v", len(records), offset, skipped, err)
	}
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
