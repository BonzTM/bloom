package core_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/BonzTM/bloom/internal/core"
)

func TestInviteStatusPrecedence(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)
	past, maxUses, revoked := now.Add(-time.Second), 1, now
	tests := []struct {
		name   string
		invite core.Invite
		want   core.InviteStatus
	}{
		{name: "active", invite: core.Invite{}, want: core.InviteActive},
		{name: "expired", invite: core.Invite{ExpiresAt: &past}, want: core.InviteExpired},
		{name: "exhausted", invite: core.Invite{MaxUses: &maxUses, UseCount: 1}, want: core.InviteExhausted},
		{name: "revoked wins", invite: core.Invite{ExpiresAt: &past, MaxUses: &maxUses, UseCount: 1, RevokedAt: &revoked}, want: core.InviteRevoked},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			if got := testCase.invite.Status(now); got != testCase.want {
				t.Fatalf("Status = %q, want %q", got, testCase.want)
			}
		})
	}
}

func TestValidateJellyfinUsername(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		valid bool
	}{
		{name: "alice", valid: true},
		{name: "Jean Luc", valid: true},
		{name: "O'Brien+tv@example.com", valid: true},
		{name: "用户", valid: true},
		{name: " alice", valid: false},
		{name: "alice ", valid: false},
		{name: "alice/bob", valid: false},
		{name: "alice\tbob", valid: false},
		{name: ".", valid: false},
		{name: "..", valid: false},
		{name: strings.Repeat("a", 65), valid: false},
		{name: "", valid: false},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			err := core.ValidateJellyfinUsername(testCase.name)
			if testCase.valid == (err != nil) {
				t.Fatalf("ValidateJellyfinUsername(%q) = %v, valid=%t", testCase.name, err, testCase.valid)
			}
		})
	}
}

func TestInviteCodeRoundTrip(t *testing.T) {
	t.Parallel()
	code, want, err := core.NewInviteCode()
	if err != nil {
		t.Fatal(err)
	}
	if len(code) != core.InviteCodeEncodedBytes || strings.Contains(code, "=") {
		t.Fatalf("code = %q", code)
	}
	got, err := core.ParseInviteCode(code)
	if err != nil || got != want {
		t.Fatalf("ParseInviteCode = %x, %v; want %x", got, err, want)
	}
}

func FuzzParseInviteCode(f *testing.F) {
	code, _, err := core.NewInviteCode()
	if err != nil {
		f.Fatal(err)
	}
	f.Add(code)
	f.Add("")
	f.Add(strings.Repeat("A", core.InviteCodeEncodedBytes))
	f.Fuzz(func(t *testing.T, value string) {
		_, err := core.ParseInviteCode(value)
		if err != nil && !errors.Is(err, core.ErrInviteUnavailable) {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}

func FuzzValidateJellyfinUsername(f *testing.F) {
	f.Add("alice")
	f.Add("用户")
	f.Add(" bad ")
	f.Fuzz(func(t *testing.T, value string) {
		if err := core.ValidateJellyfinUsername(value); err != nil && value == "alice" {
			t.Fatalf("known-valid username rejected: %v", err)
		}
	})
}
