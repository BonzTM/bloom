package core

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	// MetadataProviderTMDB identifies The Movie Database provider.
	MetadataProviderTMDB MetadataProviderKind = "tmdb"
	// MediaKindMovie identifies a movie.
	MediaKindMovie MediaKind = "movie"
	// MediaKindSeries identifies a television series.
	MediaKindSeries MediaKind = "series"
	// MaxMetadataQueryBytes bounds a metadata search query.
	MaxMetadataQueryBytes = 200
	// MaxMetadataTitleBytes bounds a provider title.
	MaxMetadataTitleBytes = 500
	// MaxMetadataOverviewBytes bounds a provider overview.
	MaxMetadataOverviewBytes = 10_000
	// MaxMetadataPosterPathBytes bounds a provider image path.
	MaxMetadataPosterPathBytes = 500
)

var (
	// ErrMetadataNotConfigured reports missing provider configuration.
	ErrMetadataNotConfigured = errors.New("metadata provider not configured")
	// ErrMetadataUnauthorized reports a credential rejected by the provider.
	ErrMetadataUnauthorized = errors.New("metadata provider credential rejected")
	// ErrMetadataUnavailable reports a transient provider failure.
	ErrMetadataUnavailable = errors.New("metadata provider unavailable")
	// ErrMetadataMalformed reports an invalid provider response.
	ErrMetadataMalformed = errors.New("metadata provider returned malformed data")
)

// MetadataProviderKind is a supported metadata provider identifier.
type MetadataProviderKind string

// Valid reports whether the provider kind is supported.
func (k MetadataProviderKind) Valid() bool { return k == MetadataProviderTMDB }

// MediaKind is a supported requestable media kind.
type MediaKind string

// Valid reports whether the media kind is supported.
func (k MediaKind) Valid() bool { return k == MediaKindMovie || k == MediaKindSeries }

// MetadataSearch is a validated provider search request.
type MetadataSearch struct {
	Query string
	Kind  *MediaKind
}

// MetadataTitle is a normalized movie or series result.
type MetadataTitle struct {
	Kind       MediaKind
	Provider   MetadataProviderKind
	ProviderID string
	Title      string
	Year       int
	Overview   string
	PosterPath string
}

// MetadataSeason is a normalized series season.
type MetadataSeason struct {
	Number       int
	Name         string
	EpisodeCount int
	AirDate      *time.Time
}

// MetadataSeries combines normalized title and season details.
type MetadataSeries struct {
	MetadataTitle
	Seasons []MetadataSeason
}

// MetadataProvider searches and retrieves normalized provider metadata.
type MetadataProvider interface {
	Search(ctx context.Context, input MetadataSearch) ([]MetadataTitle, error)
	Movie(ctx context.Context, providerID string) (MetadataTitle, error)
	Series(ctx context.Context, providerID string, includeSpecials bool) (MetadataSeries, error)
}

// MetadataProviderRecord stores one encrypted provider credential.
type MetadataProviderRecord struct {
	Kind                 MetadataProviderKind
	CredentialCiphertext []byte
	KeyID                string
	CreatedAt            time.Time
	UpdatedAt            time.Time
}

// MetadataProviderReader retrieves encrypted provider configuration.
type MetadataProviderReader interface {
	GetMetadataProvider(ctx context.Context, kind MetadataProviderKind) (MetadataProviderRecord, error)
}

// MetadataProviderWriter mutates encrypted provider configuration.
type MetadataProviderWriter interface {
	UpsertMetadataProvider(ctx context.Context, record MetadataProviderRecord) error
	DeleteMetadataProvider(ctx context.Context, kind MetadataProviderKind) error
}

// ValidateMetadataSearch validates a bounded search query and kind.
func ValidateMetadataSearch(input MetadataSearch) error {
	if input.Query == "" || len(input.Query) > MaxMetadataQueryBytes || !validText(input.Query) || input.Query != strings.TrimSpace(input.Query) {
		return fmt.Errorf("metadata search query: %w", ErrInvalidArgument)
	}
	if input.Kind != nil && !input.Kind.Valid() {
		return fmt.Errorf("metadata search kind: %w", ErrInvalidArgument)
	}
	return nil
}

// ValidateProviderID validates a positive decimal TMDB identifier.
func ValidateProviderID(value string) error {
	if value == "" || len(value) > 20 {
		return ErrInvalidArgument
	}
	id, err := strconv.ParseInt(value, 10, 64)
	if err != nil || id <= 0 {
		return ErrInvalidArgument
	}
	return nil
}

// ValidateMetadataTitle validates a normalized provider title.
func ValidateMetadataTitle(title MetadataTitle) error {
	if !title.Kind.Valid() || !title.Provider.Valid() || ValidateProviderID(title.ProviderID) != nil {
		return ErrInvalidArgument
	}
	if !boundedText(title.Title, MaxMetadataTitleBytes) || !boundedOptionalText(title.Overview, MaxMetadataOverviewBytes) ||
		!boundedOptionalText(title.PosterPath, MaxMetadataPosterPathBytes) || title.Year < 0 || title.Year > 9999 {
		return ErrInvalidArgument
	}
	return nil
}

// ValidateMetadataSeasons validates bounded, unique season metadata.
func ValidateMetadataSeasons(seasons []MetadataSeason, includeSpecials bool) error {
	if len(seasons) == 0 || len(seasons) > 100 {
		return ErrInvalidArgument
	}
	seen := make(map[int]struct{}, len(seasons))
	for _, season := range seasons {
		if season.Number < 0 || season.Number > 999 || (!includeSpecials && season.Number == 0) ||
			season.EpisodeCount < 0 || season.EpisodeCount > 9999 || !boundedText(season.Name, MaxMetadataTitleBytes) {
			return ErrInvalidArgument
		}
		if _, exists := seen[season.Number]; exists {
			return ErrInvalidArgument
		}
		seen[season.Number] = struct{}{}
	}
	return nil
}

func boundedText(value string, maximum int) bool {
	return value != "" && len(value) <= maximum && validText(value) && value == strings.TrimSpace(value)
}

func boundedOptionalText(value string, maximum int) bool {
	return value == "" || boundedText(value, maximum)
}

func validText(value string) bool {
	return utf8.ValidString(value) && strings.IndexFunc(value, unicode.IsControl) < 0
}
