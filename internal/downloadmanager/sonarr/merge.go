package sonarr

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/BonzTM/bloom/internal/downloadmanager/arr"
)

// Season numbers run 0..999 and are unique, so a series has at most 1000.
const (
	maxSeasonNumber = 999
	maxSeasons      = maxSeasonNumber + 1
)

type mergedSeries struct {
	body          json.RawMessage
	searchSeasons []int
}

type mergedSeasons struct {
	body          json.RawMessage
	searchSeasons []int
}

type mergedSeason struct {
	body   json.RawMessage
	number int32
	search bool
}

type missingSeasonsError struct {
	numbers []int
}

func hasSeasonCollection(raw json.RawMessage) (bool, error) {
	object, err := arr.DecodeObject(raw)
	if err != nil {
		return false, err
	}
	seasons, exists := object["seasons"]
	return exists && !bytes.Equal(bytes.TrimSpace(seasons), []byte("null")), nil
}

func (e *missingSeasonsError) Error() string {
	return fmt.Sprintf("requested seasons not found: %v", e.numbers)
}

func mergeExistingSeries(raw json.RawMessage, tags []int32, requested []int) (mergedSeries, error) {
	object, err := arr.MergeResource(raw, tags)
	if err != nil {
		return mergedSeries{}, err
	}
	seasons, err := mergeSeasonCollection(object["seasons"], requested)
	if err != nil {
		return mergedSeries{}, err
	}
	object["seasons"] = seasons.body
	body, err := arr.MarshalObject(object)
	if err != nil {
		return mergedSeries{}, err
	}
	return mergedSeries{body: body, searchSeasons: seasons.searchSeasons}, nil
}

func mergeSeasonCollection(raw json.RawMessage, requested []int) (mergedSeasons, error) {
	values, err := decodeSeasons(raw)
	if err != nil {
		return mergedSeasons{}, err
	}
	requestedSet := makeSeasonSet(requested)
	matched := make(map[int]bool, len(requested))
	search := make(map[int]bool, len(requested))
	seen := make(map[int32]struct{}, len(values))
	for index, rawSeason := range values {
		season, mergeErr := mergeOneSeason(rawSeason, requestedSet)
		if mergeErr != nil {
			return mergedSeasons{}, fmt.Errorf("merge season %d: %w", index, mergeErr)
		}
		if _, exists := seen[season.number]; exists {
			return mergedSeasons{}, errors.New("duplicate seasonNumber")
		}
		seen[season.number] = struct{}{}
		values[index] = season.body
		if requestedSet[int(season.number)] {
			matched[int(season.number)] = true
			search[int(season.number)] = season.search
		}
	}
	return finishSeasonCollection(values, requested, matched, search)
}

func decodeSeasons(raw json.RawMessage) ([]json.RawMessage, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, nil
	}
	values, err := arr.DecodeArray(raw, maxSeasons, "seasons")
	if err != nil {
		return nil, fmt.Errorf("decode seasons: %w", err)
	}
	return values, nil
}

func makeSeasonSet(requested []int) map[int]bool {
	values := make(map[int]bool, len(requested))
	for _, number := range requested {
		values[number] = true
	}
	return values
}

func mergeOneSeason(raw json.RawMessage, requested map[int]bool) (mergedSeason, error) {
	object, err := arr.DecodeObject(raw)
	if err != nil {
		return mergedSeason{}, err
	}
	number, err := arr.DecodeInt32(object["seasonNumber"], "seasonNumber")
	if err != nil {
		return mergedSeason{}, err
	}
	// Season zero holds specials; Bloom's season domain ends at 999.
	if number < 0 || number > maxSeasonNumber {
		return mergedSeason{}, fmt.Errorf("seasonNumber %d is out of range", number)
	}
	result := mergedSeason{body: raw, number: number}
	if !requested[int(number)] {
		return result, nil
	}
	result.search, err = seasonNeedsSearch(object)
	if err != nil {
		return mergedSeason{}, err
	}
	object["monitored"] = json.RawMessage(`true`)
	result.body, err = arr.MarshalObject(object)
	return result, err
}

// seasonNeedsSearch reports whether a requested season may lack files.
// Sonarr's episodeCount counts only monitored or downloaded episodes, so an
// unmonitored season reads 0 of 0; the file count is compared with the
// season's total instead. Missing counts mean unknown, which searches.
func seasonNeedsSearch(object map[string]json.RawMessage) (bool, error) {
	raw, exists := object["statistics"]
	if !exists || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return true, nil
	}
	statistics, err := arr.DecodeObject(raw)
	if err != nil {
		return false, fmt.Errorf("season statistics: %w", err)
	}
	files, filesKnown, err := arr.DecodeOptionalInt32(statistics, "episodeFileCount")
	if err != nil {
		return false, err
	}
	total, totalKnown, err := arr.DecodeOptionalInt32(statistics, "totalEpisodeCount")
	if err != nil {
		return false, err
	}
	if !filesKnown || !totalKnown || total <= 0 {
		return true, nil
	}
	return files < total, nil
}

func finishSeasonCollection(
	values []json.RawMessage, requested []int, matched, search map[int]bool,
) (mergedSeasons, error) {
	missing := missingSeasons(requested, matched)
	if len(missing) > 0 {
		return mergedSeasons{}, &missingSeasonsError{numbers: missing}
	}
	body, err := arr.MarshalArray(values, maxSeasons, "seasons")
	if err != nil {
		return mergedSeasons{}, err
	}
	searchSeasons := make([]int, 0, len(requested))
	for _, number := range requested {
		if search[number] {
			searchSeasons = append(searchSeasons, number)
		}
	}
	return mergedSeasons{body: body, searchSeasons: searchSeasons}, nil
}

func missingSeasons(requested []int, matched map[int]bool) []int {
	missing := make([]int, 0, len(requested))
	for _, number := range requested {
		if !matched[number] {
			missing = append(missing, number)
		}
	}
	slices.Sort(missing)
	return missing
}
