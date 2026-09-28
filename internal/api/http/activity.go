package http

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/httputil"
)

const defaultActivityLimit = 50

type activityResponse struct {
	Items      []playbackWatchResponse `json:"items"`
	Limit      int                     `json:"limit"`
	NextCursor string                  `json:"next_cursor"`
}

type timelineEntryResponse struct {
	MediaServerID  string     `json:"media_server_id"`
	MediaUserID    string     `json:"media_user_id"`
	Username       string     `json:"username"`
	ItemID         string     `json:"item_id"`
	ItemName       string     `json:"item_name"`
	ItemType       string     `json:"item_type"`
	SeriesID       string     `json:"series_id"`
	SeriesName     string     `json:"series_name"`
	LibraryID      string     `json:"library_id"`
	LibraryName    string     `json:"library_name"`
	FirstStartedAt time.Time  `json:"first_started_at"`
	LastEndedAt    *time.Time `json:"last_ended_at,omitempty"`
	PlayCount      int        `json:"play_count"`
	ActiveSeconds  int64      `json:"active_seconds"`
}

type timelineResponse struct {
	Items      []timelineEntryResponse `json:"items"`
	Limit      int                     `json:"limit"`
	GapSeconds int64                   `json:"gap_seconds"`
	NextCursor string                  `json:"next_cursor"`
}

func (s *Server) handleActivity(w http.ResponseWriter, r *http.Request) {
	query, limit, fields := parseActivityQuery(r)
	if len(fields) > 0 {
		s.writeValidation(w, r, fields)
		return
	}
	watches, err := s.activityReader.ListActivity(r.Context(), query)
	if err != nil {
		writeError(w, r, s.logger, err)
		return
	}
	watches, next := activityPage(watches, limit)
	items := make([]playbackWatchResponse, 0, len(watches))
	for _, watch := range watches {
		items = append(items, playbackWatchDTO(watch, watch.LastSeenAt))
	}
	writeJSON(w, r, s.logger, http.StatusOK, activityResponse{Items: items, Limit: limit, NextCursor: next})
}

func (s *Server) handleTimeline(w http.ResponseWriter, r *http.Request) {
	serverID, ok := s.mediaServerID(w, r)
	if !ok {
		return
	}
	userID := r.PathValue("media_user_id")
	limit, gap, cursor, fields := parseTimelineQuery(r, userID)
	if len(fields) > 0 {
		s.writeValidation(w, r, fields)
		return
	}
	watches, err := s.activityReader.ListTimelineWatches(r.Context(), core.TimelineWatchQuery{
		MediaServerID: serverID, MediaUserID: userID, Before: cursor, Limit: core.MaxTimelineWatchFetch,
	})
	if err != nil {
		writeError(w, r, s.logger, err)
		return
	}
	page := core.GroupTimeline(watches, limit, gap)
	items := make([]timelineEntryResponse, 0, len(page.Items))
	for _, entry := range page.Items {
		items = append(items, timelineEntryDTO(entry))
	}
	writeJSON(w, r, s.logger, http.StatusOK, timelineResponse{
		Items: items, Limit: limit, GapSeconds: int64(gap / time.Second), NextCursor: encodeActivityCursor(page.Next),
	})
}

func parseActivityQuery(r *http.Request) (core.ActivityQuery, int, []httputil.FieldError) {
	values, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return core.ActivityQuery{}, defaultActivityLimit, []httputil.FieldError{activityField("query", "must use valid percent encoding")}
	}
	limit, fields := activityBoundedInt(values, "limit", defaultActivityLimit, 1, core.MaxActivityPageSize)
	query := core.ActivityQuery{Limit: limit + 1}
	query.MediaServerID, fields = activityServerID(values, "media_server_id", fields)
	query.MediaUserID, fields = activityText(values, "media_user_id", fields)
	query.LibraryID, fields = activityText(values, "library_id", fields)
	query.ItemType, fields = activityText(values, "item_type", fields)
	query.Client, fields = activityText(values, "client", fields)
	query.DeviceID, fields = activityText(values, "device_id", fields)
	query.Search, fields = activityText(values, "q", fields)
	query.PlayMethod, fields = activityPlayMethod(values["play_method"], fields)
	query.Source, fields = activitySource(values["source"], fields)
	query.ImportSource, fields = activityImportSource(values["import_source"], fields)
	query.StartedAfter, fields = activityTimeFilter(values["started_after"], "started_after", fields)
	query.StartedBefore, fields = activityTimeFilter(values["started_before"], "started_before", fields)
	query.Before, fields = activityCursor(values["cursor"], fields)
	if query.StartedAfter != nil && query.StartedBefore != nil && !query.StartedAfter.Before(*query.StartedBefore) {
		fields = append(fields, activityField("started_before", "must be later than started_after"))
	}
	return query, limit, fields
}

func parseTimelineQuery(
	r *http.Request, userID string,
) (int, time.Duration, *core.ActivityCursor, []httputil.FieldError) {
	values, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return defaultActivityLimit, core.DefaultTimelineGap, nil, []httputil.FieldError{activityField("query", "must use valid percent encoding")}
	}
	limit, fields := activityBoundedInt(values, "limit", defaultActivityLimit, 1, core.MaxActivityPageSize)
	gapSeconds, fields := activityBoundedInt(values, "gap_seconds", int(core.DefaultTimelineGap/time.Second), 1, int(core.MaxTimelineGap/time.Second), fields...)
	if !validActivityTextValue(userID, core.MaxExclusionIDBytes) {
		fields = append(fields, activityField("media_user_id", "must be 1 through 128 bytes of valid text"))
	}
	cursor, fields := activityCursor(values["cursor"], fields)
	return limit, time.Duration(gapSeconds) * time.Second, cursor, fields
}

func activityBoundedInt(
	values url.Values, name string, fallback, minimum, maximum int, supplied ...httputil.FieldError,
) (int, []httputil.FieldError) {
	fields := supplied
	raw, present := values[name]
	if !present {
		return fallback, fields
	}
	parsed, err := strconv.Atoi(firstValue(raw))
	if len(raw) != 1 || err != nil || parsed < minimum || parsed > maximum {
		return fallback, append(fields, activityField(name, "is outside the allowed range"))
	}
	return parsed, fields
}

func activityServerID(values url.Values, name string, fields []httputil.FieldError) (string, []httputil.FieldError) {
	raw := values[name]
	if len(raw) == 0 {
		return "", fields
	}
	if len(raw) != 1 || !core.ValidID(raw[0]) {
		return "", append(fields, activityField(name, "must be one valid UUID"))
	}
	return raw[0], fields
}

func activityText(values url.Values, name string, fields []httputil.FieldError) (string, []httputil.FieldError) {
	raw := values[name]
	if len(raw) == 0 {
		return "", fields
	}
	if len(raw) != 1 || !validActivityTextValue(raw[0], core.MaxActivitySearchBytes) {
		return "", append(fields, activityField(name, "must be one value of at most 128 bytes"))
	}
	return raw[0], fields
}

func validActivityTextValue(value string, maximum int) bool {
	return value != "" && len(value) <= maximum && utf8.ValidString(value) && strings.IndexFunc(value, unicode.IsControl) < 0
}

func activityPlayMethod(values []string, fields []httputil.FieldError) (core.PlayMethod, []httputil.FieldError) {
	if len(values) == 0 {
		return "", fields
	}
	value := core.PlayMethod(firstValue(values))
	if len(values) != 1 || !value.Valid() {
		return "", append(fields, activityField("play_method", "is not supported"))
	}
	return value, fields
}

func activitySource(values []string, fields []httputil.FieldError) (core.WatchSource, []httputil.FieldError) {
	if len(values) == 0 {
		return "", fields
	}
	value := core.WatchSource(firstValue(values))
	if len(values) != 1 || !value.Valid() {
		return "", append(fields, activityField("source", "is not supported"))
	}
	return value, fields
}

func activityImportSource(values []string, fields []httputil.FieldError) (core.ImportSource, []httputil.FieldError) {
	if len(values) == 0 {
		return "", fields
	}
	value := core.ImportSource(firstValue(values))
	if len(values) != 1 || !value.Valid() {
		return "", append(fields, activityField("import_source", "is not supported"))
	}
	return value, fields
}

func activityTimeFilter(values []string, name string, fields []httputil.FieldError) (*time.Time, []httputil.FieldError) {
	if len(values) == 0 {
		return nil, fields
	}
	value, err := time.Parse(time.RFC3339Nano, firstValue(values))
	if len(values) != 1 || err != nil {
		return nil, append(fields, activityField(name, "must be one RFC 3339 timestamp"))
	}
	value = core.NormalizeTime(value)
	return &value, fields
}

func activityCursor(values []string, fields []httputil.FieldError) (*core.ActivityCursor, []httputil.FieldError) {
	started, id, fields := playbackCursorValues(values, fields)
	if started.IsZero() {
		return nil, fields
	}
	return &core.ActivityCursor{StartedAt: started, ID: id}, fields
}

func activityPage(watches []core.PlaybackWatch, limit int) ([]core.PlaybackWatch, string) {
	if len(watches) <= limit {
		return watches, ""
	}
	page := watches[:limit]
	last := page[len(page)-1]
	return page, encodeActivityCursor(&core.ActivityCursor{StartedAt: last.StartedAt, ID: last.ID})
}

func encodeActivityCursor(cursor *core.ActivityCursor) string {
	if cursor == nil {
		return ""
	}
	data, err := json.Marshal(playbackCursor{StartedAt: cursor.StartedAt, ID: cursor.ID})
	if err != nil {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(data)
}

func timelineEntryDTO(value core.TimelineEntry) timelineEntryResponse {
	return timelineEntryResponse{
		MediaServerID: value.MediaServerID, MediaUserID: value.MediaUserID, Username: value.Username,
		ItemID: value.ItemID, ItemName: value.ItemName, ItemType: value.ItemType,
		SeriesID: value.SeriesID, SeriesName: value.SeriesName, LibraryID: value.LibraryID,
		LibraryName: value.LibraryName, FirstStartedAt: value.FirstStartedAt, LastEndedAt: value.LastEndedAt,
		PlayCount: value.PlayCount, ActiveSeconds: int64(value.ActiveTime / time.Second),
	}
}

func activityField(name, message string) httputil.FieldError {
	return httputil.FieldError{Field: name, Code: "invalid", Message: message}
}
