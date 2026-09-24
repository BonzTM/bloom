// Package radarr implements Bloom's Radarr download-manager adapter.
package radarr

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/downloadmanager/arr"
	radarrapi "github.com/BonzTM/bloom/internal/downloadmanager/radarr/api"
)

// Config contains one Radarr connection and its bounded HTTP policy.
type Config struct {
	BaseURL       string
	APIKey        string
	AllowInsecure bool
	CallTimeout   time.Duration
	HTTPClient    *http.Client
	Observer      arr.Observer
}

// Client adapts Radarr's API to the download-manager seam.
type Client struct {
	http *arr.Client
}

var _ core.DownloadManagerAdapter = (*Client)(nil)

// New constructs a validated Radarr adapter.
func New(cfg Config) (*Client, error) {
	baseURL, err := core.ValidateMediaServerURL(cfg.BaseURL, cfg.AllowInsecure)
	if err != nil {
		return nil, fmt.Errorf("radarr client config: %w", err)
	}
	httpClient, err := arr.New(arr.Config{
		Kind: core.DownloadManagerKindRadarr, BaseURL: baseURL, APIKey: cfg.APIKey,
		Timeout: cfg.CallTimeout, HTTPClient: cfg.HTTPClient, Observer: cfg.Observer,
	})
	if err != nil {
		return nil, fmt.Errorf("radarr client config: %w", err)
	}
	return &Client{http: httpClient}, nil
}

// Probe verifies credentials and returns instance capabilities and options.
func (c *Client) Probe(ctx context.Context) (core.DownloadManagerInfo, error) {
	var status radarrapi.SystemResource
	if err := c.http.GetJSON(ctx, "probe", "/api/v3/system/status", &status); err != nil {
		return core.DownloadManagerInfo{}, err
	}
	options, err := c.options(ctx)
	if err != nil {
		return core.DownloadManagerInfo{}, err
	}
	info := core.DownloadManagerInfo{
		Name: firstText(status.InstanceName, status.AppName), Version: text(status.Version),
		Capabilities: core.DownloadManagerCapabilities{Kinds: []core.MediaKind{core.MediaKindMovie}}, Options: options,
	}
	if err := core.ValidateDownloadManagerInfo(info); err != nil {
		return core.DownloadManagerInfo{}, malformedError("probe", err)
	}
	return info, nil
}

func (c *Client) options(ctx context.Context) (core.DownloadManagerOptions, error) {
	var profiles []radarrapi.QualityProfileResource
	if err := c.http.GetJSON(ctx, "quality_profiles", "/api/v3/qualityprofile", &profiles); err != nil {
		return core.DownloadManagerOptions{}, err
	}
	var roots []radarrapi.RootFolderResource
	if err := c.http.GetJSON(ctx, "root_folders", "/api/v3/rootfolder", &roots); err != nil {
		return core.DownloadManagerOptions{}, err
	}
	var tags []radarrapi.TagResource
	if err := c.http.GetJSON(ctx, "tags", "/api/v3/tag", &tags); err != nil {
		return core.DownloadManagerOptions{}, err
	}
	return mapOptions(profiles, roots, tags)
}

// Add adopts an existing TMDB match or creates one movie.
func (c *Client) Add(
	ctx context.Context, title core.DownloadTitle, options core.DownloadOptions,
) (string, error) {
	if err := core.ValidateDownloadTitle(title); err != nil || title.Kind != core.MediaKindMovie {
		return "", core.ErrInvalidArgument
	}
	tmdbID, qualityID, tags, err := parseOptions(title.ProviderID, options)
	if err != nil {
		return "", err
	}
	existing, err := c.movies(ctx, tmdbID)
	if err != nil {
		return "", err
	}
	if movie, ok := existingMovie(existing, tmdbID); ok {
		return c.reconcileMovie(ctx, movie, qualityID, options.RootFolder, tags)
	}
	movie, err := c.lookup(ctx, tmdbID)
	if err != nil {
		return "", err
	}
	configureMovie(&movie, qualityID, options.RootFolder, tags, true)
	var added radarrapi.MovieResource
	if err := c.http.PostJSON(ctx, "add", "/api/v3/movie", movie, &added); err != nil {
		return "", err
	}
	if added.Id == nil || *added.Id <= 0 {
		return "", malformedError("add", errors.New("missing movie id"))
	}
	return strconv.FormatInt(int64(*added.Id), 10), nil
}

func (c *Client) reconcileMovie(
	ctx context.Context, movie radarrapi.MovieResource, qualityID int32, root string, tags []int32,
) (string, error) {
	id := *movie.Id
	configureMovie(&movie, qualityID, root, tags, false)
	path := "/api/v3/movie/" + strconv.FormatInt(int64(id), 10)
	if err := c.http.PutJSON(ctx, "reconcile", path, movie, &movie); err != nil {
		return "", err
	}
	if movie.HasFile == nil || !*movie.HasFile {
		command := map[string]any{"name": "MoviesSearch", "movieIds": []int32{id}}
		var response map[string]any
		if err := c.http.PostJSON(ctx, "search", "/api/v3/command", command, &response); err != nil {
			return "", err
		}
	}
	return strconv.FormatInt(int64(id), 10), nil
}

func (c *Client) movies(ctx context.Context, tmdbID int32) ([]radarrapi.MovieResource, error) {
	var values []radarrapi.MovieResource
	path := "/api/v3/movie?tmdbId=" + strconv.FormatInt(int64(tmdbID), 10)
	if err := c.http.GetJSON(ctx, "find", path, &values); err != nil {
		return nil, err
	}
	return values, nil
}

func (c *Client) lookup(ctx context.Context, tmdbID int32) (radarrapi.MovieResource, error) {
	var value radarrapi.MovieResource
	path := "/api/v3/movie/lookup/tmdb?tmdbId=" + strconv.FormatInt(int64(tmdbID), 10)
	if err := c.http.GetJSON(ctx, "lookup", path, &value); err != nil {
		return radarrapi.MovieResource{}, err
	}
	if value.TmdbId == nil || *value.TmdbId != tmdbID {
		return radarrapi.MovieResource{}, malformedError("lookup", errors.New("TMDB id mismatch"))
	}
	return value, nil
}

// Queue reads live queue progress and falls back to the movie's file state.
func (c *Client) Queue(ctx context.Context, managerID string, _ []int) (core.DownloadProgress, error) {
	id, err := parsePositiveInt32(managerID)
	if err != nil {
		return core.DownloadProgress{}, err
	}
	var queue radarrapi.QueueResourcePagingResource
	path := "/api/v3/queue?page=1&pageSize=100&includeMovie=true&movieIds=" + url.QueryEscape(managerID)
	if err := c.http.GetJSON(ctx, "queue", path, &queue); err != nil {
		return core.DownloadProgress{}, err
	}
	progress := core.DownloadProgress{Status: "not_queued"}
	if queue.Records != nil {
		for _, item := range *queue.Records {
			if item.MovieId != nil && *item.MovieId == id {
				progress = queueProgress(item)
				break
			}
		}
	}
	var movie radarrapi.MovieResource
	if err := c.http.GetJSON(ctx, "movie", "/api/v3/movie/"+managerID, &movie); err != nil {
		return core.DownloadProgress{}, err
	}
	hasFile := movie.HasFile != nil && *movie.HasFile
	progress.HasFile = hasFile
	if progress.Status == "not_queued" {
		progress.Complete = hasFile
	}
	return progress, nil
}

// CloseIdleConnections releases pooled connections.
func (c *Client) CloseIdleConnections() { c.http.CloseIdleConnections() }

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

func existingMovie(values []radarrapi.MovieResource, tmdbID int32) (radarrapi.MovieResource, bool) {
	for _, value := range values {
		if value.TmdbId != nil && *value.TmdbId == tmdbID && value.Id != nil && *value.Id > 0 {
			return value, true
		}
	}
	return radarrapi.MovieResource{}, false
}

func configureMovie(movie *radarrapi.MovieResource, qualityID int32, root string, tags []int32, add bool) {
	monitored, search := true, true
	movie.QualityProfileId = &qualityID
	movie.RootFolderPath = &root
	movie.Tags = &tags
	movie.Monitored = &monitored
	if add {
		movie.AddOptions = &radarrapi.AddMovieOptions{SearchForMovie: &search}
	} else {
		movie.AddOptions = nil
	}
}

func queueProgress(item radarrapi.QueueResource) core.DownloadProgress {
	size, left := boundedSize(item.Size), boundedSize(item.Sizeleft) //nolint:staticcheck // Radarr's v3 wire field remains sizeleft.
	status := textEnum(item.Status)
	return core.DownloadProgress{
		Status: status, Size: size, SizeLeft: left, EstimatedCompletion: item.EstimatedCompletionTime,
		Complete: left == 0 && size > 0,
	}
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
	profiles []radarrapi.QualityProfileResource, roots []radarrapi.RootFolderResource, tags []radarrapi.TagResource,
) (core.DownloadManagerOptions, error) {
	result := core.DownloadManagerOptions{
		QualityProfiles: make([]core.DownloadManagerOption, 0, len(profiles)),
		RootFolders:     make([]core.DownloadManagerOption, 0, len(roots)),
		Tags:            make([]core.DownloadManagerOption, 0, len(tags)),
	}
	for _, value := range profiles {
		if value.Id == nil || *value.Id <= 0 || value.Name == nil || *value.Name == "" {
			return result, malformedError("quality_profiles", errors.New("invalid quality profile"))
		}
		result.QualityProfiles = append(result.QualityProfiles, option(*value.Id, *value.Name))
	}
	for _, value := range roots {
		if value.Path == nil || *value.Path == "" {
			return result, malformedError("root_folders", errors.New("invalid root folder"))
		}
		result.RootFolders = append(result.RootFolders, core.DownloadManagerOption{ID: *value.Path, Name: *value.Path})
	}
	for _, value := range tags {
		if value.Id == nil || *value.Id <= 0 || value.Label == nil || *value.Label == "" {
			return result, malformedError("tags", errors.New("invalid tag"))
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

func malformedError(operation string, err error) error {
	return &core.DownloadManagerError{Kind: core.DownloadManagerMalformed, Operation: operation, Err: err}
}
