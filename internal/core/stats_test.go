package core

import (
	"errors"
	"testing"
	"time"
)

func TestNewStatsWindowValidatesAndDefaults(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 23, 15, 4, 5, 123456789, time.FixedZone("source", 3600))
	window, err := NewStatsWindow(30, "", "UTC", now)
	if err != nil {
		t.Fatalf("NewStatsWindow: %v", err)
	}
	wantEnd := NormalizeTime(now)
	if window.Zone != "UTC" || window.Location != time.UTC || !window.End.Equal(wantEnd) ||
		!window.Start.Equal(wantEnd.Add(-30*24*time.Hour)) {
		t.Fatalf("window = %+v", window)
	}
}

func TestNewStatsWindowRejectsInvalidInputs(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name, server, zone string
		days               int
		now                time.Time
	}{
		{name: "zero days", now: now},
		{name: "too many days", days: 366, now: now},
		{name: "zero now", days: 1},
		{name: "bad server", days: 1, server: "bad", now: now},
		{name: "bad zone", days: 1, zone: "Mars/Olympus", now: now},
		{name: "empty zone", days: 1, now: now},
		{name: "host local zone", days: 1, zone: "Local", now: now},
		{name: "long zone", days: 1, zone: string(make([]byte, 65)), now: now},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := NewStatsWindow(test.days, test.server, test.zone, test.now)
			if !errors.Is(err, ErrInvalidArgument) {
				t.Fatalf("error = %v, want ErrInvalidArgument", err)
			}
		})
	}
}

func TestValidStatsMediaUserID(t *testing.T) {
	t.Parallel()
	tests := []struct {
		value string
		valid bool
	}{
		{value: "user-é", valid: true},
		{value: "", valid: false},
		{value: "nul\x00user", valid: false},
		{value: "delete\x7fuser", valid: false},
		{value: "next\u0085line", valid: false},
		{value: "shift\u008euser", valid: false},
		{value: string([]byte{0xff}), valid: false},
		{value: string(make([]byte, MaxStatsMediaUserIDBytes+1)), valid: false},
	}
	for _, test := range tests {
		if got := ValidStatsMediaUserID(test.value); got != test.valid {
			t.Errorf("ValidStatsMediaUserID(%q) = %t, want %t", test.value, got, test.valid)
		}
	}
}

func TestValidStatsLibraryID(t *testing.T) {
	t.Parallel()
	tests := []struct {
		value string
		valid bool
	}{
		{value: "library-é", valid: true},
		{value: "", valid: false},
		{value: "nul\x00library", valid: false},
		{value: "delete\x7flibrary", valid: false},
		{value: "next\u0085line", valid: false},
		{value: string([]byte{0xff}), valid: false},
		{value: string(make([]byte, MaxStatsLibraryIDBytes+1)), valid: false},
	}
	for _, test := range tests {
		if got := ValidStatsLibraryID(test.value); got != test.valid {
			t.Errorf("ValidStatsLibraryID(%q) = %t, want %t", test.value, got, test.valid)
		}
	}
}

func TestStatsQueryValidate(t *testing.T) {
	t.Parallel()
	window, err := NewStatsWindow(1, "", "UTC", time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	valid := []StatsQuery{
		{Window: window, Report: StatsReportOverview},
		{Window: window, Report: StatsReportLibraries},
		{Window: window, Report: StatsReportTitles, TitleKind: StatsTitleMovie},
		{Window: window, Report: StatsReportUser, UserServerID: "11111111-1111-4111-8111-111111111111", MediaUserID: "user"},
	}
	for _, query := range valid {
		if err := query.Validate(); err != nil {
			t.Errorf("Validate(%+v): %v", query, err)
		}
	}
	invalid := []StatsQuery{
		{},
		{Window: window, Report: StatsReportTitles},
		{Window: window, Report: StatsReportUser, UserServerID: "bad", MediaUserID: "user"},
		{Window: window, Report: StatsReportOverview, LibraryID: "library"},
		{Window: window, Report: StatsReportOverview, LibraryID: "nul\x00library"},
	}
	for _, query := range invalid {
		if !errors.Is(query.Validate(), ErrInvalidArgument) {
			t.Errorf("Validate(%+v) did not reject invalid input", query)
		}
	}
	mutations := []func(*StatsQuery){
		func(query *StatsQuery) { query.Window.Start = query.Window.Start.Add(time.Second) },
		func(query *StatsQuery) { query.Window.MediaServerID = "bad" },
		func(query *StatsQuery) { query.Window.Zone = "America/New_York" },
		func(query *StatsQuery) { query.Window.Zone, query.Window.Location = "Local", time.Local },
	}
	for _, mutate := range mutations {
		query := StatsQuery{Window: window, Report: StatsReportOverview}
		mutate(&query)
		if !errors.Is(query.Validate(), ErrInvalidArgument) {
			t.Errorf("Validate(%+v) did not reject mutated window", query)
		}
	}
}
