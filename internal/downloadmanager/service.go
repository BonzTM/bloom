// Package downloadmanager coordinates encrypted registrations and adapters.
package downloadmanager

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/secrets"
)

const credentialPurpose = "download-manager-api-key"

type credentialCipher interface {
	Encrypt(plaintext []byte, metadata secrets.Context) ([]byte, error)
	Decrypt(ciphertext []byte, metadata secrets.Context) ([]byte, error)
	KeyID() string
}

type adapterFactory interface {
	New(kind core.DownloadManagerKind, baseURL, credential string, allowInsecure bool) (core.DownloadManagerAdapter, error)
}

// Service manages encrypted registrations and resolves adapters on demand.
type Service struct {
	reader  core.DownloadManagerReader
	writer  core.DownloadManagerWriter
	cipher  credentialCipher
	factory adapterFactory
	clock   core.Clock
	mu      sync.Mutex
	cache   map[string]core.DownloadManagerAdapter
}

// NewService validates dependencies and creates an empty adapter cache.
func NewService(
	reader core.DownloadManagerReader, writer core.DownloadManagerWriter,
	cipher credentialCipher, factory adapterFactory, clock core.Clock,
) (*Service, error) {
	if reader == nil || writer == nil || cipher == nil || factory == nil || clock == nil {
		return nil, errors.New("download manager service: all dependencies are required")
	}
	return &Service{
		reader: reader, writer: writer, cipher: cipher, factory: factory, clock: clock,
		cache: make(map[string]core.DownloadManagerAdapter),
	}, nil
}

// Register probes a manager before encrypting and persisting its credential.
func (s *Service) Register(
	ctx context.Context, kind core.DownloadManagerKind, name, baseURL, apiKey string, allowInsecure bool,
) (core.DownloadManagerConnection, error) {
	if !kind.Valid() || apiKey == "" || core.ValidateMediaServerName(name) != nil {
		return core.DownloadManagerConnection{}, core.ErrInvalidArgument
	}
	normalizedURL, err := core.ValidateMediaServerURL(baseURL, allowInsecure)
	if err != nil {
		return core.DownloadManagerConnection{}, err
	}
	adapter, err := s.factory.New(kind, normalizedURL, apiKey, allowInsecure)
	if err != nil {
		return core.DownloadManagerConnection{}, fmt.Errorf("construct download manager adapter: %w", err)
	}
	info, err := adapter.Probe(ctx)
	if err != nil {
		closeAdapter(adapter)
		return core.DownloadManagerConnection{}, fmt.Errorf("probe download manager: %w", err)
	}
	record, err := s.encryptRecord(kind, name, normalizedURL, apiKey, allowInsecure)
	if err != nil {
		closeAdapter(adapter)
		return core.DownloadManagerConnection{}, err
	}
	if err := s.writer.CreateDownloadManager(ctx, record); err != nil {
		closeAdapter(adapter)
		return core.DownloadManagerConnection{}, fmt.Errorf("store download manager: %w", err)
	}
	s.mu.Lock()
	s.cache[record.ID] = adapter
	s.mu.Unlock()
	return core.DownloadManagerConnection{Manager: record.DownloadManager, Info: info}, nil
}

func (s *Service) encryptRecord(
	kind core.DownloadManagerKind, name, baseURL, apiKey string, allowInsecure bool,
) (core.DownloadManagerRecord, error) {
	id, err := core.NewID()
	if err != nil {
		return core.DownloadManagerRecord{}, err
	}
	now := core.NormalizeTime(s.clock.Now())
	manager := core.DownloadManager{
		ID: id, Kind: kind, Name: name, BaseURL: baseURL, AllowInsecure: allowInsecure,
		CreatedAt: now, UpdatedAt: now,
	}
	ciphertext, err := s.cipher.Encrypt([]byte(apiKey), credentialContext(manager))
	if err != nil {
		return core.DownloadManagerRecord{}, fmt.Errorf("encrypt download manager credential: %w", err)
	}
	return core.DownloadManagerRecord{
		DownloadManager: manager, CredentialCiphertext: ciphertext, KeyID: s.cipher.KeyID(),
	}, nil
}

// List returns a bounded name-key page without credentials.
func (s *Service) List(
	ctx context.Context, afterNameKey string, pageSize int,
) ([]core.DownloadManager, error) {
	values, err := s.reader.ListDownloadManagers(ctx, afterNameKey, pageSize)
	if err != nil {
		return nil, fmt.Errorf("list download managers: %w", err)
	}
	return values, nil
}

// Options probes a registered manager for profile configuration choices.
func (s *Service) Options(ctx context.Context, id string) (core.DownloadManagerOptions, error) {
	adapter, err := s.adapterByID(ctx, id)
	if err != nil {
		return core.DownloadManagerOptions{}, err
	}
	info, err := adapter.Probe(ctx)
	if err != nil {
		return core.DownloadManagerOptions{}, fmt.Errorf("read download manager options: %w", err)
	}
	return info.Options, nil
}

// Delete removes an unreferenced registration and closes its cached adapter.
func (s *Service) Delete(ctx context.Context, id string) (core.DownloadManager, error) {
	record, err := s.reader.GetDownloadManager(ctx, id)
	if err != nil {
		return core.DownloadManager{}, err
	}
	if err := s.writer.DeleteDownloadManager(ctx, id); err != nil {
		return core.DownloadManager{}, err
	}
	s.mu.Lock()
	adapter := s.cache[id]
	delete(s.cache, id)
	s.mu.Unlock()
	closeAdapter(adapter)
	return record.DownloadManager, nil
}

// Resolve retrieves a registration by its case-insensitive name.
func (s *Service) Resolve(ctx context.Context, name string) (core.DownloadManager, error) {
	record, err := s.reader.GetDownloadManagerByName(ctx, name)
	if err != nil {
		return core.DownloadManager{}, err
	}
	return record.DownloadManager, nil
}

// Add dispatches a title through the snapshotted manager registration.
func (s *Service) Add(
	ctx context.Context, managerID string, title core.DownloadTitle, options core.DownloadOptions,
) (string, error) {
	record, adapter, err := s.recordAndAdapterByID(ctx, managerID)
	if err != nil {
		return "", err
	}
	if !record.Kind.Handles(title.Kind) {
		return "", core.ErrInvalidArgument
	}
	id, err := adapter.Add(ctx, title, options)
	if err != nil {
		return "", fmt.Errorf("add title to download manager: %w", err)
	}
	return id, nil
}

// Progress reads live queue state for a recorded manager item.
func (s *Service) Progress(
	ctx context.Context, managerID, managerItemID string, seasons []int,
) (core.DownloadProgress, error) {
	if managerItemID == "" {
		return core.DownloadProgress{}, core.ErrDownloadItemMissing
	}
	adapter, err := s.adapterByID(ctx, managerID)
	if err != nil {
		return core.DownloadProgress{}, err
	}
	progress, err := adapter.Queue(ctx, managerItemID, seasons)
	if err != nil {
		return core.DownloadProgress{}, fmt.Errorf("read download manager queue: %w", err)
	}
	return progress, nil
}

func (s *Service) recordAndAdapterByID(
	ctx context.Context, id string,
) (core.DownloadManager, core.DownloadManagerAdapter, error) {
	record, err := s.reader.GetDownloadManager(ctx, id)
	if err != nil {
		return core.DownloadManager{}, nil, fmt.Errorf("get download manager: %w", err)
	}
	adapter, err := s.adapterByID(ctx, id)
	return record.DownloadManager, adapter, err
}

func (s *Service) adapterByID(ctx context.Context, id string) (core.DownloadManagerAdapter, error) {
	s.mu.Lock()
	adapter := s.cache[id]
	s.mu.Unlock()
	if adapter != nil {
		return adapter, nil
	}
	record, err := s.reader.GetDownloadManager(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("get download manager: %w", err)
	}
	return s.buildAdapter(record)
}

func (s *Service) buildAdapter(record core.DownloadManagerRecord) (core.DownloadManagerAdapter, error) {
	credential, err := s.cipher.Decrypt(record.CredentialCiphertext, credentialContext(record.DownloadManager))
	if err != nil {
		return nil, fmt.Errorf("decrypt download manager credential: %w", err)
	}
	defer clear(credential)
	adapter, err := s.factory.New(record.Kind, record.BaseURL, string(credential), record.AllowInsecure)
	if err != nil {
		return nil, fmt.Errorf("construct download manager adapter: %w", err)
	}
	s.mu.Lock()
	if existing := s.cache[record.ID]; existing != nil {
		s.mu.Unlock()
		closeAdapter(adapter)
		return existing, nil
	}
	s.cache[record.ID] = adapter
	s.mu.Unlock()
	return adapter, nil
}

// CloseIdleConnections releases all cached adapter transports.
func (s *Service) CloseIdleConnections() {
	s.mu.Lock()
	values := make([]core.DownloadManagerAdapter, 0, len(s.cache))
	for _, adapter := range s.cache {
		values = append(values, adapter)
	}
	s.mu.Unlock()
	for _, adapter := range values {
		closeAdapter(adapter)
	}
}

func credentialContext(value core.DownloadManager) secrets.Context {
	return secrets.Context{
		Purpose: credentialPurpose, RecordID: value.ID, Kind: string(value.Kind), BaseURL: value.BaseURL,
	}
}

func closeAdapter(adapter core.DownloadManagerAdapter) {
	if closer, ok := adapter.(interface{ CloseIdleConnections() }); ok {
		closer.CloseIdleConnections()
	}
}
