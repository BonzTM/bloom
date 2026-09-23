package core_test

import (
	"testing"

	"github.com/BonzTM/bloom/internal/core"
)

func FuzzValidateMediaServerURL(f *testing.F) {
	for _, seed := range []struct {
		url           string
		allowInsecure bool
	}{
		{url: "https://media.example.test"},
		{url: "http://media.example.test/jellyfin", allowInsecure: true},
		{url: "ftp://bad"},
		{url: "https://user:pass@host"},
		{url: "https://[fd00:ec2::254]"},
		{url: "https://[fd00:0ec2:0000:0000:0000:0000:0000:0254]"},
		{url: "https://[fd00:ec2::254%25eth0]"},
		{url: "https://[fd00:0ec2:0000:0000:0000:0000:0000:0254%25eth0]"},
		{url: "https://[fd12::1%25eth0]"},
		{url: "https://[::ffff:192.168.1.10%25eth0]"},
	} {
		f.Add(seed.url, seed.allowInsecure)
	}
	f.Fuzz(func(t *testing.T, raw string, allowInsecure bool) {
		normalized, err := core.ValidateMediaServerURL(raw, allowInsecure)
		if err == nil {
			if normalized == "" {
				t.Fatal("valid URL normalized to empty")
			}
			if second, secondErr := core.ValidateMediaServerURL(normalized, allowInsecure); secondErr != nil || second != normalized {
				t.Fatalf("normalization is not idempotent: %q, %v", second, secondErr)
			}
		}
	})
}

func TestValidateMediaServerURLSecurityPolicy(t *testing.T) {
	tests := []struct {
		url           string
		allowInsecure bool
		valid         bool
	}{
		{url: "https://media.example.test", valid: true},
		{url: "https://media.example.test", allowInsecure: true},
		{url: "http://media.example.test", allowInsecure: true, valid: true},
		{url: "http://media.example.test"},
		{url: "https://user:pass@media.example.test"},
		{url: "https://media.example.test/#fragment"},
		{url: "https://"},
		{url: "https://127.0.0.1"},
		{url: "https://169.254.169.254"},
		{url: "https://[fd00:ec2::254]"},
		{url: "https://[fd00:0ec2:0000:0000:0000:0000:0000:0254]"},
		{url: "https://[fd00:ec2::254%25eth0]"},
		{url: "https://[fd00:0ec2:0000:0000:0000:0000:0000:0254%25eth0]"},
		{url: "https://[fd12::1%25eth0]"},
		{url: "https://[::ffff:192.168.1.10%25eth0]"},
		{url: "https://[::]"},
		{url: "https://224.0.0.1"},
		{url: "https://192.168.1.10", valid: true},
	}
	for _, testCase := range tests {
		_, err := core.ValidateMediaServerURL(testCase.url, testCase.allowInsecure)
		if (err == nil) != testCase.valid {
			t.Errorf("ValidateMediaServerURL(%q, %t) error=%v, valid=%t", testCase.url, testCase.allowInsecure, err, testCase.valid)
		}
	}
}

func TestMediaServerNameKeyFoldsAndNormalizes(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"mixed case":        "HOME Server",
		"composed accent":   "\u00c9clair",
		"decomposed accent": "E\u0301clair",
	}
	want := map[string]string{
		"mixed case":        "home server",
		"composed accent":   "\u00e9clair",
		"decomposed accent": "\u00e9clair",
	}
	for name, input := range tests {
		if got := core.MediaServerNameKey(input); got != want[name] {
			t.Errorf("MediaServerNameKey(%q) = %q, want %q", input, got, want[name])
		}
	}
}
