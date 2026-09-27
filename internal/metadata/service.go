// Package metadata coordinates provider credentials, adapters, and bounded detail caching.
package metadata

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/secrets"
)

const (
	credentialPurpose       = "metadata-provider-api-key"
	providerBaseURL         = "https://api.themoviedb.org"
	cacheCapacity           = 512
	cacheTTL                = 15 * time.Minute
	genreCacheTTL           = 24 * time.Hour
	providerWarningInterval = time.Minute
)

type credentialCipher interface {
	Encrypt([]byte, secrets.Context) ([]byte, error)
	Decrypt([]byte, secrets.Context) ([]byte, error)
	KeyID() string
}

type providerFactory interface {
	New(kind core.MetadataProviderKind, credential string) (core.MetadataProvider, error)
	Probe(ctx context.Context, kind core.MetadataProviderKind, credential string) error
}

type idleCloser interface{ CloseIdleConnections() }

// Service coordinates encrypted provider configuration and cached metadata reads.
type Service struct {
	reader      core.MetadataProviderReader
	writer      core.MetadataProviderWriter
	states      core.MetadataRequestStateReader
	cipher      credentialCipher
	factory     providerFactory
	clock       core.Clock
	logger      *slog.Logger
	cache       *detailCache
	loads       singleflight.Group
	mu          sync.Mutex
	warnInvalid sync.Once
	warnMu      sync.Mutex
	warnedAt    [4]time.Time
	warned      [4]bool
	provider    core.MetadataProvider
	fingerprint [sha256.Size]byte
}

// NewService creates a metadata service with explicit storage and clock dependencies.
func NewService(
	reader core.MetadataProviderReader, writer core.MetadataProviderWriter, states core.MetadataRequestStateReader, cipher credentialCipher,
	factory providerFactory, clock core.Clock, logger *slog.Logger,
) (*Service, error) {
	if reader == nil || writer == nil || states == nil || cipher == nil || factory == nil || clock == nil || logger == nil {
		return nil, errors.New("metadata service: all dependencies are required")
	}
	return &Service{
		reader: reader, writer: writer, states: states, cipher: cipher, factory: factory, clock: clock, logger: logger,
		cache: newDetailCache(clock, cacheCapacity, cacheTTL),
	}, nil
}

// SetKey encrypts and stores a provider credential without retaining plaintext.
func (s *Service) SetKey(ctx context.Context, kind core.MetadataProviderKind, credential string) error {
	if !kind.Valid() || core.ValidateMetadataCredential(kind, credential) != nil {
		return core.ErrInvalidArgument
	}
	if err := s.factory.Probe(ctx, kind, credential); err != nil {
		return s.providerError(ctx, "probe metadata provider credential", err)
	}
	now := core.NormalizeTime(s.clock.Now())
	existing, err := s.reader.GetMetadataProvider(ctx, kind)
	createdAt := now
	if err == nil {
		createdAt = existing.CreatedAt
	} else if !errors.Is(err, core.ErrNotFound) {
		return fmt.Errorf("read metadata provider before update: %w", err)
	}
	context := credentialContext(kind)
	ciphertext, err := s.cipher.Encrypt([]byte(credential), context)
	if err != nil {
		return fmt.Errorf("encrypt metadata provider credential: %w", err)
	}
	record := core.MetadataProviderRecord{
		Kind: kind, CredentialCiphertext: ciphertext,
		KeyID: s.cipher.KeyID(), CreatedAt: createdAt, UpdatedAt: now,
	}
	if err := s.writer.UpsertMetadataProvider(ctx, record); err != nil {
		return fmt.Errorf("store metadata provider: %w", err)
	}
	s.resetProvider()
	return nil
}

// HasKey reports credential presence without returning credential material.
func (s *Service) HasKey(ctx context.Context, kind core.MetadataProviderKind) (bool, error) {
	if !kind.Valid() {
		return false, core.ErrInvalidArgument
	}
	record, err := s.reader.GetMetadataProvider(ctx, kind)
	if errors.Is(err, core.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read metadata provider: %w", err)
	}
	credential, err := s.decryptCredential(ctx, record, kind)
	defer clear(credential)
	if errors.Is(err, core.ErrMetadataNotConfigured) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// RemoveKey deletes a provider credential and clears provider state.
func (s *Service) RemoveKey(ctx context.Context, kind core.MetadataProviderKind) error {
	if !kind.Valid() {
		return core.ErrInvalidArgument
	}
	if err := s.writer.DeleteMetadataProvider(ctx, kind); err != nil {
		return fmt.Errorf("delete metadata provider: %w", err)
	}
	s.resetProvider()
	return nil
}

// Search returns movie and series matches from the configured provider.
func (s *Service) Search(ctx context.Context, input core.MetadataSearch) ([]core.MetadataTitle, error) {
	provider, err := s.loadProvider(ctx, core.MetadataProviderTMDB)
	if err != nil {
		return nil, err
	}
	results, err := provider.Search(ctx, input)
	if err != nil {
		return nil, s.providerError(ctx, "search metadata", err)
	}
	return results, nil
}

// Movie returns cached or upstream movie details.
func (s *Service) Movie(ctx context.Context, providerID string) (core.MetadataTitle, error) {
	key := cacheKey{provider: core.MetadataProviderTMDB, id: providerID, kind: core.MediaKindMovie}
	if value, ok := s.cache.get(key); ok {
		return value.title, nil
	}
	provider, err := s.loadProvider(ctx, key.provider)
	if err != nil {
		return core.MetadataTitle{}, err
	}
	title, err := provider.Movie(ctx, providerID)
	if err != nil {
		return core.MetadataTitle{}, s.providerError(ctx, "load movie metadata", err)
	}
	s.cache.put(key, cacheValue{title: title})
	return title, nil
}

// Series returns cached or upstream series details with optional specials.
func (s *Service) Series(ctx context.Context, providerID string, includeSpecials bool) (core.MetadataSeries, error) {
	key := cacheKey{provider: core.MetadataProviderTMDB, id: providerID, kind: core.MediaKindSeries}
	if value, ok := s.cache.get(key); ok {
		return filterSpecials(value.series, includeSpecials), nil
	}
	provider, err := s.loadProvider(ctx, key.provider)
	if err != nil {
		return core.MetadataSeries{}, err
	}
	series, err := provider.Series(ctx, providerID, true)
	if err != nil {
		return core.MetadataSeries{}, s.providerError(ctx, "load series metadata", err)
	}
	s.cache.put(key, cacheValue{series: series})
	return filterSpecials(series, includeSpecials), nil
}

// Discover returns a cached provider page enriched with the caller's current request state.
func (s *Service) Discover(
	ctx context.Context, accountID string, input core.MetadataDiscover,
) (core.MetadataDiscoverPage, error) {
	if !core.ValidID(accountID) || core.ValidateMetadataDiscover(input) != nil {
		return core.MetadataDiscoverPage{}, core.ErrInvalidArgument
	}
	page, err := s.discoverPage(ctx, input)
	if err != nil {
		return core.MetadataDiscoverPage{}, err
	}
	states, err := s.states.MetadataRequestStates(ctx, accountID, page.Items)
	if err != nil {
		return core.MetadataDiscoverPage{}, fmt.Errorf("load metadata request states: %w", err)
	}
	return discoverPageWithStates(page, states), nil
}

func (s *Service) discoverPage(ctx context.Context, input core.MetadataDiscover) (core.MetadataPage, error) {
	key := cacheKey{
		provider: core.MetadataProviderTMDB, resource: "discover", id: string(input.List) + ":" + strconv.Itoa(input.Page),
	}
	value, err := s.cachedLoad(key, cacheTTL, func() (cacheValue, error) {
		provider, providerErr := s.discoveryProvider(ctx)
		if providerErr != nil {
			return cacheValue{}, providerErr
		}
		page, discoverErr := provider.Discover(ctx, input)
		if discoverErr != nil {
			return cacheValue{}, s.providerError(ctx, "discover metadata", discoverErr)
		}
		return cacheValue{page: page}, nil
	})
	if err != nil {
		return core.MetadataPage{}, err
	}
	return value.page, nil
}

func discoverPageWithStates(
	page core.MetadataPage, states map[core.MetadataTitleKey]core.RequestStatus,
) core.MetadataDiscoverPage {
	items := make([]core.MetadataDiscoverItem, 0, len(page.Items))
	for _, title := range page.Items {
		state := core.MetadataRequestNone
		if status, ok := states[core.MetadataKey(title)]; ok {
			state = core.MetadataRequestState(status)
		}
		items = append(items, core.MetadataDiscoverItem{MetadataTitle: title, RequestState: state})
	}
	return core.MetadataDiscoverPage{Items: items, Page: page.Page, TotalPages: page.TotalPages}
}

// Genres returns a provider genre list cached for one day.
func (s *Service) Genres(ctx context.Context, kind core.MediaKind) ([]core.MetadataGenre, error) {
	if !kind.Valid() {
		return nil, core.ErrInvalidArgument
	}
	key := cacheKey{provider: core.MetadataProviderTMDB, resource: "genres", kind: kind}
	value, err := s.cachedLoad(key, genreCacheTTL, func() (cacheValue, error) {
		provider, providerErr := s.discoveryProvider(ctx)
		if providerErr != nil {
			return cacheValue{}, providerErr
		}
		genres, genreErr := provider.Genres(ctx, kind)
		if genreErr != nil {
			return cacheValue{}, s.providerError(ctx, "load metadata genres", genreErr)
		}
		return cacheValue{genres: genres}, nil
	})
	if err != nil {
		return nil, err
	}
	return value.genres, nil
}

func (s *Service) cachedLoad(
	key cacheKey, ttl time.Duration, load func() (cacheValue, error),
) (cacheValue, error) {
	if cached, ok := s.cache.get(key); ok {
		return cached, nil
	}
	loaded, err, _ := s.loads.Do(key.flightKey(), func() (any, error) {
		if cached, ok := s.cache.get(key); ok {
			return cached, nil
		}
		value, loadErr := load()
		if loadErr != nil {
			return cacheValue{}, loadErr
		}
		s.cache.putFor(key, value, ttl)
		return value, nil
	})
	if err != nil {
		return cacheValue{}, err
	}
	value, ok := loaded.(cacheValue)
	if !ok {
		return cacheValue{}, errors.New("metadata cache load returned an invalid value")
	}
	return cloneCacheValue(value), nil
}

func (k cacheKey) flightKey() string {
	return string(k.provider) + "\x00" + k.resource + "\x00" + string(k.kind) + "\x00" + k.id
}

func (s *Service) discoveryProvider(ctx context.Context) (core.MetadataDiscoveryProvider, error) {
	provider, err := s.loadProvider(ctx, core.MetadataProviderTMDB)
	if err != nil {
		return nil, err
	}
	discovery, ok := provider.(core.MetadataDiscoveryProvider)
	if !ok {
		return nil, errors.New("metadata provider does not support discovery")
	}
	return discovery, nil
}

func (s *Service) loadProvider(ctx context.Context, kind core.MetadataProviderKind) (core.MetadataProvider, error) {
	record, err := s.reader.GetMetadataProvider(ctx, kind)
	if errors.Is(err, core.ErrNotFound) {
		return nil, core.ErrMetadataNotConfigured
	}
	if err != nil {
		return nil, fmt.Errorf("read metadata provider credential: %w", err)
	}
	fingerprint := sha256.Sum256(record.CredentialCiphertext)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.provider != nil && s.fingerprint == fingerprint {
		return s.provider, nil
	}
	plaintext, err := s.decryptCredential(ctx, record, kind)
	defer clear(plaintext)
	if err != nil {
		return nil, err
	}
	provider, err := s.factory.New(kind, string(plaintext))
	if err != nil {
		return nil, fmt.Errorf("construct metadata provider: %w", err)
	}
	closeProvider(s.provider)
	s.provider, s.fingerprint = provider, fingerprint
	return provider, nil
}

func (s *Service) decryptCredential(
	ctx context.Context, record core.MetadataProviderRecord, kind core.MetadataProviderKind,
) ([]byte, error) {
	plaintext, err := s.cipher.Decrypt(record.CredentialCiphertext, credentialContext(kind))
	if err != nil {
		return nil, fmt.Errorf("decrypt metadata provider credential: %w", err)
	}
	if core.ValidateMetadataCredential(kind, string(plaintext)) != nil {
		clear(plaintext)
		s.warnInvalid.Do(func() {
			s.logger.WarnContext(ctx, "stored TMDB credential is not an API Read Access Token", "provider", kind)
		})
		return nil, core.ErrMetadataNotConfigured
	}
	return plaintext, nil
}

func (s *Service) providerError(ctx context.Context, action string, err error) error {
	s.warnProviderFailure(ctx, err)
	return fmt.Errorf("%s: %w", action, err)
}

func (s *Service) warnProviderFailure(ctx context.Context, err error) {
	reason, index := metadataFailureReason(err)
	if index < 0 || !s.admitProviderWarning(index) {
		return
	}
	s.logger.WarnContext(ctx, "metadata provider request failed", "provider", core.MetadataProviderTMDB, "reason", reason)
}

func (s *Service) admitProviderWarning(index int) bool {
	s.warnMu.Lock()
	defer s.warnMu.Unlock()
	now := s.clock.Now()
	if s.warned[index] && now.Sub(s.warnedAt[index]) < providerWarningInterval {
		return false
	}
	s.warned[index], s.warnedAt[index] = true, now
	return true
}

func metadataFailureReason(err error) (string, int) {
	switch {
	case errors.Is(err, core.ErrMetadataUnreachable):
		return "unreachable", 0
	case errors.Is(err, core.ErrMetadataUnauthorized):
		return "unauthorized", 1
	case errors.Is(err, core.ErrMetadataMalformed):
		return "malformed", 2
	case errors.Is(err, core.ErrMetadataUnavailable):
		return "unavailable", 3
	default:
		return "", -1
	}
}

func (s *Service) resetProvider() {
	s.mu.Lock()
	closeProvider(s.provider)
	s.provider = nil
	s.fingerprint = [sha256.Size]byte{}
	s.mu.Unlock()
	s.cache.clear()
}

// CloseIdleConnections closes the active provider transport and clears cached details.
func (s *Service) CloseIdleConnections() { s.resetProvider() }

func closeProvider(provider core.MetadataProvider) {
	if closer, ok := provider.(idleCloser); ok {
		closer.CloseIdleConnections()
	}
}

func credentialContext(kind core.MetadataProviderKind) secrets.Context {
	return secrets.Context{Purpose: credentialPurpose, RecordID: string(kind), Kind: string(kind), BaseURL: providerBaseURL}
}

func filterSpecials(series core.MetadataSeries, include bool) core.MetadataSeries {
	if include {
		return series
	}
	filtered := series
	filtered.Seasons = make([]core.MetadataSeason, 0, len(series.Seasons))
	for _, season := range series.Seasons {
		if season.Number != 0 {
			filtered.Seasons = append(filtered.Seasons, season)
		}
	}
	return filtered
}
