package radarr

import (
	"encoding/json"

	"github.com/BonzTM/bloom/internal/downloadmanager/arr"
)

func mergeExistingMovie(raw json.RawMessage, tags []int32) (json.RawMessage, error) {
	object, err := arr.MergeResource(raw, tags)
	if err != nil {
		return nil, err
	}
	return arr.MarshalObject(object)
}
