package oidc

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	coreoidc "github.com/coreos/go-oidc/v3/oidc"
)

func TestParseRetryAfter(t *testing.T) {
	now := time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)
	tests := []struct {
		value string
		want  time.Duration
		ok    bool
	}{
		{value: "12", want: 12 * time.Second, ok: true},
		{value: now.Add(3 * time.Second).Format(http.TimeFormat), want: 3 * time.Second, ok: true},
		{value: "invalid", ok: false},
	}
	for _, testCase := range tests {
		got, ok := parseRetryAfter(testCase.value, now)
		if got != testCase.want || ok != testCase.ok {
			t.Errorf("parseRetryAfter(%q) = %s, %v; want %s, %v", testCase.value, got, ok, testCase.want, testCase.ok)
		}
	}
}

func TestValidateIdentityClaims(t *testing.T) {
	tests := []struct {
		name, issuer, subject string
		valid                 bool
	}{
		{name: "valid", issuer: "https://id.example", subject: "subject-1", valid: true},
		{name: "empty issuer", subject: "subject-1"},
		{name: "oversized issuer", issuer: strings.Repeat("i", maxIssuerBytes+1), subject: "subject-1"},
		{name: "empty subject", issuer: "https://id.example"},
		{name: "oversized subject", issuer: "https://id.example", subject: strings.Repeat("s", maxSubjectBytes+1)},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			err := validateIdentityClaims(&coreoidc.IDToken{Issuer: testCase.issuer, Subject: testCase.subject})
			if (err == nil) != testCase.valid {
				t.Fatalf("validateIdentityClaims error = %v, want valid %v", err, testCase.valid)
			}
		})
	}
}

func FuzzRoleClaimParser(f *testing.F) {
	for _, seed := range []string{`"admin"`, `["admin","member"]`, `[]`, `{}`, `null`, `[`} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		claims := map[string]json.RawMessage{"groups": json.RawMessage(raw)}
		values, err := roleValues(claims, "groups")
		if err != nil {
			return
		}
		if len(values) > maxRoleValues {
			t.Fatalf("role count = %d", len(values))
		}
		for _, value := range values {
			if value == "" || len(value) > maxRoleValueBytes {
				t.Fatalf("unbounded role value %q", value)
			}
		}
	})
}

func FuzzValidateIdentityClaims(f *testing.F) {
	f.Add("https://id.example", "subject-1")
	f.Add("", "subject-1")
	f.Add("https://id.example", "")
	f.Add(strings.Repeat("i", maxIssuerBytes+1), "subject-1")
	f.Add("https://id.example", strings.Repeat("s", maxSubjectBytes+1))
	f.Fuzz(func(t *testing.T, issuer, subject string) {
		token := &coreoidc.IDToken{Issuer: issuer, Subject: subject}
		err := validateIdentityClaims(token)
		valid := issuer != "" && len(issuer) <= maxIssuerBytes && utf8.ValidString(issuer) &&
			subject != "" && len(subject) <= maxSubjectBytes && utf8.ValidString(subject)
		if (err == nil) != valid {
			t.Fatalf("validateIdentityClaims(%d issuer bytes, %d subject bytes) error = %v, want valid %v",
				len(issuer), len(subject), err, valid)
		}
	})
}
