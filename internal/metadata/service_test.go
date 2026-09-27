package metadata

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/secrets"
	"github.com/BonzTM/bloom/internal/testutil"
)

const serviceTestReadAccessToken = "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJhdWQiOiJ0bWRiIiwic3ViIjoiYmxvb20tdGVzdCIsImlhdCI6MTcwMDAwMDAwMH0.c2lnbmF0dXJlLXNpZ25hdHVyZS1zaWduYXR1cmUtc2lnbmF0dXJl"

type serviceTestStore struct {
	record      core.MetadataProviderRecord
	upsertCalls int
}

func (s *serviceTestStore) GetMetadataProvider(context.Context, core.MetadataProviderKind) (core.MetadataProviderRecord, error) {
	if len(s.record.CredentialCiphertext) == 0 {
		return core.MetadataProviderRecord{}, core.ErrNotFound
	}
	return s.record, nil
}

func (s *serviceTestStore) UpsertMetadataProvider(_ context.Context, record core.MetadataProviderRecord) error {
	s.record = record
	s.upsertCalls++
	return nil
}

func (s *serviceTestStore) DeleteMetadataProvider(context.Context, core.MetadataProviderKind) error {
	s.record = core.MetadataProviderRecord{}
	return nil
}

type serviceTestCipher struct{}

func (serviceTestCipher) Encrypt(value []byte, _ secrets.Context) ([]byte, error) {
	return bytes.Clone(value), nil
}

func (serviceTestCipher) Decrypt(value []byte, _ secrets.Context) ([]byte, error) {
	return bytes.Clone(value), nil
}

func (serviceTestCipher) KeyID() string { return "test-key" }

type serviceTestFactory struct {
	probeErr   error
	probeCalls int
	newCalls   int
	provider   core.MetadataProvider
}

func (f *serviceTestFactory) Probe(context.Context, core.MetadataProviderKind, string) error {
	f.probeCalls++
	return f.probeErr
}

func (f *serviceTestFactory) New(core.MetadataProviderKind, string) (core.MetadataProvider, error) {
	f.newCalls++
	if f.provider != nil {
		return f.provider, nil
	}
	return &serviceTestProvider{}, nil
}

type serviceTestProvider struct {
	discoverCalls   atomic.Int32
	genreCalls      atomic.Int32
	searchErr       error
	discoverEntered chan<- struct{}
	genreEntered    chan<- struct{}
	release         <-chan struct{}
}

func (p *serviceTestProvider) Search(context.Context, core.MetadataSearch) ([]core.MetadataTitle, error) {
	return []core.MetadataTitle{}, p.searchErr
}

func TestProviderFailureWarningsAreLimitedPerReason(t *testing.T) {
	provider := &serviceTestProvider{}
	store := &serviceTestStore{record: core.MetadataProviderRecord{
		Kind: core.MetadataProviderTMDB, CredentialCiphertext: []byte(serviceTestReadAccessToken), KeyID: "test-key",
	}}
	clock := testutil.NewFakeClock(time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC))
	var logs bytes.Buffer
	service, err := NewService(
		store, store, &serviceTestStates{}, serviceTestCipher{}, &serviceTestFactory{provider: provider},
		clock, slog.New(slog.NewTextHandler(&logs, nil)),
	)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	for _, failure := range []error{
		core.ErrMetadataUnreachable, core.ErrMetadataUnauthorized, core.ErrMetadataMalformed, core.ErrMetadataUnavailable,
	} {
		provider.searchErr = failure
		for range 2 {
			_, searchErr := service.Search(t.Context(), core.MetadataSearch{Query: "movie"})
			if !errors.Is(searchErr, failure) {
				t.Fatalf("Search error = %v, want %v", searchErr, failure)
			}
		}
	}
	assertMetadataWarningCounts(t, logs.String(), 1)
	clock.Advance(time.Minute)
	for _, failure := range []error{
		core.ErrMetadataUnreachable, core.ErrMetadataUnauthorized, core.ErrMetadataMalformed, core.ErrMetadataUnavailable,
	} {
		provider.searchErr = failure
		_, searchErr := service.Search(t.Context(), core.MetadataSearch{Query: "movie"})
		if !errors.Is(searchErr, failure) {
			t.Fatalf("Search error = %v, want %v", searchErr, failure)
		}
	}
	assertMetadataWarningCounts(t, logs.String(), 2)
}

func assertMetadataWarningCounts(t *testing.T, logs string, want int) {
	t.Helper()
	if got := strings.Count(logs, "level=WARN"); got != want*4 {
		t.Fatalf("warning records = %d, want %d: %s", got, want*4, logs)
	}
	for _, reason := range []string{"unreachable", "unauthorized", "malformed", "unavailable"} {
		if got := strings.Count(logs, "reason="+reason); got != want {
			t.Errorf("reason %s warnings = %d, want %d: %s", reason, got, want, logs)
		}
	}
}

func (*serviceTestProvider) Movie(context.Context, string) (core.MetadataTitle, error) {
	return core.MetadataTitle{}, nil
}

func (*serviceTestProvider) Series(context.Context, string, bool) (core.MetadataSeries, error) {
	return core.MetadataSeries{}, nil
}

func (p *serviceTestProvider) Discover(context.Context, core.MetadataDiscover) (core.MetadataPage, error) {
	p.discoverCalls.Add(1)
	blockServiceTestProvider(p.discoverEntered, p.release)
	return core.MetadataPage{Items: []core.MetadataTitle{{
		Kind: core.MediaKindMovie, Provider: core.MetadataProviderTMDB, ProviderID: "11", Title: "Film",
	}}, Page: 1, TotalPages: 2}, nil
}

func (p *serviceTestProvider) Genres(context.Context, core.MediaKind) ([]core.MetadataGenre, error) {
	p.genreCalls.Add(1)
	blockServiceTestProvider(p.genreEntered, p.release)
	return []core.MetadataGenre{{ID: 28, Name: "Action"}}, nil
}

func blockServiceTestProvider(entered chan<- struct{}, release <-chan struct{}) {
	if entered != nil {
		entered <- struct{}{}
	}
	if release != nil {
		<-release
	}
}

type serviceTestStates struct {
	mu        sync.Mutex
	calls     int
	byAccount map[string]core.RequestStatus
}

func (s *serviceTestStates) MetadataRequestStates(
	_ context.Context, accountID string, _ []core.MetadataTitle,
) (map[core.MetadataTitleKey]core.RequestStatus, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	status := core.RequestPending
	if configured, ok := s.byAccount[accountID]; ok {
		status = configured
	}
	return map[core.MetadataTitleKey]core.RequestStatus{
		{Kind: core.MediaKindMovie, Provider: core.MetadataProviderTMDB, ProviderID: "11"}: status,
	}, nil
}

func (s *serviceTestStates) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

func TestSetKeyValidatesAndProbesBeforeStore(t *testing.T) {
	tests := []struct {
		name       string
		credential string
		probeErr   error
		wantErr    error
		wantProbe  int
		wantStore  int
	}{
		{name: "v3 key", credential: "0123456789abcdef0123456789abcdef", wantErr: core.ErrInvalidArgument},
		{name: "unauthorized", credential: serviceTestReadAccessToken, probeErr: core.ErrMetadataUnauthorized, wantErr: core.ErrMetadataUnauthorized, wantProbe: 1},
		{name: "valid", credential: serviceTestReadAccessToken, wantProbe: 1, wantStore: 1},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			store := &serviceTestStore{}
			factory := &serviceTestFactory{probeErr: testCase.probeErr}
			service := newServiceTestService(t, store, factory, slog.New(slog.DiscardHandler))
			err := service.SetKey(t.Context(), core.MetadataProviderTMDB, testCase.credential)
			if !errors.Is(err, testCase.wantErr) {
				t.Fatalf("SetKey error = %v, want %v", err, testCase.wantErr)
			}
			if factory.probeCalls != testCase.wantProbe || store.upsertCalls != testCase.wantStore {
				t.Fatalf("calls = probe %d store %d, want %d %d", factory.probeCalls, store.upsertCalls, testCase.wantProbe, testCase.wantStore)
			}
		})
	}
}

func TestStoredInvalidCredentialIsNotConfiguredAndWarnsOnce(t *testing.T) {
	const legacyKey = "0123456789abcdef0123456789abcdef"
	store := &serviceTestStore{record: core.MetadataProviderRecord{
		Kind: core.MetadataProviderTMDB, CredentialCiphertext: []byte(legacyKey), KeyID: "test-key",
	}}
	factory := &serviceTestFactory{}
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	service := newServiceTestService(t, store, factory, logger)
	for range 2 {
		configured, err := service.HasKey(t.Context(), core.MetadataProviderTMDB)
		if err != nil || configured {
			t.Fatalf("HasKey = %t, %v; want false, nil", configured, err)
		}
		_, err = service.Search(t.Context(), core.MetadataSearch{Query: "movie"})
		if !errors.Is(err, core.ErrMetadataNotConfigured) {
			t.Fatalf("Search error = %v, want %v", err, core.ErrMetadataNotConfigured)
		}
	}
	if factory.newCalls != 0 {
		t.Fatalf("provider factory calls = %d, want 0", factory.newCalls)
	}
	if got := strings.Count(logs.String(), "stored TMDB credential is not an API Read Access Token"); got != 1 {
		t.Fatalf("warning count = %d, want 1: %s", got, logs.String())
	}
	if strings.Contains(logs.String(), legacyKey) {
		t.Fatalf("warning exposed credential: %s", logs.String())
	}
}

func TestDiscoverCachesProviderPageAndIsolatesCallerState(t *testing.T) {
	const firstAccount = "11111111-1111-4111-8111-111111111111"
	const secondAccount = "22222222-2222-4222-8222-222222222222"
	provider := &serviceTestProvider{}
	states := &serviceTestStates{byAccount: map[string]core.RequestStatus{
		firstAccount: core.RequestPending, secondAccount: core.RequestDeclined,
	}}
	store := &serviceTestStore{record: core.MetadataProviderRecord{
		Kind: core.MetadataProviderTMDB, CredentialCiphertext: []byte(serviceTestReadAccessToken), KeyID: "test-key",
	}}
	factory := &serviceTestFactory{provider: provider}
	service := newServiceTestServiceWithStates(t, store, factory, states, slog.New(slog.DiscardHandler))
	input := core.MetadataDiscover{List: core.MetadataTrending, Page: 1}
	first, firstErr := service.Discover(t.Context(), firstAccount, input)
	second, secondErr := service.Discover(t.Context(), secondAccount, input)
	if firstErr != nil || len(first.Items) != 1 || first.Items[0].RequestState != core.MetadataRequestPending {
		t.Fatalf("first Discover = %+v, %v", first, firstErr)
	}
	if secondErr != nil || len(second.Items) != 1 || second.Items[0].RequestState != core.MetadataRequestDeclined {
		t.Fatalf("second Discover = %+v, %v", second, secondErr)
	}
	if provider.discoverCalls.Load() != 1 || states.callCount() != 2 {
		t.Fatalf("calls = provider %d states %d, want 1 and 2", provider.discoverCalls.Load(), states.callCount())
	}
}

func TestGenresCachesProviderList(t *testing.T) {
	provider := &serviceTestProvider{}
	store := &serviceTestStore{record: core.MetadataProviderRecord{
		Kind: core.MetadataProviderTMDB, CredentialCiphertext: []byte(serviceTestReadAccessToken), KeyID: "test-key",
	}}
	service := newServiceTestServiceWithStates(
		t, store, &serviceTestFactory{provider: provider}, &serviceTestStates{}, slog.New(slog.DiscardHandler),
	)
	for range 2 {
		genres, err := service.Genres(t.Context(), core.MediaKindMovie)
		if err != nil || len(genres) != 1 || genres[0].Name != "Action" {
			t.Fatalf("Genres = %+v, %v", genres, err)
		}
	}
	if provider.genreCalls.Load() != 1 {
		t.Fatalf("genre provider calls = %d, want 1", provider.genreCalls.Load())
	}
}

func TestConcurrentCacheMissesCollapseProviderLoads(t *testing.T) {
	t.Run("discovery", func(t *testing.T) {
		entered, release := make(chan struct{}, 8), make(chan struct{})
		provider := &serviceTestProvider{discoverEntered: entered, release: release}
		service := newCachingServiceTestService(t, provider)
		input := core.MetadataDiscover{List: core.MetadataTrending, Page: 1}
		call := func() error {
			_, err := service.Discover(t.Context(), "11111111-1111-4111-8111-111111111111", input)
			return err
		}
		assertConcurrentLoadCollapsed(t, entered, release, call)
		if provider.discoverCalls.Load() != 1 {
			t.Fatalf("discovery provider calls = %d, want 1", provider.discoverCalls.Load())
		}
	})
	t.Run("genres", func(t *testing.T) {
		entered, release := make(chan struct{}, 8), make(chan struct{})
		provider := &serviceTestProvider{genreEntered: entered, release: release}
		service := newCachingServiceTestService(t, provider)
		call := func() error {
			_, err := service.Genres(t.Context(), core.MediaKindMovie)
			return err
		}
		assertConcurrentLoadCollapsed(t, entered, release, call)
		if provider.genreCalls.Load() != 1 {
			t.Fatalf("genre provider calls = %d, want 1", provider.genreCalls.Load())
		}
	})
}

func assertConcurrentLoadCollapsed(
	t *testing.T, entered <-chan struct{}, release chan struct{}, call func() error,
) {
	t.Helper()
	const callers = 8
	start := make(chan struct{})
	results := make(chan error, callers)
	for range callers {
		go func() {
			<-start
			results <- call()
		}()
	}
	close(start)
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("provider load did not start")
	}
	duplicate := false
	select {
	case <-entered:
		duplicate = true
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	for range callers {
		if err := <-results; err != nil {
			t.Fatalf("concurrent cached load: %v", err)
		}
	}
	if duplicate {
		t.Fatal("duplicate provider load started for one cache key")
	}
}

func newCachingServiceTestService(t *testing.T, provider *serviceTestProvider) *Service {
	t.Helper()
	store := &serviceTestStore{record: core.MetadataProviderRecord{
		Kind: core.MetadataProviderTMDB, CredentialCiphertext: []byte(serviceTestReadAccessToken), KeyID: "test-key",
	}}
	return newServiceTestServiceWithStates(
		t, store, &serviceTestFactory{provider: provider}, &serviceTestStates{}, slog.New(slog.DiscardHandler),
	)
}

func newServiceTestService(
	t *testing.T, store *serviceTestStore, factory *serviceTestFactory, logger *slog.Logger,
) *Service {
	t.Helper()
	return newServiceTestServiceWithStates(t, store, factory, &serviceTestStates{}, logger)
}

func newServiceTestServiceWithStates(
	t *testing.T, store *serviceTestStore, factory *serviceTestFactory,
	states core.MetadataRequestStateReader, logger *slog.Logger,
) *Service {
	t.Helper()
	clock := testutil.NewFakeClock(time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC))
	service, err := NewService(store, store, states, serviceTestCipher{}, factory, clock, logger)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return service
}
