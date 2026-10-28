package http

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/httputil"
)

type recentlyAddedReader interface {
	List(context.Context, int) ([]core.RecentlyAddedTitle, error)
}

type recentlyAddedItemResponse struct {
	MediaServerID string         `json:"media_server_id"`
	ItemID        string         `json:"item_id"`
	Kind          core.MediaKind `json:"kind"`
	Title         string         `json:"title"`
	Year          int            `json:"year"`
	TMDBID        int64          `json:"tmdb_id"`
	AddedAt       time.Time      `json:"added_at"`
}

type recentlyAddedResponse struct {
	Items []recentlyAddedItemResponse `json:"items"`
}

func (s *Server) handleRecentlyAdded(w http.ResponseWriter, r *http.Request) {
	limit, ok := recentlyAddedLimit(r)
	if !ok {
		s.writeValidation(w, r, []httputil.FieldError{{
			Field: "limit", Code: "out_of_range", Message: "must be one integer from 1 through 40",
		}})
		return
	}
	titles, err := s.recentlyAdded.List(r.Context(), limit)
	if err != nil {
		writeError(w, r, s.logger, err)
		return
	}
	writeJSON(w, r, s.logger, http.StatusOK, recentlyAddedDTO(titles))
}

func recentlyAddedLimit(r *http.Request) (int, bool) {
	values, exists := r.URL.Query()["limit"]
	if !exists {
		return core.DefaultRecentlyAddedLimit, true
	}
	if len(values) != 1 || len(values[0]) > 2 {
		return 0, false
	}
	limit, err := strconv.Atoi(values[0])
	if err != nil || strconv.Itoa(limit) != values[0] || !core.ValidRecentlyAddedLimit(limit) {
		return 0, false
	}
	return limit, true
}

func recentlyAddedDTO(titles []core.RecentlyAddedTitle) recentlyAddedResponse {
	items := make([]recentlyAddedItemResponse, 0, len(titles))
	for _, title := range titles {
		items = append(items, recentlyAddedItemResponse{
			MediaServerID: title.MediaServerID, ItemID: title.ItemID, Kind: title.Kind, Title: title.Title,
			Year: title.Year, TMDBID: title.TMDBID, AddedAt: title.AddedAt.UTC(),
		})
	}
	return recentlyAddedResponse{Items: items}
}
