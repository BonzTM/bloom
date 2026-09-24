package core

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const (
	// DownloadManagerKindRadarr accepts movie requests.
	DownloadManagerKindRadarr DownloadManagerKind = "radarr"
	// DownloadManagerKindSonarr accepts series requests.
	DownloadManagerKindSonarr DownloadManagerKind = "sonarr"
	// MaxDownloadManagerOptions bounds each option collection returned by a manager.
	MaxDownloadManagerOptions = 256
)

var (
	// ErrDownloadManagerInUse prevents deleting a manager referenced by a profile.
	ErrDownloadManagerInUse = errors.New("download manager is in use")
	// ErrDownloadItemMissing reports that no manager item was recorded for a request.
	ErrDownloadItemMissing = errors.New("download manager item is not recorded")
)

// DownloadManagerKind is a supported external manager family.
type DownloadManagerKind string

// Valid reports whether k belongs to the closed manager-kind set.
func (k DownloadManagerKind) Valid() bool {
	return k == DownloadManagerKindRadarr || k == DownloadManagerKindSonarr
}

// Handles reports whether k accepts the media kind.
func (k DownloadManagerKind) Handles(kind MediaKind) bool {
	return k == DownloadManagerKindRadarr && kind == MediaKindMovie ||
		k == DownloadManagerKindSonarr && kind == MediaKindSeries
}

// DownloadManager is the public, credential-free registration.
type DownloadManager struct {
	ID            string
	Kind          DownloadManagerKind
	Name          string
	BaseURL       string
	AllowInsecure bool
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// DownloadManagerRecord is the encrypted persistence representation.
type DownloadManagerRecord struct {
	DownloadManager
	CredentialCiphertext []byte
	KeyID                string
}

// DownloadManagerCapabilities describes the media kinds an instance handles.
type DownloadManagerCapabilities struct {
	Kinds []MediaKind
}

// DownloadManagerOption is an external identifier and display name.
type DownloadManagerOption struct {
	ID   string
	Name string
}

// DownloadManagerOptions contains current profile configuration choices.
type DownloadManagerOptions struct {
	QualityProfiles []DownloadManagerOption
	RootFolders     []DownloadManagerOption
	Tags            []DownloadManagerOption
}

// DownloadManagerInfo is returned by a successful instance probe.
type DownloadManagerInfo struct {
	Name         string
	Version      string
	Capabilities DownloadManagerCapabilities
	Options      DownloadManagerOptions
}

// DownloadManagerConnection combines a saved registration and probe result.
type DownloadManagerConnection struct {
	Manager DownloadManager
	Info    DownloadManagerInfo
}

// DownloadTitle is the provider identity and requested series selection sent to an adapter.
type DownloadTitle struct {
	Kind       MediaKind
	ProviderID string
	Title      string
	Year       int
	Seasons    []int
}

// DownloadOptions selects the profile, destination, and tags for one dispatch.
type DownloadOptions struct {
	QualityProfile string
	RootFolder     string
	Tags           []string
}

// DownloadProgress is a bounded live queue snapshot.
type DownloadProgress struct {
	Status              string
	Size                int64
	SizeLeft            int64
	EstimatedCompletion *time.Time
	Complete            bool
	HasFile             bool
}

// DownloadManagerAdapter is the consumer-owned external manager seam.
type DownloadManagerAdapter interface {
	Probe(ctx context.Context) (DownloadManagerInfo, error)
	Add(ctx context.Context, title DownloadTitle, options DownloadOptions) (string, error)
	Queue(ctx context.Context, managerID string, seasons []int) (DownloadProgress, error)
}

// DownloadManagerReader retrieves encrypted registrations and public pages.
type DownloadManagerReader interface {
	GetDownloadManager(ctx context.Context, id string) (DownloadManagerRecord, error)
	GetDownloadManagerByName(ctx context.Context, name string) (DownloadManagerRecord, error)
	ListDownloadManagers(ctx context.Context, afterNameKey string, pageSize int) ([]DownloadManager, error)
}

// DownloadManagerWriter creates and removes registrations.
type DownloadManagerWriter interface {
	CreateDownloadManager(ctx context.Context, manager DownloadManagerRecord) error
	DeleteDownloadManager(ctx context.Context, id string) error
}

// DownloadManagerErrorKind classifies safe external failure categories.
type DownloadManagerErrorKind string

const (
	// DownloadManagerUnauthorized reports rejected credentials.
	DownloadManagerUnauthorized DownloadManagerErrorKind = "unauthorized"
	// DownloadManagerNotFound reports a missing external resource.
	DownloadManagerNotFound DownloadManagerErrorKind = "not_found"
	// DownloadManagerUnavailable reports a retryable external failure.
	DownloadManagerUnavailable DownloadManagerErrorKind = "unavailable"
	// DownloadManagerMalformed reports an unexpected response or status.
	DownloadManagerMalformed DownloadManagerErrorKind = "malformed"
)

// DownloadManagerError carries safe classification without response bodies or credentials.
type DownloadManagerError struct {
	Kind       DownloadManagerErrorKind
	Operation  string
	Retryable  bool
	RetryAfter time.Duration
	Err        error
}

func (e *DownloadManagerError) Error() string {
	return "download manager " + e.Operation + ": " + string(e.Kind)
}

func (e *DownloadManagerError) Unwrap() error { return e.Err }

// Transient reports whether a bounded retry may succeed.
func (e *DownloadManagerError) Transient() bool { return e.Retryable }

// ValidateDownloadManager validates a public registration.
func ValidateDownloadManager(manager DownloadManager) error {
	if !ValidID(manager.ID) || !manager.Kind.Valid() {
		return ErrInvalidArgument
	}
	if err := ValidateMediaServerName(manager.Name); err != nil {
		return err
	}
	_, err := ValidateMediaServerURL(manager.BaseURL, manager.AllowInsecure)
	return err
}

// ValidateDownloadTitle validates a dispatch title and season selection.
func ValidateDownloadTitle(title DownloadTitle) error {
	if !title.Kind.Valid() || ValidateProviderID(title.ProviderID) != nil ||
		!boundedText(title.Title, MaxMetadataTitleBytes) || title.Year < 0 || title.Year > 9999 {
		return ErrInvalidArgument
	}
	if title.Kind == MediaKindMovie && len(title.Seasons) != 0 {
		return ErrInvalidArgument
	}
	if title.Kind == MediaKindSeries {
		return ValidateSeasonNumbers(title.Seasons)
	}
	return nil
}

// ParseDownloadOptionID parses a positive external numeric option identifier.
func ParseDownloadOptionID(value string) (int32, error) {
	parsed, err := strconv.ParseInt(value, 10, 32)
	if err != nil || parsed <= 0 {
		return 0, fmt.Errorf("download manager option id: %w", ErrInvalidArgument)
	}
	return int32(parsed), nil
}

// ValidateDownloadManagerInfo validates a bounded probe response.
func ValidateDownloadManagerInfo(info DownloadManagerInfo) error {
	if strings.TrimSpace(info.Name) == "" || strings.TrimSpace(info.Version) == "" || len(info.Capabilities.Kinds) != 1 {
		return ErrInvalidArgument
	}
	if len(info.Options.QualityProfiles) > MaxDownloadManagerOptions ||
		len(info.Options.RootFolders) > MaxDownloadManagerOptions || len(info.Options.Tags) > MaxDownloadManagerOptions {
		return ErrInvalidArgument
	}
	return nil
}
