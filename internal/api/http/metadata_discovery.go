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

func (s *Server) handleMetadataDiscover(
	w http.ResponseWriter, r *http.Request, list core.MetadataDiscoverList,
) {
	page, err := metadataPage(r)
	if err != nil {
		s.writeValidation(w, r, []httputil.FieldError{{Field: "cursor", Code: "invalid", Message: "must be a valid discovery cursor"}})
		return
	}
	account, _ := accountFrom(r.Context())
	result, err := s.metadataDiscovery.Discover(r.Context(), account.ID, core.MetadataDiscover{List: list, Page: page})
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

func metadataDiscoverDTO(page core.MetadataDiscoverPage) metadataDiscoverResponse {
	items := make([]metadataDiscoverItemResponse, 0, len(page.Items))
	for _, item := range page.Items {
		items = append(items, metadataDiscoverItemResponse{
			metadataTitleResponse: metadataTitleDTO(item.MetadataTitle), RequestState: item.RequestState,
		})
	}
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
