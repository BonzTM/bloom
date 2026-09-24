package http

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/httputil"
)

const (
	defaultPlaybackHistoryPageSize = 50
	maxPlaybackHistoryPageSize     = 100
	maxPlaybackCursorBytes         = 256
)

type playbackWatchResponse struct {
	ID              string           `json:"id"`
	MediaServerID   string           `json:"media_server_id"`
	MediaServerName string           `json:"media_server_name"`
	MediaUserID     string           `json:"media_user_id"`
	Username        string           `json:"username"`
	DeviceID        string           `json:"device_id"`
	DeviceName      string           `json:"device_name"`
	Client          string           `json:"client"`
	ItemID          string           `json:"item_id"`
	ItemName        string           `json:"item_name"`
	ItemType        string           `json:"item_type"`
	SeriesName      string           `json:"series_name"`
	LibraryID       string           `json:"library_id"`
	LibraryName     string           `json:"library_name"`
	SeasonNumber    *int32           `json:"season_number"`
	EpisodeNumber   *int32           `json:"episode_number"`
	PositionMS      int64            `json:"position_ms"`
	Paused          bool             `json:"paused"`
	PlayMethod      core.PlayMethod  `json:"play_method"`
	Source          core.WatchSource `json:"source"`
	ActiveSeconds   int64            `json:"active_seconds"`
	StartedAt       time.Time        `json:"started_at"`
	EndedAt         *time.Time       `json:"ended_at,omitempty"`
}

type playbackNowResponse struct {
	Items      []playbackWatchResponse `json:"items"`
	NextCursor string                  `json:"next_cursor"`
}

type playbackHistoryResponse struct {
	Items      []playbackWatchResponse `json:"items"`
	NextCursor string                  `json:"next_cursor"`
}

type playbackCursor struct {
	StartedAt time.Time `json:"started_at"`
	ID        string    `json:"id"`
}

func (s *Server) handlePlaybackNow(w http.ResponseWriter, r *http.Request) {
	query, fields := playbackListQuery(r.URL.RawQuery, core.PlaybackQueryNow, false)
	if len(fields) > 0 {
		s.writeValidation(w, r, fields)
		return
	}
	pageSize := query.PageSize
	query.PageSize++
	watches, err := s.playbackReader.ListWatches(r.Context(), query)
	if err != nil {
		writeError(w, r, s.logger, err)
		return
	}
	watches, nextCursor := playbackPage(watches, pageSize)
	items := make([]playbackWatchResponse, 0, len(watches))
	now := core.NormalizeTime(s.clock.Now())
	for _, watch := range watches {
		items = append(items, playbackWatchDTO(watch, now))
	}
	writeJSON(w, r, s.logger, http.StatusOK, playbackNowResponse{Items: items, NextCursor: nextCursor})
}

func (s *Server) handlePlaybackHistory(w http.ResponseWriter, r *http.Request) {
	query, fields := playbackListQuery(r.URL.RawQuery, core.PlaybackQueryHistory, true)
	if len(fields) > 0 {
		s.writeValidation(w, r, fields)
		return
	}
	pageSize := query.PageSize
	query.PageSize++
	watches, err := s.playbackReader.ListWatches(r.Context(), query)
	if err != nil {
		writeError(w, r, s.logger, err)
		return
	}
	watches, nextCursor := playbackPage(watches, pageSize)
	items := make([]playbackWatchResponse, 0, len(watches))
	for _, watch := range watches {
		items = append(items, playbackWatchDTO(watch, watch.LastSeenAt))
	}
	writeJSON(w, r, s.logger, http.StatusOK, playbackHistoryResponse{
		Items: items, NextCursor: nextCursor,
	})
}

func playbackListQuery(
	rawQuery string,
	mode core.PlaybackQueryMode,
	allowServerFilter bool,
) (core.PlaybackQuery, []httputil.FieldError) {
	query := core.PlaybackQuery{Mode: mode, PageSize: defaultPlaybackHistoryPageSize}
	values, err := url.ParseQuery(rawQuery)
	if err != nil {
		return query, []httputil.FieldError{{Field: "query", Code: "invalid", Message: "must use valid percent encoding"}}
	}
	fields := make([]httputil.FieldError, 0, 3)
	query.PageSize, fields = playbackPageSize(values["page_size"], fields)
	if allowServerFilter {
		query.MediaServerID, fields = playbackServerFilter(values["media_server_id"], fields)
	}
	query.BeforeStartedAt, query.BeforeID, fields = playbackCursorValues(values["cursor"], fields)
	return query, fields
}

func playbackPageSize(values []string, fields []httputil.FieldError) (int, []httputil.FieldError) {
	if len(values) == 0 {
		return defaultPlaybackHistoryPageSize, fields
	}
	value, err := strconv.Atoi(values[0])
	if len(values) != 1 || err != nil || value < 1 || value > maxPlaybackHistoryPageSize {
		return defaultPlaybackHistoryPageSize, append(fields, httputil.FieldError{
			Field: "page_size", Code: "invalid", Message: "must be one integer from 1 through 100",
		})
	}
	return value, fields
}

func playbackServerFilter(values []string, fields []httputil.FieldError) (string, []httputil.FieldError) {
	if len(values) == 0 {
		return "", fields
	}
	if len(values) != 1 || !core.ValidID(values[0]) {
		return "", append(fields, httputil.FieldError{
			Field: "media_server_id", Code: "invalid", Message: "must be one valid UUID",
		})
	}
	return values[0], fields
}

func playbackCursorValues(
	values []string,
	fields []httputil.FieldError,
) (time.Time, string, []httputil.FieldError) {
	if len(values) == 0 {
		return time.Time{}, "", fields
	}
	invalid := len(values) != 1 || values[0] == "" || len(values[0]) > maxPlaybackCursorBytes
	var cursor playbackCursor
	if !invalid {
		data, err := base64.RawURLEncoding.DecodeString(values[0])
		invalid = err != nil || json.Unmarshal(data, &cursor) != nil ||
			cursor.StartedAt.IsZero() || !core.ValidID(cursor.ID)
	}
	if invalid {
		fields = append(fields, httputil.FieldError{
			Field: "cursor", Code: "invalid", Message: "must be one valid cursor",
		})
		return time.Time{}, "", fields
	}
	return core.NormalizeTime(cursor.StartedAt), cursor.ID, fields
}

func playbackPage(
	watches []core.PlaybackWatch,
	pageSize int,
) ([]core.PlaybackWatch, string) {
	if len(watches) <= pageSize {
		return watches, ""
	}
	page := watches[:pageSize]
	last := page[len(page)-1]
	data, err := json.Marshal(playbackCursor{StartedAt: last.StartedAt, ID: last.ID})
	if err != nil {
		return page, ""
	}
	return page, base64.RawURLEncoding.EncodeToString(data)
}

func playbackWatchDTO(watch core.PlaybackWatch, now time.Time) playbackWatchResponse {
	return playbackWatchResponse{
		ID: watch.ID, MediaServerID: watch.MediaServerID, MediaServerName: watch.MediaServerName,
		MediaUserID: watch.MediaUserID, Username: watch.Username,
		DeviceID: watch.DeviceID, DeviceName: watch.DeviceName, Client: watch.Client,
		ItemID: watch.ItemID, ItemName: watch.ItemName, ItemType: watch.ItemType,
		SeriesName: watch.SeriesName, LibraryID: watch.LibraryID, LibraryName: watch.LibraryName,
		SeasonNumber: watch.SeasonNumber, EpisodeNumber: watch.EpisodeNumber,
		PositionMS: int64(watch.LastPosition / time.Millisecond), Paused: watch.State == core.WatchPaused,
		PlayMethod: watch.PlayMethod, Source: watch.Source,
		ActiveSeconds: int64(watch.ActiveTimeAt(now) / time.Second),
		StartedAt:     watch.StartedAt, EndedAt: watch.EndedAt,
	}
}
