package omdb

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/BonzTM/bloom/internal/core"
)

const (
	rottenTomatoesSource = "Rotten Tomatoes"
	notAvailable         = "N/A"
	maxIMDbScore         = 10
	maxPercentScore      = 100
)

// rawTitle is the part of an OMDb title answer Bloom reads. OMDb writes
// "N/A" for a value it does not have.
type rawTitle struct {
	Response   string      `json:"Response"`
	Error      string      `json:"Error"`
	IMDbRating string      `json:"imdbRating"`
	IMDbVotes  string      `json:"imdbVotes"`
	Metascore  string      `json:"Metascore"`
	Ratings    []rawRating `json:"Ratings"`
}

type rawRating struct {
	Source string `json:"Source"`
	Value  string `json:"Value"`
}

// answer classifies an OMDb reply. OMDb answers an unknown title with 200
// and Response "False", and a bad key or spent quota with 401.
// It reports found false, with no error, for an unknown title.
func answer(status int, title rawTitle) (bool, error) {
	switch {
	case status == http.StatusOK && title.Response == "True":
		return true, nil
	case status == http.StatusOK && title.Response == "False":
		return false, nil
	case status == http.StatusUnauthorized && strings.Contains(strings.ToLower(title.Error), "limit"):
		return false, statusError(status, core.ErrMetadataUnavailable)
	case status == http.StatusUnauthorized:
		return false, statusError(status, core.ErrMetadataUnauthorized)
	case status == http.StatusTooManyRequests || (status >= http.StatusInternalServerError && status <= 599):
		return false, statusError(status, core.ErrMetadataUnavailable)
	default:
		return false, statusError(status, core.ErrMetadataMalformed)
	}
}

func statusError(status int, err error) error {
	return fmt.Errorf("omdb status %d: %w", status, err)
}

// mapRatings keeps each score that parses within its scale and drops the rest.
func mapRatings(title rawTitle) core.TitleRatings {
	var ratings core.TitleRatings
	if score, ok := parseScore(title.IMDbRating, maxIMDbScore); ok {
		ratings.IMDb = &core.TitleRating{Score: score, Votes: parseVotes(title.IMDbVotes), Source: core.RatingSourceOMDb}
	}
	if score, ok := parseScore(strings.TrimSuffix(rottenTomatoesValue(title.Ratings), "%"), maxPercentScore); ok {
		ratings.RottenTomatoes = &core.TitleRating{Score: score, Source: core.RatingSourceOMDb}
	}
	if score, ok := parseScore(title.Metascore, maxPercentScore); ok {
		ratings.Metacritic = &core.TitleRating{Score: score, Source: core.RatingSourceOMDb}
	}
	return ratings
}

func rottenTomatoesValue(ratings []rawRating) string {
	for _, rating := range ratings {
		if rating.Source == rottenTomatoesSource {
			return rating.Value
		}
	}
	return ""
}

func parseScore(value string, maxScore float64) (float64, bool) {
	if value == "" || value == notAvailable {
		return 0, false
	}
	score, err := strconv.ParseFloat(value, 64)
	rating := core.TitleRating{Score: score, Source: core.RatingSourceOMDb}
	if err != nil || !core.ValidTitleRating(rating, maxScore) {
		return 0, false
	}
	return score, true
}

// parseVotes reads a count such as "2,123,456", or zero when OMDb has none.
func parseVotes(value string) int {
	votes, err := strconv.Atoi(strings.ReplaceAll(value, ",", ""))
	if err != nil || votes < 0 || votes > core.MaxIMDbVotes {
		return 0
	}
	return votes
}
