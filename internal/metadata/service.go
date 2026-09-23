// Package metadata coordinates provider credentials, adapters, and bounded detail caching.
package metadata

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/secrets"
)

const (
	credentialPurpose = "metadata-provider-api-key"
	providerBaseURL   = "https://api.themoviedb.org"
	cacheCapacity     = 512
	cacheTTL          = 15 * time.Minute
	maxAPIKeyBytes    = 4096
)

type credentialCipher interface {
	Encrypt([]byte, secrets.Context) ([]byte, error)
	Decrypt([]byte, secrets.Context) ([]byte, error)
	KeyID() string
}

type providerFactory interface {
	New(kind core.MetadataProviderKind, credential string) (core.MetadataProvider, error)
}

type idleCloser interface{ CloseIdleConnections() }

// Service coordinates encrypted provider configuration and cached metadata reads.
type Service struct {
	reader      core.MetadataProviderReader
	writer      core.MetadataProviderWriter
	cipher      credentialCipher
	factory     providerFactory
	clock       core.Clock
	cache       *detailCache
	mu          sync.Mutex
	provider    core.MetadataProvider
	fingerprint [sha256.Size]byte
}

// NewService creates a metadata service with explicit storage and clock dependencies.
func NewService(reader core.MetadataProviderReader, writer core.MetadataProviderWriter, cipher credentialCipher, factory providerFactory, clock core.Clock) (*Service, error) {
	if reader == nil || writer == nil || cipher == nil || factory == nil || clock == nil {
		return nil, errors.New("metadata service: all dependencies are required")
	}
	return &Service{
		reader: reader, writer: writer, cipher: cipher, factory: factory, clock: clock,
		cache: newDetailCache(clock, cacheCapacity, cacheTTL),
	}, nil
}

// SetKey encrypts and stores a provider credential without retaining plaintext.
func (s *Service) SetKey(ctx context.Context, kind core.MetadataProviderKind, apiKey string) error {
	if !kind.Valid() || !validAPIKey(apiKey) {
		return core.ErrInvalidArgument
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
	ciphertext, err := s.cipher.Encrypt([]byte(apiKey), context)
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
	_, err := s.reader.GetMetadataProvider(ctx, kind)
	if errors.Is(err, core.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read metadata provider: %w", err)
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
		return nil, fmt.Errorf("search metadata: %w", err)
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
		return core.MetadataTitle{}, fmt.Errorf("load movie metadata: %w", err)
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
		return core.MetadataSeries{}, fmt.Errorf("load series metadata: %w", err)
	}
	s.cache.put(key, cacheValue{series: series})
	return filterSpecials(series, includeSpecials), nil
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
	plaintext, err := s.cipher.Decrypt(record.CredentialCiphertext, credentialContext(kind))
	if err != nil {
		return nil, fmt.Errorf("decrypt metadata provider credential: %w", err)
	}
	defer clear(plaintext)
	provider, err := s.factory.New(kind, string(plaintext))
	if err != nil {
		return nil, fmt.Errorf("construct metadata provider: %w", err)
	}
	closeProvider(s.provider)
	s.provider, s.fingerprint = provider, fingerprint
	return provider, nil
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

func validAPIKey(value string) bool {
	return value != "" && len(value) <= maxAPIKeyBytes && utf8.ValidString(value) &&
		stringsIndexControl(value) < 0
}

func stringsIndexControl(value string) int { return strings.IndexFunc(value, unicode.IsControl) }

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
