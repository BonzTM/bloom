package core

import (
	"context"
	"time"
)

// Bounds on the rich detail a title page shows. Each list is cut to its bound
// when the provider sends more, so one cached detail stays small.
const (
	// MaxMetadataCast bounds the cast row.
	MaxMetadataCast = 30
	// MaxMetadataKeyCrew bounds the key crew grid.
	MaxMetadataKeyCrew = 8
	// MaxMetadataCreators bounds a series' creators.
	MaxMetadataCreators = 10
	// MaxMetadataKeywords bounds the keyword chips.
	MaxMetadataKeywords = 40
	// MaxMetadataGenres bounds a title's genres.
	MaxMetadataGenres = 10
	// MaxMetadataCompanies bounds studios and networks.
	MaxMetadataCompanies = 10
	// MaxMetadataCountries bounds production countries.
	MaxMetadataCountries = 10
	// MaxMetadataWatchProviders bounds streaming provider logos.
	MaxMetadataWatchProviders = 12
	// MaxMetadataReleaseDates bounds release dates, one per release type.
	MaxMetadataReleaseDates = 6
	// MaxMetadataRelated bounds the recommendation and similar rows.
	MaxMetadataRelated = MetadataPageSize
	// MaxMetadataShortTextBytes bounds names, roles, keywords, and image paths.
	MaxMetadataShortTextBytes = 500
	// MetadataDetailRegion is the country whose certification, release
	// dates, and streaming providers a title page shows.
	MetadataDetailRegion = "US"
)

// MetadataReleaseType is TMDB's release type, by name.
type MetadataReleaseType string

const (
	// MetadataReleasePremiere is a premiere screening.
	MetadataReleasePremiere MetadataReleaseType = "premiere"
	// MetadataReleaseTheatricalLimited is a limited theatrical run.
	MetadataReleaseTheatricalLimited MetadataReleaseType = "theatrical_limited"
	// MetadataReleaseTheatrical is a wide theatrical run.
	MetadataReleaseTheatrical MetadataReleaseType = "theatrical"
	// MetadataReleaseDigital is a digital release.
	MetadataReleaseDigital MetadataReleaseType = "digital"
	// MetadataReleasePhysical is a disc release.
	MetadataReleasePhysical MetadataReleaseType = "physical"
	// MetadataReleaseTV is a television premiere.
	MetadataReleaseTV MetadataReleaseType = "tv"
)

// MetadataCredit is one credited person: a cast member with the character
// played, or a crew member with the job done.
type MetadataCredit struct {
	PersonID    int
	Name        string
	Role        string
	ProfilePath string
}

// MetadataCompany is a studio or a network.
type MetadataCompany struct {
	ID       int
	Name     string
	LogoPath string
}

// MetadataCountry is an ISO 3166-1 country.
type MetadataCountry struct {
	Code string
	Name string
}

// MetadataReleaseDate is the first release of one type in the detail region.
type MetadataReleaseDate struct {
	Type MetadataReleaseType
	Date time.Time
}

// MetadataWatchProvider is a service that streams a title in the detail region.
type MetadataWatchProvider struct {
	ID       int
	Name     string
	LogoPath string
}

// MetadataEpisode is a series' next or last aired episode.
type MetadataEpisode struct {
	Season  int
	Episode int
	Name    string
	AirDate *time.Time
}

// MetadataSeriesFacts are the facts only a series has.
type MetadataSeriesFacts struct {
	Creators     []MetadataCredit
	Networks     []MetadataCompany
	FirstAirDate *time.Time
	LastAirDate  *time.Time
	NextEpisode  *MetadataEpisode
	SeasonCount  int
	EpisodeCount int
	InProduction bool
}

// MetadataDetails is the rich detail a title page shows beside the title.
// A movie leaves Series empty; a series leaves ReleaseDates, Budget, and
// Revenue empty.
type MetadataDetails struct {
	Tagline          string
	Status           string
	Certification    string
	RuntimeMinutes   int
	VoteAverage      float64
	VoteCount        int
	OriginalLanguage string
	IMDbID           string
	TrailerKey       string
	Budget           int64
	Revenue          int64
	Genres           []string
	Keywords         []string
	Countries        []MetadataCountry
	Studios          []MetadataCompany
	ReleaseDates     []MetadataReleaseDate
	WatchProviders   []MetadataWatchProvider
	Cast             []MetadataCredit
	Crew             []MetadataCredit
	Recommendations  []MetadataDiscoverItem
	Similar          []MetadataDiscoverItem
	Series           MetadataSeriesFacts
}

// MetadataMovieDetail is a movie with its rich detail.
type MetadataMovieDetail struct {
	MetadataTitle
	Details MetadataDetails
}

// MetadataSeriesDetail is a series with its seasons and rich detail.
type MetadataSeriesDetail struct {
	MetadataSeries
	Details MetadataDetails
}

// MetadataDetailProvider retrieves a title with its rich detail in one bounded call.
type MetadataDetailProvider interface {
	MovieDetail(ctx context.Context, providerID string) (MetadataMovieDetail, error)
	SeriesDetail(ctx context.Context, providerID string, includeSpecials bool) (MetadataSeriesDetail, error)
}

// MetadataTextValid reports whether a provider string is non-empty, trimmed,
// printable, valid UTF-8, and at most maximum bytes.
func MetadataTextValid(value string, maximum int) bool {
	return boundedText(value, maximum)
}
