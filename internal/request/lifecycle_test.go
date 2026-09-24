package request

import (
	"context"
	"testing"
	"time"

	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/testutil"
)

type lifecycleStore struct {
	profile core.RequestProfile
	request core.MediaRequest
}

func (s *lifecycleStore) GetRequestProfile(context.Context, string) (core.RequestProfile, error) {
	return s.profile, nil
}

func (s *lifecycleStore) ListRequestProfiles(context.Context, string, int) ([]core.RequestProfile, error) {
	return []core.RequestProfile{s.profile}, nil
}

func (*lifecycleStore) CreateRequestProfile(context.Context, core.RequestProfile) error { return nil }

func (*lifecycleStore) UpdateRequestProfile(context.Context, core.RequestProfile) error { return nil }

func (*lifecycleStore) DeleteRequestProfile(context.Context, string) error { return nil }

func (s *lifecycleStore) GetRequest(context.Context, string) (core.MediaRequest, error) {
	return s.request, nil
}

func (s *lifecycleStore) ListRequests(context.Context, core.RequestListFilter) ([]core.MediaRequest, error) {
	return []core.MediaRequest{s.request}, nil
}

func (s *lifecycleStore) CreateRequest(_ context.Context, request core.MediaRequest, _ time.Time, _ bool) error {
	s.request = request
	return nil
}

func (s *lifecycleStore) TransitionRequest(
	_ context.Context, _ string, from, to core.RequestStatus, actorID, reason string, at time.Time,
) (core.MediaRequest, error) {
	if s.request.Status != from {
		return core.MediaRequest{}, core.ErrInvalidTransition
	}
	s.request.Status, s.request.DecidedBy, s.request.DecisionReason, s.request.UpdatedAt = to, actorID, reason, at
	if to == core.RequestApproved {
		s.request.FailureReason = ""
	}
	return s.request, nil
}

func (*lifecycleStore) GetRoleRequestQuota(context.Context, string) (core.RoleRequestQuota, error) {
	return core.RoleRequestQuota{}, core.ErrNotFound
}

func (*lifecycleStore) GetAccountRequestQuota(context.Context, string) (core.AccountRequestQuota, error) {
	return core.AccountRequestQuota{}, core.ErrNotFound
}

func (*lifecycleStore) SetRoleRequestQuota(context.Context, core.RoleRequestQuota) error { return nil }

func (*lifecycleStore) DeleteRoleRequestQuota(context.Context, string) error { return nil }

func (*lifecycleStore) SetAccountRequestQuota(context.Context, core.AccountRequestQuota) error {
	return nil
}
func (*lifecycleStore) DeleteAccountRequestQuota(context.Context, string) error { return nil }

type lifecycleMetadata struct{ movie core.MetadataTitle }

func (*lifecycleMetadata) Search(context.Context, core.MetadataSearch) ([]core.MetadataTitle, error) {
	return nil, nil
}

func (m *lifecycleMetadata) Movie(context.Context, string) (core.MetadataTitle, error) {
	return m.movie, nil
}

func (*lifecycleMetadata) Series(context.Context, string, bool) (core.MetadataSeries, error) {
	return core.MetadataSeries{}, nil
}

type lifecycleRecorder struct {
	events   []core.RequestEvent
	enqueued []string
}

func (r *lifecycleRecorder) PublishRequestEvent(_ context.Context, event core.RequestEvent) {
	r.events = append(r.events, event)
}
func (r *lifecycleRecorder) Enqueue(id string) { r.enqueued = append(r.enqueued, id) }

func TestAutoApprovalAndFailedReapprovalPublishAndEnqueue(t *testing.T) {
	const actorID = "11111111-1111-4111-8111-111111111111"
	const profileID = "22222222-2222-4222-8222-222222222222"
	store := &lifecycleStore{profile: core.RequestProfile{
		ID: profileID, Kinds: []core.MediaKind{core.MediaKindMovie}, DownloadManagerKind: "radarr", DownloadManagerInstance: "Main",
	}}
	recorder := &lifecycleRecorder{}
	service, err := NewService(Dependencies{
		Profiles: store, ProfileWriter: store, Requests: store, RequestWriter: store,
		QuotaReader: store, QuotaWriter: store, QuotaDeleter: store,
		Metadata: &lifecycleMetadata{movie: core.MetadataTitle{
			Kind: core.MediaKindMovie, Provider: core.MetadataProviderTMDB, ProviderID: "10", Title: "Film", Year: 2026,
		}},
		Clock:  testutil.NewFakeClock(time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)),
		Events: recorder, Fulfilment: recorder,
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	created, err := service.Create(t.Context(), actorID, CreateInput{
		Kind: core.MediaKindMovie, ProviderID: "10", ProfileID: profileID,
	}, true)
	if err != nil || created.Status != core.RequestApproved || len(recorder.enqueued) != 1 ||
		len(recorder.events) != 2 || recorder.events[0].Type != core.RequestEventCreated || recorder.events[1].Type != core.RequestEventApproved {
		t.Fatalf("Create = %+v, %v; events=%+v enqueued=%v", created, err, recorder.events, recorder.enqueued)
	}
	store.request.Status, store.request.FailureReason = core.RequestFailed, "previous failure"
	reapproved, err := service.Decide(t.Context(), actorID, created.ID, true, "retry")
	if err != nil || reapproved.Status != core.RequestApproved || reapproved.FailureReason != "" ||
		len(recorder.enqueued) != 2 || recorder.events[len(recorder.events)-1].Type != core.RequestEventApproved {
		t.Fatalf("Decide = %+v, %v; events=%+v enqueued=%v", reapproved, err, recorder.events, recorder.enqueued)
	}
}
