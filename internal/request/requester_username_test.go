package request

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/BonzTM/bloom/internal/core"
)

type requesterUsernameStore struct {
	lifecycleStore
	requests  []core.MediaRequest
	usernames map[string]string
	batches   [][]string
	err       error
}

func (s *requesterUsernameStore) GetRequest(_ context.Context, id string) (core.MediaRequest, error) {
	for _, request := range s.requests {
		if request.ID == id {
			return request, nil
		}
	}
	return core.MediaRequest{}, core.ErrNotFound
}

func (s *requesterUsernameStore) ListRequests(context.Context, core.RequestListFilter) ([]core.MediaRequest, error) {
	return slices.Clone(s.requests), nil
}

func (s *requesterUsernameStore) UsernamesByAccountIDs(_ context.Context, ids []string) (map[string]string, error) {
	s.batches = append(s.batches, slices.Clone(ids))
	return s.usernames, s.err
}

func TestRequestReadsResolveCurrentRequesterUsernamesInOneBatch(t *testing.T) {
	const aliceID = "11111111-1111-4111-8111-111111111111"
	const goneID = "22222222-2222-4222-8222-222222222222"
	store := &requesterUsernameStore{
		requests: []core.MediaRequest{
			{ID: "33333333-3333-4333-8333-333333333333", RequesterID: aliceID},
			{ID: "44444444-4444-4444-8444-444444444444", RequesterID: aliceID},
			{ID: "55555555-5555-4555-8555-555555555555", RequesterID: goneID},
		},
		usernames: map[string]string{aliceID: "alice"},
	}
	service := newRequesterUsernameService(t, store)
	items, err := service.List(t.Context(), aliceID, true, core.RequestListFilter{PageSize: 3})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(store.batches) != 1 || !slices.Equal(store.batches[0], []string{aliceID, goneID}) {
		t.Fatalf("username batches = %v, want one deduplicated batch", store.batches)
	}
	if items[0].RequesterUsername != "alice" || items[1].RequesterUsername != "alice" || items[2].RequesterUsername != "" {
		t.Fatalf("resolved requester usernames = %q, %q, %q", items[0].RequesterUsername, items[1].RequesterUsername, items[2].RequesterUsername)
	}

	store.usernames[aliceID] = "alice-renamed"
	item, err := service.Get(t.Context(), aliceID, store.requests[0].ID, false)
	if err != nil || item.RequesterUsername != "alice-renamed" {
		t.Fatalf("Get after rename = %+v, %v", item, err)
	}
}

func TestRequestUsernameLookupIsBoundedAndPropagatesFailures(t *testing.T) {
	const actorID = "11111111-1111-4111-8111-111111111111"
	store := &requesterUsernameStore{err: errors.New("lookup failed")}
	store.requests = []core.MediaRequest{{ID: "33333333-3333-4333-8333-333333333333", RequesterID: actorID}}
	service := newRequesterUsernameService(t, store)
	if _, err := service.List(t.Context(), actorID, true, core.RequestListFilter{PageSize: 1}); err == nil {
		t.Fatal("List accepted a failed username lookup")
	}

	store.err = nil
	store.requests = make([]core.MediaRequest, maxRequesterUsernameBatchSize+1)
	for index := range store.requests {
		store.requests[index].RequesterID = actorID
	}
	if _, err := service.List(t.Context(), actorID, true, core.RequestListFilter{PageSize: 1}); !errors.Is(err, core.ErrInvalidArgument) {
		t.Fatalf("oversized username batch error = %v, want ErrInvalidArgument", err)
	}
}

func newRequesterUsernameService(t *testing.T, store *requesterUsernameStore) *Service {
	t.Helper()
	service, err := NewService(Dependencies{
		Profiles: store, ProfileWriter: store, Requests: store, Usernames: store, RequestWriter: store,
		QuotaReader: store, QuotaWriter: store, QuotaDeleter: store,
		Metadata: &lifecycleMetadata{}, Clock: fixedClock{},
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return service
}

type fixedClock struct{}

func (fixedClock) Now() time.Time { return time.Unix(0, 0).UTC() }
