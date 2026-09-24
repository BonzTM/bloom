// Package sonarr implements Bloom's Sonarr download-manager adapter.
package sonarr

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"time"

	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/downloadmanager/arr"
	sonarrapi "github.com/BonzTM/bloom/internal/downloadmanager/sonarr/api"
)

// Config contains one Sonarr connection and its bounded HTTP policy.
type Config struct {
	BaseURL       string
	APIKey        string
	AllowInsecure bool
	CallTimeout   time.Duration
	HTTPClient    *http.Client
	Observer      arr.Observer
}

// Client adapts Sonarr's API to the download-manager seam.
type Client struct {
	http *arr.Client
}

var _ core.DownloadManagerAdapter = (*Client)(nil)

// New constructs a validated Sonarr adapter.
func New(cfg Config) (*Client, error) {
	baseURL, err := core.ValidateMediaServerURL(cfg.BaseURL, cfg.AllowInsecure)
	if err != nil {
		return nil, fmt.Errorf("sonarr client config: %w", err)
	}
	httpClient, err := arr.New(arr.Config{
		Kind: core.DownloadManagerKindSonarr, BaseURL: baseURL, APIKey: cfg.APIKey,
		Timeout: cfg.CallTimeout, HTTPClient: cfg.HTTPClient, Observer: cfg.Observer,
	})
	if err != nil {
		return nil, fmt.Errorf("sonarr client config: %w", err)
	}
	return &Client{http: httpClient}, nil
}

// Probe verifies credentials and returns instance capabilities and options.
func (c *Client) Probe(ctx context.Context) (core.DownloadManagerInfo, error) {
	var status sonarrapi.SystemResource
	if err := c.http.GetJSON(ctx, "probe", "/api/v3/system/status", &status); err != nil {
		return core.DownloadManagerInfo{}, err
	}
	options, err := c.options(ctx)
	if err != nil {
		return core.DownloadManagerInfo{}, err
	}
	info := core.DownloadManagerInfo{
		Name: firstText(status.InstanceName, status.AppName), Version: text(status.Version),
		Capabilities: core.DownloadManagerCapabilities{Kinds: []core.MediaKind{core.MediaKindSeries}}, Options: options,
	}
	if err := core.ValidateDownloadManagerInfo(info); err != nil {
		return core.DownloadManagerInfo{}, managerError("probe", core.DownloadManagerMalformed, err)
	}
	return info, nil
}

func (c *Client) options(ctx context.Context) (core.DownloadManagerOptions, error) {
	var profiles []sonarrapi.QualityProfileResource
	if err := c.http.GetJSON(ctx, "quality_profiles", "/api/v3/qualityprofile", &profiles); err != nil {
		return core.DownloadManagerOptions{}, err
	}
	var roots []sonarrapi.RootFolderResource
	if err := c.http.GetJSON(ctx, "root_folders", "/api/v3/rootfolder", &roots); err != nil {
		return core.DownloadManagerOptions{}, err
	}
	var tags []sonarrapi.TagResource
	if err := c.http.GetJSON(ctx, "tags", "/api/v3/tag", &tags); err != nil {
		return core.DownloadManagerOptions{}, err
	}
	return mapOptions(profiles, roots, tags)
}

// Add resolves TMDB through Sonarr, then adopts or creates the series.
func (c *Client) Add(
	ctx context.Context, title core.DownloadTitle, options core.DownloadOptions,
) (string, error) {
	if err := core.ValidateDownloadTitle(title); err != nil || title.Kind != core.MediaKindSeries {
		return "", core.ErrInvalidArgument
	}
	tmdbID, qualityID, tags, err := parseOptions(title.ProviderID, options)
	if err != nil {
		return "", err
	}
	series, err := c.lookup(ctx, tmdbID)
	if err != nil {
		return "", err
	}
	existing, err := c.find(ctx, *series.TvdbId)
	if err != nil {
		return "", err
	}
	if existing != nil {
		newSeasons := configureSeries(existing, qualityID, options.RootFolder, tags, title.Seasons, true)
		if err := c.searchSeasons(ctx, *existing.Id, newSeasons); err != nil {
			return "", err
		}
		if err := c.http.PutJSON(ctx, "monitor_seasons", "/api/v3/series/"+strconv.FormatInt(int64(*existing.Id), 10), *existing, existing); err != nil {
			return "", err
		}
		return strconv.FormatInt(int64(*existing.Id), 10), nil
	}
	configureSeries(&series, qualityID, options.RootFolder, tags, title.Seasons, false)
	var added sonarrapi.SeriesResource
	if err := c.http.PostJSON(ctx, "add", "/api/v3/series", series, &added); err != nil {
		return "", err
	}
	if added.Id == nil || *added.Id <= 0 {
		return "", managerError("add", core.DownloadManagerMalformed, errors.New("missing series id"))
	}
	return strconv.FormatInt(int64(*added.Id), 10), nil
}

func (c *Client) searchSeasons(ctx context.Context, seriesID int32, seasons []int) error {
	for _, season := range seasons {
		command := map[string]any{
			"name": "SeasonSearch", "seriesId": seriesID, "seasonNumber": season,
		}
		var response map[string]any
		if err := c.http.PostJSON(ctx, "search_season", "/api/v3/command", command, &response); err != nil {
			return err
		}
	}
	return nil
}

func (c *Client) lookup(ctx context.Context, tmdbID int32) (sonarrapi.SeriesResource, error) {
	var values []sonarrapi.SeriesResource
	term := url.QueryEscape("tmdb:" + strconv.FormatInt(int64(tmdbID), 10))
	if err := c.http.GetJSON(ctx, "lookup", "/api/v3/series/lookup?term="+term, &values); err != nil {
		return sonarrapi.SeriesResource{}, err
	}
	for _, value := range values {
		if value.TmdbId != nil && *value.TmdbId == tmdbID && value.TvdbId != nil && *value.TvdbId > 0 {
			return value, nil
		}
	}
	return sonarrapi.SeriesResource{}, managerError("lookup", core.DownloadManagerNotFound, errors.New("series not found"))
}

func (c *Client) find(ctx context.Context, tvdbID int32) (*sonarrapi.SeriesResource, error) {
	var values []sonarrapi.SeriesResource
	path := "/api/v3/series?tvdbId=" + strconv.FormatInt(int64(tvdbID), 10)
	if err := c.http.GetJSON(ctx, "find", path, &values); err != nil {
		return nil, err
	}
	for index := range values {
		if values[index].TvdbId != nil && *values[index].TvdbId == tvdbID && values[index].Id != nil && *values[index].Id > 0 {
			return &values[index], nil
		}
	}
	return nil, nil
}

// Queue reads live queue progress and falls back to the series file state.
func (c *Client) Queue(ctx context.Context, managerID string, seasons []int) (core.DownloadProgress, error) {
	id, err := parsePositiveInt32(managerID)
	if err != nil {
		return core.DownloadProgress{}, err
	}
	var queue sonarrapi.QueueResourcePagingResource
	path := "/api/v3/queue?page=1&pageSize=100&includeSeries=true&seriesIds=" + url.QueryEscape(managerID)
	if requestErr := c.http.GetJSON(ctx, "queue", path, &queue); requestErr != nil {
		return core.DownloadProgress{}, requestErr
	}
	progress := core.DownloadProgress{Status: "not_queued"}
	if queue.Records != nil {
		for _, item := range *queue.Records {
			if item.SeriesId != nil && *item.SeriesId == id {
				progress = queueProgress(item)
				break
			}
		}
	}
	var series sonarrapi.SeriesResource
	if requestErr := c.http.GetJSON(ctx, "series", "/api/v3/series/"+managerID, &series); requestErr != nil {
		return core.DownloadProgress{}, requestErr
	}
	hasFile, err := requestedSeasonsHaveFiles(series, seasons)
	if err != nil {
		return progress, err
	}
	progress.HasFile = hasFile
	if progress.Status == "not_queued" {
		progress.Complete = hasFile
	}
	return progress, nil
}

// CloseIdleConnections releases pooled connections.
func (c *Client) CloseIdleConnections() { c.http.CloseIdleConnections() }

func configureSeries(
	series *sonarrapi.SeriesResource, qualityID int32, root string, tags []int32, seasons []int, preserve bool,
) []int {
	monitored, search := true, true
	newSeasons := make([]int, 0, len(seasons))
	series.QualityProfileId = &qualityID
	series.RootFolderPath = &root
	series.Tags = &tags
	series.Monitored = &monitored
	if series.Seasons != nil {
		for index := range *series.Seasons {
			season := &(*series.Seasons)[index]
			selected := season.SeasonNumber != nil && containsSeason(seasons, int(*season.SeasonNumber))
			wasMonitored := season.Monitored != nil && *season.Monitored
			if selected && !wasMonitored {
				newSeasons = append(newSeasons, int(*season.SeasonNumber))
			}
			value := selected || preserve && wasMonitored
			season.Monitored = &value
		}
	}
	if preserve {
		series.AddOptions = nil
	} else {
		monitor := sonarrapi.MonitorTypes("none")
		series.AddOptions = &sonarrapi.AddSeriesOptions{Monitor: &monitor, SearchForMissingEpisodes: &search}
	}
	return newSeasons
}

func containsSeason(values []int, target int) bool {
	return slices.Contains(values, target)
}

func parseOptions(providerID string, options core.DownloadOptions) (int32, int32, []int32, error) {
	tmdbID, err := parsePositiveInt32(providerID)
	if err != nil {
		return 0, 0, nil, err
	}
	qualityID, err := core.ParseDownloadOptionID(options.QualityProfile)
	if err != nil || options.RootFolder == "" {
		return 0, 0, nil, core.ErrInvalidArgument
	}
	tags := make([]int32, 0, len(options.Tags))
	for _, value := range options.Tags {
		id, parseErr := core.ParseDownloadOptionID(value)
		if parseErr != nil {
			return 0, 0, nil, parseErr
		}
		tags = append(tags, id)
	}
	return tmdbID, qualityID, tags, nil
}

func parsePositiveInt32(value string) (int32, error) {
	parsed, err := strconv.ParseInt(value, 10, 32)
	if err != nil || parsed <= 0 {
		return 0, core.ErrInvalidArgument
	}
	return int32(parsed), nil
}

func queueProgress(item sonarrapi.QueueResource) core.DownloadProgress {
	size, left := boundedSize(item.Size), boundedSize(item.Sizeleft) //nolint:staticcheck // Sonarr's v3 wire field remains sizeleft.
	return core.DownloadProgress{
		Status: textEnum(item.Status), Size: size, SizeLeft: left,
		EstimatedCompletion: item.EstimatedCompletionTime, Complete: left == 0 && size > 0,
	}
}

func requestedSeasonsHaveFiles(series sonarrapi.SeriesResource, requested []int) (bool, error) {
	if len(requested) == 0 {
		return false, core.ErrInvalidArgument
	}
	if series.Seasons == nil {
		return false, managerError("series", core.DownloadManagerMalformed, errors.New("missing seasons"))
	}
	for _, number := range requested {
		statistics, found := requestedSeasonStatistics(*series.Seasons, number)
		if !found || statistics == nil || statistics.EpisodeCount == nil || statistics.EpisodeFileCount == nil {
			return false, managerError("series", core.DownloadManagerMalformed, errors.New("missing requested season statistics"))
		}
		if !seasonStatisticsComplete(statistics) {
			return false, nil
		}
	}
	return true, nil
}

func requestedSeasonStatistics(
	seasons []sonarrapi.SeasonResource, requested int,
) (*sonarrapi.SeasonStatisticsResource, bool) {
	for _, season := range seasons {
		if season.SeasonNumber != nil && int(*season.SeasonNumber) == requested {
			return season.Statistics, true
		}
	}
	return nil, false
}

func seasonStatisticsComplete(statistics *sonarrapi.SeasonStatisticsResource) bool {
	return statistics != nil && statistics.EpisodeCount != nil && statistics.EpisodeFileCount != nil &&
		*statistics.EpisodeCount > 0 && *statistics.EpisodeFileCount >= *statistics.EpisodeCount
}

func boundedSize(value *float64) int64 {
	if value == nil || *value <= 0 {
		return 0
	}
	if *value >= math.MaxInt64 {
		return math.MaxInt64
	}
	return int64(*value)
}

func mapOptions(
	profiles []sonarrapi.QualityProfileResource, roots []sonarrapi.RootFolderResource, tags []sonarrapi.TagResource,
) (core.DownloadManagerOptions, error) {
	result := core.DownloadManagerOptions{
		QualityProfiles: make([]core.DownloadManagerOption, 0, len(profiles)),
		RootFolders:     make([]core.DownloadManagerOption, 0, len(roots)),
		Tags:            make([]core.DownloadManagerOption, 0, len(tags)),
	}
	for _, value := range profiles {
		if value.Id == nil || *value.Id <= 0 || value.Name == nil || *value.Name == "" {
			return result, managerError("quality_profiles", core.DownloadManagerMalformed, errors.New("invalid quality profile"))
		}
		result.QualityProfiles = append(result.QualityProfiles, option(*value.Id, *value.Name))
	}
	for _, value := range roots {
		if value.Path == nil || *value.Path == "" {
			return result, managerError("root_folders", core.DownloadManagerMalformed, errors.New("invalid root folder"))
		}
		result.RootFolders = append(result.RootFolders, core.DownloadManagerOption{ID: *value.Path, Name: *value.Path})
	}
	for _, value := range tags {
		if value.Id == nil || *value.Id <= 0 || value.Label == nil || *value.Label == "" {
			return result, managerError("tags", core.DownloadManagerMalformed, errors.New("invalid tag"))
		}
		result.Tags = append(result.Tags, option(*value.Id, *value.Label))
	}
	return result, nil
}

func option(id int32, name string) core.DownloadManagerOption {
	return core.DownloadManagerOption{ID: strconv.FormatInt(int64(id), 10), Name: name}
}

func text(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func firstText(values ...*string) string {
	for _, value := range values {
		if text(value) != "" {
			return text(value)
		}
	}
	return ""
}

func textEnum[T ~string](value *T) string {
	if value == nil {
		return ""
	}
	return string(*value)
}

func managerError(operation string, kind core.DownloadManagerErrorKind, err error) error {
	return &core.DownloadManagerError{Kind: kind, Operation: operation, Err: err}
}
