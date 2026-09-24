package core_test

import (
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/BonzTM/bloom/internal/core"
)

const playbackServerID = "11111111-1111-4111-8111-111111111111"

func TestPlaybackTrackerTransitions(t *testing.T) {
	now := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		run  func(*testing.T, *core.PlaybackTracker, *idSequence, time.Time)
	}{
		{name: "start pause resume and stop", run: testPlaybackLifecycle},
		{name: "reopen within window", run: testPlaybackReopen},
		{name: "new watch after window", run: testPlaybackAfterWindow},
		{name: "device reuse by another user", run: testPlaybackDeviceReuse},
		{name: "paused session never resumes", run: testPlaybackPausedStop},
		{name: "backward seek does not reduce active time", run: testPlaybackBackwardSeek},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			tracker, err := core.NewPlaybackTracker(playbackServerID, core.PlaybackTrackerConfig{
				MissedPolls: 3, ResumeWindow: 5 * time.Minute,
			}, nil, nil)
			if err != nil {
				t.Fatalf("NewPlaybackTracker: %v", err)
			}
			testCase.run(t, tracker, &idSequence{}, now)
		})
	}
}

type idSequence struct{ next int }

func (s *idSequence) New() (string, error) {
	s.next++
	return fmt.Sprintf("00000000-0000-4000-8000-%012d", s.next), nil
}

func playbackSession() core.PlaybackSession {
	return core.PlaybackSession{
		ServerSessionID: "session-1", MediaUserID: "user-1", Username: "alice",
		DeviceID: "device-1", DeviceName: "TV", Client: "Jellyfin Web",
		ItemID: "item-1", ItemName: "Pilot", ItemType: "Episode", SeriesName: "Show",
		Position: time.Minute, PlayMethod: core.PlayMethodDirectPlay,
	}
}

func observe(
	t *testing.T,
	tracker *core.PlaybackTracker,
	ids *idSequence,
	now time.Time,
	sessions ...core.PlaybackSession,
) []core.PlaybackMutation {
	t.Helper()
	mutations, err := tracker.Observe(now, core.WatchSourcePoll, sessions, ids.New)
	if err != nil {
		t.Fatalf("Observe: %v", err)
	}
	return mutations
}

func TestPlaybackTrackerKeepsOpeningSourceAcrossMixedObservations(t *testing.T) {
	now := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)
	tracker, err := core.NewPlaybackTracker(playbackServerID, core.PlaybackTrackerConfig{
		MissedPolls: 3, ResumeWindow: 5 * time.Minute,
	}, nil, nil)
	if err != nil {
		t.Fatalf("NewPlaybackTracker: %v", err)
	}
	ids := &idSequence{}
	session := playbackSession()
	started, err := tracker.Observe(now, core.WatchSourceWebsocket, []core.PlaybackSession{session}, ids.New)
	if err != nil {
		t.Fatalf("Observe(start): %v", err)
	}
	session.Paused = true
	paused, err := tracker.Observe(now.Add(time.Second), core.WatchSourceWebhook, []core.PlaybackSession{session}, ids.New)
	if err != nil {
		t.Fatalf("Observe(pause): %v", err)
	}
	session.Paused = false
	resumed, err := tracker.Observe(now.Add(2*time.Second), core.WatchSourcePoll, []core.PlaybackSession{session}, ids.New)
	if err != nil {
		t.Fatalf("Observe(resume): %v", err)
	}
	start, pause, resume := onlyMutation(t, started), onlyMutation(t, paused), onlyMutation(t, resumed)
	if start.Watch.Source != core.WatchSourceWebsocket || pause.Watch.Source != core.WatchSourceWebsocket ||
		resume.Watch.Source != core.WatchSourceWebsocket {
		t.Fatalf("watch sources = %q, %q, %q", start.Watch.Source, pause.Watch.Source, resume.Watch.Source)
	}
	if start.SegmentSource != core.WatchSourceWebsocket || start.Position.Source != core.WatchSourceWebsocket ||
		pause.Position.Source != core.WatchSourceWebhook || resume.SegmentSource != core.WatchSourcePoll ||
		resume.Position.Source != core.WatchSourcePoll {
		t.Fatalf("row sources = start %+v, pause %+v, resume %+v", start, pause, resume)
	}
}

func TestPlaybackTrackerSamplesStreamChangesWithoutDuplicateUnchangedSamples(t *testing.T) {
	now := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)
	tracker, err := core.NewPlaybackTracker(playbackServerID, core.PlaybackTrackerConfig{
		MissedPolls: 3, ResumeWindow: 5 * time.Minute,
	}, nil, nil)
	if err != nil {
		t.Fatalf("NewPlaybackTracker: %v", err)
	}
	ids := &idSequence{}
	session := playbackSession()
	session.Stream = testDirectStreamDetails()
	started := onlyMutation(t, observe(t, tracker, ids, now, session))
	if started.Position == nil || started.Position.Stream == nil {
		t.Fatalf("start sample = %+v", started.Position)
	}
	unchanged := onlyMutation(t, observe(t, tracker, ids, now.Add(time.Second), session))
	if unchanged.Position != nil {
		t.Fatalf("unchanged sample = %+v, want nil", unchanged.Position)
	}
	session.PlayMethod = core.PlayMethodTranscode
	session.Stream = testTranscodeStreamDetails("h264")
	transcoded := onlyMutation(t, observe(t, tracker, ids, now.Add(2*time.Second), session))
	if transcoded.Position == nil || transcoded.Position.PlayMethod != core.PlayMethodTranscode ||
		transcoded.Position.Stream.VideoCodec != "h264" {
		t.Fatalf("transcode sample = %+v", transcoded.Position)
	}
	session.Stream = testTranscodeStreamDetails("hevc")
	codecChanged := onlyMutation(t, observe(t, tracker, ids, now.Add(3*time.Second), session))
	if codecChanged.Position == nil || codecChanged.Position.Stream.VideoCodec != "hevc" {
		t.Fatalf("codec-change sample = %+v", codecChanged.Position)
	}
}

func testDirectStreamDetails() *core.StreamDetails {
	return &core.StreamDetails{Container: "mkv", VideoCodec: "hevc", AudioCodec: "aac"}
}

func testTranscodeStreamDetails(codec string) *core.StreamDetails {
	videoDirect, audioDirect := false, true
	return &core.StreamDetails{
		Container: "ts", VideoCodec: codec, AudioCodec: "aac", Bitrate: 8_000_000,
		Width: 1920, Height: 1080, Framerate: 23.98, AudioChannels: 6,
		IsVideoDirect: &videoDirect, IsAudioDirect: &audioDirect,
		TranscodeReasons: []string{"VideoCodecNotSupported"},
	}
}

func testPlaybackLifecycle(t *testing.T, tracker *core.PlaybackTracker, ids *idSequence, now time.Time) {
	t.Helper()
	session := playbackSession()
	started := onlyMutation(t, observe(t, tracker, ids, now, session))
	if started.Watch.State != core.WatchPlaying || started.SegmentStart == nil {
		t.Fatalf("start mutation = %+v", started)
	}
	session.Paused = true
	paused := onlyMutation(t, observe(t, tracker, ids, now.Add(5*time.Second), session))
	if paused.Watch.ActiveTime != 5*time.Second || paused.SegmentEnd == nil || paused.Watch.State != core.WatchPaused {
		t.Fatalf("pause mutation = %+v", paused)
	}
	session.Paused = false
	resumed := onlyMutation(t, observe(t, tracker, ids, now.Add(10*time.Second), session))
	if resumed.Watch.ActiveTime != 5*time.Second || resumed.SegmentStart == nil {
		t.Fatalf("resume mutation = %+v", resumed)
	}
	_ = observe(t, tracker, ids, now.Add(11*time.Second))
	_ = observe(t, tracker, ids, now.Add(12*time.Second))
	stopped := onlyMutation(t, observe(t, tracker, ids, now.Add(13*time.Second)))
	if stopped.Watch.State != core.WatchStopped || stopped.CloseReason != "timeout" ||
		stopped.Watch.EndedAt == nil || !stopped.Watch.EndedAt.Equal(now.Add(10*time.Second)) {
		t.Fatalf("stop mutation = %+v", stopped)
	}
}

func testPlaybackReopen(t *testing.T, tracker *core.PlaybackTracker, ids *idSequence, now time.Time) {
	t.Helper()
	session := playbackSession()
	first := onlyMutation(t, observe(t, tracker, ids, now, session))
	stopByMisses(t, tracker, ids, now)
	reopened := onlyMutation(t, observe(t, tracker, ids, now.Add(time.Minute), session))
	if reopened.Watch.ID != first.Watch.ID || reopened.Watch.EndedAt != nil || reopened.SegmentStart == nil {
		t.Fatalf("reopen mutation = %+v, first id %s", reopened, first.Watch.ID)
	}
}

func testPlaybackAfterWindow(t *testing.T, tracker *core.PlaybackTracker, ids *idSequence, now time.Time) {
	t.Helper()
	session := playbackSession()
	first := onlyMutation(t, observe(t, tracker, ids, now, session))
	stopByMisses(t, tracker, ids, now)
	second := onlyMutation(t, observe(t, tracker, ids, now.Add(6*time.Minute), session))
	if second.Watch.ID == first.Watch.ID {
		t.Fatalf("watch after resume window reused id %s", second.Watch.ID)
	}
}

func testPlaybackDeviceReuse(t *testing.T, tracker *core.PlaybackTracker, ids *idSequence, now time.Time) {
	t.Helper()
	first := playbackSession()
	second := first
	second.MediaUserID, second.Username, second.ServerSessionID = "user-2", "bob", "session-2"
	mutations := observe(t, tracker, ids, now, first, second)
	if len(mutations) != 2 || tracker.OpenCount() != 2 || mutations[0].Watch.ID == mutations[1].Watch.ID {
		t.Fatalf("device reuse mutations = %+v, open = %d", mutations, tracker.OpenCount())
	}
}

func testPlaybackPausedStop(t *testing.T, tracker *core.PlaybackTracker, ids *idSequence, now time.Time) {
	t.Helper()
	session := playbackSession()
	session.Paused = true
	started := onlyMutation(t, observe(t, tracker, ids, now, session))
	if started.SegmentStart != nil || started.Watch.State != core.WatchPaused {
		t.Fatalf("paused start = %+v", started)
	}
	_ = observe(t, tracker, ids, now.Add(time.Second))
	_ = observe(t, tracker, ids, now.Add(2*time.Second))
	stopped := onlyMutation(t, observe(t, tracker, ids, now.Add(3*time.Second)))
	if stopped.SegmentEnd != nil || stopped.Watch.ActiveTime != 0 {
		t.Fatalf("paused stop = %+v", stopped)
	}
}

func testPlaybackBackwardSeek(t *testing.T, tracker *core.PlaybackTracker, ids *idSequence, now time.Time) {
	t.Helper()
	session := playbackSession()
	_ = observe(t, tracker, ids, now, session)
	session.Position = 15 * time.Second
	mutation := onlyMutation(t, observe(t, tracker, ids, now.Add(5*time.Second), session))
	if mutation.Watch.LastPosition != 15*time.Second || mutation.Watch.ActiveTime != 5*time.Second {
		t.Fatalf("backward seek mutation = %+v", mutation)
	}
}

func stopByMisses(t *testing.T, tracker *core.PlaybackTracker, ids *idSequence, now time.Time) {
	t.Helper()
	_ = observe(t, tracker, ids, now.Add(time.Second))
	_ = observe(t, tracker, ids, now.Add(2*time.Second))
	_ = observe(t, tracker, ids, now.Add(3*time.Second))
}

func onlyMutation(t *testing.T, mutations []core.PlaybackMutation) core.PlaybackMutation {
	t.Helper()
	if len(mutations) != 1 {
		t.Fatalf("mutation count = %d, want 1", len(mutations))
	}
	return mutations[0]
}

func TestPlaybackTrackerClosesStaleRestoredWatchAtLastSeen(t *testing.T) {
	now := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)
	watch := core.PlaybackWatch{
		ID: "00000000-0000-4000-8000-000000000001", MediaServerID: playbackServerID,
		MediaUserID: "user", DeviceID: "device", ItemID: "item",
		PlayMethod: core.PlayMethodDirectPlay, State: core.WatchPlaying,
		StartedAt: now.Add(-time.Minute), LastSeenAt: now.Add(-20 * time.Second),
		Source: core.WatchSourcePoll, CreatedAt: now.Add(-time.Minute), UpdatedAt: now.Add(-20 * time.Second),
	}
	tracker, err := core.NewPlaybackTracker(playbackServerID, core.PlaybackTrackerConfig{
		MissedPolls: 3, ResumeWindow: 5 * time.Minute,
	}, []core.PlaybackWatch{watch}, nil)
	if err != nil {
		t.Fatalf("NewPlaybackTracker: %v", err)
	}
	mutation := onlyMutation(t, tracker.CloseStale(now, 15*time.Second))
	if mutation.CloseReason != "startup" || mutation.Watch.EndedAt == nil ||
		!mutation.Watch.EndedAt.Equal(watch.LastSeenAt) {
		t.Fatalf("stale close = %+v", mutation)
	}
}

func TestPlaybackTrackerRestoresWatchBeforeSessionIDAppears(t *testing.T) {
	now := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)
	session := playbackSession()
	watch := core.PlaybackWatch{
		ID: "00000000-0000-4000-8000-000000000001", MediaServerID: playbackServerID,
		MediaUserID: session.MediaUserID, DeviceID: session.DeviceID, ItemID: session.ItemID,
		PlayMethod: core.PlayMethodDirectPlay, State: core.WatchPlaying,
		StartedAt: now.Add(-time.Minute), LastSeenAt: now.Add(-time.Second),
		Source: core.WatchSourcePoll, CreatedAt: now.Add(-time.Minute), UpdatedAt: now.Add(-time.Second),
	}
	tracker, err := core.NewPlaybackTracker(playbackServerID, core.PlaybackTrackerConfig{
		MissedPolls: 3, ResumeWindow: 5 * time.Minute,
	}, []core.PlaybackWatch{watch}, nil)
	if err != nil {
		t.Fatalf("NewPlaybackTracker: %v", err)
	}
	mutation := onlyMutation(t, observe(t, tracker, &idSequence{}, now, session))
	if mutation.Watch.ID != watch.ID || tracker.OpenCount() != 1 {
		t.Fatalf("restored watch observation = %+v, open = %d", mutation, tracker.OpenCount())
	}
}

func TestPlaybackTrackerDeduplicatesMissingSessionIdentifier(t *testing.T) {
	now := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)
	tracker, err := core.NewPlaybackTracker(playbackServerID, core.PlaybackTrackerConfig{
		MissedPolls: 3, ResumeWindow: 5 * time.Minute,
	}, nil, nil)
	if err != nil {
		t.Fatalf("NewPlaybackTracker: %v", err)
	}
	withID := playbackSession()
	withoutID := withID
	withoutID.ServerSessionID = ""
	mutations := observe(t, tracker, &idSequence{}, now, withID, withoutID)
	if len(mutations) != 1 || tracker.OpenCount() != 1 {
		t.Fatalf("duplicate observations = %+v, open = %d", mutations, tracker.OpenCount())
	}
}

func TestPlaybackTrackerRestoresMoreThanOneSnapshot(t *testing.T) {
	now := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)
	watches := playbackOpenWatches(1025, now)
	tracker, err := core.NewPlaybackTracker(playbackServerID, core.PlaybackTrackerConfig{
		MissedPolls: 3, ResumeWindow: 5 * time.Minute,
	}, watches, nil)
	if err != nil {
		t.Fatalf("NewPlaybackTracker: %v", err)
	}
	if tracker.OpenCount() != len(watches) {
		t.Fatalf("open count = %d, want %d", tracker.OpenCount(), len(watches))
	}
}

func TestPlaybackTrackerClosesOldestOverflow(t *testing.T) {
	now := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)
	tracker, err := core.NewPlaybackTracker(playbackServerID, core.PlaybackTrackerConfig{
		MissedPolls: 1, ResumeWindow: 5 * time.Minute,
	}, playbackOpenWatches(1025, now), nil)
	if err != nil {
		t.Fatalf("NewPlaybackTracker: %v", err)
	}
	closed := tracker.CloseOverflow(core.MaxPlaybackSessions)
	if len(closed) != 1 || closed[0].CloseReason != "overflow" ||
		closed[0].Watch.MediaUserID != "user-0000" || closed[0].Watch.EndedAt == nil ||
		!closed[0].Watch.EndedAt.Equal(closed[0].Watch.LastSeenAt) {
		t.Fatalf("overflow closures = %+v", closed)
	}
	if tracker.OpenCount() != core.MaxPlaybackSessions {
		t.Fatalf("open count = %d, want %d", tracker.OpenCount(), core.MaxPlaybackSessions)
	}
}

func playbackOpenWatches(count int, now time.Time) []core.PlaybackWatch {
	watches := make([]core.PlaybackWatch, 0, count)
	for index := range count {
		seenAt := now.Add(time.Duration(index-count) * time.Second)
		watches = append(watches, core.PlaybackWatch{
			ID:            fmt.Sprintf("00000000-0000-4000-8000-%012d", index+1),
			MediaServerID: playbackServerID, MediaUserID: fmt.Sprintf("user-%04d", index),
			DeviceID: "device", ItemID: "item", PlayMethod: core.PlayMethodDirectPlay,
			State: core.WatchPlaying, StartedAt: seenAt.Add(-time.Minute), LastSeenAt: seenAt,
			Source: core.WatchSourcePoll, CreatedAt: seenAt.Add(-time.Minute), UpdatedAt: seenAt,
		})
	}
	slices.SortFunc(watches, func(left, right core.PlaybackWatch) int {
		return left.LastSeenAt.Compare(right.LastSeenAt)
	})
	return watches
}
