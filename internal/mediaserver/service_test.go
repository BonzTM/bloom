package mediaserver

import (
	"context"
	"crypto/sha256"
	"errors"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/secrets"
	"github.com/BonzTM/bloom/internal/testutil"
)

type memoryStore struct {
	mu          sync.Mutex
	records     []core.MediaServerRecord
	afterCreate func(core.MediaServerRecord)
	createErr   error
	getErr      error
	listErr     error
	deleteErr   error
	deadlines   atomic.Int32
	creates     atomic.Int32
}

func (s *memoryStore) CreateMediaServer(ctx context.Context, record core.MediaServerRecord) error {
	s.recordDeadline(ctx)
	s.creates.Add(1)
	if s.createErr != nil {
		return s.createErr
	}
	s.mu.Lock()
	s.records = append(s.records, record)
	s.mu.Unlock()
	if s.afterCreate != nil {
		s.afterCreate(record)
	}
	return nil
}

func (s *memoryStore) GetMediaServer(ctx context.Context, id string) (core.MediaServerRecord, error) {
	s.recordDeadline(ctx)
	if s.getErr != nil {
		return core.MediaServerRecord{}, s.getErr
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, record := range s.records {
		if record.ID == id {
			return record, nil
		}
	}
	return core.MediaServerRecord{}, core.ErrNotFound
}

func (s *memoryStore) ListMediaServers(ctx context.Context, _ string, _ int) ([]core.MediaServer, error) {
	s.recordDeadline(ctx)
	if s.listErr != nil {
		return nil, s.listErr
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	servers := make([]core.MediaServer, 0, len(s.records))
	for _, record := range s.records {
		servers = append(servers, record.MediaServer)
	}
	return servers, nil
}

func (s *memoryStore) DeleteMediaServer(ctx context.Context, id string) error {
	s.recordDeadline(ctx)
	if s.deleteErr != nil {
		return s.deleteErr
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for index, record := range s.records {
		if record.ID == id {
			s.records = slices.Delete(s.records, index, index+1)
			return nil
		}
	}
	return core.ErrNotFound
}

func (s *memoryStore) recordCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.records)
}

func (s *memoryStore) recordDeadline(ctx context.Context) {
	if _, ok := ctx.Deadline(); ok {
		s.deadlines.Add(1)
	}
}

type fakeAdapter struct {
	probeErr     error
	librariesErr error
	deadlineSeen atomic.Bool
	afterProbe   func()
	started      chan<- struct{}
	release      <-chan struct{}
	probeCalls   atomic.Int32
	libraryCalls atomic.Int32
	closeCalls   atomic.Int32
}

func (a *fakeAdapter) Probe(ctx context.Context) (core.ServerInfo, error) {
	a.probeCalls.Add(1)
	_, deadlineSeen := ctx.Deadline()
	a.deadlineSeen.Store(deadlineSeen)
	if a.afterProbe != nil {
		a.afterProbe()
	}
	if a.started != nil {
		a.started <- struct{}{}
		<-a.release
	}
	return core.ServerInfo{Name: "Jellyfin", Version: "12.1.0", ID: "jf-1"}, a.probeErr
}

func (a *fakeAdapter) ListLibraries(ctx context.Context) ([]core.Library, error) {
	a.libraryCalls.Add(1)
	_, deadlineSeen := ctx.Deadline()
	a.deadlineSeen.Store(deadlineSeen)
	return []core.Library{{ID: "lib-1", Name: "Movies"}}, a.librariesErr
}

func (*fakeAdapter) Capabilities() core.Capabilities {
	return core.Capabilities{CreateUserWithPassword: true}
}

func (a *fakeAdapter) CloseIdleConnections() { a.closeCalls.Add(1) }

type fakeFactory struct {
	adapter       *fakeAdapter
	newErr        error
	capabilityErr error
	newCalls      *atomic.Int32
	newStarted    chan<- struct{}
	newRelease    <-chan struct{}
}

type sequenceFactory struct {
	mu       sync.Mutex
	adapters []*fakeAdapter
}

func (f *sequenceFactory) New(kind core.MediaServerKind, _, _ string, _ bool) (core.MediaServerAdapter, error) {
	if !kind.Valid() {
		return nil, core.ErrInvalidArgument
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.adapters) == 0 {
		return nil, errors.New("adapter sequence exhausted")
	}
	adapter := f.adapters[0]
	f.adapters = f.adapters[1:]
	return adapter, nil
}

func (*sequenceFactory) Capabilities(core.MediaServerKind) (core.Capabilities, error) {
	return core.Capabilities{CreateUserWithPassword: true}, nil
}

func (f fakeFactory) New(kind core.MediaServerKind, _, _ string, _ bool) (core.MediaServerAdapter, error) {
	if f.newCalls != nil {
		f.newCalls.Add(1)
	}
	if f.newStarted != nil {
		f.newStarted <- struct{}{}
		<-f.newRelease
	}
	if !kind.Valid() {
		return nil, core.ErrInvalidArgument
	}
	if f.newErr != nil {
		return nil, f.newErr
	}
	return f.adapter, nil
}

func (f fakeFactory) Capabilities(kind core.MediaServerKind) (core.Capabilities, error) {
	if !kind.Valid() {
		return core.Capabilities{}, core.ErrInvalidArgument
	}
	if f.capabilityErr != nil {
		return core.Capabilities{}, f.capabilityErr
	}
	return core.Capabilities{CreateUserWithPassword: true}, nil
}

func TestRegisterProbesBeforeSavingAndEncryptsCredential(t *testing.T) {
	store := &memoryStore{}
	cipher, err := secrets.New([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatalf("secrets.New: %v", err)
	}
	clock := testutil.NewFakeClock(time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC))
	adapter := &fakeAdapter{}
	service, err := NewService(store, store, cipher, fakeFactory{adapter: adapter}, clock)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	connection, err := service.Register(context.Background(), core.MediaServerKindJellyfin, "Home", "https://media.example.test/", "plaintext-key", false)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if connection.Server.BaseURL != "https://media.example.test" || len(store.records) != 1 {
		t.Fatalf("connection=%+v records=%d", connection, len(store.records))
	}
	ciphertext := store.records[0].CredentialCiphertext
	if string(ciphertext) == "plaintext-key" {
		t.Fatal("credential was stored in plaintext")
	}
	plaintext, err := cipher.Decrypt(ciphertext, credentialContext(store.records[0].MediaServer))
	if err != nil || string(plaintext) != "plaintext-key" {
		t.Fatalf("Decrypt = %q, %v", plaintext, err)
	}
	adapter.probeErr = &core.MediaServerError{Kind: core.MediaServerUnauthorized, Operation: "probe"}
	if _, err := service.Register(context.Background(), core.MediaServerKindJellyfin, "Other", "https://other.example.test", "bad", false); err == nil {
		t.Fatal("Register with failed probe succeeded")
	}
	if len(store.records) != 1 {
		t.Fatalf("failed probe persisted a record: %d", len(store.records))
	}
}

func TestRegisterRejectsInsecureOverrideForHTTPS(t *testing.T) {
	store, service := newTestService(t, fakeFactory{adapter: &fakeAdapter{}})
	_, err := service.Register(
		context.Background(), core.MediaServerKindJellyfin, "Home",
		"https://media.example.test/", "key", true,
	)
	if !errors.Is(err, core.ErrInvalidArgument) {
		t.Fatalf("Register error = %v, want ErrInvalidArgument", err)
	}
	if len(store.records) != 0 {
		t.Fatalf("rejected registration stored %d records", len(store.records))
	}
}

func TestRegistryRejectsUnknownKind(t *testing.T) {
	registry := NewRegistry("dev", "33333333-3333-4333-8333-333333333333", nil)
	if _, err := registry.New("plex", "https://example.test", "key", false); !errors.Is(err, core.ErrInvalidArgument) {
		t.Fatalf("New(plex) = %v, want ErrInvalidArgument", err)
	}
}

func TestServiceBoundaryFailuresRemainMatchable(t *testing.T) {
	sentinel := errors.New("dependency failure")
	tests := []struct {
		name string
		run  func(*testing.T, *memoryStore, *Service) error
	}{
		{name: "create store", run: func(t *testing.T, store *memoryStore, service *Service) error {
			t.Helper()
			store.createErr = sentinel
			_, err := service.Register(context.Background(), core.MediaServerKindJellyfin, "Home", "https://media.example.test", "key", false)
			return err
		}},
		{name: "list store", run: func(_ *testing.T, store *memoryStore, service *Service) error {
			store.listErr = sentinel
			_, err := service.List(context.Background(), "", 10)
			return err
		}},
		{name: "get store", run: func(_ *testing.T, store *memoryStore, service *Service) error {
			store.getErr = sentinel
			_, err := service.Get(context.Background(), "id")
			return err
		}},
		{name: "libraries adapter", run: func(t *testing.T, store *memoryStore, service *Service) error {
			t.Helper()
			factory, ok := service.factory.(fakeFactory)
			if !ok {
				t.Fatal("service factory has unexpected type")
			}
			factory.adapter.librariesErr = sentinel
			seedEncryptedRecord(t, store, service)
			_, err := service.Libraries(context.Background(), store.records[0].ID)
			return err
		}},
		{name: "delete store", run: func(t *testing.T, store *memoryStore, service *Service) error {
			t.Helper()
			seedEncryptedRecord(t, store, service)
			store.deleteErr = sentinel
			_, err := service.Delete(context.Background(), store.records[0].ID)
			return err
		}},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			store, service := newTestService(t, fakeFactory{adapter: &fakeAdapter{}})
			if err := testCase.run(t, store, service); !errors.Is(err, sentinel) {
				t.Fatalf("error = %v, want sentinel", err)
			}
		})
	}
}

func TestServiceCipherAndFactoryFailuresRemainMatchable(t *testing.T) {
	sentinel := errors.New("dependency failure")
	store := &memoryStore{}
	clock := testutil.NewFakeClock(time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC))
	service, err := NewService(store, store, failingCipher{encryptErr: sentinel}, fakeFactory{adapter: &fakeAdapter{}}, clock)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	if _, err := service.Register(context.Background(), core.MediaServerKindJellyfin, "Home", "https://media.example.test", "key", false); !errors.Is(err, sentinel) {
		t.Fatalf("Register encryption error = %v", err)
	}
	_, service = newTestService(t, fakeFactory{adapter: &fakeAdapter{}, newErr: sentinel})
	if _, err := service.Register(context.Background(), core.MediaServerKindJellyfin, "Home", "https://media.example.test", "key", false); !errors.Is(err, sentinel) {
		t.Fatalf("Register factory error = %v", err)
	}
}

func TestServiceLoadedRecordFailuresRemainMatchable(t *testing.T) {
	sentinel := errors.New("dependency failure")
	store, seeder := newTestService(t, fakeFactory{adapter: &fakeAdapter{}})
	seedEncryptedRecord(t, store, seeder)
	id := store.records[0].ID
	tests := []struct {
		name    string
		factory fakeFactory
		cipher  credentialCipher
		run     func(*Service) error
	}{
		{
			name: "factory failure", factory: fakeFactory{adapter: &fakeAdapter{}, newErr: sentinel}, cipher: seeder.cipher,
			run: func(service *Service) error { _, err := service.Probe(context.Background(), id); return err },
		},
		{
			name: "decryption failure", factory: fakeFactory{adapter: &fakeAdapter{}},
			cipher: failingCipher{decryptErr: sentinel},
			run:    func(service *Service) error { _, err := service.Probe(context.Background(), id); return err },
		},
		{
			name: "list capabilities", factory: fakeFactory{adapter: &fakeAdapter{}, capabilityErr: sentinel}, cipher: seeder.cipher,
			run: func(service *Service) error { _, err := service.List(context.Background(), "", 10); return err },
		},
		{
			name: "get capabilities", factory: fakeFactory{adapter: &fakeAdapter{}, capabilityErr: sentinel}, cipher: seeder.cipher,
			run: func(service *Service) error { _, err := service.Get(context.Background(), id); return err },
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			service, err := NewService(store, store, testCase.cipher, testCase.factory, seeder.clock)
			if err != nil {
				t.Fatalf("NewService: %v", err)
			}
			if err := testCase.run(service); !errors.Is(err, sentinel) {
				t.Fatalf("error = %v, want sentinel", err)
			}
		})
	}
}

func TestServiceReusesAndInvalidatesRegisteredAdapter(t *testing.T) {
	adapter := &fakeAdapter{}
	var newCalls atomic.Int32
	store, service := newTestService(t, fakeFactory{adapter: adapter, newCalls: &newCalls})
	seedEncryptedRecord(t, store, service)
	record := store.records[0]
	if _, err := service.Probe(context.Background(), record.ID); err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if _, err := service.Libraries(context.Background(), record.ID); err != nil {
		t.Fatalf("Libraries: %v", err)
	}
	if got := newCalls.Load(); got != 1 {
		t.Fatalf("adapter constructions = %d, want 1", got)
	}
	if _, err := service.Delete(context.Background(), record.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if got := adapter.closeCalls.Load(); got != 1 {
		t.Fatalf("idle closes after delete = %d, want 1", got)
	}
	store.records = append(store.records, record)
	if _, err := service.Probe(context.Background(), record.ID); err != nil {
		t.Fatalf("Probe after reinsert: %v", err)
	}
	if got := newCalls.Load(); got != 2 {
		t.Fatalf("adapter constructions after invalidation = %d, want 2", got)
	}
	service.CloseIdleConnections()
	if got := adapter.closeCalls.Load(); got != 2 {
		t.Fatalf("idle closes after shutdown = %d, want 2", got)
	}
}

func TestServiceBulkheadRejectsExcessWithoutOutboundCall(t *testing.T) {
	started := make(chan struct{}, maxConcurrentCallsPerServer)
	release := make(chan struct{})
	adapter := &fakeAdapter{}
	store, service := newTestService(t, fakeFactory{adapter: adapter})
	seedEncryptedRecord(t, store, service)
	id := store.records[0].ID
	adapter.started, adapter.release = started, release
	errs := make(chan error, maxConcurrentCallsPerServer)
	for range maxConcurrentCallsPerServer {
		go func() {
			_, err := service.Probe(context.Background(), id)
			errs <- err
		}()
	}
	for range maxConcurrentCallsPerServer {
		<-started
	}
	_, err := service.Libraries(context.Background(), id)
	var mediaErr *core.MediaServerError
	if !errors.As(err, &mediaErr) || mediaErr.Kind != core.MediaServerSaturated ||
		!mediaErr.Retryable || mediaErr.RetryAfter != bulkheadRetryAfter {
		t.Fatalf("excess request error = %#v", err)
	}
	if got := adapter.libraryCalls.Load(); got != 0 {
		t.Fatalf("excess request started %d outbound calls", got)
	}
	close(release)
	for range maxConcurrentCallsPerServer {
		if err := <-errs; err != nil {
			t.Errorf("admitted Probe: %v", err)
		}
	}
}

func TestServiceBulkheadSurvivesConcurrentTTLExpiry(t *testing.T) {
	retired, replacement := &fakeAdapter{}, &fakeAdapter{}
	store, service, clock := newSequenceService(t, retired, replacement)
	seedEncryptedRecord(t, store, service)
	id := store.records[0].ID
	errs, release := startBlockedProbes(t, service, id, retired)
	clock.Advance(adapterCacheTTL)
	assertSaturatedProbe(t, service, id)
	assertRetiredClientState(t, retired, replacement, 0)
	finishBlockedProbes(t, errs, release)
	assertRetiredClientState(t, retired, replacement, 1)
}

func TestServiceBulkheadSurvivesConcurrentLRUEviction(t *testing.T) {
	retired, replacement := &fakeAdapter{}, &fakeAdapter{}
	store, service, _ := newSequenceService(t, retired, replacement)
	service.adapters.capacity = 1
	seedEncryptedRecord(t, store, service)
	id := store.records[0].ID
	errs, release := startBlockedProbes(t, service, id, retired)
	service.adapters.put("other", newAdapterEntry(&fakeAdapter{}, [sha256.Size]byte{2}))
	assertSaturatedProbe(t, service, id)
	assertRetiredClientState(t, retired, replacement, 0)
	finishBlockedProbes(t, errs, release)
	assertRetiredClientState(t, retired, replacement, 1)
}

func startBlockedProbes(
	t *testing.T, service *Service, id string, adapter *fakeAdapter,
) (<-chan error, chan struct{}) {
	t.Helper()
	started := make(chan struct{}, maxConcurrentCallsPerServer)
	release := make(chan struct{})
	adapter.started, adapter.release = started, release
	errs := make(chan error, maxConcurrentCallsPerServer)
	for range maxConcurrentCallsPerServer {
		go func() { _, err := service.Probe(context.Background(), id); errs <- err }()
	}
	for range maxConcurrentCallsPerServer {
		<-started
	}
	return errs, release
}

func assertSaturatedProbe(t *testing.T, service *Service, id string) {
	t.Helper()
	_, err := service.Probe(context.Background(), id)
	var mediaErr *core.MediaServerError
	if !errors.As(err, &mediaErr) || mediaErr.Kind != core.MediaServerSaturated {
		t.Fatalf("fifth Probe error = %#v, want saturation", err)
	}
}

func assertRetiredClientState(t *testing.T, retired, replacement *fakeAdapter, wantCloses int32) {
	t.Helper()
	if got := replacement.probeCalls.Load(); got != 0 {
		t.Fatalf("replacement outbound calls = %d, want zero", got)
	}
	if got := retired.closeCalls.Load(); got != wantCloses {
		t.Fatalf("retired adapter closes = %d, want %d", got, wantCloses)
	}
}

func finishBlockedProbes(t *testing.T, errs <-chan error, release chan struct{}) {
	t.Helper()
	close(release)
	for range maxConcurrentCallsPerServer {
		if err := <-errs; err != nil {
			t.Errorf("admitted Probe: %v", err)
		}
	}
}

func TestServiceRegistrationBulkheadRejectsFifthWithoutProbeOrPersistence(t *testing.T) {
	started := make(chan struct{}, maxConcurrentRegistrations)
	release := make(chan struct{})
	adapter := &fakeAdapter{started: started, release: release}
	store, service := newTestService(t, fakeFactory{adapter: adapter})
	errs := make(chan error, maxConcurrentRegistrations)
	for index := range maxConcurrentRegistrations {
		go func() {
			_, err := service.Register(
				context.Background(), core.MediaServerKindJellyfin,
				"Server "+string(rune('A'+index)), "https://media.example.test", "key", false,
			)
			errs <- err
		}()
	}
	for range maxConcurrentRegistrations {
		<-started
	}
	_, err := service.Register(
		context.Background(), core.MediaServerKindJellyfin,
		"Overflow", "https://overflow.example.test", "key", false,
	)
	var mediaErr *core.MediaServerError
	if !errors.As(err, &mediaErr) || mediaErr.Kind != core.MediaServerSaturated {
		t.Fatalf("fifth Register error = %#v, want saturation", err)
	}
	if got := adapter.probeCalls.Load(); got != maxConcurrentRegistrations {
		t.Fatalf("probe calls = %d, want %d", got, maxConcurrentRegistrations)
	}
	if got := store.creates.Load(); got != 0 || store.recordCount() != 0 {
		t.Fatalf("persistence attempts = %d, records = %d, want zero", got, store.recordCount())
	}
	close(release)
	for range maxConcurrentRegistrations {
		if err := <-errs; err != nil {
			t.Errorf("admitted Register: %v", err)
		}
	}
}

func TestServiceDeletionInvalidatesAdapterConstruction(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store, seeder := newTestService(t, fakeFactory{adapter: &fakeAdapter{}})
		seedEncryptedRecord(t, store, seeder)
		record := store.records[0]
		adapter := &fakeAdapter{}
		started := make(chan struct{}, 1)
		release := make(chan struct{})
		service, err := NewService(store, store, seeder.cipher, fakeFactory{
			adapter: adapter, newStarted: started, newRelease: release,
		}, seeder.clock)
		if err != nil {
			t.Fatalf("NewService: %v", err)
		}
		probeResult := make(chan error, 1)
		go func() {
			_, probeErr := service.Probe(context.Background(), record.ID)
			probeResult <- probeErr
		}()
		<-started
		deleteResult := make(chan error, 1)
		go func() {
			_, deleteErr := service.Delete(context.Background(), record.ID)
			deleteResult <- deleteErr
		}()
		synctest.Wait()
		select {
		case deleteErr := <-deleteResult:
			if deleteErr != nil {
				t.Fatalf("Delete: %v", deleteErr)
			}
		default:
			t.Fatal("Delete blocked behind adapter construction")
		}
		assertLimiterState(t, service.adapters, record.ID, true, 1, true)
		close(release)
		if probeErr := <-probeResult; !errors.Is(probeErr, core.ErrNotFound) {
			t.Fatalf("Probe error = %v, want ErrNotFound", probeErr)
		}
		if got := adapter.closeCalls.Load(); got != 1 {
			t.Fatalf("stale adapter closes = %d, want 1", got)
		}
		assertLimiterState(t, service.adapters, record.ID, false, 0, false)
		requireCacheMiss(t, service.adapters, record.ID, recordFingerprint(record))
	})
}

func TestRegistrationDeletionBeforePublicationRejectsAdapter(t *testing.T) {
	created := make(chan core.MediaServerRecord, 1)
	releaseCreate := make(chan struct{})
	adapter := &fakeAdapter{}
	store, service := newTestService(t, fakeFactory{adapter: adapter})
	store.afterCreate = func(record core.MediaServerRecord) {
		created <- record
		<-releaseCreate
	}
	registerResult := make(chan error, 1)
	go func() {
		_, err := service.Register(
			context.Background(), core.MediaServerKindJellyfin, "Home",
			"https://media.example.test", "key", false,
		)
		registerResult <- err
	}()
	record := <-created
	if _, err := service.Delete(context.Background(), record.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	close(releaseCreate)
	if err := <-registerResult; !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("Register error = %v, want ErrNotFound", err)
	}
	if got := adapter.closeCalls.Load(); got != 1 {
		t.Fatalf("rejected adapter closes = %d, want 1", got)
	}
	assertCacheAbsent(t, service.adapters, record.ID)
	assertLimiterState(t, service.adapters, record.ID, false, 0, false)
}

func assertCacheAbsent(t *testing.T, cache *adapterCache, id string) {
	t.Helper()
	cache.mu.Lock()
	_, exists := cache.entries[id]
	cache.mu.Unlock()
	if exists {
		t.Fatalf("cache entry %q exists, want absent", id)
	}
}

func TestServiceRejectsDuplicateNameAndDecryptFailure(t *testing.T) {
	adapter := &fakeAdapter{}
	store, service := newTestService(t, fakeFactory{adapter: adapter})
	store.createErr = core.ErrAlreadyExists
	if _, err := service.Register(context.Background(), core.MediaServerKindJellyfin, "Home", "https://media.example.test", "key", false); !errors.Is(err, core.ErrAlreadyExists) {
		t.Fatalf("Register duplicate = %v", err)
	}
	store.createErr = nil
	seedEncryptedRecord(t, store, service)
	closesBeforeChange := adapter.closeCalls.Load()
	store.records[0].BaseURL = "https://edited.example.test"
	if _, err := service.Probe(context.Background(), store.records[0].ID); !errors.Is(err, secrets.ErrAuthentication) {
		t.Fatalf("Probe edited metadata = %v, want authentication failure", err)
	}
	if got := adapter.closeCalls.Load(); got != closesBeforeChange+1 {
		t.Fatalf("idle closes after configuration change = %d, want %d", got, closesBeforeChange+1)
	}
}

func TestServiceDerivesDeadlinesAndSkipsExpiredInsert(t *testing.T) {
	store, service := newTestService(t, fakeFactory{adapter: &fakeAdapter{}})
	if _, err := service.Register(context.Background(), core.MediaServerKindJellyfin, "Home", "https://media.example.test", "key", false); err != nil {
		t.Fatalf("Register: %v", err)
	}
	factory, ok := service.factory.(fakeFactory)
	if !ok {
		t.Fatal("service factory has unexpected type")
	}
	adapter := factory.adapter
	if !adapter.deadlineSeen.Load() || store.deadlines.Load() == 0 {
		t.Fatalf("deadline adapter=%t store=%d", adapter.deadlineSeen.Load(), store.deadlines.Load())
	}
	cancelCtx, cancel := context.WithCancel(context.Background())
	adapter.afterProbe = cancel
	before := len(store.records)
	if _, err := service.Register(cancelCtx, core.MediaServerKindJellyfin, "Other", "https://other.example.test", "key", false); !errors.Is(err, context.Canceled) {
		t.Fatalf("Register expired = %v", err)
	}
	if len(store.records) != before {
		t.Fatal("registration inserted after total budget expired")
	}
}

type failingCipher struct {
	encryptErr error
	decryptErr error
}

func (c failingCipher) Encrypt([]byte, secrets.Context) ([]byte, error) { return nil, c.encryptErr }
func (c failingCipher) Decrypt([]byte, secrets.Context) ([]byte, error) { return nil, c.decryptErr }

func newTestService(t *testing.T, factory fakeFactory) (*memoryStore, *Service) {
	t.Helper()
	store := &memoryStore{}
	cipher, err := secrets.New([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatalf("secrets.New: %v", err)
	}
	clock := testutil.NewFakeClock(time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC))
	service, err := NewService(store, store, cipher, factory, clock)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return store, service
}

func newSequenceService(
	t *testing.T, adapters ...*fakeAdapter,
) (*memoryStore, *Service, *testutil.FakeClock) {
	t.Helper()
	store := &memoryStore{}
	cipher, err := secrets.New([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatalf("secrets.New: %v", err)
	}
	clock := testutil.NewFakeClock(time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC))
	service, err := NewService(store, store, cipher, &sequenceFactory{adapters: adapters}, clock)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return store, service, clock
}

func seedEncryptedRecord(t *testing.T, store *memoryStore, service *Service) {
	t.Helper()
	if len(store.records) > 0 {
		return
	}
	if _, err := service.Register(context.Background(), core.MediaServerKindJellyfin, "Home", "https://media.example.test", "key", false); err != nil {
		t.Fatalf("seed Register: %v", err)
	}
}
