package jellyfin

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/BonzTM/bloom/internal/core"
)

func TestPlaybackReportingUsesFixedQueryAndMapsRows(t *testing.T) {
	client := newTransportClient(t, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost || r.URL.Path != playbackReportingPath {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		var request playbackReportingRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if request.ReplaceUserID || request.CustomQueryString != playbackReportingQuery(41) {
			t.Fatalf("request = %+v", request)
		}
		return jsonResponse(http.StatusOK, `{"colums":["rowid","DateCreated","UserId","ItemId","ItemType","ItemName","PlaybackMethod","ClientName","DeviceName","PlayDuration"],"results":[[42,"2026-09-20 12:34:56.1234567","user-1","item-1","Movie","Title","DirectStream","Web","TV",90]],"message":""}`), nil
	}))
	page, err := client.PlaybackReporting(t.Context(), 41, core.ImportBatchSize)
	if err != nil || len(page.Records) != 1 || page.Cursor != 42 || page.Skipped != 0 {
		t.Fatalf("PlaybackReporting = %+v, %v", page, err)
	}
	record := page.Records[0]
	if record.RecordID != "42" || record.PlayMethod != core.PlayMethodDirectStream ||
		record.Duration != 90*time.Second || record.Username != "" {
		t.Fatalf("record = %+v", record)
	}
}

func TestPlaybackReportingClassifiesMissingPlugin(t *testing.T) {
	client := newTransportClient(t, roundTripFunc(func(*http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusNotFound, `{}`), nil
	}))
	_, err := client.PlaybackReporting(t.Context(), 0, core.ImportBatchSize)
	if !errors.Is(err, core.ErrImportPluginMissing) {
		t.Fatalf("error = %v", err)
	}
}

func TestPlaybackReportingSkipsBadDateAndAdvances(t *testing.T) {
	client := newTransportClient(t, roundTripFunc(func(*http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, `{"colums":["rowid","DateCreated","UserId","ItemId","ItemType","ItemName","PlaybackMethod","ClientName","DeviceName","PlayDuration"],"results":[[42,"bad","user","item","Movie","Title","DirectPlay","Web","TV",1],[43,"2026-09-20 12:35:56","user","item","Movie","Title","DirectPlay","Web","TV",1]],"message":""}`), nil
	}))
	assertPlaybackReportingProgress(t, client)
}

func TestPlaybackReportingSkipsBadDurationAndAdvances(t *testing.T) {
	client := newTransportClient(t, roundTripFunc(func(*http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, `{"colums":["rowid","DateCreated","UserId","ItemId","ItemType","ItemName","PlaybackMethod","ClientName","DeviceName","PlayDuration"],"results":[[42,"2026-09-20 12:34:56","user","item","Movie","Title","DirectPlay","Web","TV","bad"],[43,"2026-09-20 12:35:56","user","item","Movie","Title","DirectPlay","Web","TV",1]],"message":""}`), nil
	}))
	assertPlaybackReportingProgress(t, client)
}

func TestPlaybackReportingPlaybackDurationBound(t *testing.T) {
	tests := []struct {
		name     string
		duration int64
		wantErr  bool
	}{
		{"accepted boundary", core.MaxImportPlaybackSeconds, false},
		{"just over boundary", core.MaxImportPlaybackSeconds + 1, true},
		{"multi-year", 3 * 365 * 24 * 60 * 60, true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			row := []any{
				json.Number("42"), "2026-09-20 12:34:56", "user", "item", "Movie",
				"Title", "DirectPlay", "Web", "TV", json.Number(strconv.FormatInt(test.duration, 10)),
			}
			_, err := playbackReportingRow(row, 42)
			if (err != nil) != test.wantErr {
				t.Fatalf("playbackReportingRow duration %d = %v, want error %t", test.duration, err, test.wantErr)
			}
		})
	}
}

func TestPlaybackReportingSkipsMalformedTextAndAdvances(t *testing.T) {
	client := newTransportClient(t, roundTripFunc(func(*http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, `{"colums":["rowid","DateCreated","UserId","ItemId","ItemType","ItemName","PlaybackMethod","ClientName","DeviceName","PlayDuration"],"results":[[42,"2026-09-20 12:34:56",null,"item","Movie","Title","DirectPlay","Web","TV",1],[43,"2026-09-20 12:35:56","user","item","Movie","Title","DirectPlay","Web","TV",1]],"message":""}`), nil
	}))
	page, err := client.PlaybackReporting(t.Context(), 41, core.ImportBatchSize)
	if err != nil || page.Cursor != 43 || page.Skipped != 1 || len(page.Records) != 1 ||
		page.Records[0].RecordID != "43" {
		t.Fatalf("PlaybackReporting = %+v, %v", page, err)
	}
}

func TestPlaybackReportingRejectsMalformedOrNonAscendingRowID(t *testing.T) {
	for _, rows := range []string{
		`[["bad","2026-09-20 12:34:56","user","item","Movie","Title","DirectPlay","Web","TV",1]]`,
		`[[42,"2026-09-20 12:34:56","user","item","Movie","Title","DirectPlay","Web","TV",1],[42,"2026-09-20 12:35:56","user","item","Movie","Title","DirectPlay","Web","TV",1]]`,
	} {
		client := newTransportClient(t, roundTripFunc(func(*http.Request) (*http.Response, error) {
			body := `{"colums":["rowid","DateCreated","UserId","ItemId","ItemType","ItemName","PlaybackMethod","ClientName","DeviceName","PlayDuration"],"results":` + rows + `,"message":""}`
			return jsonResponse(http.StatusOK, body), nil
		}))
		_, err := client.PlaybackReporting(t.Context(), 41, core.ImportBatchSize)
		assertMediaError(t, err, core.MediaServerMalformed)
	}
}

func assertPlaybackReportingProgress(t *testing.T, client *Client) {
	t.Helper()
	page, err := client.PlaybackReporting(t.Context(), 41, core.ImportBatchSize)
	if err != nil || page.Cursor != 43 || page.Skipped != 1 || len(page.Records) != 1 ||
		page.Records[0].RecordID != "43" {
		t.Fatalf("PlaybackReporting = %+v, %v", page, err)
	}
}
