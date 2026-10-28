package ratings

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/secrets"
	"github.com/BonzTM/bloom/internal/testutil"
)

const (
	testKey    = "a1b2c3d4"
	testIMDbID = "tt0133093"
)

var testTitle = core.RatedTitle{Kind: core.MediaKindMovie, TMDBID: "603", IMDbID: testIMDbID, Title: "The Matrix"}

var testRatings = core.TitleRatings{
	IMDb: &core.TitleRating{Score: 8.7, Votes: 2_000_000, Source: core.RatingSourceOMDb},
}

type harness struct {
	service *Service
	store   *memoryStore
	factory *fakeFactory
	clock   *testutil.FakeClock
}

func newHarness(t *testing.T, owned ...core.OwnedRatingsSource) harness {
	t.Helper()
	cipher, err := secrets.New([]byte("ratings-service-test-master-key"))
	if err != nil {
		t.Fatalf("secrets.New: %v", err)
	}
	store := &memoryStore{records: map[core.MetadataProviderKind]core.MetadataProviderRecord{}}
	factory := &fakeFactory{client: &fakeClient{ratings: testRatings}}
	clock := testutil.NewFakeClock(time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC))
	service, err := NewService(store, store, cipher, factory, owned, clock, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return harness{service: service, store: store, factory: factory, clock: clock}
}

func TestRatingsWithoutAKeyAreEmpty(t *testing.T) {
	h := newHarness(t)
	ratings, err := h.service.Ratings(t.Context(), testTitle)
	if err != nil || ratings != (core.TitleRatings{}) {
		t.Fatalf("Ratings = %+v, %v; want empty, nil", ratings, err)
	}
	if configured, err := h.service.HasKey(t.Context()); configured || err != nil {
		t.Fatalf("HasKey = %t, %v", configured, err)
	}
	if h.factory.built() != 0 {
		t.Fatalf("factory built %d clients without a key", h.factory.built())
	}
}

func TestSetKeyStoresItEncryptedAndServesCachedRatings(t *testing.T) {
	h := newHarness(t)
	if err := h.service.SetKey(t.Context(), testKey); err != nil {
		t.Fatalf("SetKey: %v", err)
	}
	record := h.store.records[core.MetadataProviderOMDb]
	if bytes.Contains(record.CredentialCiphertext, []byte(testKey)) || record.KeyID == "" {
		t.Fatalf("stored record holds the plaintext key or no key id: %+v", record)
	}
	if configured, err := h.service.HasKey(t.Context()); !configured || err != nil {
		t.Fatalf("HasKey = %t, %v", configured, err)
	}
	for range 2 {
		ratings, err := h.service.Ratings(t.Context(), testTitle)
		if err != nil || ratings.IMDb == nil || *ratings.IMDb != *testRatings.IMDb {
			t.Fatalf("Ratings = %+v, %v", ratings, err)
		}
	}
	if calls := h.factory.client.ratingCalls(); calls != 1 {
		t.Fatalf("upstream rating calls = %d, want 1", calls)
	}
	h.clock.Advance(cacheTTL)
	if _, err := h.service.Ratings(t.Context(), testTitle); err != nil || h.factory.client.ratingCalls() != 2 {
		t.Fatalf("expired entry was not reloaded: calls %d, %v", h.factory.client.ratingCalls(), err)
	}
}

func TestRemoveKeyForgetsTheKeyAndCachedRatings(t *testing.T) {
	h := newHarness(t)
	if err := h.service.SetKey(t.Context(), testKey); err != nil {
		t.Fatalf("SetKey: %v", err)
	}
	if _, err := h.service.Ratings(t.Context(), testTitle); err != nil {
		t.Fatalf("Ratings: %v", err)
	}
	if err := h.service.RemoveKey(t.Context()); err != nil {
		t.Fatalf("RemoveKey: %v", err)
	}
	ratings, err := h.service.Ratings(t.Context(), testTitle)
	if err != nil || ratings != (core.TitleRatings{}) {
		t.Fatalf("Ratings after removal = %+v, %v", ratings, err)
	}
	if !h.factory.client.closed() {
		t.Fatal("removing the key did not close the client")
	}
}

func TestSetKeyRejectsBadKeysWithoutStoringThem(t *testing.T) {
	h := newHarness(t)
	if err := h.service.SetKey(t.Context(), "bad key!"); !errors.Is(err, core.ErrInvalidArgument) {
		t.Fatalf("SetKey(malformed) error = %v", err)
	}
	h.factory.client.probeErr = core.ErrMetadataUnauthorized
	if err := h.service.SetKey(t.Context(), testKey); !errors.Is(err, core.ErrMetadataUnauthorized) {
		t.Fatalf("SetKey(rejected) error = %v", err)
	}
	if len(h.store.records) != 0 {
		t.Fatalf("a rejected key was stored: %+v", h.store.records)
	}
}

func TestRatingsFailuresAreReturnedAndNotCached(t *testing.T) {
	h := newHarness(t)
	person := testTitle
	person.IMDbID = "nm0000206"
	if _, err := h.service.Ratings(t.Context(), person); !errors.Is(err, core.ErrInvalidArgument) {
		t.Fatalf("Ratings(person id) error = %v", err)
	}
	if err := h.service.SetKey(t.Context(), testKey); err != nil {
		t.Fatalf("SetKey: %v", err)
	}
	h.factory.client.ratingsErr = core.ErrMetadataUnavailable
	if _, err := h.service.Ratings(t.Context(), testTitle); !errors.Is(err, core.ErrMetadataUnavailable) {
		t.Fatalf("Ratings error = %v", err)
	}
	h.factory.client.ratingsErr = nil
	if ratings, err := h.service.Ratings(t.Context(), testTitle); err != nil || ratings.IMDb == nil {
		t.Fatalf("Ratings after recovery = %+v, %v", ratings, err)
	}
}

func TestCacheEvictsTheSoonestExpiryWhenFull(t *testing.T) {
	clock := testutil.NewFakeClock(time.Unix(0, 0).UTC())
	cache := newRatingsCache(clock, 2, time.Hour)
	cache.put("tt0000001", testRatings, 0)
	clock.Advance(time.Minute)
	cache.put("tt0000002", testRatings, 0)
	cache.put("tt0000003", testRatings, 0)
	if _, ok := cache.get("tt0000001"); ok {
		t.Fatal("oldest entry survived a full cache")
	}
	for _, key := range []string{"tt0000002", "tt0000003"} {
		if _, ok := cache.get(key); !ok {
			t.Fatalf("%s was evicted", key)
		}
	}
}

func TestCacheReturnsCopies(t *testing.T) {
	cache := newRatingsCache(testutil.NewFakeClock(time.Unix(0, 0).UTC()), 4, time.Hour)
	cache.put(testIMDbID, testRatings, 0)
	got, _ := cache.get(testIMDbID)
	got.IMDb.Score = 1
	again, _ := cache.get(testIMDbID)
	if again.IMDb.Score != testRatings.IMDb.Score {
		t.Fatalf("cache entry was mutated through a returned value: %v", again.IMDb.Score)
	}
}

type memoryStore struct {
	mu      sync.Mutex
	records map[core.MetadataProviderKind]core.MetadataProviderRecord
}

func (s *memoryStore) GetMetadataProvider(_ context.Context, kind core.MetadataProviderKind) (core.MetadataProviderRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.records[kind]
	if !ok {
		return core.MetadataProviderRecord{}, core.ErrNotFound
	}
	return record, nil
}

func (s *memoryStore) UpsertMetadataProvider(_ context.Context, record core.MetadataProviderRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.records[record.Kind] = record
	return nil
}

func (s *memoryStore) DeleteMetadataProvider(_ context.Context, kind core.MetadataProviderKind) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.records, kind)
	return nil
}

type fakeFactory struct {
	mu     sync.Mutex
	count  int
	client *fakeClient
}

func (f *fakeFactory) New(key string) (Client, error) {
	if key != testKey {
		return nil, core.ErrInvalidArgument
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.count++
	return f.client, nil
}

func (f *fakeFactory) built() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.count
}

type fakeClient struct {
	mu         sync.Mutex
	ratings    core.TitleRatings
	probeErr   error
	ratingsErr error
	calls      int
	isClosed   bool
}

func (c *fakeClient) Probe(context.Context) error { return c.probeErr }

func (c *fakeClient) TitleRatings(context.Context, string) (core.TitleRatings, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls++
	if c.ratingsErr != nil {
		return core.TitleRatings{}, c.ratingsErr
	}
	return cloneRatings(c.ratings), nil
}

func (c *fakeClient) CloseIdleConnections() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.isClosed = true
}

func (c *fakeClient) ratingCalls() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

func (c *fakeClient) closed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.isClosed
}

func TestCacheDropsAPutFromBeforeAClear(t *testing.T) {
	cache := newRatingsCache(testutil.NewFakeClock(time.Unix(0, 0).UTC()), 4, time.Hour)
	generation := cache.generation()
	cache.clear()
	cache.put(testIMDbID, testRatings, generation)
	if _, ok := cache.get(testIMDbID); ok {
		t.Fatal("a load that began before the key changed was cached")
	}
	cache.put(testIMDbID, testRatings, cache.generation())
	if _, ok := cache.get(testIMDbID); !ok {
		t.Fatal("a current load was not cached")
	}
}
