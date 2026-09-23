package core

import (
	"regexp"
	"testing"
)

// uuidV4 matches the canonical form with the version nibble fixed to 4 and the
// variant nibble in [89ab].
var uuidV4 = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func TestNewIDFormat(t *testing.T) {
	seen := make(map[string]struct{}, 64)
	for range 64 {
		id, err := NewID()
		if err != nil {
			t.Fatalf("NewID: %v", err)
		}
		if !uuidV4.MatchString(id) {
			t.Fatalf("NewID() = %q, not a canonical v4 UUID", id)
		}
		if _, dup := seen[id]; dup {
			t.Fatalf("NewID() repeated %q", id)
		}
		seen[id] = struct{}{}
	}
}

func TestValidID(t *testing.T) {
	if !ValidID("33333333-3333-4333-8333-333333333333") {
		t.Fatal("ValidID rejected a canonical UUID")
	}
	for _, value := range []string{"", "not-a-uuid", "33333333-3333-4333-7333-333333333333"} {
		if ValidID(value) {
			t.Errorf("ValidID(%q) = true", value)
		}
	}
}

func TestFormatUUID(t *testing.T) {
	var b [16]byte
	for i := range b {
		b[i] = byte(i)
	}
	if got, want := formatUUID(b), "00010203-0405-0607-0809-0a0b0c0d0e0f"; got != want {
		t.Errorf("formatUUID = %q, want %q", got, want)
	}
}
