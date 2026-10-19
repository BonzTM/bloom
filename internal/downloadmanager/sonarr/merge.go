package sonarr

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/BonzTM/bloom/internal/downloadmanager/arr"
)

func mergeExistingSeries(raw json.RawMessage, tags []int32, requested []int) (json.RawMessage, error) {
	object, err := arr.MergeResource(raw, tags)
	if err != nil {
		return nil, err
	}
	seasons, err := mergeSeasons(object["seasons"], requested)
	if err != nil {
		return nil, err
	}
	object["seasons"] = seasons
	return arr.MarshalObject(object)
}

func mergeSeasons(raw json.RawMessage, requested []int) (json.RawMessage, error) {
	var seasons []json.RawMessage
	if err := json.Unmarshal(raw, &seasons); err != nil {
		return nil, fmt.Errorf("decode seasons: %w", err)
	}
	if seasons == nil {
		return nil, errors.New("seasons must be an array")
	}
	for index, season := range seasons {
		merged, selected, err := mergeSeason(season, requested)
		if err != nil {
			return nil, fmt.Errorf("merge season %d: %w", index, err)
		}
		if selected {
			seasons[index] = merged
		}
	}
	return arr.MarshalArray(seasons)
}

func mergeSeason(raw json.RawMessage, requested []int) (json.RawMessage, bool, error) {
	object, err := arr.DecodeObject(raw)
	if err != nil {
		return nil, false, err
	}
	var number *int
	if value, exists := object["seasonNumber"]; exists {
		if decodeErr := json.Unmarshal(value, &number); decodeErr != nil {
			return nil, false, fmt.Errorf("decode season number: %w", decodeErr)
		}
	}
	if number == nil || !slices.Contains(requested, *number) {
		return raw, false, nil
	}
	object["monitored"] = json.RawMessage(`true`)
	merged, err := arr.MarshalObject(object)
	return merged, true, err
}
