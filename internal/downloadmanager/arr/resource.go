package arr

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
)

// MergeResource prepares an existing Arr resource for a full-record update.
func MergeResource(raw json.RawMessage, tags []int32) (map[string]json.RawMessage, error) {
	object, err := DecodeObject(raw)
	if err != nil {
		return nil, err
	}
	mergedTags, err := mergeTags(object["tags"], tags)
	if err != nil {
		return nil, err
	}
	object["monitored"] = json.RawMessage(`true`)
	object["tags"] = mergedTags
	delete(object, "addOptions")
	return object, nil
}

// DecodeObject decodes one JSON object while retaining every field value as raw JSON.
func DecodeObject(raw json.RawMessage) (map[string]json.RawMessage, error) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		return nil, fmt.Errorf("decode resource object: %w", err)
	}
	if object == nil {
		return nil, errors.New("resource must be a JSON object")
	}
	return object, nil
}

// MarshalObject encodes a raw resource object without decoding retained values.
func MarshalObject(object map[string]json.RawMessage) (json.RawMessage, error) {
	if object == nil {
		return nil, errors.New("resource object is nil")
	}
	keys := make([]string, 0, len(object))
	for key := range object {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	encoded := []byte{'{'}
	for index, key := range keys {
		field, err := marshalField(key, object[key])
		if err != nil {
			return nil, err
		}
		if index > 0 {
			encoded = append(encoded, ',')
		}
		encoded = append(encoded, field...)
	}
	return append(encoded, '}'), nil
}

// MarshalArray encodes raw JSON values without rewriting their bytes.
func MarshalArray(values []json.RawMessage) (json.RawMessage, error) {
	if values == nil {
		return nil, errors.New("resource array is nil")
	}
	encoded := []byte{'['}
	for index, value := range values {
		if !json.Valid(value) {
			return nil, fmt.Errorf("array value %d is invalid JSON", index)
		}
		if index > 0 {
			encoded = append(encoded, ',')
		}
		encoded = append(encoded, value...)
	}
	return append(encoded, ']'), nil
}

func mergeTags(raw json.RawMessage, requested []int32) (json.RawMessage, error) {
	existing, err := decodeIntegerArray(raw)
	if err != nil {
		return nil, fmt.Errorf("decode tags: %w", err)
	}
	seen := make(map[int64]struct{}, len(existing)+len(requested))
	merged := make([]int64, 0, len(existing)+len(requested))
	for _, tag := range existing {
		merged = appendUniqueTag(merged, seen, tag)
	}
	for _, tag := range requested {
		merged = appendUniqueTag(merged, seen, int64(tag))
	}
	encoded, err := json.Marshal(merged)
	if err != nil {
		return nil, fmt.Errorf("encode tags: %w", err)
	}
	return json.RawMessage(encoded), nil
}

func decodeIntegerArray(raw json.RawMessage) ([]int64, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var decoded any
	if err := decoder.Decode(&decoded); err != nil {
		return nil, err
	}
	if err := requireJSONEnd(decoder); err != nil {
		return nil, err
	}
	values, ok := decoded.([]any)
	if !ok {
		return nil, errors.New("value must be an integer array")
	}
	result := make([]int64, 0, len(values))
	for _, value := range values {
		number, numberOK := value.(json.Number)
		if !numberOK {
			return nil, errors.New("array value must be an integer")
		}
		integer, err := number.Int64()
		if err != nil {
			return nil, fmt.Errorf("array value must be an integer: %w", err)
		}
		result = append(result, integer)
	}
	return result, nil
}

func requireJSONEnd(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return fmt.Errorf("decode trailing JSON: %w", err)
	}
	return nil
}

func appendUniqueTag(values []int64, seen map[int64]struct{}, tag int64) []int64 {
	if _, exists := seen[tag]; exists {
		return values
	}
	seen[tag] = struct{}{}
	return append(values, tag)
}

func marshalField(key string, value json.RawMessage) ([]byte, error) {
	if !json.Valid(value) {
		return nil, fmt.Errorf("resource field %q is invalid JSON", key)
	}
	encodedKey, err := json.Marshal(key)
	if err != nil {
		return nil, fmt.Errorf("encode resource field name: %w", err)
	}
	field := make([]byte, 0, len(encodedKey)+len(value)+1)
	field = append(field, encodedKey...)
	field = append(field, ':')
	return append(field, value...), nil
}
