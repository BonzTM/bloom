package core

import (
	"strings"
	"testing"
)

func TestMediaServerExclusionsBounds(t *testing.T) {
	base := MediaServerExclusions{MediaServerID: "00000000-0000-4000-8000-000000000001"}
	tests := []struct {
		name  string
		value MediaServerExclusions
	}{
		{name: "invalid server", value: MediaServerExclusions{MediaServerID: "server"}},
		{name: "empty user", value: withExcludedUser(base, "")},
		{name: "oversized user", value: withExcludedUser(base, strings.Repeat("u", MaxExclusionIDBytes+1))},
		{name: "control user", value: withExcludedUser(base, "user\n")},
		{name: "duplicate user", value: MediaServerExclusions{MediaServerID: base.MediaServerID, MediaUserIDs: []string{"user", "user"}}},
		{name: "empty library", value: MediaServerExclusions{MediaServerID: base.MediaServerID, LibraryIDs: []string{""}}},
		{name: "oversized total", value: MediaServerExclusions{MediaServerID: base.MediaServerID, MediaUserIDs: exclusionIDs(MaxMediaServerExclusions + 1)}},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			if testCase.value.Valid() {
				t.Fatalf("Valid(%+v) = true", testCase.value)
			}
		})
	}
}

func withExcludedUser(value MediaServerExclusions, id string) MediaServerExclusions {
	value.MediaUserIDs = []string{id}
	return value
}

func exclusionIDs(count int) []string {
	values := make([]string, count)
	for index := range values {
		values[index] = strings.Repeat("x", index/10+1) + string(rune('0'+index%10))
	}
	return values
}
