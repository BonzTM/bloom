package tmdb

import (
	"slices"
	"strings"
	"time"

	"github.com/BonzTM/bloom/internal/core"
)

const (
	maxStatusBytes        = 100
	maxCertificationBytes = 20
	maxRuntimeMinutes     = 10_000
	maxVideoKeyBytes      = 32
	maxIMDbDigits         = 12
	maxSeasonNumber       = 999
	maxEpisodeNumber      = 99_999
)

// keyCrewJobs are the crew jobs a title page shows, most important first.
var keyCrewJobs = []string{
	"Director", "Screenplay", "Writer", "Story", "Novel", "Characters",
	"Original Music Composer", "Director of Photography", "Editor", "Producer",
}

// releaseTypes names TMDB's numbered release types.
var releaseTypes = map[int]core.MetadataReleaseType{
	1: core.MetadataReleasePremiere, 2: core.MetadataReleaseTheatricalLimited, 3: core.MetadataReleaseTheatrical,
	4: core.MetadataReleaseDigital, 5: core.MetadataReleasePhysical, 6: core.MetadataReleaseTV,
}

// mapDetails bounds and validates every appended field. It returns the
// number of items it dropped as malformed.
func mapDetails(raw rawDetail, kind core.MediaKind) (core.MetadataDetails, int) {
	details := mapFacts(raw, kind)
	dropped := 0
	var count int
	details.Genres, count = mapBounded(raw.Genres, core.MaxMetadataGenres, genreName)
	dropped += count
	details.Keywords, count = mapBounded(slices.Concat(raw.Keywords.Keywords, raw.Keywords.Results), core.MaxMetadataKeywords, keywordName)
	dropped += count
	details.Countries, count = mapBounded(raw.Countries, core.MaxMetadataCountries, mapCountry)
	dropped += count
	details.Studios, count = mapBounded(raw.Companies, core.MaxMetadataCompanies, mapCompany)
	dropped += count
	details.Cast, count = mapBounded(raw.Credits.Cast, core.MaxMetadataCast, castCredit)
	dropped += count
	details.Crew = keyCrew(raw.Credits.Crew)
	details.WatchProviders, count = watchProviders(raw.WatchProviders)
	dropped += count
	details.Recommendations, count = mapBounded(raw.Recommendations.Results, core.MaxMetadataRelated, relatedTitle(kind))
	dropped += count
	details.Similar, count = mapBounded(raw.Similar.Results, core.MaxMetadataRelated, relatedTitle(kind))
	dropped += count
	if kind == core.MediaKindSeries {
		details.Series, count = mapSeriesFacts(raw)
		dropped += count
	}
	return details, dropped
}

// mapFacts maps the scalar facts and the region-specific release detail.
func mapFacts(raw rawDetail, kind core.MediaKind) core.MetadataDetails {
	details := core.MetadataDetails{
		Tagline: optionalText(raw.Tagline, core.MaxMetadataShortTextBytes), Status: optionalText(raw.Status, maxStatusBytes),
		RuntimeMinutes: runtimeMinutes(raw), VoteAverage: voteAverage(raw.VoteAverage), VoteCount: max(raw.VoteCount, 0),
		OriginalLanguage: languageCode(raw.OriginalLanguage), IMDbID: imdbID(raw.IMDbID, raw.ExternalIDs.IMDbID),
		TrailerKey: trailerKey(raw.Videos.Results),
	}
	if kind == core.MediaKindSeries {
		details.Certification = seriesCertification(raw.ContentRatings.Results)
		return details
	}
	details.Budget, details.Revenue = max(raw.Budget, 0), max(raw.Revenue, 0)
	region := regionReleases(raw.ReleaseDates.Results)
	details.Certification = movieCertification(region)
	details.ReleaseDates = releaseDates(region)
	return details
}

func mapSeriesFacts(raw rawDetail) (core.MetadataSeriesFacts, int) {
	facts := core.MetadataSeriesFacts{
		FirstAirDate: parseDate(raw.FirstAirDate), LastAirDate: parseDate(raw.LastAirDate),
		NextEpisode: nextEpisode(raw.NextEpisode), SeasonCount: min(max(raw.SeasonCount, 0), maxSeasonNumber),
		EpisodeCount: min(max(raw.EpisodeCount, 0), maxEpisodeNumber), InProduction: raw.InProduction,
	}
	var creators, networks int
	facts.Creators, creators = mapBounded(raw.CreatedBy, core.MaxMetadataCreators, creatorCredit)
	facts.Networks, networks = mapBounded(raw.Networks, core.MaxMetadataCompanies, mapCompany)
	return facts, creators + networks
}

// mapBounded maps at most limit valid items and counts the invalid items it
// dropped on the way.
func mapBounded[R, T any](items []R, limit int, mapItem func(R) (T, bool)) ([]T, int) {
	mapped := make([]T, 0, min(len(items), limit))
	dropped := 0
	for _, item := range items {
		if len(mapped) == limit {
			break
		}
		value, ok := mapItem(item)
		if !ok {
			dropped++
			continue
		}
		mapped = append(mapped, value)
	}
	return mapped, dropped
}

func genreName(raw rawNamed) (string, bool) {
	name := strings.TrimSpace(raw.Name)
	return name, core.MetadataTextValid(name, core.MaxMetadataGenreNameBytes)
}

func keywordName(raw rawNamed) (string, bool) {
	name := strings.TrimSpace(raw.Name)
	return name, core.MetadataTextValid(name, core.MaxMetadataShortTextBytes)
}

func mapCountry(raw rawCountry) (core.MetadataCountry, bool) {
	name := strings.TrimSpace(raw.Name)
	ok := countryCode(raw.Code) && core.MetadataTextValid(name, core.MaxMetadataShortTextBytes)
	return core.MetadataCountry{Code: raw.Code, Name: name}, ok
}

func mapCompany(raw rawCompany) (core.MetadataCompany, bool) {
	name := strings.TrimSpace(raw.Name)
	ok := raw.ID > 0 && core.MetadataTextValid(name, core.MaxMetadataShortTextBytes)
	return core.MetadataCompany{ID: raw.ID, Name: name, LogoPath: imagePath(raw.LogoPath)}, ok
}

func castCredit(raw rawPerson) (core.MetadataCredit, bool) {
	return personCredit(raw, optionalText(raw.Character, core.MaxMetadataShortTextBytes))
}

func creatorCredit(raw rawPerson) (core.MetadataCredit, bool) {
	return personCredit(raw, "Creator")
}

func personCredit(raw rawPerson, role string) (core.MetadataCredit, bool) {
	name := strings.TrimSpace(raw.Name)
	ok := raw.ID > 0 && core.MetadataTextValid(name, core.MaxMetadataShortTextBytes)
	return core.MetadataCredit{PersonID: raw.ID, Name: name, Role: role, ProfilePath: imagePath(raw.ProfilePath)}, ok
}

// keyCrew picks the crew a title page names, by job importance and then in
// TMDB's order, once per person and job.
func keyCrew(crew []rawPerson) []core.MetadataCredit {
	picked := make([]core.MetadataCredit, 0, core.MaxMetadataKeyCrew)
	for _, job := range keyCrewJobs {
		for _, member := range crew {
			if len(picked) == core.MaxMetadataKeyCrew {
				return picked
			}
			if member.Job != job {
				continue
			}
			credit, ok := personCredit(member, job)
			if !ok || slices.ContainsFunc(picked, func(seen core.MetadataCredit) bool {
				return seen.PersonID == credit.PersonID && seen.Role == job
			}) {
				continue
			}
			picked = append(picked, credit)
		}
	}
	return picked
}

func relatedTitle(parent core.MediaKind) func(rawTitle) (core.MetadataDiscoverItem, bool) {
	return func(raw rawTitle) (core.MetadataDiscoverItem, bool) {
		kind := parent
		if raw.MediaType != nil {
			var known bool
			if kind, known = resultKind(*raw.MediaType); !known {
				return core.MetadataDiscoverItem{}, false
			}
		}
		title, ok := mapSearchTitle(raw, kind)
		return core.MetadataDiscoverItem{MetadataTitle: title, RequestState: core.MetadataRequestNone}, ok
	}
}

func watchProviders(raw rawWatchProviders) ([]core.MetadataWatchProvider, int) {
	region := raw.Results[core.MetadataDetailRegion]
	all := slices.Concat(region.Flatrate, region.Free, region.Ads)
	seen := make(map[int]struct{}, len(all))
	unique := make([]rawProvider, 0, len(all))
	for _, provider := range all {
		if _, duplicate := seen[provider.ID]; duplicate {
			continue
		}
		seen[provider.ID] = struct{}{}
		unique = append(unique, provider)
	}
	return mapBounded(unique, core.MaxMetadataWatchProviders, mapProvider)
}

func mapProvider(raw rawProvider) (core.MetadataWatchProvider, bool) {
	name := strings.TrimSpace(raw.Name)
	ok := raw.ID > 0 && core.MetadataTextValid(name, core.MaxMetadataShortTextBytes)
	return core.MetadataWatchProvider{ID: raw.ID, Name: name, LogoPath: imagePath(raw.LogoPath)}, ok
}

func nextEpisode(raw *rawEpisode) *core.MetadataEpisode {
	if raw == nil || raw.Season < 0 || raw.Season > maxSeasonNumber || raw.Episode < 0 || raw.Episode > maxEpisodeNumber {
		return nil
	}
	return &core.MetadataEpisode{
		Season: raw.Season, Episode: raw.Episode, Name: optionalText(raw.Name, core.MaxMetadataShortTextBytes),
		AirDate: parseDate(raw.AirDate),
	}
}

func regionReleases(regions []rawRegion) []rawReleaseDate {
	for _, region := range regions {
		if region.Code == core.MetadataDetailRegion {
			return region.Releases
		}
	}
	return nil
}

// movieCertification prefers the theatrical rating, then any rating.
func movieCertification(releases []rawReleaseDate) string {
	fallback := ""
	for _, release := range releases {
		rating := optionalText(release.Certification, maxCertificationBytes)
		if rating != "" && releaseTypes[release.Type] == core.MetadataReleaseTheatrical {
			return rating
		}
		if fallback == "" {
			fallback = rating
		}
	}
	return fallback
}

func seriesCertification(ratings []rawRating) string {
	for _, rating := range ratings {
		if rating.Code == core.MetadataDetailRegion {
			return optionalText(rating.Rating, maxCertificationBytes)
		}
	}
	return ""
}

// releaseDates keeps the first release of each type, in date order.
func releaseDates(releases []rawReleaseDate) []core.MetadataReleaseDate {
	first := make(map[core.MetadataReleaseType]time.Time, len(releaseTypes))
	for _, release := range releases {
		kind, known := releaseTypes[release.Type]
		date, err := time.Parse(time.RFC3339, release.Date)
		if !known || err != nil {
			continue
		}
		if existing, seen := first[kind]; !seen || date.Before(existing) {
			first[kind] = date.UTC()
		}
	}
	dates := make([]core.MetadataReleaseDate, 0, len(first))
	for kind, date := range first {
		dates = append(dates, core.MetadataReleaseDate{Type: kind, Date: date})
	}
	slices.SortFunc(dates, func(a, b core.MetadataReleaseDate) int {
		if order := a.Date.Compare(b.Date); order != 0 {
			return order
		}
		return strings.Compare(string(a.Type), string(b.Type))
	})
	return dates
}

// trailerKey prefers an official YouTube trailer, then any YouTube trailer,
// then a YouTube teaser.
func trailerKey(videos []rawVideo) string {
	best, bestRank := "", 0
	for _, video := range videos {
		rank := videoRank(video)
		if rank > bestRank && youTubeKey(video.Key) {
			best, bestRank = video.Key, rank
		}
	}
	return best
}

func videoRank(video rawVideo) int {
	switch {
	case video.Site != "YouTube":
		return 0
	case video.Type == "Trailer" && video.Official:
		return 3
	case video.Type == "Trailer":
		return 2
	case video.Type == "Teaser":
		return 1
	default:
		return 0
	}
}

func runtimeMinutes(raw rawDetail) int {
	runtime := raw.Runtime
	if runtime <= 0 {
		for _, episode := range raw.EpisodeRunTime {
			if episode > 0 {
				runtime = episode
				break
			}
		}
	}
	if runtime < 0 || runtime > maxRuntimeMinutes {
		return 0
	}
	return runtime
}

func voteAverage(value float64) float64 {
	if value < 0 || value > 10 {
		return 0
	}
	return value
}

func optionalText(value string, maximum int) string {
	trimmed := strings.TrimSpace(value)
	if !core.MetadataTextValid(trimmed, maximum) {
		return ""
	}
	return trimmed
}

// imagePath keeps a TMDB image path the SPA can append to the image host.
func imagePath(value string) string {
	if !strings.HasPrefix(value, "/") || strings.Contains(value, "..") ||
		!core.MetadataTextValid(value, core.MaxMetadataShortTextBytes) {
		return ""
	}
	return value
}

func imdbID(values ...string) string {
	for _, value := range values {
		digits, found := strings.CutPrefix(value, "tt")
		if found && digits != "" && len(digits) <= maxIMDbDigits && allBytes(digits, isDigit) {
			return value
		}
	}
	return ""
}

func youTubeKey(value string) bool {
	return value != "" && len(value) <= maxVideoKeyBytes && allBytes(value, func(char byte) bool {
		return isDigit(char) || isLetter(char) || char == '-' || char == '_'
	})
}

func languageCode(value string) string {
	if len(value) < 2 || len(value) > 3 || !allBytes(value, func(char byte) bool { return char >= 'a' && char <= 'z' }) {
		return ""
	}
	return value
}

func countryCode(value string) bool {
	return len(value) == 2 && allBytes(value, func(char byte) bool { return char >= 'A' && char <= 'Z' })
}

func allBytes(value string, accept func(byte) bool) bool {
	for index := range len(value) {
		if !accept(value[index]) {
			return false
		}
	}
	return true
}

func isDigit(char byte) bool { return char >= '0' && char <= '9' }

func isLetter(char byte) bool { return char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' }
