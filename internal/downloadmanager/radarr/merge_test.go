package radarr

import (
	"encoding/json"
	"testing"
)

func TestMergeExistingMoviePreservesFieldsAndUnionsTags(t *testing.T) {
	t.Parallel()
	raw := json.RawMessage(`{
		"id":17,
		"qualityProfileId":7,
		"rootFolderPath":"/operator/movies",
		"path":"/operator/movies/film",
		"monitored":false,
		"tags":[8,2,8],
		"addOptions":{"searchForMovie":false},
		"language":{ "id" : 1, "name" : "English", "symbol":"<" },
		"folders":[{"path":"/operator/movies/film","size":9007199254740993}],
		"translations":[{"title":"Film","language":{"id":1}}]
	}`)

	merged, err := mergeExistingMovie(raw, []int32{2, 9, 9})
	if err != nil {
		t.Fatalf("mergeExistingMovie: %v", err)
	}
	object := decodeRawObjectForTest(t, merged)
	assertRawValue(t, object, "qualityProfileId", `7`)
	assertRawValue(t, object, "rootFolderPath", `"/operator/movies"`)
	assertRawValue(t, object, "path", `"/operator/movies/film"`)
	assertRawValue(t, object, "monitored", `true`)
	assertRawValue(t, object, "tags", `[8,2,9]`)
	assertRawValue(t, object, "language", `{ "id" : 1, "name" : "English", "symbol":"<" }`)
	assertRawValue(t, object, "folders", `[{"path":"/operator/movies/film","size":9007199254740993}]`)
	assertRawValue(t, object, "translations", `[{"title":"Film","language":{"id":1}}]`)
	if _, exists := object["addOptions"]; exists {
		t.Fatal("addOptions remains in merged movie")
	}
}

func TestMergeExistingMovieRejectsMalformedShapes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		raw  string
	}{
		{name: "invalid JSON", raw: `{`},
		{name: "not object", raw: `[]`},
		{name: "tags not array", raw: `{"tags":{}}`},
		{name: "tag not integer", raw: `{"tags":[1.5]}`},
		{name: "tag not number", raw: `{"tags":["1"]}`},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			if _, err := mergeExistingMovie(json.RawMessage(testCase.raw), []int32{2}); err == nil {
				t.Fatal("mergeExistingMovie returned nil error")
			}
		})
	}
}

func TestMergeExistingMovieTreatsNullOrAbsentTagsAsEmpty(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{`{}`, `{"tags":null}`} {
		merged, err := mergeExistingMovie(json.RawMessage(raw), []int32{2})
		if err != nil {
			t.Fatalf("mergeExistingMovie(%s): %v", raw, err)
		}
		assertRawValue(t, decodeRawObjectForTest(t, merged), "tags", `[2]`)
	}
}

func decodeRawObjectForTest(t *testing.T, raw json.RawMessage) map[string]json.RawMessage {
	t.Helper()
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		t.Fatalf("decode object: %v", err)
	}
	return object
}

func assertRawValue(t *testing.T, object map[string]json.RawMessage, key, want string) {
	t.Helper()
	got, exists := object[key]
	if !exists {
		t.Fatalf("missing %q in %s", key, mustJSON(object))
	}
	if string(got) != want {
		t.Fatalf("%s = %s, want %s", key, got, want)
	}
}

func mustJSON(value any) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "<invalid JSON>"
	}
	return string(encoded)
}

func TestFindExistingMovieBoundsResultList(t *testing.T) {
	t.Parallel()
	list := func(count int) json.RawMessage {
		items := make([]map[string]any, count)
		for index := range items {
			items[index] = map[string]any{"id": index + 1, "tmdbId": 1}
		}
		value, err := json.Marshal(items)
		if err != nil {
			t.Fatalf("marshal list: %v", err)
		}
		return value
	}
	if _, err := findExistingMovie(list(maxMovieResults), 2); err != nil {
		t.Fatalf("list at the limit: %v", err)
	}
	if _, err := findExistingMovie(list(maxMovieResults+1), 2); err == nil {
		t.Fatal("list past the limit was accepted")
	}
}
