// Package request implements request profiles, quotas, and request lifecycle policy.
package request

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/BonzTM/bloom/internal/core"
)

// Metrics observes media request outcomes by kind.
type Metrics interface {
	IncMediaRequest(kind, outcome string)
}

type nopMetrics struct{}

func (nopMetrics) IncMediaRequest(string, string) {}

type managerResolver interface {
	Resolve(ctx context.Context, name string) (core.DownloadManager, error)
}

type progressReader interface {
	Progress(ctx context.Context, managerID, managerItemID string, seasons []int) (core.DownloadProgress, error)
}

type fulfilmentEnqueuer interface {
	Enqueue(requestID string)
}

// Service applies request profile, ownership, approval, and quota policy.
type Service struct {
	profiles      core.RequestProfileReader
	profileWriter core.RequestProfileWriter
	reader        core.RequestReader
	writer        core.RequestWriter
	quotaReader   core.RequestQuotaReader
	quotaWriter   core.RequestQuotaWriter
	quotaDeleter  core.AccountRequestQuotaDeleter
	metadata      core.MetadataProvider
	clock         core.Clock
	metrics       Metrics
	events        core.RequestEventPublisher
	managers      managerResolver
	progress      progressReader
	fulfilment    fulfilmentEnqueuer
}

// Dependencies supplies every request service boundary explicitly.
type Dependencies struct {
	Profiles      core.RequestProfileReader
	ProfileWriter core.RequestProfileWriter
	Requests      core.RequestReader
	RequestWriter core.RequestWriter
	QuotaReader   core.RequestQuotaReader
	QuotaWriter   core.RequestQuotaWriter
	QuotaDeleter  core.AccountRequestQuotaDeleter
	Metadata      core.MetadataProvider
	Clock         core.Clock
	Metrics       Metrics
	Events        core.RequestEventPublisher
	Managers      managerResolver
	Progress      progressReader
	Fulfilment    fulfilmentEnqueuer
}

// NewService constructs a request service from required boundaries.
func NewService(deps Dependencies) (*Service, error) {
	if deps.Profiles == nil || deps.ProfileWriter == nil || deps.Requests == nil || deps.RequestWriter == nil ||
		deps.QuotaReader == nil || deps.QuotaWriter == nil || deps.QuotaDeleter == nil || deps.Metadata == nil || deps.Clock == nil {
		return nil, errors.New("request service: all dependencies are required")
	}
	if deps.Metrics == nil {
		deps.Metrics = nopMetrics{}
	}
	if deps.Events == nil {
		deps.Events = nopEvents{}
	}
	return &Service{
		profiles: deps.Profiles, profileWriter: deps.ProfileWriter, reader: deps.Requests, writer: deps.RequestWriter,
		quotaReader: deps.QuotaReader, quotaWriter: deps.QuotaWriter, quotaDeleter: deps.QuotaDeleter,
		metadata: deps.Metadata, clock: deps.Clock, metrics: deps.Metrics, events: deps.Events,
		managers: deps.Managers, progress: deps.Progress, fulfilment: deps.Fulfilment,
	}, nil
}

// CreateInput identifies metadata and a profile for a new request.
type CreateInput struct {
	Kind       core.MediaKind
	ProviderID string
	ProfileID  string
	Seasons    []int
}

// Create resolves metadata and inserts one request under its rolling quota.
func (s *Service) Create(ctx context.Context, requesterID string, input CreateInput, autoApprove bool) (core.MediaRequest, error) {
	if !core.ValidID(requesterID) || !input.Kind.Valid() || core.ValidateProviderID(input.ProviderID) != nil || !core.ValidID(input.ProfileID) {
		return core.MediaRequest{}, core.ErrInvalidArgument
	}
	profile, err := s.profiles.GetRequestProfile(ctx, input.ProfileID)
	if err != nil {
		return core.MediaRequest{}, fmt.Errorf("get request profile: %w", err)
	}
	if !slices.Contains(profile.Kinds, input.Kind) {
		return core.MediaRequest{}, core.ErrInvalidArgument
	}
	title, seasons, err := s.resolveMetadata(ctx, input)
	if err != nil {
		return core.MediaRequest{}, err
	}
	request, err := s.newRequest(requesterID, profile.ID, title, seasons, autoApprove)
	if err != nil {
		return core.MediaRequest{}, err
	}
	if err := s.writer.CreateRequest(ctx, request, request.CreatedAt, autoApprove); err != nil {
		s.metrics.IncMediaRequest(string(request.Kind), requestOutcome(err))
		return core.MediaRequest{}, fmt.Errorf("create request: %w", err)
	}
	s.metrics.IncMediaRequest(string(request.Kind), string(request.Status))
	s.events.PublishRequestEvent(ctx, requestEvent(request, core.RequestEventCreated, requesterID))
	if autoApprove {
		s.events.PublishRequestEvent(ctx, requestEvent(request, core.RequestEventApproved, requesterID))
		s.enqueue(request.ID)
	}
	return request, nil
}

func (s *Service) resolveMetadata(ctx context.Context, input CreateInput) (core.MetadataTitle, []core.RequestSeason, error) {
	if input.Kind == core.MediaKindMovie {
		if len(input.Seasons) != 0 {
			return core.MetadataTitle{}, nil, core.ErrInvalidArgument
		}
		title, err := s.metadata.Movie(ctx, input.ProviderID)
		return title, nil, err
	}
	if err := core.ValidateSeasonNumbers(input.Seasons); err != nil {
		return core.MetadataTitle{}, nil, err
	}
	series, err := s.metadata.Series(ctx, input.ProviderID, false)
	if err != nil {
		return core.MetadataTitle{}, nil, err
	}
	available := make(map[int]struct{}, len(series.Seasons))
	for _, season := range series.Seasons {
		available[season.Number] = struct{}{}
	}
	seasons := make([]core.RequestSeason, 0, len(input.Seasons))
	for _, number := range input.Seasons {
		if _, ok := available[number]; !ok {
			return core.MetadataTitle{}, nil, core.ErrInvalidArgument
		}
		seasons = append(seasons, core.RequestSeason{Number: number, Status: core.SeasonPending})
	}
	return series.MetadataTitle, seasons, nil
}

func (s *Service) newRequest(requesterID, profileID string, title core.MetadataTitle, seasons []core.RequestSeason, autoApprove bool) (core.MediaRequest, error) {
	id, err := core.NewID()
	if err != nil {
		return core.MediaRequest{}, err
	}
	now := core.NormalizeTime(s.clock.Now())
	status := core.RequestPending
	decidedBy := ""
	var decidedAt *time.Time
	if autoApprove {
		status, decidedBy = core.RequestApproved, requesterID
		decidedAt = &now
		for index := range seasons {
			seasons[index].Status = core.SeasonApproved
		}
	}
	return core.MediaRequest{
		ID: id, Kind: title.Kind, Provider: title.Provider, ProviderID: title.ProviderID,
		Title: title.Title, Year: title.Year, PosterPath: title.PosterPath, RequesterID: requesterID, ProfileID: profileID,
		Status: status, Seasons: seasons, DecidedBy: decidedBy, DecidedAt: decidedAt, CreatedAt: now, UpdatedAt: now,
	}, nil
}

// List returns all requests for approvers and only owned requests otherwise.
func (s *Service) List(ctx context.Context, actorID string, approver bool, filter core.RequestListFilter) ([]core.MediaRequest, error) {
	filter, err := core.ScopeRequestList(actorID, approver, filter)
	if err != nil {
		return nil, err
	}
	return s.reader.ListRequests(ctx, filter)
}

// Get returns one request when the actor owns it or is an approver.
func (s *Service) Get(ctx context.Context, actorID, id string, approver bool) (core.MediaRequest, error) {
	request, err := s.reader.GetRequest(ctx, id)
	if err != nil {
		return core.MediaRequest{}, err
	}
	if err := core.AuthorizeRequestRead(actorID, approver, request); err != nil {
		return core.MediaRequest{}, err
	}
	return request, nil
}

// Decide approves or declines a pending request, or re-approves a failed request.
func (s *Service) Decide(ctx context.Context, actorID, id string, approve bool, reason string) (core.MediaRequest, error) {
	if err := core.ValidateDecisionReason(reason); err != nil {
		return core.MediaRequest{}, err
	}
	current, err := s.reader.GetRequest(ctx, id)
	if err != nil {
		return core.MediaRequest{}, err
	}
	from, to := current.Status, core.RequestDeclined
	if approve {
		to = core.RequestApproved
	}
	if from != core.RequestPending && (!approve || from != core.RequestFailed) {
		return core.MediaRequest{}, core.ErrInvalidTransition
	}
	request, err := s.writer.TransitionRequest(ctx, id, from, to, actorID, reason, core.NormalizeTime(s.clock.Now()))
	if err != nil {
		return core.MediaRequest{}, fmt.Errorf("decide request: %w", err)
	}
	s.metrics.IncMediaRequest(string(request.Kind), string(to))
	eventType := core.RequestEventDeclined
	if approve {
		eventType = core.RequestEventApproved
		s.enqueue(request.ID)
	}
	s.events.PublishRequestEvent(ctx, requestEvent(request, eventType, actorID))
	return request, nil
}

// CreateProfile assigns identity and timestamps before persistence.
func (s *Service) CreateProfile(ctx context.Context, profile core.RequestProfile) (core.RequestProfile, error) {
	id, err := core.NewID()
	if err != nil {
		return core.RequestProfile{}, err
	}
	now := core.NormalizeTime(s.clock.Now())
	profile.ID, profile.CreatedAt, profile.UpdatedAt = id, now, now
	if err := s.normalizeProfileTarget(ctx, &profile); err != nil {
		return core.RequestProfile{}, err
	}
	if err := s.profileWriter.CreateRequestProfile(ctx, profile); err != nil {
		return core.RequestProfile{}, err
	}
	return profile, nil
}

// UpdateProfile preserves creation time and replaces editable settings.
func (s *Service) UpdateProfile(ctx context.Context, profile core.RequestProfile) (core.RequestProfile, error) {
	existing, err := s.profiles.GetRequestProfile(ctx, profile.ID)
	if err != nil {
		return core.RequestProfile{}, err
	}
	profile.CreatedAt, profile.UpdatedAt = existing.CreatedAt, core.NormalizeTime(s.clock.Now())
	if err := s.normalizeProfileTarget(ctx, &profile); err != nil {
		return core.RequestProfile{}, err
	}
	if err := s.profileWriter.UpdateRequestProfile(ctx, profile); err != nil {
		return core.RequestProfile{}, err
	}
	return profile, nil
}

// DeleteProfile removes an unreferenced request profile.
func (s *Service) DeleteProfile(ctx context.Context, id string) error {
	return s.profileWriter.DeleteRequestProfile(ctx, id)
}

// ListProfiles returns one bounded page of profiles.
func (s *Service) ListProfiles(ctx context.Context, after string, pageSize int) ([]core.RequestProfile, error) {
	return s.profiles.ListRequestProfiles(ctx, after, pageSize)
}

// Progress returns live queue state after applying the request read rule.
func (s *Service) Progress(
	ctx context.Context, actorID, id string, approver bool,
) (core.DownloadProgress, error) {
	request, err := s.Get(ctx, actorID, id, approver)
	if err != nil {
		return core.DownloadProgress{}, err
	}
	if request.DownloadManagerItemID == "" {
		return core.DownloadProgress{}, core.ErrDownloadItemMissing
	}
	if request.Status != core.RequestProcessing || s.progress == nil {
		return core.DownloadProgress{}, core.ErrNotFound
	}
	return s.progress.Progress(
		ctx, request.DownloadManagerID, request.DownloadManagerItemID, requestSeasonNumbers(request.Seasons),
	)
}

func requestSeasonNumbers(seasons []core.RequestSeason) []int {
	result := make([]int, 0, len(seasons))
	for _, season := range seasons {
		result = append(result, season.Number)
	}
	return result
}

func (s *Service) normalizeProfileTarget(ctx context.Context, profile *core.RequestProfile) error {
	if s.managers == nil {
		return nil
	}
	manager, err := s.managers.Resolve(ctx, profile.DownloadManagerInstance)
	if err != nil {
		return fmt.Errorf("resolve download manager: %w", err)
	}
	if string(manager.Kind) != profile.DownloadManagerKind {
		return core.ErrInvalidArgument
	}
	for _, kind := range profile.Kinds {
		if !manager.Kind.Handles(kind) {
			return core.ErrInvalidArgument
		}
	}
	profile.DownloadManagerKind = string(manager.Kind)
	profile.DownloadManagerInstance = manager.Name
	return nil
}

func (s *Service) enqueue(id string) {
	if s.fulfilment != nil {
		s.fulfilment.Enqueue(id)
	}
}

// GetRoleQuota returns one role quota.
func (s *Service) GetRoleQuota(ctx context.Context, id string) (core.RoleRequestQuota, error) {
	return s.quotaReader.GetRoleRequestQuota(ctx, id)
}

// SetRoleQuota creates or replaces one role quota.
func (s *Service) SetRoleQuota(ctx context.Context, value core.RoleRequestQuota) error {
	return s.quotaWriter.SetRoleRequestQuota(ctx, value)
}

// DeleteRoleQuota removes one role quota.
func (s *Service) DeleteRoleQuota(ctx context.Context, id string) error {
	return s.quotaWriter.DeleteRoleRequestQuota(ctx, id)
}

// GetAccountQuota returns one account quota override.
func (s *Service) GetAccountQuota(ctx context.Context, id string) (core.AccountRequestQuota, error) {
	return s.quotaReader.GetAccountRequestQuota(ctx, id)
}

// SetAccountQuota creates or replaces one account quota override.
func (s *Service) SetAccountQuota(ctx context.Context, value core.AccountRequestQuota) error {
	return s.quotaWriter.SetAccountRequestQuota(ctx, value)
}

// DeleteAccountQuota removes one account quota override.
func (s *Service) DeleteAccountQuota(ctx context.Context, id string) error {
	return s.quotaDeleter.DeleteAccountRequestQuota(ctx, id)
}

func requestOutcome(err error) string {
	if errors.Is(err, core.ErrQuotaExceeded) {
		return "quota_exceeded"
	}
	if errors.Is(err, core.ErrAlreadyExists) {
		return "conflict"
	}
	return "failed"
}

type nopEvents struct{}

func (nopEvents) PublishRequestEvent(context.Context, core.RequestEvent) {}

func requestEvent(request core.MediaRequest, eventType core.RequestEventType, actorID string) core.RequestEvent {
	return core.RequestEvent{
		Type: eventType, RequestID: request.ID, ActorID: actorID,
		Kind: request.Kind, Title: request.Title, At: request.UpdatedAt,
	}
}
