package jellyfin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/BonzTM/bloom/internal/core"
)

const playbackReportingPath = "/user_usage_stats/submit_custom_query"

type playbackReportingRequest struct {
	CustomQueryString string `json:"CustomQueryString"`
	ReplaceUserID     bool   `json:"ReplaceUserId"`
}

type playbackReportingResponse struct {
	Columns []string `json:"colums"`
	Results [][]any  `json:"results"`
	Message string   `json:"message"`
}

// PlaybackReporting reads one rowid-keyset page from the Playback Reporting plugin.
func (c *Client) PlaybackReporting(ctx context.Context, cursor int64, limit int) (core.PlaybackReportingPage, error) {
	if cursor < 0 || limit != core.ImportBatchSize {
		return core.PlaybackReportingPage{}, core.ErrInvalidArgument
	}
	query := playbackReportingQuery(cursor)
	body, err := json.Marshal(playbackReportingRequest{CustomQueryString: query, ReplaceUserID: false})
	if err != nil {
		return core.PlaybackReportingPage{}, mediaError("playback_reporting", core.MediaServerMalformed, err)
	}
	response, started, err := c.doWithRetry(ctx, "playback_reporting", http.MethodPost, playbackReportingPath, body)
	if isMediaNotFound(err) {
		return core.PlaybackReportingPage{}, core.ErrImportPluginMissing
	}
	if err != nil {
		return core.PlaybackReportingPage{}, err
	}
	rows, err := decodePlaybackReporting(response)
	if err != nil {
		c.observe("playback_reporting", "malformed", started)
		return core.PlaybackReportingPage{}, mediaError("playback_reporting", core.MediaServerMalformed, err)
	}
	c.observe("playback_reporting", "success", started)
	return rows, nil
}

// Contract verified against jellyfin-plugin-playbackreporting's
// Api/PlaybackReportingActivityController.cs: POST submit_custom_query accepts
// CustomQueryString and ReplaceUserId and returns the intentionally misspelled
// "colums" array alongside "results" rows.
func playbackReportingQuery(cursor int64) string {
	return "SELECT rowid, DateCreated, UserId, ItemId, ItemType, ItemName, PlaybackMethod, " +
		"ClientName, DeviceName, PlayDuration FROM PlaybackActivity WHERE rowid > " +
		strconv.FormatInt(cursor, 10) + " ORDER BY rowid LIMIT 500"
}

func decodePlaybackReporting(body []byte) (core.PlaybackReportingPage, error) {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	var response playbackReportingResponse
	if err := decoder.Decode(&response); err != nil {
		return core.PlaybackReportingPage{}, fmt.Errorf("decode plugin response: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return core.PlaybackReportingPage{}, errors.New("plugin response contains trailing data")
	}
	if len(response.Results) > core.ImportBatchSize || len(response.Columns) != 10 || response.Message != "" {
		return core.PlaybackReportingPage{}, errors.New("plugin response has invalid dimensions or message")
	}
	want := []string{"rowid", "DateCreated", "UserId", "ItemId", "ItemType", "ItemName", "PlaybackMethod", "ClientName", "DeviceName", "PlayDuration"}
	for index, column := range response.Columns {
		if column != want[index] {
			return core.PlaybackReportingPage{}, errors.New("plugin response columns do not match query")
		}
	}
	page := core.PlaybackReportingPage{Records: make([]core.ImportedWatch, 0, len(response.Results))}
	for _, row := range response.Results {
		rowID, err := playbackReportingRowID(row, page.Cursor)
		if err != nil {
			return core.PlaybackReportingPage{}, err
		}
		page.Cursor = rowID
		record, err := playbackReportingRow(row, rowID)
		if errors.Is(err, errSkippedPlaybackRow) {
			page.Skipped++
			continue
		}
		if err != nil {
			return core.PlaybackReportingPage{}, err
		}
		page.Records = append(page.Records, record)
	}
	return page, nil
}

var errSkippedPlaybackRow = errors.New("skip malformed playback reporting row")

func playbackReportingRowID(row []any, previous int64) (int64, error) {
	if len(row) != 10 {
		return 0, errors.New("plugin row has invalid width")
	}
	rowID, err := reportingInt(row[0])
	if err != nil || rowID < 1 || rowID <= previous {
		return 0, errors.New("plugin rowid is invalid or out of order")
	}
	return rowID, nil
}

func playbackReportingRow(row []any, rowID int64) (core.ImportedWatch, error) {
	started, err := reportingTime(row[1])
	if err != nil {
		return core.ImportedWatch{}, err
	}
	duration, err := reportingInt(row[9])
	if err != nil || duration < 0 || duration > math.MaxInt32 {
		return core.ImportedWatch{}, errors.New("plugin duration is invalid")
	}
	values := make([]string, 0, 7)
	for _, index := range []int{2, 3, 4, 5, 6, 7, 8} {
		value, ok := row[index].(string)
		if !ok {
			return core.ImportedWatch{}, errSkippedPlaybackRow
		}
		values = append(values, value)
	}
	record := core.ImportedWatch{
		RecordID: strconv.FormatInt(rowID, 10), MediaUserID: values[0], ItemID: values[1],
		ItemType: values[2], ItemName: values[3], PlayMethod: reportingPlayMethod(values[4]),
		Client: values[5], DeviceName: values[6], StartedAt: started,
		Duration: time.Duration(duration) * time.Second,
	}
	if !record.Valid() {
		return core.ImportedWatch{}, errSkippedPlaybackRow
	}
	return record, nil
}

func reportingInt(value any) (int64, error) {
	switch number := value.(type) {
	case json.Number:
		return number.Int64()
	case string:
		return strconv.ParseInt(number, 10, 64)
	default:
		return 0, errors.New("value is not an integer")
	}
}

func reportingTime(value any) (time.Time, error) {
	text, ok := value.(string)
	if !ok {
		return time.Time{}, errors.New("plugin date has invalid type")
	}
	for _, layout := range []string{"2006-01-02 15:04:05.999999999", "2006-01-02 15:04:05", time.RFC3339Nano} {
		if parsed, err := time.ParseInLocation(layout, text, time.UTC); err == nil {
			return core.NormalizeTime(parsed), nil
		}
	}
	return time.Time{}, errors.New("plugin date has invalid format")
}

func reportingPlayMethod(value string) core.PlayMethod {
	switch value {
	case "DirectPlay":
		return core.PlayMethodDirectPlay
	case "DirectStream":
		return core.PlayMethodDirectStream
	case "Transcode":
		return core.PlayMethodTranscode
	default:
		return core.PlayMethodUnknown
	}
}
