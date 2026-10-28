package http

import (
	"encoding/base64"
	"net/http"
	"strconv"

	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/httputil"
)

const maxMetadataCursorBytes = 4

type metadataDiscoverItemResponse struct {
	metadataTitleResponse
	RequestState core.MetadataRequestState `json:"request_state"`
}

type metadataDiscoverResponse struct {
	Items      []metadataDiscoverItemResponse `json:"items"`
	NextCursor string                         `json:"next_cursor"`
}

type metadataGenreResponse struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type metadataGenresResponse struct {
	Items []metadataGenreResponse `json:"items"`
}

func (s *Server) handleMetadataTrending(w http.ResponseWriter, r *http.Request) {
	s.handleMetadataDiscover(w, r, core.MetadataTrending)
}

func (s *Server) handleMetadataMoviesPopular(w http.ResponseWriter, r *http.Request) {
	s.handleMetadataDiscover(w, r, core.MetadataMoviesPopular)
}

func (s *Server) handleMetadataSeriesPopular(w http.ResponseWriter, r *http.Request) {
	s.handleMetadataDiscover(w, r, core.MetadataSeriesPopular)
}

func (s *Server) handleMetadataMoviesUpcoming(w http.ResponseWriter, r *http.Request) {
	s.handleMetadataDiscover(w, r, core.MetadataMoviesUpcoming)
}

func (s *Server) handleMetadataSeriesUpcoming(w http.ResponseWriter, r *http.Request) {
	s.handleMetadataDiscover(w, r, core.MetadataSeriesUpcoming)
}

func (s *Server) handleMetadataMoviesGenre(w http.ResponseWriter, r *http.Request) {
	s.handleMetadataFilteredDiscover(w, r, core.MetadataMoviesGenre)
}

func (s *Server) handleMetadataSeriesGenre(w http.ResponseWriter, r *http.Request) {
	s.handleMetadataFilteredDiscover(w, r, core.MetadataSeriesGenre)
}

func (s *Server) handleMetadataMoviesStudio(w http.ResponseWriter, r *http.Request) {
	s.handleMetadataFilteredDiscover(w, r, core.MetadataMoviesStudio)
}

func (s *Server) handleMetadataSeriesNetwork(w http.ResponseWriter, r *http.Request) {
	s.handleMetadataFilteredDiscover(w, r, core.MetadataSeriesNetwork)
}

func (s *Server) handleMetadataDiscover(
	w http.ResponseWriter, r *http.Request, list core.MetadataDiscoverList,
) {
	s.discover(w, r, core.MetadataDiscover{List: list})
}

func (s *Server) handleMetadataFilteredDiscover(
	w http.ResponseWriter, r *http.Request, list core.MetadataDiscoverList,
) {
	filter, err := metadataFilterID(r.PathValue("id"))
	if err != nil {
		s.writeValidation(w, r, []httputil.FieldError{{Field: "id", Code: "invalid", Message: "must be a positive TMDB identifier"}})
		return
	}
	s.discover(w, r, core.MetadataDiscover{List: list, FilterID: filter})
}

func (s *Server) discover(w http.ResponseWriter, r *http.Request, input core.MetadataDiscover) {
	page, err := metadataPage(r)
	if err != nil {
		s.writeValidation(w, r, []httputil.FieldError{{Field: "cursor", Code: "invalid", Message: "must be a valid discovery cursor"}})
		return
	}
	input.Page = page
	account, _ := accountFrom(r.Context())
	result, err := s.metadataDiscovery.Discover(r.Context(), account.ID, input)
	if err != nil {
		writeError(w, r, s.logger, err)
		return
	}
	writeJSON(w, r, s.logger, http.StatusOK, metadataDiscoverDTO(result))
}

func metadataPage(r *http.Request) (int, error) {
	values, exists := r.URL.Query()["cursor"]
	if !exists {
		return 1, nil
	}
	if len(values) != 1 || len(values[0]) == 0 || len(values[0]) > maxMetadataCursorBytes {
		return 0, core.ErrInvalidArgument
	}
	decoded, err := base64.RawURLEncoding.DecodeString(values[0])
	if err != nil {
		return 0, core.ErrInvalidArgument
	}
	page, err := strconv.Atoi(string(decoded))
	if err != nil || page < 1 || page > core.MaxMetadataPage || strconv.Itoa(page) != string(decoded) {
		return 0, core.ErrInvalidArgument
	}
	return page, nil
}

// metadataFilterID accepts only the canonical decimal form of a TMDB genre,
// company, or network identifier, so one filter has one cache entry.
func metadataFilterID(raw string) (int, error) {
	if len(raw) == 0 || len(raw) > len(strconv.Itoa(core.MaxMetadataFilterID)) {
		return 0, core.ErrInvalidArgument
	}
	id, err := strconv.Atoi(raw)
	if err != nil || id < 1 || id > core.MaxMetadataFilterID || strconv.Itoa(id) != raw {
		return 0, core.ErrInvalidArgument
	}
	return id, nil
}

func metadataDiscoverDTO(page core.MetadataDiscoverPage) metadataDiscoverResponse {
	items := mapDTOs(page.Items, metadataDiscoverItemDTO)
	next := ""
	if page.Page < page.TotalPages && page.Page < core.MaxMetadataPage {
		next = base64.RawURLEncoding.EncodeToString([]byte(strconv.Itoa(page.Page + 1)))
	}
	return metadataDiscoverResponse{Items: items, NextCursor: next}
}

func (s *Server) handleMetadataGenres(w http.ResponseWriter, r *http.Request) {
	values := r.URL.Query()["kind"]
	if len(values) != 1 {
		s.writeValidation(w, r, []httputil.FieldError{{Field: "kind", Code: "invalid", Message: "must be movie or series"}})
		return
	}
	kind := core.MediaKind(values[0])
	if !kind.Valid() {
		s.writeValidation(w, r, []httputil.FieldError{{Field: "kind", Code: "invalid", Message: "must be movie or series"}})
		return
	}
	genres, err := s.metadataDiscovery.Genres(r.Context(), kind)
	if err != nil {
		writeError(w, r, s.logger, err)
		return
	}
	items := make([]metadataGenreResponse, 0, len(genres))
	for _, genre := range genres {
		items = append(items, metadataGenreResponse{ID: genre.ID, Name: genre.Name})
	}
	writeJSON(w, r, s.logger, http.StatusOK, metadataGenresResponse{Items: items})
}
