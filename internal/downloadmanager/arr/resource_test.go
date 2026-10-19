package arr

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
)

func TestDecodeObjectRejectsDuplicateMemberNames(t *testing.T) {
	t.Parallel()
	_, err := DecodeObject(json.RawMessage(`{"x":1,"x":2}`))
	if err == nil {
		t.Fatal("DecodeObject returned nil error")
	}
}

func TestMergeResourceBoundsObjectMembers(t *testing.T) {
	t.Parallel()
	if _, err := MergeResource(resourceWithMembers(maxObjectMembers), nil); err != nil {
		t.Fatalf("MergeResource at limit: %v", err)
	}
	if _, err := MergeResource(resourceWithMembers(maxObjectMembers+1), nil); err == nil {
		t.Fatal("MergeResource over limit returned nil error")
	}
}

func TestMergeResourceBoundsTags(t *testing.T) {
	t.Parallel()
	if _, err := MergeResource(resourceWithTags(maxTags), nil); err != nil {
		t.Fatalf("MergeResource at limit: %v", err)
	}
	if _, err := MergeResource(resourceWithTags(maxTags+1), nil); err == nil {
		t.Fatal("MergeResource over limit returned nil error")
	}
}

func resourceWithMembers(count int) json.RawMessage {
	var value strings.Builder
	value.WriteString(`{"tags":[],"monitored":false`)
	for index := range count - 2 {
		value.WriteByte(',')
		value.WriteString(strconv.Quote("field" + strconv.Itoa(index)))
		value.WriteString(`:0`)
	}
	value.WriteByte('}')
	return json.RawMessage(value.String())
}

func resourceWithTags(count int) json.RawMessage {
	var value strings.Builder
	value.WriteString(`{"tags":[`)
	for index := range count {
		if index > 0 {
			value.WriteByte(',')
		}
		value.WriteString(strconv.Itoa(index))
	}
	value.WriteString(`]}`)
	return json.RawMessage(value.String())
}
