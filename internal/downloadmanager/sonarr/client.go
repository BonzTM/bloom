// Package sonarr implements Bloom's Sonarr download-manager adapter.
package sonarr

import (
	"context"
	"encoding/json"
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

type existingSeries struct {
	id  int32
	raw json.RawMessage
}

const maxSeriesResults = 1024

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
		return c.reconcileSeries(ctx, existing, tags, title.Seasons)
	}
	configureNewSeries(&series, qualityID, options.RootFolder, tags, title.Seasons)
	var added sonarrapi.SeriesResource
	if err := c.http.PostJSON(ctx, "add", "/api/v3/series", series, &added); err != nil {
		return "", err
	}
	if added.Id == nil || *added.Id <= 0 {
		return "", managerError("add", core.DownloadManagerMalformed, errors.New("missing series id"))
	}
	return strconv.FormatInt(int64(*added.Id), 10), nil
}

func (c *Client) reconcileSeries(
	ctx context.Context, existing *existingSeries, tags []int32, requested []int,
) (string, error) {
	id, err := existingSeriesID(existing)
	if err != nil {
		return "", managerError("monitor_seasons", core.DownloadManagerMalformed, err)
	}
	raw, err := c.completeSeriesRaw(ctx, id, existing.raw)
	if err != nil {
		return "", err
	}
	merged, err := mergeExistingSeries(raw, tags, requested)
	if err != nil {
		return "", classifySeriesMergeError(err)
	}
	path := "/api/v3/series/" + strconv.FormatInt(int64(id), 10)
	response, err := c.http.PutRawJSON(ctx, "monitor_seasons", path, merged.body)
	if err != nil {
		return "", err
	}
	responseID, err := decodeSeriesResponseID(response)
	if err != nil {
		return "", managerError("monitor_seasons", core.DownloadManagerMalformed, err)
	}
	if err := validateSeriesResponseID(id, responseID); err != nil {
		return "", managerError("monitor_seasons", core.DownloadManagerMalformed, err)
	}
	if err := c.searchSeasons(ctx, id, merged.searchSeasons); err != nil {
		return "", err
	}
	return strconv.FormatInt(int64(id), 10), nil
}

func (c *Client) completeSeriesRaw(
	ctx context.Context, id int32, raw json.RawMessage,
) (json.RawMessage, error) {
	present, err := hasSeasonCollection(raw)
	if err != nil {
		return nil, managerError("monitor_seasons", core.DownloadManagerMalformed, err)
	}
	if present {
		return raw, nil
	}
	path := "/api/v3/series/" + strconv.FormatInt(int64(id), 10)
	detailRaw, err := c.http.GetRawJSON(ctx, "series", path)
	if err != nil {
		return nil, err
	}
	if _, decodeErr := hasSeasonCollection(detailRaw); decodeErr != nil {
		return nil, managerError("series", core.DownloadManagerMalformed, decodeErr)
	}
	detailID, err := decodeSeriesResponseID(detailRaw)
	if err != nil {
		return nil, managerError("series", core.DownloadManagerMalformed, err)
	}
	if err := validateSeriesResponseID(id, detailID); err != nil {
		return nil, managerError("series", core.DownloadManagerMalformed, err)
	}
	return detailRaw, nil
}

func existingSeriesID(existing *existingSeries) (int32, error) {
	if existing == nil || existing.id <= 0 {
		return 0, errors.New("missing series id")
	}
	return existing.id, nil
}

func validateSeriesResponseID(expected int32, actual *int32) error {
	if actual != nil && *actual != expected {
		return errors.New("series id changed in response")
	}
	return nil
}

func decodeSeriesResponseID(raw json.RawMessage) (*int32, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	object, err := arr.DecodeObject(raw)
	if err != nil {
		return nil, err
	}
	id, present, err := arr.DecodeOptionalInt32(object, "id")
	if err != nil || !present {
		return nil, err
	}
	return &id, nil
}

func classifySeriesMergeError(err error) error {
	if _, found := errors.AsType[*missingSeasonsError](err); found {
		return managerError("monitor_seasons", core.DownloadManagerNotFound, err)
	}
	return managerError("monitor_seasons", core.DownloadManagerMalformed, err)
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

func (c *Client) find(ctx context.Context, tvdbID int32) (*existingSeries, error) {
	path := "/api/v3/series?tvdbId=" + strconv.FormatInt(int64(tvdbID), 10)
	raw, err := c.http.GetRawJSON(ctx, "find", path)
	if err != nil {
		return nil, err
	}
	existing, err := findExistingSeries(raw, tvdbID)
	if err != nil {
		return nil, managerError("find", core.DownloadManagerMalformed, err)
	}
	return existing, nil
}

func findExistingSeries(raw json.RawMessage, tvdbID int32) (*existingSeries, error) {
	values, err := arr.DecodeArray(raw, maxSeriesResults, "series")
	if err != nil {
		return nil, err
	}
	for _, value := range values {
		object, decodeErr := arr.DecodeObject(value)
		if decodeErr != nil {
			return nil, decodeErr
		}
		candidate, matched, decodeErr := matchExistingSeries(object, value, tvdbID)
		if decodeErr != nil {
			return nil, decodeErr
		}
		if matched {
			return candidate, nil
		}
	}
	return nil, nil
}

func matchExistingSeries(
	object map[string]json.RawMessage, raw json.RawMessage, tvdbID int32,
) (*existingSeries, bool, error) {
	candidateTVDBID, present, err := arr.DecodeOptionalInt32(object, "tvdbId")
	if err != nil || !present || candidateTVDBID != tvdbID {
		return nil, false, err
	}
	id, present, err := arr.DecodeOptionalInt32(object, "id")
	if err != nil {
		return nil, false, err
	}
	if !present || id <= 0 {
		return nil, false, errors.New("matched series has no positive id")
	}
	return &existingSeries{id: id, raw: raw}, true, nil
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

func configureNewSeries(
	series *sonarrapi.SeriesResource, qualityID int32, root string, tags []int32, seasons []int,
) {
	monitored, search := true, true
	series.QualityProfileId = &qualityID
	series.RootFolderPath = &root
	series.Tags = &tags
	series.Monitored = &monitored
	if series.Seasons != nil {
		for index := range *series.Seasons {
			season := &(*series.Seasons)[index]
			selected := season.SeasonNumber != nil && containsSeason(seasons, int(*season.SeasonNumber))
			value := selected
			season.Monitored = &value
		}
	}
	monitor := sonarrapi.MonitorTypes("none")
	series.AddOptions = &sonarrapi.AddSeriesOptions{Monitor: &monitor, SearchForMissingEpisodes: &search}
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
