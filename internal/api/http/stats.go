package http

import (
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/httputil"
)

const (
	defaultStatsDays = 30
)

type statsWindowResponse struct {
	Days          int       `json:"days"`
	Start         time.Time `json:"start"`
	End           time.Time `json:"end"`
	MediaServerID string    `json:"media_server_id"`
	TimeZone      string    `json:"time_zone"`
}

type statsTotalsResponse struct {
	Plays        int64 `json:"plays"`
	WatchSeconds int64 `json:"watch_seconds"`
	UniqueUsers  int64 `json:"unique_users"`
	UniqueTitles int64 `json:"unique_titles"`
}

type statsTitleResponse struct {
	Kind          core.StatsTitleKind `json:"kind"`
	MediaServerID string              `json:"media_server_id"`
	Key           string              `json:"key"`
	Name          string              `json:"name"`
	Plays         int64               `json:"plays"`
	WatchSeconds  int64               `json:"watch_seconds"`
	LastWatchedAt time.Time           `json:"last_watched_at"`
}

type statsUserResponse struct {
	MediaServerID string    `json:"media_server_id"`
	MediaUserID   string    `json:"media_user_id"`
	Username      string    `json:"username"`
	Plays         int64     `json:"plays"`
	WatchSeconds  int64     `json:"watch_seconds"`
	LastWatchedAt time.Time `json:"last_watched_at"`
}

type statsLibraryResponse struct {
	MediaServerID string    `json:"media_server_id"`
	LibraryID     string    `json:"library_id"`
	LibraryName   string    `json:"library_name"`
	Plays         int64     `json:"plays"`
	WatchSeconds  int64     `json:"watch_seconds"`
	UniqueUsers   int64     `json:"unique_users"`
	UniqueTitles  int64     `json:"unique_titles"`
	LastWatchedAt time.Time `json:"last_watched_at"`
}

type statsBreakdownResponse struct {
	Name         string `json:"name"`
	Plays        int64  `json:"plays"`
	WatchSeconds int64  `json:"watch_seconds"`
}

type statsOverviewResponse struct {
	Window      statsWindowResponse      `json:"window"`
	Totals      statsTotalsResponse      `json:"totals"`
	Titles      []statsTitleResponse     `json:"titles"`
	Users       []statsUserResponse      `json:"users"`
	Clients     []statsBreakdownResponse `json:"clients"`
	Devices     []statsBreakdownResponse `json:"devices"`
	PlayMethods []statsBreakdownResponse `json:"play_methods"`
}

type statsDailyResponse struct {
	Window statsWindowResponse        `json:"window"`
	Items  []statsDailyBucketResponse `json:"items"`
}

type statsDailyBucketResponse struct {
	Date         string `json:"date"`
	Plays        int64  `json:"plays"`
	WatchSeconds int64  `json:"watch_seconds"`
}

type statsWeekdayBucketResponse struct {
	Weekday int   `json:"weekday"`
	Plays   int64 `json:"plays"`
}

type statsHourBucketResponse struct {
	Hour  int   `json:"hour"`
	Plays int64 `json:"plays"`
}

type statsPatternsResponse struct {
	Window   statsWindowResponse          `json:"window"`
	Weekdays []statsWeekdayBucketResponse `json:"weekdays"`
	Hours    []statsHourBucketResponse    `json:"hours"`
}

type statsTitlesResponse struct {
	Window statsWindowResponse  `json:"window"`
	Kind   core.StatsTitleKind  `json:"kind"`
	Items  []statsTitleResponse `json:"items"`
}

type statsUsersResponse struct {
	Window statsWindowResponse `json:"window"`
	Items  []statsUserResponse `json:"items"`
}

type statsLibrariesResponse struct {
	Window statsWindowResponse    `json:"window"`
	Items  []statsLibraryResponse `json:"items"`
}

type statsUserDetailResponse struct {
	Window      statsWindowResponse        `json:"window"`
	Totals      statsTotalsResponse        `json:"totals"`
	Titles      []statsTitleResponse       `json:"titles"`
	Clients     []statsBreakdownResponse   `json:"clients"`
	Devices     []statsBreakdownResponse   `json:"devices"`
	PlayMethods []statsBreakdownResponse   `json:"play_methods"`
	Daily       []statsDailyBucketResponse `json:"daily"`
	Watches     []playbackWatchResponse    `json:"watches"`
}

func (s *Server) handleStatsOverview(w http.ResponseWriter, r *http.Request) {
	s.handleStatsReport(w, r, core.StatsReportOverview)
}

func (s *Server) handleStatsDaily(w http.ResponseWriter, r *http.Request) {
	s.handleStatsReport(w, r, core.StatsReportDaily)
}

func (s *Server) handleStatsPatterns(w http.ResponseWriter, r *http.Request) {
	s.handleStatsReport(w, r, core.StatsReportPatterns)
}

func (s *Server) handleStatsUsers(w http.ResponseWriter, r *http.Request) {
	s.handleStatsReport(w, r, core.StatsReportUsers)
}

func (s *Server) handleStatsLibraries(w http.ResponseWriter, r *http.Request) {
	s.handleStatsReport(w, r, core.StatsReportLibraries)
}

func (s *Server) handleStatsTitles(w http.ResponseWriter, r *http.Request) {
	values, fields := statsQueryValues(r.URL.RawQuery)
	kind := core.StatsTitleKind(firstValue(values["kind"]))
	if len(values["kind"]) != 1 || !kind.Valid() {
		fields = append(fields, httputil.FieldError{
			Field: "kind", Code: "invalid", Message: "must be one of movie, series, or other",
		})
	}
	s.handleParsedStats(w, r, values, fields, core.StatsReportTitles, kind, "", "")
}

func (s *Server) handleStatsUser(w http.ResponseWriter, r *http.Request) {
	serverID, userID := r.PathValue("media_server_id"), r.PathValue("media_user_id")
	values, fields := statsQueryValues(r.URL.RawQuery)
	if !core.ValidID(serverID) {
		fields = append(fields, httputil.FieldError{Field: "media_server_id", Code: "invalid", Message: "must be a valid UUID"})
	}
	if !core.ValidStatsMediaUserID(userID) {
		fields = append(fields, httputil.FieldError{
			Field: "media_user_id", Code: "invalid",
			Message: "must contain 1 to 256 valid UTF-8 bytes without control characters",
		})
	}
	if filter := firstValue(values["media_server_id"]); filter != "" && filter != serverID {
		fields = append(fields, httputil.FieldError{Field: "media_server_id", Code: "invalid", Message: "must match the path media server"})
	} else if len(values["media_server_id"]) == 0 {
		values.Set("media_server_id", serverID)
	}
	s.handleParsedStats(w, r, values, fields, core.StatsReportUser, "", serverID, userID)
}

func (s *Server) handleStatsReport(w http.ResponseWriter, r *http.Request, report core.StatsReport) {
	values, fields := statsQueryValues(r.URL.RawQuery)
	s.handleParsedStats(w, r, values, fields, report, "", "", "")
}

func (s *Server) handleParsedStats(
	w http.ResponseWriter, r *http.Request, values url.Values, fields []httputil.FieldError,
	report core.StatsReport, kind core.StatsTitleKind, userServerID, userID string,
) {
	days, fields := statsDays(values["days"], fields)
	serverID, fields := statsServer(values["media_server_id"], fields)
	libraryID, fields := statsLibrary(values["library_id"], serverID, fields)
	zone, fields := statsZone(values["tz"], fields)
	window, err := core.NewStatsWindow(days, serverID, zone, s.clock.Now())
	if err != nil && len(fields) == 0 {
		fields = append(fields, httputil.FieldError{Field: "tz", Code: "invalid", Message: "must be one IANA time-zone name of at most 64 bytes"})
	}
	if len(fields) > 0 {
		s.writeValidation(w, r, fields)
		return
	}
	query := core.StatsQuery{
		Window: window, Report: report, TitleKind: kind,
		UserServerID: userServerID, MediaUserID: userID, LibraryID: libraryID,
	}
	result, err := s.statsReader.ReadStats(r.Context(), query)
	if err != nil {
		writeError(w, r, s.logger, err)
		return
	}
	s.writeStatsResult(w, r, report, kind, result)
}

func statsLibrary(
	values []string, mediaServerID string, fields []httputil.FieldError,
) (string, []httputil.FieldError) {
	if len(values) == 0 {
		return "", fields
	}
	if len(values) != 1 || !core.ValidStatsLibraryID(values[0]) {
		return "", append(fields, httputil.FieldError{
			Field: "library_id", Code: "invalid",
			Message: "must be one value of 1 to 128 valid UTF-8 bytes without control characters",
		})
	}
	if mediaServerID == "" {
		return "", append(fields, httputil.FieldError{
			Field: "media_server_id", Code: "required", Message: "is required with library_id",
		})
	}
	return values[0], fields
}

func statsQueryValues(raw string) (url.Values, []httputil.FieldError) {
	values, err := url.ParseQuery(raw)
	if err != nil {
		return values, []httputil.FieldError{{Field: "query", Code: "invalid", Message: "must use valid percent encoding"}}
	}
	return values, nil
}

func statsDays(values []string, fields []httputil.FieldError) (int, []httputil.FieldError) {
	if len(values) == 0 {
		return defaultStatsDays, fields
	}
	days, err := strconv.Atoi(values[0])
	if len(values) != 1 || err != nil || days < core.MinStatsDays || days > core.MaxStatsDays {
		fields = append(fields, httputil.FieldError{Field: "days", Code: "invalid", Message: "must be one integer from 1 through 365"})
		return defaultStatsDays, fields
	}
	return days, fields
}

func statsServer(values []string, fields []httputil.FieldError) (string, []httputil.FieldError) {
	if len(values) == 0 {
		return "", fields
	}
	if len(values) != 1 || !core.ValidID(values[0]) {
		fields = append(fields, httputil.FieldError{Field: "media_server_id", Code: "invalid", Message: "must be one valid UUID"})
		return "", fields
	}
	return values[0], fields
}

func statsZone(values []string, fields []httputil.FieldError) (string, []httputil.FieldError) {
	if len(values) == 0 {
		return "UTC", fields
	}
	if len(values) != 1 || values[0] == "" || len(values[0]) > core.MaxStatsZoneBytes {
		fields = append(fields, httputil.FieldError{Field: "tz", Code: "invalid", Message: "must be one IANA time-zone name of at most 64 bytes"})
		return "UTC", fields
	}
	return values[0], fields
}

func firstValue(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

func (s *Server) writeStatsResult(
	w http.ResponseWriter, r *http.Request, report core.StatsReport, kind core.StatsTitleKind, result core.StatsResult,
) {
	window := statsWindowDTO(result.Window)
	switch report {
	case core.StatsReportOverview:
		writeJSON(w, r, s.logger, http.StatusOK, statsOverviewDTO(window, result))
	case core.StatsReportDaily:
		writeJSON(w, r, s.logger, http.StatusOK, statsDailyResponse{Window: window, Items: nonNilDaily(result.Daily)})
	case core.StatsReportPatterns:
		writeJSON(w, r, s.logger, http.StatusOK, statsPatternsResponse{Window: window, Weekdays: nonNilWeekdays(result.Weekdays), Hours: nonNilHours(result.Hours)})
	case core.StatsReportTitles:
		writeJSON(w, r, s.logger, http.StatusOK, statsTitlesResponse{Window: window, Kind: kind, Items: statsTitleDTOs(result.Titles)})
	case core.StatsReportUsers:
		writeJSON(w, r, s.logger, http.StatusOK, statsUsersResponse{Window: window, Items: statsUserDTOs(result.Users)})
	case core.StatsReportLibraries:
		writeJSON(w, r, s.logger, http.StatusOK, statsLibrariesResponse{Window: window, Items: statsLibraryDTOs(result.Libraries)})
	case core.StatsReportUser:
		writeJSON(w, r, s.logger, http.StatusOK, statsUserDetailDTO(window, result))
	}
}

func statsWindowDTO(window core.StatsWindow) statsWindowResponse {
	return statsWindowResponse{
		Days: window.Days, Start: window.Start, End: window.End,
		MediaServerID: window.MediaServerID, TimeZone: window.Zone,
	}
}

func statsOverviewDTO(window statsWindowResponse, result core.StatsResult) statsOverviewResponse {
	return statsOverviewResponse{
		Window: window, Totals: statsTotalsDTO(result.Totals), Titles: statsTitleDTOs(result.Titles),
		Users: statsUserDTOs(result.Users), Clients: statsBreakdownDTOs(result.Clients),
		Devices: statsBreakdownDTOs(result.Devices), PlayMethods: statsBreakdownDTOs(result.PlayMethods),
	}
}

func statsUserDetailDTO(window statsWindowResponse, result core.StatsResult) statsUserDetailResponse {
	watches := make([]playbackWatchResponse, 0, len(result.Watches))
	for _, watch := range result.Watches {
		watches = append(watches, playbackWatchDTO(watch, watch.LastSeenAt))
	}
	return statsUserDetailResponse{
		Window: window, Totals: statsTotalsDTO(result.Totals), Titles: statsTitleDTOs(result.Titles),
		Clients: statsBreakdownDTOs(result.Clients), Devices: statsBreakdownDTOs(result.Devices),
		PlayMethods: statsBreakdownDTOs(result.PlayMethods), Daily: nonNilDaily(result.Daily), Watches: watches,
	}
}

func statsTotalsDTO(value core.StatsTotals) statsTotalsResponse {
	return statsTotalsResponse(value)
}

func statsTitleDTOs(values []core.StatsTitle) []statsTitleResponse {
	result := make([]statsTitleResponse, 0, len(values))
	for _, value := range values {
		result = append(result, statsTitleResponse(value))
	}
	return result
}

func statsUserDTOs(values []core.StatsUser) []statsUserResponse {
	result := make([]statsUserResponse, 0, len(values))
	for _, value := range values {
		result = append(result, statsUserResponse(value))
	}
	return result
}

func statsLibraryDTOs(values []core.StatsLibrary) []statsLibraryResponse {
	result := make([]statsLibraryResponse, 0, len(values))
	for _, value := range values {
		result = append(result, statsLibraryResponse(value))
	}
	return result
}

func statsBreakdownDTOs(values []core.StatsBreakdown) []statsBreakdownResponse {
	result := make([]statsBreakdownResponse, 0, len(values))
	for _, value := range values {
		result = append(result, statsBreakdownResponse(value))
	}
	return result
}

func nonNilDaily(values []core.StatsDailyBucket) []statsDailyBucketResponse {
	result := make([]statsDailyBucketResponse, 0, len(values))
	for _, value := range values {
		result = append(result, statsDailyBucketResponse(value))
	}
	return result
}

func nonNilWeekdays(values []core.StatsWeekdayBucket) []statsWeekdayBucketResponse {
	result := make([]statsWeekdayBucketResponse, 0, len(values))
	for _, value := range values {
		result = append(result, statsWeekdayBucketResponse(value))
	}
	return result
}

func nonNilHours(values []core.StatsHourBucket) []statsHourBucketResponse {
	result := make([]statsHourBucketResponse, 0, len(values))
	for _, value := range values {
		result = append(result, statsHourBucketResponse(value))
	}
	return result
}
