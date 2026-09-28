package core

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestImportJobValidationAndSafeErrors(t *testing.T) {
	now := NormalizeTime(time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC))
	job := ImportJob{
		ID:            "11111111-1111-4111-8111-111111111111",
		MediaServerID: "22222222-2222-4222-8222-222222222222",
		RequestedBy:   "33333333-3333-4333-8333-333333333333",
		Source:        ImportSourcePlaybackReporting, State: ImportPending,
		CreatedAt: now, UpdatedAt: now,
	}
	if !job.Valid() {
		t.Fatal("valid pending import was rejected")
	}
	job.UnresolvedLibrary = 1
	if job.Valid() {
		t.Fatal("unresolved library count above skipped count was accepted")
	}
	job.Read, job.Skipped = 1, 1
	if !job.Valid() {
		t.Fatal("unresolved library count within skipped count was rejected")
	}
	job.Read, job.Skipped = 0, 0
	job.UnresolvedLibrary = 0
	job.Cursor = strings.Repeat("x", MaxImportCursorBytes+1)
	if job.Valid() {
		t.Fatal("oversized cursor was accepted")
	}
	if got := SafeImportError(ErrImportPluginMissing); got != "Playback Reporting plugin is not installed" {
		t.Fatalf("plugin error = %q", got)
	}
	if got := SafeImportError(ErrImportRecordCountMismatch); got !=
		"Bloom export watch record count does not match summary" {
		t.Fatalf("record count error = %q", got)
	}
	if got := SafeImportError(errors.New("credential=secret")); strings.Contains(got, "secret") {
		t.Fatalf("safe error disclosed cause: %q", got)
	}
}

func TestJellystatImportSourceIsValid(t *testing.T) {
	if !ImportSourceJellystat.Valid() {
		t.Fatal("Jellystat import source was rejected")
	}
}

func TestImportedWatchRejectsControlsAndOversizedIdentifiers(t *testing.T) {
	record := ImportedWatch{
		RecordID: "source-1", MediaUserID: "user-1", Username: "alice",
		ItemID: "item-1", ItemName: "Film", ItemType: "Movie",
		PlayMethod: PlayMethodUnknown, StartedAt: time.Now(), Duration: time.Minute,
	}
	if !record.Valid() {
		t.Fatal("valid imported watch was rejected")
	}
	negativeRuntime := -time.Millisecond
	record.Runtime = &negativeRuntime
	if record.Valid() {
		t.Fatal("negative runtime was accepted")
	}
	record.Runtime = nil
	record.ItemName = "bad\nname"
	if record.Valid() {
		t.Fatal("control character was accepted")
	}
	record.ItemName = "Film"
	record.OriginRecordID = strings.Repeat("x", MaxImportOriginRecordIDBytes+1)
	if record.Valid() {
		t.Fatal("oversized origin record id was accepted")
	}
	record.OriginRecordID = ""
	record.RecordID = strings.Repeat("x", MaxImportRecordIDBytes+1)
	if record.Valid() {
		t.Fatal("oversized source record id was accepted")
	}
}
