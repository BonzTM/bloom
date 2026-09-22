package core

import (
	"testing"
	"time"
)

func TestNormalizeTime(t *testing.T) {
	loc := time.FixedZone("plus2", 2*60*60)
	in := time.Date(2026, 9, 22, 10, 30, 0, 123456789, loc)

	got := NormalizeTime(in)
	if got.Location() != time.UTC {
		t.Errorf("Location = %v, want UTC", got.Location())
	}
	want := time.Date(2026, 9, 22, 8, 30, 0, 123456000, time.UTC)
	if !got.Equal(want) {
		t.Errorf("NormalizeTime = %v, want %v", got, want)
	}
	if got.Nanosecond()%int(TimestampPrecision) != 0 {
		t.Errorf("nanoseconds %d not truncated to %s", got.Nanosecond(), TimestampPrecision)
	}
}
