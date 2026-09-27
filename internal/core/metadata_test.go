package core

import (
	"strings"
	"testing"
)

const testTMDBReadAccessToken = "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJhdWQiOiJ0bWRiIiwic3ViIjoiYmxvb20tdGVzdCIsImlhdCI6MTcwMDAwMDAwMH0.c2lnbmF0dXJlLXNpZ25hdHVyZS1zaWduYXR1cmUtc2lnbmF0dXJl"

func TestValidateMetadataCredentialRequiresTMDBReadAccessToken(t *testing.T) {
	tests := []struct {
		name  string
		value string
		valid bool
	}{
		{name: "read access token", value: testTMDBReadAccessToken, valid: true},
		{name: "v3 API key", value: "0123456789abcdef0123456789abcdef"},
		{name: "too short", value: "eyJ.e30.c2ln"},
		{name: "too long", value: testTMDBReadAccessToken + strings.Repeat("a", 4096)},
		{name: "two segments", value: strings.Replace(testTMDBReadAccessToken, ".", "", 1)},
		{name: "four segments", value: testTMDBReadAccessToken + ".extra"},
		{name: "wrong header", value: "e30" + testTMDBReadAccessToken[3:]},
		{name: "padded base64url", value: testTMDBReadAccessToken + "="},
		{name: "invalid base64url", value: testTMDBReadAccessToken[:len(testTMDBReadAccessToken)-1] + "+"},
		{name: "whitespace", value: testTMDBReadAccessToken + " "},
		{name: "control", value: testTMDBReadAccessToken + "\n"},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			err := ValidateMetadataCredential(MetadataProviderTMDB, testCase.value)
			if (err == nil) != testCase.valid {
				t.Fatalf("ValidateMetadataCredential() error = %v, valid = %t", err, testCase.valid)
			}
		})
	}
}
