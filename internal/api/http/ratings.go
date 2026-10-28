package http

import (
	"context"
	"net/http"

	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/httputil"
	"github.com/BonzTM/bloom/internal/telemetry"
)

type ratingsManager interface {
	SetKey(context.Context, string) error
	HasKey(context.Context) (bool, error)
	RemoveKey(context.Context) error
	Ratings(context.Context, string) (core.TitleRatings, error)
}

type (
	titleRatingsResponse struct {
		IMDb           *titleRatingResponse `json:"imdb,omitempty"`
		RottenTomatoes *titleRatingResponse `json:"rotten_tomatoes,omitempty"`
		Metacritic     *titleRatingResponse `json:"metacritic,omitempty"`
	}
	titleRatingResponse struct {
		Score  float64 `json:"score"`
		Votes  int     `json:"votes"`
		Source string  `json:"source"`
	}
)

func (s *Server) handleMovieRatings(w http.ResponseWriter, r *http.Request) {
	s.writeTitleRatings(w, r, func(accountID, id string) (string, error) {
		movie, err := s.metadataReader.MovieDetail(r.Context(), accountID, id)
		return movie.Details.IMDbID, err
	})
}

func (s *Server) handleSeriesRatings(w http.ResponseWriter, r *http.Request) {
	s.writeTitleRatings(w, r, func(accountID, id string) (string, error) {
		series, err := s.metadataReader.SeriesDetail(r.Context(), accountID, id, false)
		return series.Details.IMDbID, err
	})
}

// writeTitleRatings resolves the title's IMDb ID through the cached detail,
// then answers with whatever scores are known. A title with no IMDb ID, or
// no OMDb key stored, answers with no scores rather than an error.
func (s *Server) writeTitleRatings(
	w http.ResponseWriter, r *http.Request, imdbIDFor func(accountID, id string) (string, error),
) {
	id := r.PathValue("id")
	if core.ValidateProviderID(id) != nil {
		s.writeRequestError(w, r, core.ErrInvalidArgument)
		return
	}
	account, ok := accountFrom(r.Context())
	if !ok {
		writeError(w, r, s.logger, errAuthenticationRequired)
		return
	}
	imdbID, err := imdbIDFor(account.ID, id)
	if err != nil {
		writeError(w, r, s.logger, err)
		return
	}
	var ratings core.TitleRatings
	if core.ValidIMDbID(imdbID) {
		if ratings, err = s.ratings.Ratings(r.Context(), imdbID); err != nil {
			writeError(w, r, s.logger, err)
			return
		}
	}
	writeJSON(w, r, s.logger, http.StatusOK, titleRatingsDTO(ratings))
}

func titleRatingsDTO(ratings core.TitleRatings) titleRatingsResponse {
	return titleRatingsResponse{
		IMDb: titleRatingDTO(ratings.IMDb), RottenTomatoes: titleRatingDTO(ratings.RottenTomatoes),
		Metacritic: titleRatingDTO(ratings.Metacritic),
	}
}

func titleRatingDTO(rating *core.TitleRating) *titleRatingResponse {
	if rating == nil {
		return nil
	}
	return &titleRatingResponse{Score: rating.Score, Votes: rating.Votes, Source: string(rating.Source)}
}

func (s *Server) handleOMDbKeyPresence(w http.ResponseWriter, r *http.Request) {
	present, err := s.ratings.HasKey(r.Context())
	if err != nil {
		writeError(w, r, s.logger, err)
		return
	}
	writeJSON(w, r, s.logger, http.StatusOK, metadataKeyPresenceResponse{Configured: present})
}

func (s *Server) handleSetOMDbKey(w http.ResponseWriter, r *http.Request) {
	if !loginContentTypeSupported(r.Header.Get("Content-Type")) {
		writeError(w, r, s.logger, errUnsupportedMediaType)
		return
	}
	request, err := httputil.DecodeJSON[metadataKeyRequest](w, r, s.maxBodyBytes)
	if err != nil || core.ValidateOMDbKey(request.APIKey) != nil {
		s.writeValidation(w, r, []httputil.FieldError{{
			Field: "api_key", Code: "omdb_key_invalid", Message: "an OMDb API key is 8 to 64 letters and digits",
		}})
		return
	}
	if err := s.ratings.SetKey(r.Context(), request.APIKey); err != nil {
		s.emitProviderKeyAudit(r, core.MetadataProviderOMDb, "metadata_provider.key.set", telemetry.AuditFailure)
		writeError(w, r, s.logger, err)
		return
	}
	s.emitProviderKeyAudit(r, core.MetadataProviderOMDb, "metadata_provider.key.set", telemetry.AuditSuccess)
	writeJSON(w, r, s.logger, http.StatusOK, metadataKeyPresenceResponse{Configured: true})
}

func (s *Server) handleDeleteOMDbKey(w http.ResponseWriter, r *http.Request) {
	if err := s.ratings.RemoveKey(r.Context()); err != nil {
		s.emitProviderKeyAudit(r, core.MetadataProviderOMDb, "metadata_provider.key.remove", telemetry.AuditFailure)
		writeError(w, r, s.logger, err)
		return
	}
	s.emitProviderKeyAudit(r, core.MetadataProviderOMDb, "metadata_provider.key.remove", telemetry.AuditSuccess)
	w.WriteHeader(http.StatusNoContent)
}
