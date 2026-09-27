package jellyfin

import (
	"encoding/json"
	"errors"
	"net/http"
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

func TestPlaybackReportingRejectsMalformedRows(t *testing.T) {
	client := newTransportClient(t, roundTripFunc(func(*http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, `{"colums":["rowid","DateCreated","UserId","ItemId","ItemType","ItemName","PlaybackMethod","ClientName","DeviceName","PlayDuration"],"results":[[1,"bad","user","item","Movie","Title","DirectPlay","Web","TV",1]],"message":""}`), nil
	}))
	_, err := client.PlaybackReporting(t.Context(), 0, core.ImportBatchSize)
	assertMediaError(t, err, core.MediaServerMalformed)
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
