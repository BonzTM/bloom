package buildinfo_test

import (
	"testing"

	"github.com/BonzTM/bloom/internal/buildinfo"
)

// TestDefaults pins the unstamped defaults so a `go run` (no -ldflags) still has
// sensible, non-empty metadata. Release builds override these via -X.
func TestDefaults(t *testing.T) {
	if buildinfo.Name != "bloom" {
		t.Errorf("Name = %q, want bloom", buildinfo.Name)
	}
	if buildinfo.Version != "dev" {
		t.Errorf("Version = %q, want dev", buildinfo.Version)
	}
	if buildinfo.Commit != "unknown" {
		t.Errorf("Commit = %q, want unknown", buildinfo.Commit)
	}
}
