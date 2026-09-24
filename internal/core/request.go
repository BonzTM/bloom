package core

import (
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"
)

const (
	// MaxRequestProfileNameBytes bounds a request profile name.
	MaxRequestProfileNameBytes = 100
	// MaxRequestProfileFieldBytes bounds each downloader placeholder field.
	MaxRequestProfileFieldBytes = 500
	// MaxRequestProfileTags bounds tags in a request profile.
	MaxRequestProfileTags = 32
	// MaxRequestProfileTagBytes bounds each request profile tag.
	MaxRequestProfileTagBytes = 100
	// MaxRequestSeasons bounds one series request.
	MaxRequestSeasons = 100
	// MaxDecisionReasonBytes bounds optional approval or decline text.
	MaxDecisionReasonBytes = 1000
)

var (
	// ErrQuotaExceeded reports that a rolling request quota would be exceeded.
	ErrQuotaExceeded = errors.New("request quota exceeded")
	// ErrInvalidTransition reports a disallowed request state change.
	ErrInvalidTransition = errors.New("invalid request status transition")
	// ErrProfileInUse reports a profile referenced by a request.
	ErrProfileInUse = errors.New("request profile is in use")
)

// RequestStatus is the closed lifecycle state of a media request.
type RequestStatus string

const (
	// RequestPending waits for an approver.
	RequestPending RequestStatus = "pending"
	// RequestApproved may proceed to processing in the next slice.
	RequestApproved RequestStatus = "approved"
	// RequestDeclined is an approver rejection.
	RequestDeclined RequestStatus = "declined"
	// RequestProcessing is reserved for downloader dispatch.
	RequestProcessing RequestStatus = "processing"
	// RequestAvailable indicates completed media availability.
	RequestAvailable RequestStatus = "available"
	// RequestFailed indicates terminal processing failure.
	RequestFailed RequestStatus = "failed"
)

// Valid reports whether the request status belongs to the closed set.
func (s RequestStatus) Valid() bool {
	return slices.Contains([]RequestStatus{RequestPending, RequestApproved, RequestDeclined, RequestProcessing, RequestAvailable, RequestFailed}, s)
}

// SeasonStatus is the closed lifecycle state of a requested series season.
type SeasonStatus string

const (
	// SeasonPending waits for an approver.
	SeasonPending SeasonStatus = "pending"
	// SeasonApproved may proceed to processing in the next slice.
	SeasonApproved SeasonStatus = "approved"
	// SeasonDeclined is an approver rejection.
	SeasonDeclined SeasonStatus = "declined"
	// SeasonProcessing is reserved for downloader dispatch.
	SeasonProcessing SeasonStatus = "processing"
	// SeasonAvailable indicates completed media availability.
	SeasonAvailable SeasonStatus = "available"
	// SeasonFailed indicates terminal processing failure.
	SeasonFailed SeasonStatus = "failed"
)

// RequestProfile selects accepted media kinds and future downloader settings.
type RequestProfile struct {
	ID                      string
	Name                    string
	Kinds                   []MediaKind
	DownloadManagerKind     string
	DownloadManagerInstance string
	QualityProfile          string
	RootFolder              string
	Tags                    []string
	CreatedAt               time.Time
	UpdatedAt               time.Time
}

// MediaRequest is a metadata snapshot and its approval state.
type MediaRequest struct {
	ID                      string
	Kind                    MediaKind
	Provider                MetadataProviderKind
	ProviderID              string
	Title                   string
	Year                    int
	PosterPath              string
	RequesterID             string
	ProfileID               string
	Status                  RequestStatus
	Seasons                 []RequestSeason
	DecisionReason          string
	FailureReason           string
	DownloadManagerID       string
	DownloadManagerItemID   string
	DispatchQualityProfile  string
	DispatchRootFolder      string
	DispatchTags            []string
	DispatchLeaseToken      string
	DispatchLeaseExpiresAt  *time.Time
	LastAvailabilityCheckAt *time.Time
	DecidedBy               string
	DecidedAt               *time.Time
	CreatedAt               time.Time
	UpdatedAt               time.Time
}

// RequestDispatchSnapshot freezes the manager target and options used by one request.
type RequestDispatchSnapshot struct {
	DownloadManagerID string
	QualityProfile    string
	RootFolder        string
	Tags              []string
}

// RequestDispatchLease grants one worker temporary ownership of dispatch.
type RequestDispatchLease struct {
	Token     string
	ExpiresAt time.Time
}

// RequestSeason identifies one requested series season.
type RequestSeason struct {
	Number int
	Status SeasonStatus
}

// RequestCursor identifies a stable newest-first pagination boundary.
type RequestCursor struct {
	CreatedAt time.Time
	ID        string
}

// RequestListFilter bounds and filters request listings.
type RequestListFilter struct {
	RequesterID string
	Status      *RequestStatus
	After       *RequestCursor
	PageSize    int
}

// ScopeRequestList restricts a non-approver to owned requests.
func ScopeRequestList(actorID string, approver bool, filter RequestListFilter) (RequestListFilter, error) {
	if !ValidID(actorID) {
		return RequestListFilter{}, ErrInvalidArgument
	}
	if !approver {
		filter.RequesterID = actorID
	}
	return filter, nil
}

// AuthorizeRequestRead permits approvers and the request owner.
func AuthorizeRequestRead(actorID string, approver bool, request MediaRequest) error {
	if !ValidID(actorID) || !ValidID(request.RequesterID) {
		return ErrInvalidArgument
	}
	if approver || request.RequesterID == actorID {
		return nil
	}
	return ErrNotFound
}

// RequestQuota defines rolling movie and season limits.
type RequestQuota struct {
	MovieLimit   int
	MoviePeriod  time.Duration
	SeasonLimit  int
	SeasonPeriod time.Duration
}

// RequestQuotaUsage is the non-declined usage measured for one quota period.
type RequestQuotaUsage struct {
	Movies  int64
	Seasons int64
}

// RoleRequestQuota attaches a request quota to a role.
type RoleRequestQuota struct {
	RoleID string
	Quota  RequestQuota
}

// AccountRequestQuota overrides role quotas for one account.
type AccountRequestQuota struct {
	AccountID string
	Quota     RequestQuota
}

// RequestEventType is a stable request lifecycle event identifier.
type RequestEventType string

const (
	// RequestEventCreated records request creation.
	RequestEventCreated RequestEventType = "created"
	// RequestEventApproved records request approval, including auto-approval.
	RequestEventApproved RequestEventType = "approved"
	// RequestEventDeclined records request decline.
	RequestEventDeclined RequestEventType = "declined"
	// RequestEventDispatched records successful download-manager dispatch.
	RequestEventDispatched RequestEventType = "dispatched"
	// RequestEventAvailable records availability at the configured source.
	RequestEventAvailable RequestEventType = "available"
	// RequestEventFailed records terminal fulfilment failure.
	RequestEventFailed RequestEventType = "failed"
)

// RequestEvent is the in-process lifecycle payload for later consumers.
type RequestEvent struct {
	Type      RequestEventType
	RequestID string
	ActorID   string
	Kind      MediaKind
	Title     string
	At        time.Time
}

// RequestEventPublisher publishes lifecycle changes to in-process consumers.
type RequestEventPublisher interface {
	PublishRequestEvent(ctx context.Context, event RequestEvent)
}

// RequestProfileReader retrieves request profiles.
type RequestProfileReader interface {
	GetRequestProfile(ctx context.Context, id string) (RequestProfile, error)
	ListRequestProfiles(ctx context.Context, afterName string, pageSize int) ([]RequestProfile, error)
}

// RequestProfileWriter mutates request profiles.
type RequestProfileWriter interface {
	CreateRequestProfile(ctx context.Context, profile RequestProfile) error
	UpdateRequestProfile(ctx context.Context, profile RequestProfile) error
	DeleteRequestProfile(ctx context.Context, id string) error
}

// RequestReader retrieves media requests.
type RequestReader interface {
	GetRequest(ctx context.Context, id string) (MediaRequest, error)
	ListRequests(ctx context.Context, filter RequestListFilter) ([]MediaRequest, error)
}

// RequestWriter creates and transitions media requests transactionally.
type RequestWriter interface {
	CreateRequest(ctx context.Context, request MediaRequest, now time.Time, quotaExempt bool) error
	TransitionRequest(ctx context.Context, id string, from, to RequestStatus, actorID, reason string, decidedAt time.Time) (MediaRequest, error)
}

// RequestDispatchWriter records a successful download-manager dispatch.
type RequestDispatchWriter interface {
	ClaimRequestDispatch(ctx context.Context, id string, snapshot RequestDispatchSnapshot, lease RequestDispatchLease, at time.Time) (MediaRequest, error)
	RecordRequestDispatch(ctx context.Context, id, leaseToken, managerItemID string, at time.Time) (MediaRequest, error)
	FailRequestDispatch(ctx context.Context, id, leaseToken, reason string, at time.Time) (MediaRequest, error)
}

// RequestAvailabilityClaimer atomically selects and stamps the fairest processing batch.
type RequestAvailabilityClaimer interface {
	ClaimRequestsForAvailability(ctx context.Context, pageSize int, at time.Time) ([]MediaRequest, error)
}

// ValidateRequestDispatchSnapshot validates immutable dispatch routing data.
func ValidateRequestDispatchSnapshot(snapshot RequestDispatchSnapshot) error {
	if !ValidID(snapshot.DownloadManagerID) ||
		!boundedText(snapshot.QualityProfile, MaxRequestProfileFieldBytes) ||
		!boundedText(snapshot.RootFolder, MaxRequestProfileFieldBytes) ||
		!requestTagsValid(snapshot.Tags) {
		return ErrInvalidArgument
	}
	return nil
}

// ValidateRequestDispatchLease validates lease identity and a future expiry.
func ValidateRequestDispatchLease(lease RequestDispatchLease, at time.Time) error {
	if !ValidID(lease.Token) || at.IsZero() || !lease.ExpiresAt.After(at) {
		return ErrInvalidArgument
	}
	return nil
}

// RequestQuotaReader retrieves role and account request quotas.
type RequestQuotaReader interface {
	GetRoleRequestQuota(ctx context.Context, roleID string) (RoleRequestQuota, error)
	GetAccountRequestQuota(ctx context.Context, accountID string) (AccountRequestQuota, error)
}

// RequestQuotaWriter mutates role and account request quotas.
type RequestQuotaWriter interface {
	SetRoleRequestQuota(ctx context.Context, quota RoleRequestQuota) error
	DeleteRoleRequestQuota(ctx context.Context, roleID string) error
	SetAccountRequestQuota(ctx context.Context, quota AccountRequestQuota) error
}

// AccountRequestQuotaDeleter removes an account quota override.
type AccountRequestQuotaDeleter interface {
	DeleteAccountRequestQuota(ctx context.Context, accountID string) error
}

// ValidateRequestProfile validates every persisted request profile field.
func ValidateRequestProfile(profile RequestProfile) error {
	if !ValidID(profile.ID) || !boundedText(profile.Name, MaxRequestProfileNameBytes) || len(profile.Kinds) == 0 || len(profile.Kinds) > 2 {
		return ErrInvalidArgument
	}
	seen := make(map[MediaKind]struct{}, len(profile.Kinds))
	for _, kind := range profile.Kinds {
		if !kind.Valid() {
			return ErrInvalidArgument
		}
		seen[kind] = struct{}{}
	}
	if len(seen) != len(profile.Kinds) || !profileFieldsValid(profile) || !requestTagsValid(profile.Tags) {
		return ErrInvalidArgument
	}
	return nil
}

func profileFieldsValid(profile RequestProfile) bool {
	fields := []string{profile.DownloadManagerKind, profile.DownloadManagerInstance, profile.QualityProfile, profile.RootFolder}
	for _, field := range fields {
		if !boundedText(field, MaxRequestProfileFieldBytes) {
			return false
		}
	}
	return true
}

func requestTagsValid(tags []string) bool {
	if len(tags) > MaxRequestProfileTags {
		return false
	}
	seen := make(map[string]struct{}, len(tags))
	for _, tag := range tags {
		if !boundedText(tag, MaxRequestProfileTagBytes) {
			return false
		}
		key := strings.ToLower(tag)
		if _, exists := seen[key]; exists {
			return false
		}
		seen[key] = struct{}{}
	}
	return true
}

// ValidateMediaRequest validates a request and its title snapshot.
func ValidateMediaRequest(request MediaRequest) error {
	if !ValidID(request.ID) || !ValidID(request.RequesterID) || !ValidID(request.ProfileID) || !request.Status.Valid() {
		return ErrInvalidArgument
	}
	title := MetadataTitle{Kind: request.Kind, Provider: request.Provider, ProviderID: request.ProviderID, Title: request.Title, Year: request.Year, PosterPath: request.PosterPath}
	if err := ValidateMetadataTitle(title); err != nil {
		return err
	}
	if request.Kind == MediaKindMovie && len(request.Seasons) != 0 {
		return ErrInvalidArgument
	}
	if request.Kind == MediaKindSeries && !requestSeasonsValid(request.Seasons) {
		return ErrInvalidArgument
	}
	return nil
}

func requestSeasonsValid(seasons []RequestSeason) bool {
	if len(seasons) == 0 || len(seasons) > MaxRequestSeasons {
		return false
	}
	seen := make(map[int]struct{}, len(seasons))
	for _, season := range seasons {
		if season.Number < 1 || season.Number > 999 || season.Status != SeasonPending && season.Status != SeasonApproved {
			return false
		}
		if _, exists := seen[season.Number]; exists {
			return false
		}
		seen[season.Number] = struct{}{}
	}
	return true
}

// ValidateSeasonNumbers validates a bounded, unique season selection.
func ValidateSeasonNumbers(numbers []int) error {
	seasons := make([]RequestSeason, 0, len(numbers))
	for _, number := range numbers {
		seasons = append(seasons, RequestSeason{Number: number, Status: SeasonPending})
	}
	if !requestSeasonsValid(seasons) {
		return ErrInvalidArgument
	}
	return nil
}

// ValidateRequestQuota validates paired limits and rolling periods.
func ValidateRequestQuota(quota RequestQuota) error {
	if quota.MovieLimit < 0 || quota.MovieLimit > math.MaxInt32 || quota.SeasonLimit < 0 || quota.SeasonLimit > math.MaxInt32 ||
		quota.MoviePeriod < 0 || quota.SeasonPeriod < 0 {
		return ErrInvalidArgument
	}
	if (quota.MovieLimit == 0) != (quota.MoviePeriod == 0) || (quota.SeasonLimit == 0) != (quota.SeasonPeriod == 0) {
		return fmt.Errorf("request quota limit and period: %w", ErrInvalidArgument)
	}
	if quota.MoviePeriod > 10*365*24*time.Hour || quota.SeasonPeriod > 10*365*24*time.Hour {
		return ErrInvalidArgument
	}
	return nil
}

// CheckRequestQuota applies one rolling quota to already-counted usage.
func CheckRequestQuota(quota RequestQuota, kind MediaKind, seasonCount int, usage RequestQuotaUsage) error {
	if err := ValidateRequestQuota(quota); err != nil || !kind.Valid() || seasonCount < 0 || usage.Movies < 0 || usage.Seasons < 0 {
		return ErrInvalidArgument
	}
	if kind == MediaKindMovie {
		if seasonCount != 0 {
			return ErrInvalidArgument
		}
		if quota.MovieLimit > 0 && usage.Movies+1 > int64(quota.MovieLimit) {
			return ErrQuotaExceeded
		}
		return nil
	}
	if seasonCount == 0 || seasonCount > MaxRequestSeasons {
		return ErrInvalidArgument
	}
	if quota.SeasonLimit > 0 && usage.Seasons+int64(seasonCount) > int64(quota.SeasonLimit) {
		return ErrQuotaExceeded
	}
	return nil
}

// RequestTransition defines one allowed state change and its permission.
type RequestTransition struct {
	From       RequestStatus
	To         RequestStatus
	Permission Permission
}

var requestTransitions = [...]RequestTransition{
	{From: RequestPending, To: RequestApproved, Permission: PermissionRequestsApprove},
	{From: RequestPending, To: RequestDeclined, Permission: PermissionRequestsApprove},
	{From: RequestProcessing, To: RequestAvailable, Permission: PermissionAdminSettings},
	{From: RequestProcessing, To: RequestFailed, Permission: PermissionAdminSettings},
	{From: RequestFailed, To: RequestApproved, Permission: PermissionRequestsApprove},
}

// TransitionPermission returns the permission required by an allowed transition.
func TransitionPermission(from, to RequestStatus) (Permission, error) {
	for _, transition := range requestTransitions {
		if transition.From == from && transition.To == to {
			return transition.Permission, nil
		}
	}
	return "", ErrInvalidTransition
}

// ValidateDecisionReason validates optional request decision text.
func ValidateDecisionReason(reason string) error {
	if reason == "" {
		return nil
	}
	if !boundedText(reason, MaxDecisionReasonBytes) {
		return ErrInvalidArgument
	}
	return nil
}
