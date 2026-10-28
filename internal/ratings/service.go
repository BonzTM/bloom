// Package ratings coordinates the stored OMDb credential and cached title ratings.
package ratings

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/secrets"
)

const (
	credentialPurpose = "metadata-provider-api-key"
	omdbBaseURL       = "https://www.omdbapi.com"
	cacheCapacity     = 1024
	// Scores move slowly, and an OMDb key has a daily quota.
	cacheTTL = 24 * time.Hour
)

type credentialCipher interface {
	Encrypt([]byte, secrets.Context) ([]byte, error)
	Decrypt([]byte, secrets.Context) ([]byte, error)
	KeyID() string
}

// Client is the OMDb adapter the service drives.
type Client interface {
	core.TitleRatingsProvider
	Probe(ctx context.Context) error
	CloseIdleConnections()
}

// ClientFactory builds an OMDb client for a decrypted key.
type ClientFactory interface {
	New(key string) (Client, error)
}

// Service stores the OMDb key encrypted and serves cached title ratings.
type Service struct {
	reader      core.MetadataProviderReader
	writer      core.MetadataProviderWriter
	cipher      credentialCipher
	factory     ClientFactory
	clock       core.Clock
	logger      *slog.Logger
	cache       *ratingsCache
	loads       singleflight.Group
	mu          sync.Mutex
	client      Client
	fingerprint [sha256.Size]byte
}

// NewService creates a ratings service with explicit storage and clock dependencies.
func NewService(
	reader core.MetadataProviderReader, writer core.MetadataProviderWriter, cipher credentialCipher,
	factory ClientFactory, clock core.Clock, logger *slog.Logger,
) (*Service, error) {
	if reader == nil || writer == nil || cipher == nil || factory == nil || clock == nil || logger == nil {
		return nil, errors.New("ratings service: all dependencies are required")
	}
	return &Service{
		reader: reader, writer: writer, cipher: cipher, factory: factory, clock: clock, logger: logger,
		cache: newRatingsCache(clock, cacheCapacity, cacheTTL),
	}, nil
}

// SetKey checks the key against OMDb, then stores it encrypted.
func (s *Service) SetKey(ctx context.Context, key string) error {
	if core.ValidateOMDbKey(key) != nil {
		return core.ErrInvalidArgument
	}
	if err := s.probe(ctx, key); err != nil {
		return err
	}
	now := core.NormalizeTime(s.clock.Now())
	createdAt := now
	existing, err := s.reader.GetMetadataProvider(ctx, core.MetadataProviderOMDb)
	if err == nil {
		createdAt = existing.CreatedAt
	} else if !errors.Is(err, core.ErrNotFound) {
		return fmt.Errorf("read OMDb credential before update: %w", err)
	}
	ciphertext, err := s.cipher.Encrypt([]byte(key), credentialContext())
	if err != nil {
		return fmt.Errorf("encrypt OMDb credential: %w", err)
	}
	record := core.MetadataProviderRecord{
		Kind: core.MetadataProviderOMDb, CredentialCiphertext: ciphertext, KeyID: s.cipher.KeyID(),
		CreatedAt: createdAt, UpdatedAt: now,
	}
	if err := s.writer.UpsertMetadataProvider(ctx, record); err != nil {
		return fmt.Errorf("store OMDb credential: %w", err)
	}
	s.reset()
	return nil
}

func (s *Service) probe(ctx context.Context, key string) error {
	client, err := s.factory.New(key)
	if err != nil {
		return fmt.Errorf("construct OMDb client: %w", err)
	}
	defer client.CloseIdleConnections()
	if err := client.Probe(ctx); err != nil {
		return fmt.Errorf("probe OMDb credential: %w", err)
	}
	return nil
}

// HasKey reports whether a usable OMDb key is stored, never returning it.
func (s *Service) HasKey(ctx context.Context) (bool, error) {
	_, err := s.loadClient(ctx)
	if errors.Is(err, core.ErrMetadataNotConfigured) {
		return false, nil
	}
	return err == nil, err
}

// RemoveKey deletes the OMDb key and forgets cached ratings.
func (s *Service) RemoveKey(ctx context.Context) error {
	if err := s.writer.DeleteMetadataProvider(ctx, core.MetadataProviderOMDb); err != nil {
		return fmt.Errorf("delete OMDb credential: %w", err)
	}
	s.reset()
	return nil
}

// Ratings returns the cached or upstream scores for imdbID. With no key
// stored it returns no scores and no error, since ratings are optional.
func (s *Service) Ratings(ctx context.Context, imdbID string) (core.TitleRatings, error) {
	if !core.ValidIMDbID(imdbID) {
		return core.TitleRatings{}, core.ErrInvalidArgument
	}
	if ratings, ok := s.cache.get(imdbID); ok {
		return ratings, nil
	}
	client, err := s.loadClient(ctx)
	if errors.Is(err, core.ErrMetadataNotConfigured) {
		return core.TitleRatings{}, nil
	}
	if err != nil {
		return core.TitleRatings{}, err
	}
	value, err, _ := s.loads.Do(imdbID, func() (any, error) {
		return client.TitleRatings(ctx, imdbID)
	})
	if err != nil {
		return core.TitleRatings{}, fmt.Errorf("load OMDb ratings: %w", err)
	}
	ratings, ok := value.(core.TitleRatings)
	if !ok {
		return core.TitleRatings{}, fmt.Errorf("load OMDb ratings: %w", core.ErrMetadataMalformed)
	}
	s.cache.put(imdbID, ratings)
	return cloneRatings(ratings), nil
}

func (s *Service) loadClient(ctx context.Context) (Client, error) {
	record, err := s.reader.GetMetadataProvider(ctx, core.MetadataProviderOMDb)
	if errors.Is(err, core.ErrNotFound) {
		return nil, core.ErrMetadataNotConfigured
	}
	if err != nil {
		return nil, fmt.Errorf("read OMDb credential: %w", err)
	}
	fingerprint := sha256.Sum256(record.CredentialCiphertext)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.client != nil && s.fingerprint == fingerprint {
		return s.client, nil
	}
	client, err := s.newClient(ctx, record)
	if err != nil {
		return nil, err
	}
	closeClient(s.client)
	s.client, s.fingerprint = client, fingerprint
	return client, nil
}

func (s *Service) newClient(ctx context.Context, record core.MetadataProviderRecord) (Client, error) {
	plaintext, err := s.cipher.Decrypt(record.CredentialCiphertext, credentialContext())
	defer clear(plaintext)
	if err != nil {
		return nil, fmt.Errorf("decrypt OMDb credential: %w", err)
	}
	if core.ValidateOMDbKey(string(plaintext)) != nil {
		s.logger.WarnContext(ctx, "stored OMDb credential is not a valid key", "provider", core.MetadataProviderOMDb)
		return nil, core.ErrMetadataNotConfigured
	}
	client, err := s.factory.New(string(plaintext))
	if err != nil {
		return nil, fmt.Errorf("construct OMDb client: %w", err)
	}
	return client, nil
}

func (s *Service) reset() {
	s.mu.Lock()
	closeClient(s.client)
	s.client = nil
	s.fingerprint = [sha256.Size]byte{}
	s.mu.Unlock()
	s.cache.clear()
}

// CloseIdleConnections closes the active OMDb transport and clears cached ratings.
func (s *Service) CloseIdleConnections() { s.reset() }

func closeClient(client Client) {
	if client != nil {
		client.CloseIdleConnections()
	}
}

func credentialContext() secrets.Context {
	kind := string(core.MetadataProviderOMDb)
	return secrets.Context{Purpose: credentialPurpose, RecordID: kind, Kind: kind, BaseURL: omdbBaseURL}
}
