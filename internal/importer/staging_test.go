package importer

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BonzTM/bloom/internal/core"
)

func TestStagingSweepKeepsRecentlyModifiedEntries(t *testing.T) {
	staging, err := NewStaging(t.TempDir(), 10*time.Minute)
	if err != nil {
		t.Fatalf("NewStaging: %v", err)
	}
	id, err := staging.stage(strings.NewReader("published"))
	if err != nil {
		t.Fatalf("stage published entry: %v", err)
	}
	staging.release(id)
	partialID := "44444444-4444-4444-8444-444444444444"
	partialName := partialID + partialSuffix
	if err := os.WriteFile(filepath.Join(staging.root, partialName), []byte("partial"), 0o600); err != nil {
		t.Fatalf("write partial entry: %v", err)
	}
	if err := staging.sweep(nil); err != nil {
		t.Fatalf("sweep recent entries: %v", err)
	}
	for _, name := range []string{stagingName(id), partialName} {
		if _, err := os.Stat(filepath.Join(staging.root, name)); err != nil {
			t.Fatalf("recent entry %q was removed: %v", name, err)
		}
		old := time.Now().Add(-staging.transferTimeout - time.Second)
		if err := os.Chtimes(filepath.Join(staging.root, name), old, old); err != nil {
			t.Fatalf("age entry %q: %v", name, err)
		}
	}
	if err := staging.sweep(nil); err != nil {
		t.Fatalf("sweep old entries: %v", err)
	}
	for _, name := range []string{stagingName(id), partialName} {
		if _, err := os.Stat(filepath.Join(staging.root, name)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("old entry %q survived: %v", name, err)
		}
	}
}

func TestStagingBoundsUploadsAndUsesPrivateRoot(t *testing.T) {
	dataDirectory := t.TempDir()
	staging, err := NewStaging(dataDirectory, 10*time.Minute)
	if err != nil {
		t.Fatalf("NewStaging: %v", err)
	}
	staging.maxBytes = 8
	if _, stageErr := staging.stage(strings.NewReader("123456789")); !errors.Is(stageErr, core.ErrInvalidArgument) {
		t.Fatalf("oversize stage = %v, want invalid argument", stageErr)
	}
	info, err := os.Stat(filepath.Join(dataDirectory, stagingDirectory))
	if err != nil {
		t.Fatalf("stat staging root: %v", err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Fatalf("staging root mode = %v", info.Mode().Perm())
	}
	entries, err := os.ReadDir(staging.root)
	if err != nil || len(entries) != 0 {
		t.Fatalf("oversize staging residue = %+v, %v", entries, err)
	}
	id, err := staging.stage(strings.NewReader("12345678"))
	if err != nil {
		t.Fatalf("bounded stage: %v", err)
	}
	fileInfo, err := os.Stat(filepath.Join(staging.root, stagingName(id)))
	if err != nil {
		t.Fatalf("stat staged file: %v", err)
	}
	if fileInfo.Mode().Perm() != 0o600 {
		t.Fatalf("staged file mode = %v", fileInfo.Mode().Perm())
	}
}

func TestStagingRejectsSymlinkWithoutRemovingTarget(t *testing.T) {
	staging, err := NewStaging(t.TempDir(), 10*time.Minute)
	if err != nil {
		t.Fatalf("NewStaging: %v", err)
	}
	id := "11111111-1111-4111-8111-111111111111"
	target := filepath.Join(t.TempDir(), "outside.jsonl")
	if err := os.WriteFile(target, []byte("outside"), 0o600); err != nil {
		t.Fatalf("write target: %v", err)
	}
	if err := os.Symlink(target, filepath.Join(staging.root, stagingName(id))); err != nil {
		t.Fatalf("create staging symlink: %v", err)
	}
	if _, err := staging.open(id); !errors.Is(err, core.ErrInvalidArgument) {
		t.Fatalf("open symlink = %v, want invalid argument", err)
	}
	if err := staging.remove(id); err != nil {
		t.Fatalf("remove staging symlink: %v", err)
	}
	if data, err := os.ReadFile(target); err != nil || string(data) != "outside" {
		t.Fatalf("outside target = %q, %v", data, err)
	}
}

func TestNewStagingMarksSymlinkRootUnavailable(t *testing.T) {
	dataDirectory := t.TempDir()
	target := t.TempDir()
	if err := os.Symlink(target, filepath.Join(dataDirectory, stagingDirectory)); err != nil {
		t.Fatalf("create staging-root symlink: %v", err)
	}
	staging, err := NewStaging(dataDirectory, 10*time.Minute)
	if err != nil {
		t.Fatalf("NewStaging: %v", err)
	}
	if cause := staging.UnavailableCause(); !errors.Is(cause, core.ErrInvalidArgument) {
		t.Fatalf("NewStaging unavailable cause = %v, want ErrInvalidArgument", cause)
	}
}
