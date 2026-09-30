package tmdb

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/BonzTM/bloom/internal/core"
)

func TestDecodeSearchResultsRejectsMoreThanTheContractAllows(t *testing.T) {
	t.Parallel()
	items := make([]string, 0, maxSearchResults+1)
	for index := range maxSearchResults + 1 {
		items = append(items, fmt.Sprintf(`{"id":%d,"media_type":"movie","title":"T","release_date":"2020-01-01"}`, index+1))
	}
	body := []byte(`{"page":1,"results":[` + strings.Join(items, ",") + `],"total_pages":1,"total_results":101}`)
	if _, _, err := decodeSearchResults(body, nil); !errors.Is(err, core.ErrMetadataMalformed) {
		t.Fatalf("decodeSearchResults(101 results) = %v, want ErrMetadataMalformed", err)
	}
	body = []byte(`{"page":1,"results":[` + strings.Join(items[:maxSearchResults], ",") + `],"total_pages":1,"total_results":100}`)
	titles, skipped, err := decodeSearchResults(body, nil)
	if err != nil || len(titles) != maxSearchResults || skipped != 0 {
		t.Fatalf("decodeSearchResults(100 results) = %d titles, %d skipped, %v", len(titles), skipped, err)
	}
}

func TestDecodeSearchResultsCountsMissingMediaTypeAsSkipped(t *testing.T) {
	t.Parallel()
	body := []byte(`{"page":1,"results":[` +
		`{"id":1,"title":"No type","release_date":"2020-01-01"},` +
		`{"id":2,"media_type":"person","name":"Someone"},` +
		`{"id":3,"media_type":"movie","title":"Heat","release_date":"1995-12-15"}` +
		`],"total_pages":1,"total_results":3}`)
	titles, skipped, err := decodeSearchResults(body, nil)
	if err != nil {
		t.Fatalf("decodeSearchResults: %v", err)
	}
	if len(titles) != 1 || titles[0].Title != "Heat" {
		t.Fatalf("titles = %+v, want only Heat", titles)
	}
	if skipped != 1 {
		t.Fatalf("skipped = %d, want 1 (the item without a media type; a person is not malformed)", skipped)
	}
}
