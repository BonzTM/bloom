package importer

import (
	"bufio"
	"bytes"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/testutil"
)

func TestJellystatReaderMapsFixtureAndCountsEveryLine(t *testing.T) {
	payload, err := os.Open("testdata/jellystat-backup.jsonl")
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	defer payload.Close()
	store := &workerStore{}
	staging, err := NewStaging(store, 10*time.Minute)
	if err != nil {
		t.Fatalf("NewStaging: %v", err)
	}
	id, err := staging.stage(t.Context(), payload, staticClock{time.Now()})
	if err != nil {
		t.Fatalf("stage fixture: %v", err)
	}
	cursor, err := encodeFileCursor(fileCursor{ID: id})
	if err != nil {
		t.Fatalf("encode cursor: %v", err)
	}
	reader := &jellystatReader{staging: staging, storeTimeout: time.Second}
	records, _, skipped, err := reader.ReadImportBatch(t.Context(), core.ImportJob{Cursor: cursor})
	if err != nil {
		t.Fatalf("ReadImportBatch: %v", err)
	}
	if len(records) != 4 || skipped != 17 {
		t.Fatalf("fixture counters = records %d skipped %d, want 4/17", len(records), skipped)
	}
	assertJellystatMovie(t, records[0])
	assertJellystatEpisode(t, records[1])
	if records[2].MediaUserID != "missing-user" || records[2].Username != "not-used" ||
		records[2].ItemID != "missing-movie" || records[2].ItemName != "not-used" ||
		records[2].PlayMethod != core.PlayMethodUnknown {
		t.Fatalf("unknown identities = %+v", records[2])
	}
	if records[3].RecordID != "plugin:77" || records[3].PlayMethod != core.PlayMethodTranscode {
		t.Fatalf("plugin provenance = %+v", records[3])
	}
}

func TestValidateJellystatBackupRejectsWrongFirstLine(t *testing.T) {
	store := &workerStore{}
	staging, err := NewStaging(store, 10*time.Minute)
	if err != nil {
		t.Fatalf("NewStaging: %v", err)
	}
	id, err := staging.stage(t.Context(), strings.NewReader(`{"id":"bloom-watch"}`+"\n"), staticClock{time.Now()})
	if err != nil {
		t.Fatalf("stage invalid backup: %v", err)
	}
	if err := validateJellystatUpload(t.Context(), staging, id, time.Second); !errors.Is(err, core.ErrInvalidArgument) {
		t.Fatalf("validateJellystatUpload = %v, want invalid argument", err)
	}
}

func TestJellystatReaderEnforcesLineAndLookupBounds(t *testing.T) {
	line := bytes.Repeat([]byte{'x'}, maxJSONLLineBytes+1)
	oversizedReader := bufio.NewReaderSize(bytes.NewReader(line), maxJSONLLineBytes+1)
	if _, _, err := readJellystatLine(oversizedReader); err == nil {
		t.Fatal("oversized Jellystat line was accepted")
	}
	prefix, suffix := []byte(`{"type":"table","table":"`), []byte(`"}`)
	payload := append(bytes.Clone(prefix), bytes.Repeat([]byte{'x'}, maxJSONLLineBytes-len(prefix)-len(suffix))...)
	payload = append(payload, suffix...)
	boundary := append(bytes.Clone(payload), '\n')
	boundaryReader := bufio.NewReaderSize(bytes.NewReader(boundary), maxJSONLLineBytes+1)
	if got, _, err := readJellystatLine(boundaryReader); err != nil ||
		len(got) != len(boundary) {
		t.Fatalf("boundary Jellystat line = %d bytes, %v", len(got), err)
	}
	if _, err := decodeJellystatLine(payload); err != nil {
		t.Fatalf("decode boundary Jellystat payload: %v", err)
	}
	entryBound := jellystatLookups{entries: maxJellystatLookupEntries}
	if err := entryBound.reserve("id"); !errors.Is(err, core.ErrInvalidArgument) {
		t.Fatalf("entry bound = %v, want invalid argument", err)
	}
	byteBound := jellystatLookups{bytes: maxJellystatLookupBytes}
	if err := byteBound.reserve("id"); !errors.Is(err, core.ErrInvalidArgument) {
		t.Fatalf("byte bound = %v, want invalid argument", err)
	}
}

func TestJellystatLookupPassRequiresPlaybackActivityTable(t *testing.T) {
	input := `{"type":"table","table":"jf_libraries"}` + "\n"
	_, err := scanJellystatLookups(t.Context(), bufio.NewReader(strings.NewReader(input)), 2, nil)
	if !errors.Is(err, core.ErrInvalidArgument) {
		t.Fatalf("scanJellystatLookups = %v, want invalid argument", err)
	}
}

func TestJellystatLookupPassEnforcesIndependentRowLimit(t *testing.T) {
	input := `{"type":"table","table":"jf_playback_activity"}` + "\n" +
		`{"type":"row","table":"ignored","data":{}}` + "\n"
	_, err := scanJellystatLookups(t.Context(), bufio.NewReader(strings.NewReader(input)), 1, nil)
	if !errors.Is(err, core.ErrInvalidArgument) {
		t.Fatalf("scanJellystatLookups = %v, want invalid argument", err)
	}
}

func TestJellystatLookupPassRenewsLeaseAcrossOriginalExpiry(t *testing.T) {
	start := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	clock := testutil.NewFakeClock(start)
	store := &workerStore{}
	staging, err := NewStaging(store, 10*time.Minute)
	if err != nil {
		t.Fatalf("NewStaging: %v", err)
	}
	payload := jellystatMultiChunkLookupFixture()
	id, err := staging.stage(t.Context(), strings.NewReader(payload), staticClock{start})
	if err != nil {
		t.Fatalf("stage fixture: %v", err)
	}
	store.readChunkHook = func(int64) { clock.Advance(10 * time.Second) }
	job := runningJellystatJob(start.Add(30 * time.Second))
	store.job = job
	reader := jellystatReader{
		staging: staging, store: store, clock: clock,
		leaseDuration: 30 * time.Second, storeTimeout: time.Second,
	}
	if err := reader.open(t.Context(), job, fileCursor{ID: id}); err != nil {
		t.Fatalf("open: %v", err)
	}
	if !clock.Now().After(*job.LeaseExpiresAt) || store.renewed == 0 ||
		store.job.LeaseExpiresAt == nil || !store.job.LeaseExpiresAt.After(clock.Now()) {
		t.Fatalf("clock = %v, renewals = %d, lease = %v", clock.Now(), store.renewed, store.job.LeaseExpiresAt)
	}
}

func TestJellystatLookupDuplicates(t *testing.T) {
	tests := []struct {
		name        string
		identical   []byte
		conflicting []byte
		collect     func([]byte, *jellystatLookups) error
	}{
		{"user", []byte(`{"Id":"id","Name":"name"}`), []byte(`{"Name":"name","Id":"id"}`), collectJellystatUser},
		{"item", []byte(`{"Id":"id","Name":"name","Type":"Movie"}`), []byte(`{"Id":"id","Name":"other","Type":"Movie"}`), collectJellystatItem},
		{"episode", []byte(`{"EpisodeId":"id","Name":"name"}`), []byte(`{"EpisodeId":"id","Name":"other"}`), collectJellystatEpisode},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			lookups := newJellystatLookups()
			if err := test.collect(test.identical, &lookups); err != nil {
				t.Fatalf("collect first row: %v", err)
			}
			if err := test.collect(test.identical, &lookups); err != nil || lookups.entries != 1 {
				t.Fatalf("collect identical duplicate = %v, entries %d", err, lookups.entries)
			}
			if err := test.collect(test.conflicting, &lookups); !errors.Is(err, core.ErrInvalidArgument) {
				t.Fatalf("collect conflicting duplicate = %v, want invalid argument", err)
			}
		})
	}
}

func TestJellystatActivityValidatesAndUsesFallbackNames(t *testing.T) {
	row := validJellystatActivityRow()
	record, err := mapJellystatActivity(row, newJellystatLookups())
	if err != nil || record.Username != "activity user" || record.ItemName != "activity item" ||
		record.SeriesName != "activity series" {
		t.Fatalf("mapJellystatActivity = %+v, %v", record, err)
	}
	lookups := newJellystatLookups()
	lookups.users[row.UserID] = "lookup user"
	lookups.episodes[valueOrEmpty(row.EpisodeID)] = jellystatEpisode{
		Name: "lookup item", SeriesName: "lookup series",
	}
	invalid := "bad\nname"
	for _, mutate := range []func(*jellystatActivityRow){
		func(value *jellystatActivityRow) { value.UserName = &invalid },
		func(value *jellystatActivityRow) { value.NowPlayingItemName = &invalid },
		func(value *jellystatActivityRow) { value.SeriesName = &invalid },
	} {
		candidate := row
		mutate(&candidate)
		if _, err := mapJellystatActivity(candidate, lookups); !errors.Is(err, core.ErrInvalidArgument) {
			t.Fatalf("mapJellystatActivity accepted invalid fallback: %v", err)
		}
	}
}

func TestJellystatActivityFallsBackToRawIDs(t *testing.T) {
	row := validJellystatActivityRow()
	row.UserName, row.NowPlayingItemName, row.SeriesName = nil, nil, nil
	record, err := mapJellystatActivity(row, newJellystatLookups())
	if err != nil || record.Username != row.UserID || record.ItemName != valueOrEmpty(row.EpisodeID) ||
		record.SeriesName != "" {
		t.Fatalf("mapJellystatActivity = %+v, %v", record, err)
	}
}

func TestJellystatActivityDurationAndTimestampBounds(t *testing.T) {
	ended := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name      string
		duration  int64
		ended     time.Time
		wantStart time.Time
		wantError bool
	}{
		{"negative", -1, ended, time.Time{}, true},
		{
			"accepted boundary", core.MaxImportPlaybackSeconds, ended,
			ended.Add(-time.Duration(core.MaxImportPlaybackSeconds) * time.Second), false,
		},
		{"just over boundary", core.MaxImportPlaybackSeconds + 1, ended, time.Time{}, true},
		{"multi-year", 3 * 365 * 24 * 60 * 60, ended, time.Time{}, true},
		{"before supported range", 2, time.Date(1, 1, 1, 0, 0, 1, 0, time.UTC), time.Time{}, true},
		{"timezone offset", 90, time.Date(2026, 9, 27, 12, 1, 30, 0, time.FixedZone("EDT", -4*60*60)), time.Date(2026, 9, 27, 16, 0, 0, 0, time.UTC), false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			row := validJellystatActivityRow()
			row.PlaybackDuration.Value = test.duration
			row.ActivityDateInserted = test.ended
			record, err := mapJellystatActivity(row, newJellystatLookups())
			if errors.Is(err, core.ErrInvalidArgument) != test.wantError {
				t.Fatalf("mapJellystatActivity error = %v, want error %t", err, test.wantError)
			}
			if !test.wantError && !record.StartedAt.Equal(test.wantStart) {
				t.Fatalf("started at = %v, want %v", record.StartedAt, test.wantStart)
			}
		})
	}
}

func TestJellystatActivityRejectsDurationBeyondResolvedRuntime(t *testing.T) {
	runtime := 30 * time.Minute
	lookups := newJellystatLookups()
	lookups.episodes["episode-id"] = jellystatEpisode{Runtime: &runtime}
	row := validJellystatActivityRow()
	limit := 3*runtime + time.Hour
	row.PlaybackDuration.Value = int64(limit / time.Second)
	if _, err := mapJellystatActivity(row, lookups); err != nil {
		t.Fatalf("mapJellystatActivity rejected runtime-relative boundary: %v", err)
	}
	row.PlaybackDuration.Value = int64((limit + time.Second) / time.Second)
	if _, err := mapJellystatActivity(row, lookups); !errors.Is(err, core.ErrInvalidArgument) {
		t.Fatalf("mapJellystatActivity accepted duration beyond runtime allowance: %v", err)
	}
}

func TestJellystatActivityIgnoresUnknownEnvelopeFields(t *testing.T) {
	line := []byte(`{"type":"row","table":"jf_playback_activity","future_envelope":true,"data":{"Id":"id","UserId":"user","EpisodeId":"episode","PlaybackDuration":90,"ActivityDateInserted":"2026-09-27T12:01:30Z"}}`)
	if record, ok := decodeJellystatActivity(line, newJellystatLookups()); !ok || record.RecordID != "id" {
		t.Fatalf("activity with additive envelope field = %+v, %t", record, ok)
	}
}

func TestJellystatActivityIgnoresUnknownRowFields(t *testing.T) {
	line := []byte(`{"type":"row","table":"jf_playback_activity","data":{"Id":"id","UserId":"user","EpisodeId":"episode","PlaybackDuration":90,"ActivityDateInserted":"2026-09-27T12:01:30Z","future_row":true}}`)
	if record, ok := decodeJellystatActivity(line, newJellystatLookups()); !ok || record.RecordID != "id" {
		t.Fatalf("activity with additive row field = %+v, %t", record, ok)
	}
}

func TestJellystatDecodeRejectsInvalidKnownFieldAndTrailingValue(t *testing.T) {
	invalidKnown := []byte(`{"type":"row","table":"jf_playback_activity","data":{"PlaybackDuration":{}}}`)
	if _, ok := decodeJellystatActivity(invalidKnown, newJellystatLookups()); ok {
		t.Fatal("activity with invalid known field was accepted")
	}
	trailing := []byte(`{"type":"table","table":"jf_playback_activity"} {}`)
	if _, err := decodeJellystatLine(trailing); !errors.Is(err, core.ErrInvalidArgument) {
		t.Fatalf("decodeJellystatLine trailing value = %v, want invalid argument", err)
	}
}

func jellystatMultiChunkLookupFixture() string {
	marker := `{"type":"table","table":"jf_playback_activity"}` + "\n"
	padding := strings.Repeat("x", 60<<10)
	row := `{"type":"row","table":"ignored","data":{"padding":"` + padding + `"}}` + "\n"
	return marker + strings.Repeat(row, 52)
}

func runningJellystatJob(expiresAt time.Time) core.ImportJob {
	return core.ImportJob{
		ID: "11111111-1111-4111-8111-111111111111", Source: core.ImportSourceJellystat,
		State: core.ImportRunning, LeaseToken: "lease-token",
		LeaseExpiresAt: &expiresAt,
	}
}

func validJellystatActivityRow() jellystatActivityRow {
	username, itemName, seriesName := "activity user", "activity item", "activity series"
	episodeID := "episode-id"
	return jellystatActivityRow{
		ID: "activity-id", UserID: "user-id", UserName: &username,
		NowPlayingItemName: &itemName, SeriesName: &seriesName, EpisodeID: &episodeID,
		PlaybackDuration:     jellystatInt64{Value: 90, Valid: true},
		ActivityDateInserted: time.Date(2026, 9, 27, 12, 1, 30, 0, time.UTC),
	}
}

func assertJellystatMovie(t *testing.T, record core.ImportedWatch) {
	t.Helper()
	wantEnd := time.Date(2026, 9, 25, 12, 1, 30, 0, time.UTC)
	if record.RecordID != "activity-movie" || record.Username != "Alice" ||
		record.ItemID != "movie-1" || record.ItemName != "Fixture Movie" || record.ItemType != "Movie" ||
		record.Runtime == nil || *record.Runtime != 2*time.Hour || record.Duration != 90*time.Second ||
		record.EndedAt == nil || !record.EndedAt.Equal(wantEnd) ||
		!record.StartedAt.Equal(wantEnd.Add(-90*time.Second)) || record.PlayMethod != core.PlayMethodDirectPlay {
		t.Fatalf("movie = %+v", record)
	}
}

func assertJellystatEpisode(t *testing.T, record core.ImportedWatch) {
	t.Helper()
	if record.ItemID != "episode-1" || record.ItemName != "Fixture Episode" ||
		record.ItemType != "Episode" || record.SeriesName != "Fixture Series" ||
		record.SeasonNumber == nil || *record.SeasonNumber != 2 ||
		record.EpisodeNumber == nil || *record.EpisodeNumber != 3 ||
		record.Runtime == nil || *record.Runtime != 30*time.Minute ||
		record.PlayMethod != core.PlayMethodDirectStream {
		t.Fatalf("episode = %+v", record)
	}
}
