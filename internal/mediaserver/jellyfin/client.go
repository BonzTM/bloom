// Package jellyfin implements Bloom's Jellyfin media-server adapter.
package jellyfin

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/rand/v2"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"

	openapi_types "github.com/oapi-codegen/runtime/types"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"

	"github.com/BonzTM/bloom/internal/core"
	jellyfinapi "github.com/BonzTM/bloom/internal/mediaserver/jellyfin/api"
)

const (
	maxResponseBytes            = 1 << 20
	maxUsers                    = 10000
	defaultTimeout              = 10 * time.Second
	sessionsActiveWithinSeconds = 60
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

var (
	_ core.MediaServerAdapter      = (*Client)(nil)
	_ core.MediaUserProvisioner    = (*Client)(nil)
	_ core.MediaAvailabilityLookup = (*Client)(nil)
)

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

// CreateUser creates one Jellyfin user. POST /Users/New is not idempotent and
// is deliberately attempted once even when the response is unavailable.
func (c *Client) CreateUser(ctx context.Context, name, password string) (core.MediaUser, error) {
	request := jellyfinapi.CreateUserByName{Name: name, Password: &password}
	body, err := json.Marshal(request) //nolint:gosec // Required one-use upstream credential body; cleared below.
	if err != nil {
		return core.MediaUser{}, mediaError("create_user", core.MediaServerMalformed, err)
	}
	defer clear(body)
	response, started, err := c.doOnce(ctx, "create_user", http.MethodPost, "/Users/New", body)
	if err != nil {
		return c.reconcileAmbiguousCreate(ctx, name, err)
	}
	var dto jellyfinapi.UserDto
	if err := json.Unmarshal(response, &dto); err != nil || dto.Id == nil || dto.Name == nil || *dto.Name == "" {
		c.observe("create_user", "malformed", started)
		malformed := mediaError("create_user", core.MediaServerMalformed, errors.New("missing user identity"))
		return c.reconcileAmbiguousCreate(ctx, name, malformed)
	}
	c.observe("create_user", "success", started)
	return core.MediaUser{ID: dto.Id.String(), Name: *dto.Name}, nil
}

func (c *Client) reconcileAmbiguousCreate(ctx context.Context, name string, createErr error) (core.MediaUser, error) {
	if !ambiguousCreateError(createErr) {
		return core.MediaUser{}, createErr
	}
	lookupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), c.callTimeout)
	defer cancel()
	user, found, err := c.findUserByName(lookupCtx, name)
	if err != nil {
		return core.MediaUser{}, errors.Join(core.ErrMediaUserCreateAmbiguous, createErr, err)
	}
	if found {
		return user, errors.Join(core.ErrMediaUserCreateAmbiguous, createErr)
	}
	return core.MediaUser{}, createErr
}

func ambiguousCreateError(err error) bool {
	var mediaErr *core.MediaServerError
	return errors.As(err, &mediaErr) &&
		(mediaErr.Kind == core.MediaServerUnavailable || mediaErr.Kind == core.MediaServerMalformed)
}

func (c *Client) findUserByName(ctx context.Context, name string) (core.MediaUser, bool, error) {
	var users []jellyfinapi.UserDto
	started, err := c.getJSON(ctx, "list_users", "/Users", &users)
	if err != nil {
		return core.MediaUser{}, false, err
	}
	if len(users) > maxUsers {
		c.observe("list_users", "malformed", started)
		return core.MediaUser{}, false, mediaError("list_users", core.MediaServerMalformed, errors.New("user count exceeds limit"))
	}
	for _, user := range users {
		if user.Name == nil || *user.Name != name {
			continue
		}
		if user.Id == nil || !core.ValidID(user.Id.String()) {
			c.observe("list_users", "malformed", started)
			return core.MediaUser{}, false, mediaError("list_users", core.MediaServerMalformed, errors.New("missing user identity"))
		}
		c.observe("list_users", "success", started)
		return core.MediaUser{ID: user.Id.String(), Name: *user.Name}, true, nil
	}
	c.observe("list_users", "success", started)
	return core.MediaUser{}, false, nil
}

// SetLibraryAccess reads the current whole policy, changes only folder access,
// and posts the complete policy because Jellyfin treats policy updates as replace.
func (c *Client) SetLibraryAccess(ctx context.Context, userID string, libraryIDs []string, all bool) error {
	if !core.ValidID(userID) {
		return core.ErrInvalidArgument
	}
	var dto jellyfinapi.UserDto
	started, err := c.getJSON(ctx, "get_user_policy", "/Users/"+url.PathEscape(userID), &dto)
	if err != nil {
		return err
	}
	if dto.Policy == nil {
		c.observe("get_user_policy", "malformed", started)
		return mediaError("get_user_policy", core.MediaServerMalformed, errors.New("missing user policy"))
	}
	c.observe("get_user_policy", "success", started)
	folders, err := jellyfinUUIDs(libraryIDs)
	if err != nil {
		return err
	}
	dto.Policy.EnableAllFolders = &all
	dto.Policy.EnabledFolders = &folders
	body, err := json.Marshal(dto.Policy)
	if err != nil {
		return mediaError("set_library_access", core.MediaServerMalformed, err)
	}
	_, started, err = c.doWithRetry(ctx, "set_library_access", http.MethodPost,
		"/Users/"+url.PathEscape(userID)+"/Policy", body)
	if err == nil {
		c.observe("set_library_access", "success", started)
	}
	return err
}

// DeleteUser removes a Jellyfin user and may be retried because deletion is idempotent.
func (c *Client) DeleteUser(ctx context.Context, userID string) error {
	if !core.ValidID(userID) {
		return core.ErrInvalidArgument
	}
	_, started, err := c.doWithRetry(ctx, "delete_user", http.MethodDelete, "/Users/"+url.PathEscape(userID), nil)
	if isMediaNotFound(err) {
		return nil
	}
	if err == nil {
		c.observe("delete_user", "success", started)
	}
	return err
}

func isMediaNotFound(err error) bool {
	var mediaErr *core.MediaServerError
	return errors.As(err, &mediaErr) && mediaErr.Kind == core.MediaServerNotFound
}

func jellyfinUUIDs(values []string) ([]openapi_types.UUID, error) {
	result := make([]openapi_types.UUID, 0, len(values))
	for _, value := range values {
		parsed, err := jellyfinUUID(value)
		if err != nil {
			return nil, mediaError("set_library_access", core.MediaServerMalformed, err)
		}
		result = append(result, parsed)
	}
	return result, nil
}

func jellyfinUUID(value string) (openapi_types.UUID, error) {
	if !core.ValidID(value) {
		return openapi_types.UUID{}, core.ErrInvalidArgument
	}
	var parsed openapi_types.UUID
	compact := strings.ReplaceAll(value, "-", "")
	if _, err := hex.Decode(parsed[:], []byte(compact)); err != nil {
		return openapi_types.UUID{}, fmt.Errorf("decode Jellyfin UUID: %w", err)
	}
	return parsed, nil
}

// ListSessions returns active Jellyfin sessions that have a now-playing item.
func (c *Client) ListSessions(ctx context.Context) ([]core.PlaybackSession, error) {
	var dto []jellyfinapi.SessionInfoDto
	path := "/Sessions?activeWithinSeconds=" + strconv.Itoa(sessionsActiveWithinSeconds)
	started, err := c.getJSON(ctx, "list_sessions", path, &dto)
	if err != nil {
		return nil, err
	}
	sessions, err := mapSessions(dto)
	if err != nil {
		c.observe("list_sessions", "malformed", started)
		return nil, err
	}
	c.observe("list_sessions", "success", started)
	return sessions, nil
}

// Capabilities returns Jellyfin's known optional-operation support.
func (*Client) Capabilities() core.Capabilities {
	return core.Capabilities{CreateUserWithPassword: true, SetPassword: true, QuickConnectApproval: true, ProviderIDLookup: true}
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

func mapSessions(values []jellyfinapi.SessionInfoDto) ([]core.PlaybackSession, error) {
	if len(values) > core.MaxPlaybackSessions {
		return nil, mediaError("list_sessions", core.MediaServerMalformed, errors.New("session count exceeds limit"))
	}
	sessions := make([]core.PlaybackSession, 0, len(values))
	for _, value := range values {
		if value.NowPlayingItem == nil {
			continue
		}
		session, err := mapSession(value)
		if err != nil {
			return nil, mediaError("list_sessions", core.MediaServerMalformed, err)
		}
		sessions = append(sessions, session)
	}
	return sessions, nil
}

func mapSession(value jellyfinapi.SessionInfoDto) (core.PlaybackSession, error) {
	item := value.NowPlayingItem
	if value.UserId == nil || value.DeviceId == nil || *value.DeviceId == "" ||
		item.Id == nil || item.Name == nil || *item.Name == "" || item.Type == nil ||
		value.PlayState == nil || value.LastActivityDate == nil {
		return core.PlaybackSession{}, errors.New("session is missing playback identity")
	}
	position, err := positionFromTicks(value.PlayState.PositionTicks)
	if err != nil {
		return core.PlaybackSession{}, err
	}
	return core.PlaybackSession{
		ServerSessionID: stringValue(value.Id), MediaUserID: value.UserId.String(),
		Username: stringValue(value.UserName), DeviceID: *value.DeviceId,
		DeviceName: stringValue(value.DeviceName), Client: stringValue(value.Client),
		ItemID: item.Id.String(), ItemName: *item.Name, ItemType: string(*item.Type),
		SeriesName: stringValue(item.SeriesName), SeasonNumber: cloneInt32(item.ParentIndexNumber),
		EpisodeNumber: cloneInt32(item.IndexNumber), Position: position,
		Paused: boolValue(value.PlayState.IsPaused), PlayMethod: mapPlayMethod(value.PlayState.PlayMethod),
		LastActivityAt: core.NormalizeTime(*value.LastActivityDate),
	}, nil
}

func positionFromTicks(ticks *int64) (time.Duration, error) {
	if ticks == nil {
		return 0, nil
	}
	if *ticks < 0 || *ticks > math.MaxInt64/100 {
		return 0, errors.New("session position ticks are out of range")
	}
	return time.Duration(*ticks) * 100 * time.Nanosecond, nil
}

func mapPlayMethod(value *jellyfinapi.PlayMethod) core.PlayMethod {
	if value == nil {
		return core.PlayMethodUnknown
	}
	switch string(*value) {
	case "DirectPlay":
		return core.PlayMethodDirectPlay
	case "DirectStream":
		return core.PlayMethodDirectStream
	case "Transcode":
		return core.PlayMethodTranscode
	default:
		return core.PlayMethodUnknown
	}
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func boolValue(value *bool) bool { return value != nil && *value }

func cloneInt32(value *int32) *int32 {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
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
