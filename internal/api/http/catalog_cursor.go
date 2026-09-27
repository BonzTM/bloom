package http

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/httputil"
)

type catalogItemCursorPayload struct {
	Query     catalogItemCursorQuery `json:"query"`
	Sort      core.CatalogSort       `json:"sort"`
	Order     core.SortOrder         `json:"order"`
	TextValue string                 `json:"text,omitempty"`
	TimeValue *time.Time             `json:"time,omitempty"`
	Number    int64                  `json:"number,omitempty"`
	ItemID    string                 `json:"item_id"`
}

type catalogHistoryCursorPayload struct {
	MediaServerID string    `json:"media_server_id"`
	ItemID        string    `json:"item_id"`
	StartedAt     time.Time `json:"started_at"`
	ID            string    `json:"id"`
}

type catalogStaleCursorPayload struct {
	MediaServerID string     `json:"media_server_id"`
	LibraryID     string     `json:"library_id"`
	Days          int        `json:"days"`
	Before        time.Time  `json:"before"`
	LastPlayedAt  *time.Time `json:"last_played_at,omitempty"`
	Name          string     `json:"name"`
	ItemID        string     `json:"item_id"`
}

type catalogItemCursorQuery struct {
	MediaServerID string             `json:"media_server_id"`
	LibraryID     string             `json:"library_id"`
	ItemType      string             `json:"item_type"`
	Archived      bool               `json:"archived"`
	Window        core.CatalogWindow `json:"window"`
}

func catalogItemCursor(r *http.Request, query *core.CatalogItemQuery) (*core.CatalogItemPosition, []httputil.FieldError) {
	var payload catalogItemCursorPayload
	present, fields := decodeCatalogCursorQuery(r, &payload)
	if !present || len(fields) > 0 {
		return nil, fields
	}
	position := &core.CatalogItemPosition{
		TextValue: payload.TextValue, TimeValue: payload.TimeValue,
		NumberValue: payload.Number, ItemID: payload.ItemID,
	}
	probe := core.CatalogItemQuery{
		MediaServerID: catalogCursorProbeID, LibraryID: "library", Sort: query.Sort, Order: query.Order,
		Limit: 1, After: position,
	}
	if payload.Sort != query.Sort || payload.Order != query.Order || !probe.Valid() ||
		!catalogItemCursorMatches(payload.Query, *query) {
		return nil, []httputil.FieldError{catalogField("cursor", "does not match this query")}
	}
	query.Window = payload.Query.Window
	return position, nil
}

func catalogItemCursorMatches(got catalogItemCursorQuery, query core.CatalogItemQuery) bool {
	archived := query.Archived != nil && *query.Archived
	if got.MediaServerID != query.MediaServerID || got.LibraryID != query.LibraryID ||
		got.ItemType != query.ItemType || got.Archived != archived || got.Window.Enabled != query.Window.Enabled {
		return false
	}
	if !got.Window.Valid() || !got.Window.Enabled {
		return !got.Window.Enabled && got.Window.Start.IsZero() && got.Window.End.IsZero()
	}
	return got.Window.End.Sub(got.Window.Start) == query.Window.End.Sub(query.Window.Start)
}

func catalogHistoryCursor(
	r *http.Request, mediaServerID, itemID string,
) (*core.CatalogHistoryPosition, []httputil.FieldError) {
	var payload catalogHistoryCursorPayload
	present, fields := decodeCatalogCursorQuery(r, &payload)
	if !present || len(fields) > 0 {
		return nil, fields
	}
	position := &core.CatalogHistoryPosition{StartedAt: payload.StartedAt, ID: payload.ID}
	probe := core.CatalogHistoryQuery{MediaServerID: catalogCursorProbeID, ItemID: "item", Limit: 1, After: position}
	if !probe.Valid() || payload.MediaServerID != mediaServerID || payload.ItemID != itemID {
		return nil, []httputil.FieldError{catalogField("cursor", "does not match this query")}
	}
	return position, nil
}

func catalogStaleCursor(
	r *http.Request, query *core.CatalogStaleQuery, days int,
) (*core.CatalogStalePosition, []httputil.FieldError) {
	var payload catalogStaleCursorPayload
	present, fields := decodeCatalogCursorQuery(r, &payload)
	if !present || len(fields) > 0 {
		return nil, fields
	}
	position := &core.CatalogStalePosition{
		LastPlayedAt: payload.LastPlayedAt, Name: payload.Name, ItemID: payload.ItemID,
	}
	probe := core.CatalogStaleQuery{
		MediaServerID: catalogCursorProbeID, LibraryID: "library", Before: time.Now(), Limit: 1, After: position,
	}
	if !probe.Valid() || payload.MediaServerID != query.MediaServerID || payload.LibraryID != query.LibraryID ||
		payload.Days != days || payload.Before.IsZero() {
		return nil, []httputil.FieldError{catalogField("cursor", "does not match this query")}
	}
	query.Before = payload.Before
	return position, nil
}

func decodeCatalogCursorQuery(r *http.Request, target any) (bool, []httputil.FieldError) {
	values, present := r.URL.Query()["cursor"]
	if !present {
		return false, nil
	}
	if len(values) != 1 || values[0] == "" || len(values[0]) > core.MaxCatalogReadCursorBytes {
		return true, []httputil.FieldError{catalogField("cursor", "must appear once and be bounded")}
	}
	data, err := base64.RawURLEncoding.Strict().DecodeString(values[0])
	if err != nil || len(data) > core.MaxCatalogReadCursorBytes {
		return true, []httputil.FieldError{catalogField("cursor", "is invalid")}
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(target); err != nil || decoder.Decode(&struct{}{}) != io.EOF {
		return true, []httputil.FieldError{catalogField("cursor", "is invalid")}
	}
	return true, nil
}

func catalogItemPage(
	items []core.CatalogItemStats, limit int, query core.CatalogItemQuery,
) ([]core.CatalogItemStats, string) {
	if len(items) <= limit {
		return items, ""
	}
	items = items[:limit]
	last := items[len(items)-1]
	position := last.PagePosition
	if position.ItemID == "" {
		position = catalogResponsePosition(query.Sort, last)
	}
	payload := catalogItemCursorPayload{
		Query: catalogItemCursorIdentity(query), Sort: query.Sort, Order: query.Order, ItemID: position.ItemID,
	}
	switch query.Sort {
	case core.CatalogSortName:
		payload.TextValue = position.TextValue
	case core.CatalogSortDateAdded, core.CatalogSortPremiereDate, core.CatalogSortLastPlayed:
		payload.TimeValue = position.TimeValue
	case core.CatalogSortPlays:
		payload.Number = position.NumberValue
	case core.CatalogSortWatchTime:
		payload.Number = position.NumberValue
	}
	return items, encodeCatalogCursor(payload)
}

func catalogResponsePosition(sort core.CatalogSort, item core.CatalogItemStats) core.CatalogItemPosition {
	position := core.CatalogItemPosition{ItemID: item.Item.ItemID}
	switch sort {
	case core.CatalogSortName:
		position.TextValue = item.Item.Name
	case core.CatalogSortDateAdded:
		position.TimeValue = item.Item.DateCreated
	case core.CatalogSortPremiereDate:
		position.TimeValue = item.Item.PremiereDate
	case core.CatalogSortPlays:
		position.NumberValue = item.Plays
	case core.CatalogSortWatchTime:
		position.NumberValue = item.WatchSeconds
	case core.CatalogSortLastPlayed:
		position.TimeValue = item.LastPlayedAt
	}
	return position
}

func catalogItemCursorIdentity(query core.CatalogItemQuery) catalogItemCursorQuery {
	return catalogItemCursorQuery{
		MediaServerID: query.MediaServerID, LibraryID: query.LibraryID, ItemType: query.ItemType,
		Archived: query.Archived != nil && *query.Archived, Window: query.Window,
	}
}

func catalogHistoryPage(
	items []core.PlaybackWatch, limit int, mediaServerID, itemID string,
) ([]core.PlaybackWatch, string) {
	if len(items) <= limit {
		return items, ""
	}
	items = items[:limit]
	last := items[len(items)-1]
	return items, encodeCatalogCursor(catalogHistoryCursorPayload{
		MediaServerID: mediaServerID, ItemID: itemID, StartedAt: last.StartedAt, ID: last.ID,
	})
}

func catalogStalePage(
	items []core.CatalogItemStats, limit int, query core.CatalogStaleQuery, days int,
) ([]core.CatalogItemStats, string) {
	if len(items) <= limit {
		return items, ""
	}
	items = items[:limit]
	last := items[len(items)-1]
	return items, encodeCatalogCursor(catalogStaleCursorPayload{
		MediaServerID: query.MediaServerID, LibraryID: query.LibraryID, Days: days, Before: query.Before,
		LastPlayedAt: last.LastPlayedAt, Name: last.Item.Name, ItemID: last.Item.ItemID,
	})
}

func encodeCatalogCursor(value any) string {
	data, err := json.Marshal(value)
	if err != nil || len(data) > core.MaxCatalogReadCursorBytes {
		return ""
	}
	encoded := base64.RawURLEncoding.EncodeToString(data)
	if len(encoded) > core.MaxCatalogReadCursorBytes {
		return ""
	}
	return encoded
}

const catalogCursorProbeID = "94000000-0000-4000-8000-000000000099"
