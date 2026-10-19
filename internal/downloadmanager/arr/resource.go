package arr

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
)

const (
	maxObjectMembers = 4096
	maxTags          = 1024
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
	if len(object) > maxObjectMembers {
		return nil, fmt.Errorf("object member count exceeds %d", maxObjectMembers)
	}
	return object, nil
}

// DecodeObject strictly decodes one bounded object while retaining raw field values.
func DecodeObject(raw json.RawMessage) (map[string]json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if err := requireDelimiter(decoder, '{'); err != nil {
		return nil, fmt.Errorf("decode resource object: %w", err)
	}
	object := make(map[string]json.RawMessage)
	for decoder.More() {
		if len(object) >= maxObjectMembers {
			return nil, fmt.Errorf("object member count exceeds %d", maxObjectMembers)
		}
		if err := decodeObjectMember(decoder, object); err != nil {
			return nil, err
		}
	}
	if err := finishCollection(decoder, '}'); err != nil {
		return nil, err
	}
	return object, nil
}

func decodeObjectMember(decoder *json.Decoder, object map[string]json.RawMessage) error {
	token, err := decoder.Token()
	if err != nil {
		return fmt.Errorf("decode object member name: %w", err)
	}
	name, ok := token.(string)
	if !ok {
		return errors.New("object member name must be a string")
	}
	if _, exists := object[name]; exists {
		return fmt.Errorf("duplicate object member %q", name)
	}
	var value json.RawMessage
	if err := decoder.Decode(&value); err != nil {
		return fmt.Errorf("decode object member %q: %w", name, err)
	}
	object[name] = value
	return nil
}

// DecodeArray decodes at most max raw values without materializing an oversized slice.
func DecodeArray(raw json.RawMessage, max int, field string) ([]json.RawMessage, error) {
	if max < 0 || field == "" {
		return nil, errors.New("invalid array decode parameters")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	token, err := decoder.Token()
	if err != nil {
		return nil, fmt.Errorf("decode %s: %w", field, err)
	}
	if token == nil {
		return nil, requireJSONEnd(decoder)
	}
	if delimiter, ok := token.(json.Delim); !ok || delimiter != '[' {
		return nil, fmt.Errorf("%s must be an array", field)
	}
	values := make([]json.RawMessage, 0, min(max, 16))
	for decoder.More() {
		if len(values) >= max {
			return nil, fmt.Errorf("%s count exceeds %d", field, max)
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return nil, fmt.Errorf("decode %s value %d: %w", field, len(values), err)
		}
		values = append(values, value)
	}
	if err := finishCollection(decoder, ']'); err != nil {
		return nil, err
	}
	return values, nil
}

// DecodeInt32 decodes a JSON integer whose value fits in an int32.
func DecodeInt32(raw json.RawMessage, field string) (int32, error) {
	if field == "" {
		return 0, errors.New("integer field name is empty")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return 0, fmt.Errorf("decode %s: %w", field, err)
	}
	number, ok := value.(json.Number)
	if !ok {
		return 0, fmt.Errorf("%s must be an int32", field)
	}
	parsed, err := strconv.ParseInt(number.String(), 10, 32)
	if err != nil {
		return 0, fmt.Errorf("%s must be an int32", field)
	}
	if err := requireJSONEnd(decoder); err != nil {
		return 0, err
	}
	return int32(parsed), nil
}

// DecodeOptionalInt32 decodes an absent, null, or int32 object member.
func DecodeOptionalInt32(
	object map[string]json.RawMessage, field string,
) (int32, bool, error) {
	if field == "" {
		return 0, false, errors.New("integer field name is empty")
	}
	raw, exists := object[field]
	if !exists || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return 0, false, nil
	}
	value, err := DecodeInt32(raw, field)
	if err != nil {
		return 0, false, err
	}
	return value, true, nil
}

// DecodeOptionalBool decodes an absent, null, or Boolean object member.
func DecodeOptionalBool(
	object map[string]json.RawMessage, field string,
) (bool, bool, error) {
	if field == "" {
		return false, false, errors.New("boolean field name is empty")
	}
	raw, exists := object[field]
	if !exists || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return false, false, nil
	}
	var value bool
	if err := json.Unmarshal(raw, &value); err != nil {
		return false, false, fmt.Errorf("decode %s: %w", field, err)
	}
	return value, true, nil
}

// MarshalObject encodes a bounded raw resource object without rewriting retained values.
func MarshalObject(object map[string]json.RawMessage) (json.RawMessage, error) {
	if object == nil {
		return nil, errors.New("resource object is nil")
	}
	if len(object) > maxObjectMembers {
		return nil, fmt.Errorf("object member count exceeds %d", maxObjectMembers)
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

// MarshalArray encodes a bounded collection of raw JSON values without rewriting them.
func MarshalArray(values []json.RawMessage, max int, field string) (json.RawMessage, error) {
	if max < 0 || field == "" {
		return nil, errors.New("invalid array encode parameters")
	}
	if values == nil {
		return nil, fmt.Errorf("%s array is nil", field)
	}
	if len(values) > max {
		return nil, fmt.Errorf("%s count exceeds %d", field, max)
	}
	encoded := []byte{'['}
	for index, value := range values {
		if !json.Valid(value) {
			return nil, fmt.Errorf("%s value %d is invalid JSON", field, index)
		}
		if index > 0 {
			encoded = append(encoded, ',')
		}
		encoded = append(encoded, value...)
	}
	return append(encoded, ']'), nil
}

func mergeTags(raw json.RawMessage, requested []int32) (json.RawMessage, error) {
	if len(requested) > maxTags {
		return nil, fmt.Errorf("tags count exceeds %d", maxTags)
	}
	existing, err := decodeIntegerArray(raw)
	if err != nil {
		return nil, fmt.Errorf("decode tags: %w", err)
	}
	capacity := min(maxTags, len(existing)+len(requested))
	seen := make(map[int32]struct{}, capacity)
	merged := make([]int32, 0, capacity)
	for _, tag := range existing {
		merged, err = appendUniqueTag(merged, seen, tag)
		if err != nil {
			return nil, err
		}
	}
	for _, tag := range requested {
		merged, err = appendUniqueTag(merged, seen, tag)
		if err != nil {
			return nil, err
		}
	}
	encoded, err := json.Marshal(merged)
	if err != nil {
		return nil, fmt.Errorf("encode tags: %w", err)
	}
	return json.RawMessage(encoded), nil
}

func decodeIntegerArray(raw json.RawMessage) ([]int32, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return []int32{}, nil
	}
	values, err := DecodeArray(raw, maxTags, "tags")
	if err != nil {
		return nil, err
	}
	if values == nil {
		return []int32{}, nil
	}
	result := make([]int32, 0, len(values))
	for _, value := range values {
		integer, decodeErr := DecodeInt32(value, "tag")
		if decodeErr != nil {
			return nil, decodeErr
		}
		result = append(result, integer)
	}
	return result, nil
}

func appendUniqueTag(values []int32, seen map[int32]struct{}, tag int32) ([]int32, error) {
	if _, exists := seen[tag]; exists {
		return values, nil
	}
	if len(values) >= maxTags {
		return nil, fmt.Errorf("tags count exceeds %d", maxTags)
	}
	seen[tag] = struct{}{}
	return append(values, tag), nil
}

func requireDelimiter(decoder *json.Decoder, want json.Delim) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, ok := token.(json.Delim)
	if !ok || delimiter != want {
		return errors.New("resource must be a JSON object")
	}
	return nil
}

func finishCollection(decoder *json.Decoder, want json.Delim) error {
	token, err := decoder.Token()
	if err != nil {
		return fmt.Errorf("decode collection end: %w", err)
	}
	delimiter, ok := token.(json.Delim)
	if !ok || delimiter != want {
		return errors.New("invalid collection end")
	}
	return requireJSONEnd(decoder)
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
