package jellyfin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/BonzTM/bloom/internal/core"
	jellyfinapi "github.com/BonzTM/bloom/internal/mediaserver/jellyfin/api"
)

const validSessionJSON = `[{"Id":"session-1","UserId":"11111111-1111-4111-8111-111111111111","UserName":"alice","DeviceId":"device-1","DeviceName":"TV","Client":"Jellyfin Web","LastActivityDate":"2026-09-23T12:00:00Z","NowPlayingItem":{"Id":"22222222-2222-4222-8222-222222222222","Name":"Pilot","Type":"Episode","SeriesName":"Show","ParentIndexNumber":1,"IndexNumber":2},"PlayState":{"PositionTicks":12340000,"IsPaused":false,"PlayMethod":"DirectStream"}},{"Id":"idle"}]`

func TestListSessionsMapsPlayingItemsAndDropsIdleSessions(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("activeWithinSeconds"); got != "60" {
			t.Errorf("activeWithinSeconds = %q, want 60", got)
		}
		_, _ = fmt.Fprint(w, validSessionJSON)
	}))
	defer server.Close()
	client := newTestClient(t, server, nil)
	sessions, err := client.ListSessions(context.Background())
	if err != nil || len(sessions) != 1 {
		t.Fatalf("ListSessions = %+v, %v", sessions, err)
	}
	got := sessions[0]
	if got.Position != 1234*time.Millisecond || got.PlayMethod != core.PlayMethodDirectStream ||
		got.SeriesName != "Show" || got.SeasonNumber == nil || *got.SeasonNumber != 1 ||
		got.EpisodeNumber == nil || *got.EpisodeNumber != 2 {
		t.Fatalf("mapped session = %+v", got)
	}
}

func TestListSessionsRejectsMalformedTicks(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, `[{"UserId":"11111111-1111-4111-8111-111111111111","DeviceId":"device","LastActivityDate":"2026-09-23T12:00:00Z","NowPlayingItem":{"Id":"22222222-2222-4222-8222-222222222222","Name":"Movie","Type":"Movie"},"PlayState":{"PositionTicks":-1}}]`)
	}))
	defer server.Close()
	client := newTestClient(t, server, nil)
	_, err := client.ListSessions(context.Background())
	assertMediaError(t, err, core.MediaServerMalformed)
}

func TestListSessionsRejectsPositiveTickOverflow(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, `[{"UserId":"11111111-1111-4111-8111-111111111111","DeviceId":"device","LastActivityDate":"2026-09-23T12:00:00Z","NowPlayingItem":{"Id":"22222222-2222-4222-8222-222222222222","Name":"Movie","Type":"Movie"},"PlayState":{"PositionTicks":92233720368547759}}]`)
	}))
	defer server.Close()
	client := newTestClient(t, server, nil)
	_, err := client.ListSessions(context.Background())
	assertMediaError(t, err, core.MediaServerMalformed)
}

func TestListSessionsMapsTranscodeStreamDetails(t *testing.T) {
	body := `[{"Id":"session-1","UserId":"11111111-1111-4111-8111-111111111111","DeviceId":"device","LastActivityDate":"2026-09-23T12:00:00Z","NowPlayingItem":{"Id":"22222222-2222-4222-8222-222222222222","Name":"Movie","Type":"Movie"},"PlayState":{"PlayMethod":"Transcode"},"TranscodingInfo":{"Container":"ts","VideoCodec":"h264","AudioCodec":"aac","Bitrate":8000000,"Width":1920,"Height":1080,"Framerate":23.976,"AudioChannels":6,"IsVideoDirect":false,"IsAudioDirect":true,"TranscodeReasons":["VideoCodecNotSupported","FutureReason"]}}]`
	sessions := listSessionFixture(t, body)
	if len(sessions) != 1 || sessions[0].Stream == nil {
		t.Fatalf("sessions = %+v", sessions)
	}
	stream := sessions[0].Stream
	if stream.Container != "ts" || stream.VideoCodec != "h264" || stream.AudioCodec != "aac" ||
		stream.Bitrate != 8_000_000 || stream.Framerate != 23.98 ||
		stream.IsVideoDirect == nil || *stream.IsVideoDirect ||
		stream.IsAudioDirect == nil || !*stream.IsAudioDirect ||
		len(stream.TranscodeReasons) != 2 || stream.TranscodeReasons[1] != "FutureReason" {
		t.Fatalf("stream = %+v", stream)
	}
}

func TestListSessionsMapsDirectPlayMediaStreams(t *testing.T) {
	body := `[{"Id":"session-1","UserId":"11111111-1111-4111-8111-111111111111","DeviceId":"device","LastActivityDate":"2026-09-23T12:00:00Z","NowPlayingItem":{"Id":"22222222-2222-4222-8222-222222222222","Name":"Movie","Type":"Movie","Container":"mkv","MediaStreams":[{"Type":"Video","Index":0,"Codec":"hevc","BitRate":7500000,"Width":3840,"Height":2160,"AverageFrameRate":20.06},{"Type":"Audio","Index":1,"Codec":"aac","Channels":2,"IsDefault":false},{"Type":"Audio","Index":2,"Codec":"opus","Channels":6,"IsDefault":true}]},"PlayState":{"PlayMethod":"DirectPlay","AudioStreamIndex":99}}]`
	sessions := listSessionFixture(t, body)
	stream := sessions[0].Stream
	if stream == nil || stream.Container != "mkv" || stream.VideoCodec != "hevc" ||
		stream.AudioCodec != "opus" || stream.Bitrate != 7_500_000 ||
		stream.Width != 3840 || stream.Height != 2160 || stream.Framerate != 20.06 ||
		stream.AudioChannels != 6 || stream.IsVideoDirect != nil || stream.IsAudioDirect != nil ||
		len(stream.TranscodeReasons) != 0 {
		t.Fatalf("direct-play stream = %+v", stream)
	}
}

func TestListSessionsSelectedAudioTrackChangeCreatesSample(t *testing.T) {
	first := listSessionFixture(t, directPlaySessionBody(1))
	second := listSessionFixture(t, directPlaySessionBody(2))
	tracker, err := core.NewPlaybackTracker("server", core.PlaybackTrackerConfig{
		MissedPolls: 3, ResumeWindow: time.Minute,
	}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	newID := func() (string, error) { return "33333333-3333-4333-8333-333333333333", nil }
	now := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)
	mutations, err := tracker.Observe(now, core.WatchSourcePoll, first, newID)
	if err != nil || len(mutations) != 1 || mutations[0].Position == nil ||
		mutations[0].Position.Stream == nil || mutations[0].Position.Stream.AudioCodec != "aac" {
		t.Fatalf("first observation = %+v, %v", mutations, err)
	}
	mutations, err = tracker.Observe(now.Add(time.Second), core.WatchSourcePoll, second, newID)
	if err != nil || len(mutations) != 1 || mutations[0].Position == nil ||
		mutations[0].Position.Stream == nil || mutations[0].Position.Stream.AudioCodec != "opus" {
		t.Fatalf("switched observation = %+v, %v", mutations, err)
	}
}

func directPlaySessionBody(audioIndex int) string {
	return fmt.Sprintf(`[{"Id":"session-1","UserId":"11111111-1111-4111-8111-111111111111","DeviceId":"device","LastActivityDate":"2026-09-23T12:00:00Z","NowPlayingItem":{"Id":"22222222-2222-4222-8222-222222222222","Name":"Movie","Type":"Movie","Container":"mkv","MediaStreams":[{"Type":"Video","Index":0,"Codec":"hevc"},{"Type":"Audio","Index":1,"Codec":"aac","IsDefault":true},{"Type":"Audio","Index":2,"Codec":"opus","IsDefault":false}]},"PlayState":{"PlayMethod":"DirectPlay","AudioStreamIndex":%d}}]`, audioIndex)
}

func TestListSessionsRoundsFrameratesToHundredths(t *testing.T) {
	tests := []struct {
		framerate float64
		want      float64
	}{
		{framerate: 20.06, want: 20.06},
		{framerate: 29.97, want: 29.97},
		{framerate: 23.976, want: 23.98},
	}
	for _, testCase := range tests {
		body := fmt.Sprintf(`[{"Id":"session-1","UserId":"11111111-1111-4111-8111-111111111111","DeviceId":"device","LastActivityDate":"2026-09-23T12:00:00Z","NowPlayingItem":{"Id":"22222222-2222-4222-8222-222222222222","Name":"Movie","Type":"Movie"},"PlayState":{"PlayMethod":"Transcode"},"TranscodingInfo":{"Framerate":%v}}]`, testCase.framerate)
		stream := listSessionFixture(t, body)[0].Stream
		if stream == nil || stream.Framerate != testCase.want {
			t.Errorf("framerate %v mapped to %+v, want %v", testCase.framerate, stream, testCase.want)
		}
	}
}

func TestListSessionsRejectsMalformedStreamDetails(t *testing.T) {
	base := `[{"Id":"session-1","UserId":"11111111-1111-4111-8111-111111111111","DeviceId":"device","LastActivityDate":"2026-09-23T12:00:00Z","NowPlayingItem":{"Id":"22222222-2222-4222-8222-222222222222","Name":"Movie","Type":"Movie"},"PlayState":{"PlayMethod":"Transcode"},"TranscodingInfo":%s}]`
	tests := []string{
		`{"Container":"bad\u000a"}`,
		`{"VideoCodec":"` + strings.Repeat("x", core.MaxStreamTextBytes+1) + `"}`,
		`{"Bitrate":-1}`,
		`{"Framerate":1001}`,
		`{"TranscodeReasons":["one","two","three","four","five","six","seven","eight","nine","ten","eleven","twelve","thirteen","fourteen","fifteen","sixteen","seventeen"]}`,
	}
	for _, details := range tests {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = fmt.Fprintf(w, base, details)
		}))
		_, err := newTestClient(t, server, nil).ListSessions(t.Context())
		server.Close()
		assertMediaError(t, err, core.MediaServerMalformed)
	}
}

func TestListSessionsRejectsMalformedDirectPlayStreamDetails(t *testing.T) {
	tests := []struct {
		name   string
		fields string
	}{
		{name: "container control", fields: `"Container":"bad\u0085"`},
		{name: "codec length", fields: `"Container":"mkv","MediaStreams":[{"Type":"Video","Codec":"` +
			strings.Repeat("x", core.MaxStreamTextBytes+1) + `"}]`},
		{name: "negative bitrate", fields: `"MediaStreams":[{"Type":"Video","BitRate":-1}]`},
		{name: "width", fields: `"MediaStreams":[{"Type":"Video","Width":65536}]`},
		{name: "framerate", fields: `"MediaStreams":[{"Type":"Video","AverageFrameRate":1001}]`},
		{name: "channels", fields: `"MediaStreams":[{"Type":"Audio","Index":1,"IsDefault":true,"Channels":65}]`},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			body := `[{"Id":"session-1","UserId":"11111111-1111-4111-8111-111111111111","DeviceId":"device","LastActivityDate":"2026-09-23T12:00:00Z","NowPlayingItem":{"Id":"22222222-2222-4222-8222-222222222222","Name":"Movie","Type":"Movie",` + testCase.fields + `},"PlayState":{"PlayMethod":"DirectPlay"}}]`
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = fmt.Fprint(w, body)
			}))
			_, err := newTestClient(t, server, nil).ListSessions(t.Context())
			server.Close()
			assertMediaError(t, err, core.MediaServerMalformed)
		})
	}
}

func listSessionFixture(t *testing.T, body string) []core.PlaybackSession {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, body)
	}))
	t.Cleanup(server.Close)
	sessions, err := newTestClient(t, server, nil).ListSessions(t.Context())
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	return sessions
}

func TestListSessionsRetriesTransientServerFailure(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()
	client := newTestClient(t, server, nil)
	_, err := client.ListSessions(context.Background())
	assertMediaError(t, err, core.MediaServerUnavailable)
	if calls.Load() != maxAttempts {
		t.Fatalf("calls = %d, want %d", calls.Load(), maxAttempts)
	}
}

func TestResolveLibrarySelectsCollectionFolderAncestor(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.EscapedPath() != "/Items/item%2Fwith%20space/Ancestors" {
			t.Errorf("path = %q", r.URL.EscapedPath())
		}
		_, _ = fmt.Fprint(w, `[{"Id":"11111111-1111-4111-8111-111111111111","Name":"Box","Type":"BoxSet"},{"Id":"22222222-2222-4222-8222-222222222222","Name":"Movies","Type":"CollectionFolder"}]`)
	}))
	defer server.Close()
	library, found, err := newTestClient(t, server, nil).ResolveLibrary(t.Context(), "item/with space")
	if err != nil || !found || library.ID != "22222222-2222-4222-8222-222222222222" || library.Name != "Movies" {
		t.Fatalf("ResolveLibrary = %+v, %t, %v", library, found, err)
	}
}

func TestResolveLibraryReturnsNotFoundWithoutCollectionFolder(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, `[{"Id":"11111111-1111-4111-8111-111111111111","Name":"Playlist","Type":"Playlist"}]`)
	}))
	defer server.Close()
	library, found, err := newTestClient(t, server, nil).ResolveLibrary(t.Context(), "item")
	if err != nil || found || library != (core.Library{}) {
		t.Fatalf("ResolveLibrary = %+v, %t, %v", library, found, err)
	}
}

func TestResolveLibraryReturnsNotFoundForDeletedItem(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "deleted item details", http.StatusNotFound)
	}))
	defer server.Close()
	library, found, err := newTestClient(t, server, nil).ResolveLibrary(t.Context(), "item")
	if err != nil || found || library != (core.Library{}) {
		t.Fatalf("ResolveLibrary = %+v, %t, %v", library, found, err)
	}
}

func FuzzSessionMapper(f *testing.F) {
	f.Add([]byte(validSessionJSON))
	f.Add([]byte(`[{"NowPlayingItem":null}]`))
	f.Fuzz(func(t *testing.T, data []byte) {
		var sessions []jellyfinapi.SessionInfoDto
		if err := json.Unmarshal(data, &sessions); err != nil {
			return
		}
		if _, err := mapSessions(sessions); err != nil {
			return
		}
	})
}
