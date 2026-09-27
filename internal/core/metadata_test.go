package core

import (
	"errors"
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

func TestValidateMetadataDiscoverBoundsTMDBPages(t *testing.T) {
	tests := []struct {
		name  string
		input MetadataDiscover
		valid bool
	}{
		{name: "first trending page", input: MetadataDiscover{List: MetadataTrending, Page: 1}, valid: true},
		{name: "last upcoming page", input: MetadataDiscover{List: MetadataSeriesUpcoming, Page: MaxMetadataPage}, valid: true},
		{name: "unknown list", input: MetadataDiscover{List: "unknown", Page: 1}},
		{name: "zero page", input: MetadataDiscover{List: MetadataTrending, Page: 0}},
		{name: "page above provider bound", input: MetadataDiscover{List: MetadataTrending, Page: MaxMetadataPage + 1}},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			err := ValidateMetadataDiscover(testCase.input)
			if testCase.valid && err != nil {
				t.Fatalf("ValidateMetadataDiscover() error = %v", err)
			}
			if !testCase.valid && !errors.Is(err, ErrInvalidArgument) {
				t.Fatalf("ValidateMetadataDiscover() error = %v, want ErrInvalidArgument", err)
			}
		})
	}
}

func TestValidateMetadataTitleIncludesBackdropBound(t *testing.T) {
	title := MetadataTitle{
		Kind: MediaKindMovie, Provider: MetadataProviderTMDB, ProviderID: "11", Title: "Film",
		BackdropPath: "/backdrop.jpg",
	}
	if err := ValidateMetadataTitle(title); err != nil {
		t.Fatalf("ValidateMetadataTitle(valid) error = %v", err)
	}
	title.BackdropPath = "/" + strings.Repeat("a", MaxMetadataBackdropPathBytes)
	if !errors.Is(ValidateMetadataTitle(title), ErrInvalidArgument) {
		t.Fatal("ValidateMetadataTitle accepted an oversized backdrop path")
	}
}

func TestMetadataRequestStateIsClosed(t *testing.T) {
	valid := []MetadataRequestState{
		MetadataRequestNone, MetadataRequestPending, MetadataRequestApproved, MetadataRequestProcessing,
		MetadataRequestAvailable, MetadataRequestDeclined, MetadataRequestFailed,
	}
	for _, state := range valid {
		if !state.Valid() {
			t.Errorf("state %q is invalid", state)
		}
	}
	if MetadataRequestState("unknown").Valid() {
		t.Fatal("unknown request state is valid")
	}
}
