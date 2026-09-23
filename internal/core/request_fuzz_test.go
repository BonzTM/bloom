package core

import (
	"errors"
	"testing"
)

func FuzzValidateMetadataSearch(f *testing.F) {
	f.Add("matrix", "movie")
	f.Add(" show ", "series")
	f.Fuzz(func(t *testing.T, query, rawKind string) {
		kind := MediaKind(rawKind)
		err := ValidateMetadataSearch(MetadataSearch{Query: query, Kind: &kind})
		if err != nil && !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("unexpected validation error: %v", err)
		}
	})
}

func FuzzValidateSeasonNumbers(f *testing.F) {
	f.Add(1, 2, 3)
	f.Add(0, 1, 1)
	f.Fuzz(func(t *testing.T, first, second, third int) {
		err := ValidateSeasonNumbers([]int{first, second, third})
		if err != nil && !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("unexpected validation error: %v", err)
		}
	})
}
