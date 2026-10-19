package sonarr

import (
	"encoding/json"
	"fmt"
	"slices"
	"testing"
)

func TestMergeExistingSeriesPreservesFieldsAndUpdatesRequestedState(t *testing.T) {
	t.Parallel()
	raw := json.RawMessage(`{
		"id":19,
		"qualityProfileId":7,
		"rootFolderPath":"/operator/series",
		"path":"/operator/series/show",
		"monitored":false,
		"tags":[8,2,8],
		"addOptions":{"monitor":"all"},
		"language":{ "id" : 1, "name" : "English", "symbol":"<" },
		"folders":[{"path":"/operator/series/show","size":9007199254740993}],
		"translations":[{"title":"Show","language":{"id":1}}],
		"seasons":[
			{"seasonNumber":1,"monitored":false,"seasonType":"standard","future":{ "value" : 9007199254740993 }},
			{"seasonNumber":2,"monitored":false,"seasonType":"standard","future":{ "value" : 9007199254740995 }},
			{"seasonNumber":3,"monitored":true,"seasonType":"standard"}
		]
	}`)

	result, err := mergeExistingSeries(raw, []int32{2, 9, 9}, []int{2})
	if err != nil {
		t.Fatalf("mergeExistingSeries: %v", err)
	}
	if !slices.Equal(result.searchSeasons, []int{2}) {
		t.Fatalf("search seasons = %v, want [2]", result.searchSeasons)
	}
	object := decodeRawObjectForTest(t, result.body)
	assertRawValue(t, object, "qualityProfileId", `7`)
	assertRawValue(t, object, "rootFolderPath", `"/operator/series"`)
	assertRawValue(t, object, "path", `"/operator/series/show"`)
	assertRawValue(t, object, "monitored", `true`)
	assertRawValue(t, object, "tags", `[8,2,9]`)
	assertRawValue(t, object, "language", `{ "id" : 1, "name" : "English", "symbol":"<" }`)
	assertRawValue(t, object, "folders", `[{"path":"/operator/series/show","size":9007199254740993}]`)
	assertRawValue(t, object, "translations", `[{"title":"Show","language":{"id":1}}]`)
	if _, exists := object["addOptions"]; exists {
		t.Fatal("addOptions remains in merged series")
	}

	var seasons []map[string]json.RawMessage
	if err := json.Unmarshal(object["seasons"], &seasons); err != nil {
		t.Fatalf("decode seasons: %v", err)
	}
	assertRawValue(t, seasons[0], "monitored", `false`)
	assertRawValue(t, seasons[0], "seasonType", `"standard"`)
	assertRawValue(t, seasons[0], "future", `{ "value" : 9007199254740993 }`)
	assertRawValue(t, seasons[1], "monitored", `true`)
	assertRawValue(t, seasons[1], "future", `{ "value" : 9007199254740995 }`)
	assertRawValue(t, seasons[2], "monitored", `true`)
}

func TestMergeExistingSeriesSelectsSearchesFromFileState(t *testing.T) {
	t.Parallel()
	raw := json.RawMessage(`{"tags":[],"seasons":[
		{"seasonNumber":1,"statistics":{"episodeCount":8,"episodeFileCount":3,"totalEpisodeCount":8}},
		{"seasonNumber":2,"statistics":{"episodeCount":8,"episodeFileCount":8,"totalEpisodeCount":8}},
		{"seasonNumber":3}
	]}`)
	result, err := mergeExistingSeries(raw, nil, []int{1, 2, 3})
	if err != nil {
		t.Fatalf("mergeExistingSeries: %v", err)
	}
	if !slices.Equal(result.searchSeasons, []int{1, 3}) {
		t.Fatalf("search seasons = %v, want [1 3]", result.searchSeasons)
	}
}

func TestMergeExistingSeriesRejectsMalformedShapes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		raw  string
	}{
		{name: "invalid JSON", raw: `{`},
		{name: "not object", raw: `[]`},
		{name: "seasons not array", raw: `{"tags":[],"seasons":{}}`},
		{name: "season not object", raw: `{"tags":[],"seasons":[1]}`},
		{name: "tags not array", raw: `{"tags":{},"seasons":[]}`},
		{name: "tag not integer", raw: `{"tags":[1.5],"seasons":[]}`},
		{name: "tag not number", raw: `{"tags":["1"],"seasons":[]}`},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			if _, err := mergeExistingSeries(json.RawMessage(testCase.raw), []int32{2}, []int{1}); err == nil {
				t.Fatal("mergeExistingSeries returned nil error")
			}
		})
	}
}

func TestMergeExistingSeriesRejectsDuplicateSeasonMembers(t *testing.T) {
	t.Parallel()
	raw := json.RawMessage(`{"tags":[],"seasons":[{"seasonNumber":1,"seasonNumber":2}]}`)
	if _, err := mergeExistingSeries(raw, nil, []int{1}); err == nil {
		t.Fatal("mergeExistingSeries returned nil error")
	}
}

func TestMergeExistingSeriesValidatesSeasonNumbers(t *testing.T) {
	t.Parallel()
	values := []string{
		`{"tags":[],"seasons":[{"monitored":false}]}`,
		`{"tags":[],"seasons":[{"seasonNumber":1},{"seasonNumber":1}]}`,
		`{"tags":[],"seasons":[{"seasonNumber":2147483648}]}`,
	}
	for _, raw := range values {
		if _, err := mergeExistingSeries(json.RawMessage(raw), nil, []int{1}); err == nil {
			t.Fatalf("mergeExistingSeries(%s) returned nil error", raw)
		}
	}
}

func TestMergeExistingSeriesBoundsSeasons(t *testing.T) {
	t.Parallel()
	if _, err := mergeExistingSeries(seriesWithSeasons(t, maxSeasons), nil, []int{1}); err != nil {
		t.Fatalf("mergeExistingSeries at limit: %v", err)
	}
	if _, err := mergeExistingSeries(seriesWithSeasons(t, maxSeasons+1), nil, []int{1}); err == nil {
		t.Fatal("mergeExistingSeries over limit returned nil error")
	}
}

func seriesWithSeasons(t *testing.T, count int) json.RawMessage {
	t.Helper()
	seasons := make([]map[string]any, count)
	for index := range count {
		seasons[index] = map[string]any{"seasonNumber": index, "monitored": false}
	}
	value, err := json.Marshal(map[string]any{"tags": []int{}, "seasons": seasons})
	if err != nil {
		t.Fatalf("marshal series: %v", err)
		return nil
	}
	return value
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

func TestMergeExistingSeriesSearchesBySeasonTotal(t *testing.T) {
	t.Parallel()
	// Sonarr counts only monitored or downloaded episodes in episodeCount,
	// so an unmonitored season with nothing on disk reads 0 of 0.
	raw := json.RawMessage(`{"tags":[],"seasons":[
		{"seasonNumber":1,"monitored":false,"statistics":{"episodeCount":0,"episodeFileCount":0,"totalEpisodeCount":8}},
		{"seasonNumber":2,"monitored":true,"statistics":{"episodeCount":8,"episodeFileCount":8,"totalEpisodeCount":8}},
		{"seasonNumber":3,"monitored":false,"statistics":{"episodeCount":0}}
	]}`)
	result, err := mergeExistingSeries(raw, nil, []int{1, 2, 3})
	if err != nil {
		t.Fatalf("mergeExistingSeries: %v", err)
	}
	if !slices.Equal(result.searchSeasons, []int{1, 3}) {
		t.Fatalf("search seasons = %v, want [1 3]", result.searchSeasons)
	}
}

func TestMergeExistingSeriesRejectsDuplicateStatisticsMembers(t *testing.T) {
	t.Parallel()
	raw := json.RawMessage(`{"tags":[],"seasons":[
		{"seasonNumber":1,"statistics":{"totalEpisodeCount":8,"totalEpisodeCount":0,"episodeFileCount":0}}
	]}`)
	if _, err := mergeExistingSeries(raw, nil, []int{1}); err == nil {
		t.Fatal("duplicate statistics member was accepted")
	}
}

func TestMergeExistingSeriesBoundsSeasonNumbers(t *testing.T) {
	t.Parallel()
	for _, number := range []int{0, 999} {
		raw := json.RawMessage(fmt.Sprintf(`{"tags":[],"seasons":[{"seasonNumber":%d}]}`, number))
		if _, err := mergeExistingSeries(raw, nil, []int{number}); err != nil {
			t.Errorf("season %d: %v", number, err)
		}
	}
	for _, number := range []int{-1, 1000} {
		raw := json.RawMessage(fmt.Sprintf(`{"tags":[],"seasons":[{"seasonNumber":%d}]}`, number))
		if _, err := mergeExistingSeries(raw, nil, []int{1}); err == nil {
			t.Errorf("season %d was accepted", number)
		}
	}
}

func TestFindExistingSeriesBoundsResultList(t *testing.T) {
	t.Parallel()
	list := func(count int) json.RawMessage {
		items := make([]map[string]any, count)
		for index := range items {
			items[index] = map[string]any{"id": index + 1, "tvdbId": 1}
		}
		value, err := json.Marshal(items)
		if err != nil {
			t.Fatalf("marshal list: %v", err)
		}
		return value
	}
	if _, err := findExistingSeries(list(maxSeriesResults), 2); err != nil {
		t.Fatalf("list at the limit: %v", err)
	}
	if _, err := findExistingSeries(list(maxSeriesResults+1), 2); err == nil {
		t.Fatal("list past the limit was accepted")
	}
}
