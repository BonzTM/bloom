package core

import (
	"context"
	"math"
	"strings"
	"unicode/utf8"
)

const (
	// MetadataProviderOMDb identifies the OMDb ratings provider. It holds a
	// stored credential like TMDB, but it is never a title provider: requests
	// and title keys stay TMDB-only through MetadataProviderKind.Valid.
	MetadataProviderOMDb MetadataProviderKind = "omdb"
	// MinOMDbKeyBytes is the length of the shortest OMDb API key.
	MinOMDbKeyBytes = 8
	// MaxOMDbKeyBytes bounds a stored OMDb API key.
	MaxOMDbKeyBytes = 64
	// MaxIMDbVotes bounds an IMDb vote count.
	MaxIMDbVotes = 100_000_000
)

// RatingSource names where a rating came from.
type RatingSource string

const (
	// RatingSourceOMDb marks a rating read from OMDb.
	RatingSourceOMDb RatingSource = "omdb"
	// RatingSourceRadarr marks a rating Radarr keeps for a movie it manages.
	RatingSourceRadarr RatingSource = "radarr"
	// RatingSourceJellyfin marks a rating Jellyfin keeps for a library item.
	RatingSourceJellyfin RatingSource = "jellyfin"
)

// Valid reports whether the source is known.
func (s RatingSource) Valid() bool {
	return s == RatingSourceOMDb || s == RatingSourceRadarr || s == RatingSourceJellyfin
}

// TitleRating is one score with its vote count, when the source has one.
type TitleRating struct {
	Score  float64
	Votes  int
	Source RatingSource
}

// TitleRatings holds the outside scores of one title. A nil field is a score
// no source knows.
type TitleRatings struct {
	// IMDb is the IMDb user score out of 10.
	IMDb *TitleRating
	// RottenTomatoes is the Rotten Tomatoes critics score out of 100.
	RottenTomatoes *TitleRating
	// Metacritic is the Metacritic critics score out of 100.
	Metacritic *TitleRating
}

// TitleRatingsProvider reads the outside scores of a title by its IMDb ID.
type TitleRatingsProvider interface {
	TitleRatings(ctx context.Context, imdbID string) (TitleRatings, error)
}

// RatedTitle names a title whose scores are wanted. IMDbID may be empty.
type RatedTitle struct {
	Kind   MediaKind
	TMDBID string
	IMDbID string
	Title  string
}

// OwnedRatingsSource reads the scores a local service keeps for a title the
// operator holds. A title it does not hold has no scores and no error.
type OwnedRatingsSource interface {
	OwnedTitleRatings(ctx context.Context, title RatedTitle) (TitleRatings, error)
}

// MovieRatingsLookup is an optional download-manager adapter capability: the
// scores the manager keeps for a movie in its library, by TMDB ID.
type MovieRatingsLookup interface {
	MovieRatings(ctx context.Context, tmdbID string) (TitleRatings, bool, error)
}

// CriticRatingLookup is an optional media-server adapter capability: the
// critics score out of 100 the server keeps for a title, by TMDB ID. The
// title name narrows the server-side search.
type CriticRatingLookup interface {
	CriticRating(ctx context.Context, kind MediaKind, tmdbID, title string) (float64, bool, error)
}

// ValidateRatedTitle validates the identifiers of a ratings lookup.
func ValidateRatedTitle(title RatedTitle) error {
	if !title.Kind.Valid() || ValidateProviderID(title.TMDBID) != nil {
		return ErrInvalidArgument
	}
	if title.IMDbID != "" && !ValidIMDbID(title.IMDbID) {
		return ErrInvalidArgument
	}
	if len(title.Title) > MaxMetadataTitleBytes || !utf8.ValidString(title.Title) {
		return ErrInvalidArgument
	}
	return nil
}

// StoresCredential reports whether the kind holds a stored provider credential.
func (k MetadataProviderKind) StoresCredential() bool {
	return k == MetadataProviderTMDB || k == MetadataProviderOMDb
}

// ValidateOMDbKey validates the shape of an OMDb API key: ASCII letters and
// digits only, so it is safe in a query string.
func ValidateOMDbKey(value string) error {
	if len(value) < MinOMDbKeyBytes || len(value) > MaxOMDbKeyBytes {
		return ErrInvalidArgument
	}
	if strings.IndexFunc(value, func(char rune) bool { return !isASCIIAlphanumeric(char) }) >= 0 {
		return ErrInvalidArgument
	}
	return nil
}

// ValidIMDbID reports whether value is an IMDb title ID such as "tt0133093".
func ValidIMDbID(value string) bool {
	digits, ok := strings.CutPrefix(value, "tt")
	if !ok || len(digits) < 7 || len(digits) > 12 {
		return false
	}
	return strings.IndexFunc(digits, func(char rune) bool { return char < '0' || char > '9' }) < 0
}

// ValidTitleRating reports whether rating is a finite score within maxScore,
// from a known source, with a bounded vote count.
func ValidTitleRating(rating TitleRating, maxScore float64) bool {
	if math.IsNaN(rating.Score) || math.IsInf(rating.Score, 0) || rating.Score < 0 || rating.Score > maxScore {
		return false
	}
	return rating.Votes >= 0 && rating.Votes <= MaxIMDbVotes && rating.Source.Valid()
}

func isASCIIAlphanumeric(char rune) bool {
	return (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9')
}

// Complete reports whether every score is known.
func (r TitleRatings) Complete() bool {
	return r.IMDb != nil && r.RottenTomatoes != nil && r.Metacritic != nil
}

// Empty reports whether no score is known.
func (r TitleRatings) Empty() bool {
	return r.IMDb == nil && r.RottenTomatoes == nil && r.Metacritic == nil
}

// FillMissing returns r with each unknown score taken from other.
func (r TitleRatings) FillMissing(other TitleRatings) TitleRatings {
	if r.IMDb == nil {
		r.IMDb = other.IMDb
	}
	if r.RottenTomatoes == nil {
		r.RottenTomatoes = other.RottenTomatoes
	}
	if r.Metacritic == nil {
		r.Metacritic = other.Metacritic
	}
	return r
}
