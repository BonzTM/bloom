package core

import (
	"slices"
	"testing"
	"unicode/utf8"
)

func TestMapOIDCRoles(t *testing.T) {
	t.Parallel()
	got := MapOIDCRoles(
		[]string{"users", "admins", "users", "ignored"},
		map[string]string{"admins": "owner", "users": "member"},
	)
	want := []string{"member", "owner"}
	if !slices.Equal(got, want) {
		t.Fatalf("MapOIDCRoles = %v, want %v", got, want)
	}
}

func TestOIDCUsername(t *testing.T) {
	t.Parallel()
	base, err := OIDCUsername("Alice Example@example.com", "subject-1", 0)
	if err != nil || base != "alice-example-example-com" {
		t.Fatalf("OIDCUsername base = %q, %v", base, err)
	}
	collision, err := OIDCUsername("Alice Example@example.com", "subject-1", 1)
	if err != nil || collision != "alice-example-example-com-95efa64d" {
		t.Fatalf("OIDCUsername collision = %q, %v", collision, err)
	}
}

func FuzzOIDCUsername(f *testing.F) {
	f.Add("Alice Example", "https://id.example\x00subject-1", uint8(0))
	f.Add("🪻 user / name", "issuer\x00subject", uint8(1))
	f.Add("", "issuer\x00subject", uint8(9))
	f.Fuzz(func(t *testing.T, claim, identity string, collision uint8) {
		username, err := OIDCUsername(claim, identity, int(collision%10))
		if err != nil {
			return
		}
		if utf8.RuneCountInString(username) > MaxUsernameCharacters {
			t.Fatalf("username output exceeds limit: %q", username)
		}
		if _, err := CanonicalUsername(username); err != nil {
			t.Fatalf("username output is not canonical: %q: %v", username, err)
		}
	})
}
