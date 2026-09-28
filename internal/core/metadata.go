package core

import (
	"context"
	"encoding/base64"
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
	// MaxMetadataBackdropPathBytes bounds a provider backdrop path.
	MaxMetadataBackdropPathBytes = 500
	// MaxMetadataGenreNameBytes bounds a provider genre name.
	MaxMetadataGenreNameBytes = 100
	// MaxMetadataPage is the deepest TMDB page Bloom exposes.
	MaxMetadataPage = 20
	// MetadataPageSize is TMDB's fixed discovery page size.
	MetadataPageSize = 20
	// MinMetadataCredentialBytes is the shortest accepted TMDB Read Access Token.
	MinMetadataCredentialBytes = 100
	// MaxMetadataCredentialBytes bounds a stored TMDB Read Access Token.
	MaxMetadataCredentialBytes = 4096
)

var (
	// ErrMetadataNotConfigured reports missing provider configuration.
	ErrMetadataNotConfigured = errors.New("metadata provider not configured")
	// ErrMetadataUnauthorized reports a credential rejected by the provider.
	ErrMetadataUnauthorized = errors.New("metadata provider credential rejected")
	// ErrMetadataUnreachable reports a provider connection failure before a response.
	ErrMetadataUnreachable = errors.New("metadata provider unreachable")
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

// MetadataDiscoverList identifies one curated discovery row.
type MetadataDiscoverList string

const (
	// MetadataTrending is TMDB's combined weekly movie and series trend.
	MetadataTrending MetadataDiscoverList = "trending"
	// MetadataMoviesPopular is TMDB's popular movie list.
	MetadataMoviesPopular MetadataDiscoverList = "movies_popular"
	// MetadataSeriesPopular is TMDB's popular series list.
	MetadataSeriesPopular MetadataDiscoverList = "series_popular"
	// MetadataMoviesUpcoming is TMDB's upcoming movie list.
	MetadataMoviesUpcoming MetadataDiscoverList = "movies_upcoming"
	// MetadataSeriesUpcoming is TMDB's on-the-air series list.
	MetadataSeriesUpcoming MetadataDiscoverList = "series_upcoming"
)

// Valid reports whether the discovery list is supported.
func (l MetadataDiscoverList) Valid() bool {
	return l == MetadataTrending || l == MetadataMoviesPopular || l == MetadataSeriesPopular ||
		l == MetadataMoviesUpcoming || l == MetadataSeriesUpcoming
}

// MetadataSearch is a validated provider search request.
type MetadataSearch struct {
	Query string
	Kind  *MediaKind
}

// MetadataTitle is a normalized movie or series result.
type MetadataTitle struct {
	Kind         MediaKind
	Provider     MetadataProviderKind
	ProviderID   string
	Title        string
	Year         int
	Overview     string
	PosterPath   string
	BackdropPath string
}

// MetadataDiscover is a validated provider discovery request.
type MetadataDiscover struct {
	List MetadataDiscoverList
	Page int
}

// MetadataPage is one bounded provider discovery page.
type MetadataPage struct {
	Items      []MetadataTitle
	Page       int
	TotalPages int
}

// MetadataGenre is one provider genre option.
type MetadataGenre struct {
	ID   int
	Name string
}

// MetadataTitleKey identifies a requestable provider title.
type MetadataTitleKey struct {
	Kind       MediaKind
	Provider   MetadataProviderKind
	ProviderID string
}

// MetadataRequestState is the caller's latest request state for a title.
type MetadataRequestState string

const (
	// MetadataRequestNone means the caller has not requested the title.
	MetadataRequestNone MetadataRequestState = "none"
	// MetadataRequestPending means the request awaits approval.
	MetadataRequestPending MetadataRequestState = "pending"
	// MetadataRequestApproved means the request awaits dispatch.
	MetadataRequestApproved MetadataRequestState = "approved"
	// MetadataRequestProcessing means the request is being fulfilled.
	MetadataRequestProcessing MetadataRequestState = "processing"
	// MetadataRequestAvailable means the requested title is available.
	MetadataRequestAvailable MetadataRequestState = "available"
	// MetadataRequestDeclined means an approver rejected the request.
	MetadataRequestDeclined MetadataRequestState = "declined"
	// MetadataRequestFailed means fulfilment ended in failure.
	MetadataRequestFailed MetadataRequestState = "failed"
)

// Valid reports whether the request state belongs to the discovery contract.
func (s MetadataRequestState) Valid() bool {
	return s == MetadataRequestNone || s == MetadataRequestPending || s == MetadataRequestApproved ||
		s == MetadataRequestProcessing || s == MetadataRequestAvailable || s == MetadataRequestDeclined ||
		s == MetadataRequestFailed
}

// MetadataDiscoverItem combines provider metadata with the caller's request state.
type MetadataDiscoverItem struct {
	MetadataTitle
	RequestState MetadataRequestState
}

// MetadataDiscoverPage is one caller-specific discovery page.
type MetadataDiscoverPage struct {
	Items      []MetadataDiscoverItem
	Page       int
	TotalPages int
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

// MetadataDiscoveryProvider supplies curated lists and genres.
type MetadataDiscoveryProvider interface {
	Discover(ctx context.Context, input MetadataDiscover) (MetadataPage, error)
	Genres(ctx context.Context, kind MediaKind) ([]MetadataGenre, error)
}

// MetadataRequestStateReader resolves the caller's latest request state in one bounded lookup.
type MetadataRequestStateReader interface {
	MetadataRequestStates(ctx context.Context, accountID string, titles []MetadataTitle) (map[MetadataTitleKey]RequestStatus, error)
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

// ValidateMetadataDiscover validates a discovery list and TMDB page bound.
func ValidateMetadataDiscover(input MetadataDiscover) error {
	if !input.List.Valid() || input.Page < 1 || input.Page > MaxMetadataPage {
		return ErrInvalidArgument
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

// ValidateMetadataCredential validates the credential shape required by a provider.
func ValidateMetadataCredential(kind MetadataProviderKind, value string) error {
	if kind != MetadataProviderTMDB || len(value) < MinMetadataCredentialBytes || len(value) > MaxMetadataCredentialBytes {
		return ErrInvalidArgument
	}
	if strings.IndexFunc(value, func(char rune) bool { return unicode.IsSpace(char) || unicode.IsControl(char) }) >= 0 {
		return ErrInvalidArgument
	}
	segments := strings.Split(value, ".")
	if len(segments) != 3 || !strings.HasPrefix(segments[0], "eyJ") {
		return ErrInvalidArgument
	}
	if !validBase64URLSegment(segments[0]) || !validBase64URLSegment(segments[1]) || !validBase64URLSegment(segments[2]) {
		return ErrInvalidArgument
	}
	return nil
}

func validBase64URLSegment(value string) bool {
	if value == "" || strings.Contains(value, "=") {
		return false
	}
	_, err := base64.RawURLEncoding.Strict().DecodeString(value)
	return err == nil
}

// ValidateMetadataTitle validates a normalized provider title.
func ValidateMetadataTitle(title MetadataTitle) error {
	if !title.Kind.Valid() || !title.Provider.Valid() || ValidateProviderID(title.ProviderID) != nil {
		return ErrInvalidArgument
	}
	if !boundedText(title.Title, MaxMetadataTitleBytes) || !boundedOptionalText(title.Overview, MaxMetadataOverviewBytes) ||
		!boundedOptionalText(title.PosterPath, MaxMetadataPosterPathBytes) ||
		!boundedOptionalText(title.BackdropPath, MaxMetadataBackdropPathBytes) || title.Year < 0 || title.Year > 9999 {
		return ErrInvalidArgument
	}
	return nil
}

// ValidateMetadataGenres validates a bounded provider genre list.
func ValidateMetadataGenres(genres []MetadataGenre) error {
	if len(genres) > 100 {
		return ErrInvalidArgument
	}
	seen := make(map[int]struct{}, len(genres))
	for _, genre := range genres {
		if genre.ID <= 0 || !boundedText(genre.Name, MaxMetadataGenreNameBytes) {
			return ErrInvalidArgument
		}
		if _, exists := seen[genre.ID]; exists {
			return ErrInvalidArgument
		}
		seen[genre.ID] = struct{}{}
	}
	return nil
}

// MetadataKey returns the stable request lookup key for a title.
func MetadataKey(title MetadataTitle) MetadataTitleKey {
	return MetadataTitleKey{Kind: title.Kind, Provider: title.Provider, ProviderID: title.ProviderID}
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
