// Package jellyfin implements Bloom's Jellyfin media-server adapter.
package jellyfin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"

	"github.com/BonzTM/bloom/internal/core"
	jellyfinapi "github.com/BonzTM/bloom/internal/mediaserver/jellyfin/api"
)

const (
	maxResponseBytes = 1 << 20
	defaultTimeout   = 10 * time.Second
)

// Observer records outbound request and retry outcomes with finite labels.
type Observer interface {
	ObserveMediaServerRequest(kind, operation, outcome string, seconds float64)
	ObserveMediaServerRetry(kind, operation, outcome string)
}

// Config is the closed Jellyfin client configuration.
type Config struct {
	BaseURL       string
	APIKey        string
	Version       string
	DeviceID      string
	AllowInsecure bool
	CallTimeout   time.Duration
	HTTPClient    *http.Client
	Observer      Observer
}

// Client is a reusable, concurrency-safe Jellyfin API client.
type Client struct {
	baseURL       string
	authorization string
	httpClient    *http.Client
	observer      Observer
	callTimeout   time.Duration
	sleep         func(context.Context, time.Duration) error
	randomInt64N  func(int64) int64
	now           func() time.Time
}

var _ core.MediaServerAdapter = (*Client)(nil)

// New validates cfg and returns a bounded Jellyfin client.
func New(cfg Config) (*Client, error) {
	baseURL, err := core.ValidateMediaServerURL(cfg.BaseURL, cfg.AllowInsecure)
	if err != nil || !validConfig(cfg) {
		return nil, fmt.Errorf("jellyfin client config: %w", core.ErrInvalidArgument)
	}
	if cfg.Version == "" {
		cfg.Version = "dev"
	}
	if cfg.CallTimeout <= 0 {
		cfg.CallTimeout = defaultTimeout
	}
	httpClient := configuredHTTPClient(cfg.HTTPClient, cfg.CallTimeout)
	return &Client{
		baseURL: baseURL, authorization: authorizationHeader(cfg), httpClient: httpClient,
		observer: cfg.Observer, callTimeout: cfg.CallTimeout,
		sleep: sleepContext, randomInt64N: rand.Int64N, now: time.Now,
	}, nil
}

func validConfig(cfg Config) bool {
	return cfg.APIKey != "" && validHeaderValue(cfg.APIKey) &&
		validHeaderValue(cfg.Version) && core.ValidID(cfg.DeviceID)
}

func configuredHTTPClient(input *http.Client, timeout time.Duration) *http.Client {
	if input == nil {
		input = newHTTPClient(timeout)
	}
	client := *input
	transport := client.Transport
	if transport == nil {
		transport = newSafeTransport()
	}
	client.Transport = otelhttp.NewTransport(transport)
	client.Timeout = timeout
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &client
}

func authorizationHeader(cfg Config) string {
	return `MediaBrowser Client="` + encodeHeaderValue("bloom") +
		`", Device="` + encodeHeaderValue("bloom-server") +
		`", DeviceId="` + encodeHeaderValue(cfg.DeviceID) +
		`", Version="` + encodeHeaderValue(cfg.Version) +
		`", Token="` + encodeHeaderValue(cfg.APIKey) + `"`
}

func encodeHeaderValue(value string) string {
	return strings.ReplaceAll(url.QueryEscape(value), "+", "%20")
}

func validHeaderValue(value string) bool {
	return strings.IndexFunc(value, unicode.IsControl) < 0
}

// Probe verifies the credential and returns the Jellyfin server identity.
func (c *Client) Probe(ctx context.Context) (core.ServerInfo, error) {
	var dto jellyfinapi.SystemInfo
	started, err := c.getJSON(ctx, "probe", "/System/Info", &dto)
	if err != nil {
		return core.ServerInfo{}, err
	}
	if dto.ServerName == nil || dto.Version == nil || dto.Id == nil ||
		*dto.ServerName == "" || *dto.Version == "" || *dto.Id == "" {
		c.observe("probe", "malformed", started)
		return core.ServerInfo{}, mediaError("probe", core.MediaServerMalformed, errors.New("missing server identity"))
	}
	c.observe("probe", "success", started)
	return core.ServerInfo{Name: *dto.ServerName, Version: *dto.Version, ID: *dto.Id}, nil
}

// ListLibraries returns at most 256 configured Jellyfin virtual folders.
func (c *Client) ListLibraries(ctx context.Context) ([]core.Library, error) {
	var dto []jellyfinapi.VirtualFolderInfo
	started, err := c.getJSON(ctx, "list_libraries", "/Library/VirtualFolders", &dto)
	if err != nil {
		return nil, err
	}
	libraries, err := mapLibraries(dto)
	if err != nil {
		c.observe("list_libraries", "malformed", started)
		return nil, err
	}
	c.observe("list_libraries", "success", started)
	return libraries, nil
}

// Capabilities returns Jellyfin's known optional-operation support.
func (*Client) Capabilities() core.Capabilities {
	return core.Capabilities{CreateUserWithPassword: true, SetPassword: true, QuickConnectApproval: true}
}

// CloseIdleConnections releases pooled HTTP connections owned by this client.
func (c *Client) CloseIdleConnections() { c.httpClient.CloseIdleConnections() }

func mapLibraries(folders []jellyfinapi.VirtualFolderInfo) ([]core.Library, error) {
	if len(folders) > core.MaxMediaServerLibraries {
		return nil, mediaError("list_libraries", core.MediaServerMalformed, errors.New("library count exceeds limit"))
	}
	libraries := make([]core.Library, 0, len(folders))
	for _, folder := range folders {
		if folder.ItemId == nil || folder.Name == nil || *folder.ItemId == "" || *folder.Name == "" {
			return nil, mediaError("list_libraries", core.MediaServerMalformed, errors.New("missing library identity"))
		}
		libraryType := ""
		if folder.CollectionType != nil {
			libraryType = string(*folder.CollectionType)
		}
		libraries = append(libraries, core.Library{ID: *folder.ItemId, Name: *folder.Name, Type: libraryType})
	}
	return libraries, nil
}

func (c *Client) getJSON(ctx context.Context, operation, path string, target any) (time.Time, error) {
	body, started, err := c.getWithRetry(ctx, operation, path)
	if err != nil {
		return time.Time{}, err
	}
	if err := json.Unmarshal(body, target); err != nil {
		c.observe(operation, "malformed", started)
		return time.Time{}, mediaError(operation, core.MediaServerMalformed, err)
	}
	return started, nil
}

func (c *Client) observe(operation, outcome string, started time.Time) {
	if c.observer != nil {
		c.observer.ObserveMediaServerRequest(
			string(core.MediaServerKindJellyfin), operation, outcome, time.Since(started).Seconds(),
		)
	}
}
