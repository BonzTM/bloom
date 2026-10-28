package tmdb

// The appended detail of one TMDB movie or series response. Every field
// tolerates null; lists are bounded when mapped, not here.
type rawDetail struct {
	Tagline          string                `json:"tagline"`
	Status           string                `json:"status"`
	Runtime          int                   `json:"runtime"`
	EpisodeRunTime   []int                 `json:"episode_run_time"`
	VoteAverage      float64               `json:"vote_average"`
	VoteCount        int                   `json:"vote_count"`
	OriginalLanguage string                `json:"original_language"`
	IMDbID           string                `json:"imdb_id"`
	Budget           int64                 `json:"budget"`
	Revenue          int64                 `json:"revenue"`
	Genres           []rawNamed            `json:"genres"`
	Countries        []rawCountry          `json:"production_countries"`
	Companies        []rawCompany          `json:"production_companies"`
	Networks         []rawCompany          `json:"networks"`
	CreatedBy        []rawPerson           `json:"created_by"`
	FirstAirDate     string                `json:"first_air_date"`
	LastAirDate      string                `json:"last_air_date"`
	NextEpisode      *rawEpisode           `json:"next_episode_to_air"`
	SeasonCount      int                   `json:"number_of_seasons"`
	EpisodeCount     int                   `json:"number_of_episodes"`
	InProduction     bool                  `json:"in_production"`
	Credits          rawCredits            `json:"credits"`
	Keywords         rawKeywords           `json:"keywords"`
	ReleaseDates     rawResults[rawRegion] `json:"release_dates"`
	ContentRatings   rawResults[rawRating] `json:"content_ratings"`
	Recommendations  rawResults[rawTitle]  `json:"recommendations"`
	Similar          rawResults[rawTitle]  `json:"similar"`
	Videos           rawResults[rawVideo]  `json:"videos"`
	WatchProviders   rawWatchProviders     `json:"watch/providers"`
	ExternalIDs      rawExternalIDs        `json:"external_ids"`
}

type rawResults[T any] struct {
	Results []T `json:"results"`
}

type rawNamed struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type rawCountry struct {
	Code string `json:"iso_3166_1"`
	Name string `json:"name"`
}

type rawCompany struct {
	ID       int    `json:"id"`
	Name     string `json:"name"`
	LogoPath string `json:"logo_path"`
}

type rawPerson struct {
	ID          int    `json:"id"`
	Name        string `json:"name"`
	Character   string `json:"character"`
	Job         string `json:"job"`
	ProfilePath string `json:"profile_path"`
}

type rawCredits struct {
	Cast []rawPerson `json:"cast"`
	Crew []rawPerson `json:"crew"`
}

// A movie's keywords arrive under "keywords", a series' under "results".
type rawKeywords struct {
	Keywords []rawNamed `json:"keywords"`
	Results  []rawNamed `json:"results"`
}

type rawEpisode struct {
	Season  int    `json:"season_number"`
	Episode int    `json:"episode_number"`
	Name    string `json:"name"`
	AirDate string `json:"air_date"`
}

type rawRegion struct {
	Code     string           `json:"iso_3166_1"`
	Releases []rawReleaseDate `json:"release_dates"`
}

type rawReleaseDate struct {
	Certification string `json:"certification"`
	Date          string `json:"release_date"`
	Type          int    `json:"type"`
}

type rawRating struct {
	Code   string `json:"iso_3166_1"`
	Rating string `json:"rating"`
}

type rawVideo struct {
	Key      string `json:"key"`
	Site     string `json:"site"`
	Type     string `json:"type"`
	Official bool   `json:"official"`
}

type rawWatchProviders struct {
	Results map[string]rawRegionProviders `json:"results"`
}

type rawRegionProviders struct {
	Flatrate []rawProvider `json:"flatrate"`
	Free     []rawProvider `json:"free"`
	Ads      []rawProvider `json:"ads"`
}

type rawProvider struct {
	ID       int    `json:"provider_id"`
	Name     string `json:"provider_name"`
	LogoPath string `json:"logo_path"`
}

type rawExternalIDs struct {
	IMDbID string `json:"imdb_id"`
}
