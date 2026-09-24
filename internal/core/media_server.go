package core

import (
	"context"
	"fmt"
	"net/netip"
	"net/url"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

const (
	// MediaServerKindJellyfin is the first supported media-server adapter.
	MediaServerKindJellyfin MediaServerKind = "jellyfin"
	// MaxMediaServerNameBytes bounds operator-supplied display names.
	MaxMediaServerNameBytes = 100
	// MaxMediaServerNameKeyBytes bounds the normalized comparison key.
	MaxMediaServerNameKeyBytes = MaxMediaServerNameBytes * 3
	// MaxMediaServerBaseURLBytes bounds an operator-supplied destination.
	MaxMediaServerBaseURLBytes = 2048
	// MaxMediaServerLibraries bounds the unpaginated Jellyfin library response.
	MaxMediaServerLibraries = 256
	// MaxLibraryIDBytes matches the persisted invite and watch library identifier bound.
	MaxLibraryIDBytes = 128
	// MaxLibraryNameBytes bounds an upstream library display name before persistence.
	MaxLibraryNameBytes = 500
)

// MediaServerKind is a closed adapter identifier.
type MediaServerKind string

// Valid reports whether the kind belongs to the compiled registry.
func (k MediaServerKind) Valid() bool { return k == MediaServerKindJellyfin }

// MediaServer is the non-secret persisted server configuration.
type MediaServer struct {
	ID      string
	Kind    MediaServerKind
	Name    string
	BaseURL string
	// AllowInsecure records the operator's explicit acceptance of plaintext HTTP.
	AllowInsecure bool
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// MediaServerRecord is the storage-only value that includes encrypted credentials.
type MediaServerRecord struct {
	MediaServer
	CredentialCiphertext []byte
}

// ServerInfo is returned by a successful connectivity probe.
type ServerInfo struct {
	Name    string
	Version string
	ID      string
}

// Library is one media-server library visible to the configured credential.
type Library struct {
	ID   string
	Name string
	Type string
}

// Valid reports whether the library is safe to persist on both database engines.
func (l Library) Valid() bool {
	return ValidLibraryID(l.ID) && l.Name != "" && len(l.Name) <= MaxLibraryNameBytes &&
		utf8.ValidString(l.Name) && strings.IndexFunc(l.Name, unicode.IsControl) < 0
}

// ValidLibraryID reports whether an upstream library identifier is safe to persist.
func ValidLibraryID(value string) bool {
	return value != "" && len(value) <= MaxLibraryIDBytes && utf8.ValidString(value) &&
		strings.IndexFunc(value, unicode.IsControl) < 0
}

// Capabilities describes optional operations supported by an adapter kind.
type Capabilities struct {
	CreateUserWithPassword bool
	SetPassword            bool
	QuickConnectApproval   bool
	ProviderIDLookup       bool
}

// MediaServerConnection joins persisted configuration with probed adapter data.
type MediaServerConnection struct {
	Server       MediaServer
	Info         ServerInfo
	Capabilities Capabilities
}

// MediaServerAdapter is the consumer-owned external-server seam for this slice.
type MediaServerAdapter interface {
	Probe(ctx context.Context) (ServerInfo, error)
	ListLibraries(ctx context.Context) ([]Library, error)
	Capabilities() Capabilities
}

// MediaAvailabilityLookup is implemented by adapters that can find titles by provider id.
type MediaAvailabilityLookup interface {
	HasTitle(ctx context.Context, kind MediaKind, provider MetadataProviderKind, providerID string, seasons []int) (bool, []int, error)
}

// LibraryResolver is an optional adapter capability for mapping an item to its collection folder.
type LibraryResolver interface {
	ResolveLibrary(ctx context.Context, itemID string) (Library, bool, error)
}

// MediaServerReader reads registered server configuration.
type MediaServerReader interface {
	GetMediaServer(ctx context.Context, id string) (MediaServerRecord, error)
	ListMediaServers(ctx context.Context, afterNameKey string, pageSize int) ([]MediaServer, error)
}

// MediaServerWriter changes registered server configuration.
type MediaServerWriter interface {
	CreateMediaServer(ctx context.Context, server MediaServerRecord) error
	DeleteMediaServer(ctx context.Context, id string) error
}

// MediaServerErrorKind is a closed classification for adapter failures.
type MediaServerErrorKind string

const (
	// MediaServerUnauthorized means the configured credential was rejected.
	MediaServerUnauthorized MediaServerErrorKind = "unauthorized"
	// MediaServerNotFound means the requested upstream resource does not exist.
	MediaServerNotFound MediaServerErrorKind = "not_found"
	// MediaServerUnavailable means an upstream availability failure occurred.
	MediaServerUnavailable MediaServerErrorKind = "unavailable"
	// MediaServerMalformed means the upstream response violated its contract.
	MediaServerMalformed MediaServerErrorKind = "malformed"
	// MediaServerSaturated means this server's outbound concurrency limit is full.
	MediaServerSaturated MediaServerErrorKind = "saturated"
)

// MediaServerError carries safe classification without upstream response data.
type MediaServerError struct {
	Kind       MediaServerErrorKind
	Operation  string
	Retryable  bool
	RetryAfter time.Duration
	Err        error
}

func (e *MediaServerError) Error() string {
	return "media server " + e.Operation + ": " + string(e.Kind)
}
func (e *MediaServerError) Unwrap() error { return e.Err }

// Transient reports whether retrying after a bounded delay may succeed.
func (e *MediaServerError) Transient() bool { return e.Retryable }

// ValidateMediaServerURL validates and normalizes an operator-supplied base URL.
func ValidateMediaServerURL(raw string, allowInsecure bool) (string, error) {
	if raw == "" || len(raw) > MaxMediaServerBaseURLBytes || !utf8.ValidString(raw) || raw != strings.TrimSpace(raw) {
		return "", fmt.Errorf("media server URL: %w", ErrInvalidArgument)
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || parsed.Hostname() == "" {
		return "", fmt.Errorf("media server URL: %w", ErrInvalidArgument)
	}
	if (parsed.Scheme == "https" && allowInsecure) ||
		(parsed.Scheme == "http" && !allowInsecure) ||
		(parsed.Scheme != "https" && parsed.Scheme != "http") {
		return "", fmt.Errorf("media server URL: %w", ErrInvalidArgument)
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fmt.Errorf("media server URL: %w", ErrInvalidArgument)
	}
	if address, addressErr := netip.ParseAddr(parsed.Hostname()); addressErr == nil && !MediaServerAddressAllowed(address) {
		return "", fmt.Errorf("media server URL: %w", ErrInvalidArgument)
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	return parsed.String(), nil
}

// MediaServerAddressAllowed rejects destinations that are never valid media servers.
// Private ranges remain allowed because self-hosted servers commonly use them.
// Scoped IPv6 addresses are rejected outright: a zone is never part of a
// server URL. The check runs before Unmap: Unmap keeps the zone of a native
// IPv6 address but drops it from an IPv4-mapped one, so checking afterwards
// would let a scoped mapped literal through.
func MediaServerAddressAllowed(address netip.Addr) bool {
	if !address.IsValid() || address.Zone() != "" {
		return false
	}
	address = address.Unmap()
	if address.IsUnspecified() || address.IsLoopback() || address.IsMulticast() {
		return false
	}
	if address.IsLinkLocalUnicast() || address.IsLinkLocalMulticast() {
		return false
	}
	return address != netip.MustParseAddr("169.254.169.254") &&
		address != netip.MustParseAddr("fd00:ec2::254")
}

// ValidateMediaServerName validates an operator-visible unique name.
func ValidateMediaServerName(name string) error {
	if name == "" || len(name) > MaxMediaServerNameBytes || !utf8.ValidString(name) || name != strings.TrimSpace(name) {
		return ErrInvalidArgument
	}
	if strings.IndexFunc(name, unicode.IsControl) >= 0 {
		return ErrInvalidArgument
	}
	return nil
}

// MediaServerNameKey returns the engine-independent comparison and sort key.
func MediaServerNameKey(name string) string {
	return norm.NFC.String(cases.Fold().String(name))
}
