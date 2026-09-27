package http

import (
	"net/http"
	"strconv"
	"time"

	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/httputil"
)

const (
	defaultCatalogLimit = 50
	defaultRecentLimit  = 20
	defaultStaleDays    = 90
	maxCatalogDays      = 3650
)

type catalogItemResponse struct {
	MediaServerID   string     `json:"media_server_id"`
	ItemID          string     `json:"item_id"`
	LibraryID       string     `json:"library_id"`
	ParentID        string     `json:"parent_id"`
	ItemType        string     `json:"item_type"`
	Name            string     `json:"name"`
	SeriesID        string     `json:"series_id"`
	SeriesName      string     `json:"series_name"`
	SeasonID        string     `json:"season_id"`
	SeasonNumber    *int32     `json:"season_number,omitempty"`
	IndexNumber     *int32     `json:"index_number,omitempty"`
	RuntimeMS       *int64     `json:"runtime_ms,omitempty"`
	PremiereDate    *time.Time `json:"premiere_date,omitempty"`
	DateCreated     *time.Time `json:"date_created,omitempty"`
	ProductionYear  *int32     `json:"production_year,omitempty"`
	CommunityRating *float64   `json:"community_rating,omitempty"`
	Genres          []string   `json:"genres"`
	PrimaryImageTag string     `json:"primary_image_tag"`
	Archived        bool       `json:"archived"`
	FirstSeenAt     time.Time  `json:"first_seen_at"`
	LastSeenAt      time.Time  `json:"last_seen_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
	Plays           int64      `json:"plays"`
	WatchSeconds    int64      `json:"watch_seconds"`
	UniqueUsers     int64      `json:"unique_users"`
	FirstPlayedAt   *time.Time `json:"first_played_at,omitempty"`
	LastPlayedAt    *time.Time `json:"last_played_at,omitempty"`
}

type catalogItemsResponse struct {
	Items      []catalogItemResponse `json:"items"`
	Limit      int                   `json:"limit"`
	NextCursor string                `json:"next_cursor"`
}

type catalogTypeResponse struct {
	ItemType     string `json:"item_type"`
	Items        int64  `json:"items"`
	Plays        int64  `json:"plays"`
	WatchSeconds int64  `json:"watch_seconds"`
}

type catalogLibraryResponse struct {
	LibraryID string                `json:"library_id"`
	Types     []catalogTypeResponse `json:"types"`
}

type catalogChildResponse struct {
	ItemType string `json:"item_type"`
	Items    int64  `json:"items"`
}

type catalogGenreResponse struct {
	Genre        string `json:"genre"`
	Items        int64  `json:"items"`
	Plays        int64  `json:"plays"`
	WatchSeconds int64  `json:"watch_seconds"`
}

func (s *Server) handleCatalogSync(w http.ResponseWriter, r *http.Request) {
	sync, err := s.catalog.RequestSync(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, r, s.logger, err)
		return
	}
	writeJSON(w, r, s.logger, http.StatusAccepted, map[string]any{
		"media_server_id": sync.MediaServerID, "state": sync.State,
	})
}

func (s *Server) handleCatalogLibraries(w http.ResponseWriter, r *http.Request) {
	window, fields := catalogWindow(r, s.clock.Now())
	if len(fields) > 0 {
		s.writeValidation(w, r, fields)
		return
	}
	items, err := s.catalog.ListLibraries(r.Context(), r.PathValue("id"), window)
	if err != nil {
		writeError(w, r, s.logger, err)
		return
	}
	writeJSON(w, r, s.logger, http.StatusOK, map[string]any{"items": catalogLibraryDTOs(items)})
}

func (s *Server) handleCatalogItems(w http.ResponseWriter, r *http.Request) {
	query, limit, fields := catalogItemQuery(r, s.clock.Now())
	if len(fields) > 0 {
		s.writeValidation(w, r, fields)
		return
	}
	items, err := s.catalog.ListItems(r.Context(), query)
	if err != nil {
		writeError(w, r, s.logger, err)
		return
	}
	items, next := catalogItemPage(items, limit, query)
	writeJSON(w, r, s.logger, http.StatusOK, catalogItemsResponse{
		Items: catalogItemDTOs(items), Limit: limit, NextCursor: next,
	})
}

func (s *Server) handleCatalogItem(w http.ResponseWriter, r *http.Request) {
	detail, err := s.catalog.Item(r.Context(), r.PathValue("id"), r.PathValue("item_id"))
	if err != nil {
		writeError(w, r, s.logger, err)
		return
	}
	writeJSON(w, r, s.logger, http.StatusOK, map[string]any{
		"item": catalogItemDTO(detail.Item), "children": catalogChildDTOs(detail.Children),
	})
}

func (s *Server) handleCatalogHistory(w http.ResponseWriter, r *http.Request) {
	limit, fields := catalogLimit(r)
	after, cursorFields := catalogHistoryCursor(r, r.PathValue("id"), r.PathValue("item_id"))
	fields = append(fields, cursorFields...)
	if len(fields) > 0 {
		s.writeValidation(w, r, fields)
		return
	}
	watches, err := s.catalog.History(r.Context(), core.CatalogHistoryQuery{
		MediaServerID: r.PathValue("id"), ItemID: r.PathValue("item_id"), Limit: limit + 1, After: after,
	})
	if err != nil {
		writeError(w, r, s.logger, err)
		return
	}
	watches, next := catalogHistoryPage(watches, limit, r.PathValue("id"), r.PathValue("item_id"))
	items := make([]playbackWatchResponse, 0, len(watches))
	for _, watch := range watches {
		items = append(items, playbackWatchDTO(watch, s.clock.Now()))
	}
	writeJSON(w, r, s.logger, http.StatusOK, map[string]any{
		"items": items, "limit": limit, "next_cursor": next,
	})
}

func (s *Server) handleCatalogRecent(w http.ResponseWriter, r *http.Request) {
	limit, fields := boundedQueryInt(r, "limit", defaultRecentLimit, 1, core.MaxCatalogReadPageSize)
	if len(fields) > 0 {
		s.writeValidation(w, r, fields)
		return
	}
	items, err := s.catalog.Recent(r.Context(), r.PathValue("id"), r.PathValue("library_id"), limit)
	if err != nil {
		writeError(w, r, s.logger, err)
		return
	}
	writeJSON(w, r, s.logger, http.StatusOK, catalogItemsResponse{
		Items: catalogItemDTOs(items), Limit: limit, NextCursor: "",
	})
}

func (s *Server) handleCatalogGenres(w http.ResponseWriter, r *http.Request) {
	window, fields := catalogWindow(r, s.clock.Now())
	if len(fields) > 0 {
		s.writeValidation(w, r, fields)
		return
	}
	items, err := s.catalog.Genres(r.Context(), r.PathValue("id"), r.PathValue("library_id"), window)
	if err != nil {
		writeError(w, r, s.logger, err)
		return
	}
	writeJSON(w, r, s.logger, http.StatusOK, map[string]any{"items": catalogGenreDTOs(items)})
}

func (s *Server) handleCatalogStale(w http.ResponseWriter, r *http.Request) {
	days, fields := boundedQueryInt(r, "days", defaultStaleDays, 1, maxCatalogDays)
	limit, pageFields := catalogLimit(r)
	fields = append(fields, pageFields...)
	query := core.CatalogStaleQuery{
		MediaServerID: r.PathValue("id"), LibraryID: r.PathValue("library_id"),
		Before: core.NormalizeTime(s.clock.Now()).AddDate(0, 0, -days), Limit: limit + 1,
	}
	after, cursorFields := catalogStaleCursor(r, &query, days)
	fields = append(fields, cursorFields...)
	if len(fields) > 0 {
		s.writeValidation(w, r, fields)
		return
	}
	query.After = after
	items, err := s.catalog.Stale(r.Context(), query)
	if err != nil {
		writeError(w, r, s.logger, err)
		return
	}
	items, next := catalogStalePage(items, limit, query, days)
	writeJSON(w, r, s.logger, http.StatusOK, catalogItemsResponse{
		Items: catalogItemDTOs(items), Limit: limit, NextCursor: next,
	})
}

func catalogItemQuery(r *http.Request, now time.Time) (core.CatalogItemQuery, int, []httputil.FieldError) {
	limit, fields := catalogLimit(r)
	window, windowFields := catalogWindow(r, now)
	fields = append(fields, windowFields...)
	sortValue, sortFields := optionalCatalogQuery(r, "sort")
	fields = append(fields, sortFields...)
	sort := core.CatalogSort(sortValue)
	if sort == "" && len(sortFields) == 0 {
		sort = core.CatalogSortName
	}
	if len(sortFields) == 0 && !sort.Valid() {
		fields = append(fields, catalogField("sort", "is not supported"))
	}
	orderValue, orderFields := optionalCatalogQuery(r, "order")
	fields = append(fields, orderFields...)
	order := core.SortOrder(orderValue)
	if order == "" && len(orderFields) == 0 {
		order = core.SortAscending
	}
	if len(orderFields) == 0 && !order.Valid() {
		fields = append(fields, catalogField("order", "must be asc or desc"))
	}
	archived, archivedFields := catalogArchived(r.URL.Query()["archived"])
	fields = append(fields, archivedFields...)
	itemType, itemTypeFields := optionalCatalogQuery(r, "item_type")
	fields = append(fields, itemTypeFields...)
	query := core.CatalogItemQuery{
		MediaServerID: r.PathValue("id"), LibraryID: r.PathValue("library_id"),
		ItemType: itemType, Sort: sort, Order: order,
		Archived: archived, Limit: limit + 1, Window: window,
	}
	after, cursorFields := catalogItemCursor(r, &query)
	fields = append(fields, cursorFields...)
	query.After = after
	return query, limit, fields
}

func catalogWindow(r *http.Request, now time.Time) (core.CatalogWindow, []httputil.FieldError) {
	values, present := r.URL.Query()["days"]
	if !present {
		return core.CatalogWindow{}, nil
	}
	if len(values) != 1 {
		return core.CatalogWindow{}, []httputil.FieldError{catalogField("days", "must appear once")}
	}
	days, err := strconv.Atoi(values[0])
	if err != nil || days < 1 || days > maxCatalogDays {
		return core.CatalogWindow{}, []httputil.FieldError{catalogField("days", "must be between 1 and 3650")}
	}
	end := core.NormalizeTime(now)
	return core.CatalogWindow{Enabled: true, Start: end.AddDate(0, 0, -days), End: end}, nil
}

func catalogLimit(r *http.Request) (int, []httputil.FieldError) {
	return boundedQueryInt(r, "limit", defaultCatalogLimit, 1, core.MaxCatalogReadPageSize)
}

func boundedQueryInt(
	r *http.Request, name string, fallback, minimum, maximum int,
) (int, []httputil.FieldError) {
	values, present := r.URL.Query()[name]
	if !present {
		return fallback, nil
	}
	if len(values) != 1 {
		return 0, []httputil.FieldError{catalogField(name, "must appear once")}
	}
	parsed, err := strconv.Atoi(values[0])
	if err != nil || parsed < minimum || parsed > maximum {
		return 0, []httputil.FieldError{catalogField(name, "is outside the allowed range")}
	}
	return parsed, nil
}

func optionalCatalogQuery(r *http.Request, name string) (string, []httputil.FieldError) {
	values, present := r.URL.Query()[name]
	if !present {
		return "", nil
	}
	if len(values) != 1 || values[0] == "" {
		return "", []httputil.FieldError{catalogField(name, "must appear once and not be empty")}
	}
	return values[0], nil
}

func catalogArchived(values []string) (*bool, []httputil.FieldError) {
	if len(values) == 0 {
		value := false
		return &value, nil
	}
	if len(values) != 1 {
		return nil, []httputil.FieldError{catalogField("archived", "must appear once")}
	}
	value, err := strconv.ParseBool(values[0])
	if err != nil {
		return nil, []httputil.FieldError{catalogField("archived", "must be true or false")}
	}
	return &value, nil
}

func catalogField(name, message string) httputil.FieldError {
	return httputil.FieldError{Field: name, Code: "invalid", Message: message}
}

func catalogItemDTOs(items []core.CatalogItemStats) []catalogItemResponse {
	result := make([]catalogItemResponse, 0, len(items))
	for _, item := range items {
		result = append(result, catalogItemDTO(item))
	}
	return result
}

func catalogItemDTO(value core.CatalogItemStats) catalogItemResponse {
	item := value.Item
	return catalogItemResponse{
		MediaServerID: item.MediaServerID, ItemID: item.ItemID, LibraryID: item.LibraryID,
		ParentID: item.ParentID, ItemType: item.ItemType, Name: item.Name,
		SeriesID: item.SeriesID, SeriesName: item.SeriesName, SeasonID: item.SeasonID,
		SeasonNumber: item.SeasonNumber, IndexNumber: item.IndexNumber, RuntimeMS: durationMillis(item.Runtime),
		PremiereDate: item.PremiereDate, DateCreated: item.DateCreated, ProductionYear: item.ProductionYear,
		CommunityRating: item.CommunityRating, Genres: item.Genres, PrimaryImageTag: item.PrimaryImageTag,
		Archived: item.Archived, FirstSeenAt: item.FirstSeenAt, LastSeenAt: item.LastSeenAt, UpdatedAt: item.UpdatedAt,
		Plays: value.Plays, WatchSeconds: value.WatchSeconds, UniqueUsers: value.UniqueUsers,
		FirstPlayedAt: value.FirstPlayedAt, LastPlayedAt: value.LastPlayedAt,
	}
}

func durationMillis(value *time.Duration) *int64 {
	if value == nil {
		return nil
	}
	milliseconds := value.Milliseconds()
	return &milliseconds
}

func catalogLibraryDTOs(items []core.CatalogLibrarySummary) []catalogLibraryResponse {
	result := make([]catalogLibraryResponse, 0, len(items))
	for _, item := range items {
		types := make([]catalogTypeResponse, 0, len(item.Types))
		for _, value := range item.Types {
			types = append(types, catalogTypeResponse{
				ItemType: value.ItemType, Items: value.Items, Plays: value.Plays, WatchSeconds: value.WatchSeconds,
			})
		}
		result = append(result, catalogLibraryResponse{LibraryID: item.LibraryID, Types: types})
	}
	return result
}

func catalogChildDTOs(items []core.CatalogChildSummary) []catalogChildResponse {
	result := make([]catalogChildResponse, 0, len(items))
	for _, item := range items {
		result = append(result, catalogChildResponse{ItemType: item.ItemType, Items: item.Items})
	}
	return result
}

func catalogGenreDTOs(items []core.CatalogGenreSummary) []catalogGenreResponse {
	result := make([]catalogGenreResponse, 0, len(items))
	for _, item := range items {
		result = append(result, catalogGenreResponse{
			Genre: item.Genre, Items: item.Items, Plays: item.Plays, WatchSeconds: item.WatchSeconds,
		})
	}
	return result
}
