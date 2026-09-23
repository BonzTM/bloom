// Package mediaserver coordinates registered server configuration and adapters.
package mediaserver

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/secrets"
)

const (
	credentialPurpose  = "media-server-api-key"
	dependencyCallTime = 5 * time.Second
)

type credentialCipher interface {
	Encrypt(plaintext []byte, metadata secrets.Context) ([]byte, error)
	Decrypt(ciphertext []byte, metadata secrets.Context) ([]byte, error)
}

type adapterFactory interface {
	New(kind core.MediaServerKind, baseURL, credential string, allowInsecure bool) (core.MediaServerAdapter, error)
	Capabilities(kind core.MediaServerKind) (core.Capabilities, error)
}

// Service coordinates probe-before-save and secret-safe adapter construction.
type Service struct {
	reader            core.MediaServerReader
	writer            core.MediaServerWriter
	cipher            credentialCipher
	factory           adapterFactory
	clock             core.Clock
	adapters          *adapterCache
	registrationSlots chan struct{}
}

// NewService returns a media-server application service.
func NewService(
	reader core.MediaServerReader,
	writer core.MediaServerWriter,
	cipher credentialCipher,
	factory adapterFactory,
	clock core.Clock,
) (*Service, error) {
	if reader == nil || writer == nil || cipher == nil || factory == nil || clock == nil {
		return nil, errors.New("media server service: all dependencies are required")
	}
	return &Service{
		reader: reader, writer: writer, cipher: cipher, factory: factory, clock: clock,
		adapters:          newAdapterCache(clock, adapterCacheCapacity, adapterCacheTTL),
		registrationSlots: make(chan struct{}, maxConcurrentRegistrations),
	}, nil
}

// Register probes a server before encrypting and persisting its credential.
func (s *Service) Register(
	ctx context.Context,
	kind core.MediaServerKind,
	name, baseURL, credential string,
	allowInsecure bool,
) (core.MediaServerConnection, error) {
	adapter, normalizedURL, err := s.registrationAdapter(kind, name, baseURL, credential, allowInsecure)
	if err != nil {
		return core.MediaServerConnection{}, err
	}
	info, err := s.probeRegistration(ctx, adapter)
	if err != nil {
		closeIdleConnections(adapter)
		return core.MediaServerConnection{}, fmt.Errorf("probe media server: %w", err)
	}
	record, err := s.encryptedRecord(kind, name, normalizedURL, credential, allowInsecure)
	if err != nil {
		closeIdleConnections(adapter)
		return core.MediaServerConnection{}, err
	}
	if err := s.persistRegistration(ctx, record, adapter); err != nil {
		return core.MediaServerConnection{}, err
	}
	return core.MediaServerConnection{Server: record.MediaServer, Info: info, Capabilities: adapter.Capabilities()}, nil
}

func (s *Service) persistRegistration(
	ctx context.Context, record core.MediaServerRecord, adapter core.MediaServerAdapter,
) error {
	build := s.adapters.begin(record.ID, "register")
	if err := s.storeRecord(ctx, record); err != nil {
		s.adapters.cancel(build)
		closeIdleConnections(adapter)
		return err
	}
	entry := newAdapterEntry(adapter, recordFingerprint(record))
	if err := s.adapters.publish(build, entry); err != nil {
		return fmt.Errorf("publish media server adapter: %w", err)
	}
	return nil
}

func (s *Service) registrationAdapter(
	kind core.MediaServerKind, name, baseURL, credential string, allowInsecure bool,
) (core.MediaServerAdapter, string, error) {
	baseURL, err := validateRegistration(kind, name, baseURL, credential, allowInsecure)
	if err != nil {
		return nil, "", err
	}
	adapter, err := s.factory.New(kind, baseURL, credential, allowInsecure)
	if err != nil {
		return nil, "", fmt.Errorf("construct media server adapter: %w", err)
	}
	return adapter, baseURL, nil
}

func (s *Service) storeRecord(ctx context.Context, record core.MediaServerRecord) error {
	storeCtx, storeCancel := dependencyContext(ctx)
	defer storeCancel()
	if contextErr := storeCtx.Err(); contextErr != nil {
		return fmt.Errorf("store media server before deadline: %w", contextErr)
	}
	if err := s.writer.CreateMediaServer(storeCtx, record); err != nil {
		return fmt.Errorf("store media server: %w", err)
	}
	return nil
}

func validateRegistration(kind core.MediaServerKind, name, baseURL, credential string, allowInsecure bool) (string, error) {
	if !kind.Valid() || credential == "" {
		return "", core.ErrInvalidArgument
	}
	if err := core.ValidateMediaServerName(name); err != nil {
		return "", err
	}
	return core.ValidateMediaServerURL(baseURL, allowInsecure)
}

func (s *Service) newServer(kind core.MediaServerKind, name, baseURL string, allowInsecure bool) (core.MediaServer, error) {
	id, err := core.NewID()
	if err != nil {
		return core.MediaServer{}, err
	}
	now := core.NormalizeTime(s.clock.Now())
	return core.MediaServer{
		ID: id, Kind: kind, Name: name, BaseURL: baseURL, AllowInsecure: allowInsecure,
		CreatedAt: now, UpdatedAt: now,
	}, nil
}

func (s *Service) encryptedRecord(
	kind core.MediaServerKind, name, baseURL, credential string, allowInsecure bool,
) (core.MediaServerRecord, error) {
	server, err := s.newServer(kind, name, baseURL, allowInsecure)
	if err != nil {
		return core.MediaServerRecord{}, fmt.Errorf("create media server identity: %w", err)
	}
	ciphertext, err := s.cipher.Encrypt([]byte(credential), credentialContext(server))
	if err != nil {
		return core.MediaServerRecord{}, fmt.Errorf("encrypt media server credential: %w", err)
	}
	return core.MediaServerRecord{MediaServer: server, CredentialCiphertext: ciphertext}, nil
}

// List returns one stable normalized-name-key-ordered page with adapter capabilities.
func (s *Service) List(ctx context.Context, afterNameKey string, pageSize int) ([]core.MediaServerConnection, error) {
	callCtx, cancel := dependencyContext(ctx)
	servers, err := s.reader.ListMediaServers(callCtx, afterNameKey, pageSize)
	cancel()
	if err != nil {
		return nil, fmt.Errorf("list media servers: %w", err)
	}
	connections := make([]core.MediaServerConnection, 0, len(servers))
	for _, server := range servers {
		capabilities, capabilityErr := s.factory.Capabilities(server.Kind)
		if capabilityErr != nil {
			return nil, fmt.Errorf("media server capabilities: %w", capabilityErr)
		}
		connections = append(connections, core.MediaServerConnection{Server: server, Capabilities: capabilities})
	}
	return connections, nil
}

// Get returns one server configuration and its adapter capabilities.
func (s *Service) Get(ctx context.Context, id string) (core.MediaServerConnection, error) {
	callCtx, cancel := dependencyContext(ctx)
	record, err := s.reader.GetMediaServer(callCtx, id)
	cancel()
	if err != nil {
		return core.MediaServerConnection{}, fmt.Errorf("get media server: %w", err)
	}
	capabilities, err := s.factory.Capabilities(record.Kind)
	if err != nil {
		return core.MediaServerConnection{}, fmt.Errorf("media server capabilities: %w", err)
	}
	return core.MediaServerConnection{Server: record.MediaServer, Capabilities: capabilities}, nil
}

// Probe decrypts the stored credential only long enough to call the adapter.
func (s *Service) Probe(ctx context.Context, id string) (core.ServerInfo, error) {
	call, err := s.adapter(ctx, id, "probe")
	if err != nil {
		return core.ServerInfo{}, err
	}
	defer call.release()
	callCtx, cancel := dependencyContext(ctx)
	defer cancel()
	info, err := call.entry.adapter.Probe(callCtx)
	if err != nil {
		return core.ServerInfo{}, fmt.Errorf("probe media server: %w", err)
	}
	return info, nil
}

// Libraries lists libraries through the configured adapter.
func (s *Service) Libraries(ctx context.Context, id string) ([]core.Library, error) {
	call, err := s.adapter(ctx, id, "list_libraries")
	if err != nil {
		return nil, err
	}
	defer call.release()
	callCtx, cancel := dependencyContext(ctx)
	defer cancel()
	libraries, err := call.entry.adapter.ListLibraries(callCtx)
	if err != nil {
		return nil, fmt.Errorf("list media server libraries: %w", err)
	}
	return libraries, nil
}

func (s *Service) adapter(ctx context.Context, id, operation string) (*adapterCall, error) {
	build := s.adapters.begin(id, operation)
	callCtx, cancel := dependencyContext(ctx)
	record, err := s.reader.GetMediaServer(callCtx, id)
	cancel()
	if err != nil {
		s.adapters.cancel(build)
		return nil, fmt.Errorf("get media server credential: %w", err)
	}
	if call, found, cacheErr := s.adapters.get(build, recordFingerprint(record)); found {
		return call, cacheErr
	}
	credential, err := s.cipher.Decrypt(record.CredentialCiphertext, credentialContext(record.MediaServer))
	if err != nil {
		s.adapters.cancel(build)
		return nil, fmt.Errorf("decrypt media server credential: %w", err)
	}
	defer clear(credential)
	entry, err := s.adapters.create(build, s.factory, record, string(credential))
	if err != nil {
		return nil, fmt.Errorf("construct media server adapter: %w", err)
	}
	return entry, nil
}

// Delete removes one registration and returns its non-secret identity for audit.
func (s *Service) Delete(ctx context.Context, id string) (core.MediaServer, error) {
	readCtx, readCancel := dependencyContext(ctx)
	record, err := s.reader.GetMediaServer(readCtx, id)
	readCancel()
	if err != nil {
		return core.MediaServer{}, fmt.Errorf("get media server before delete: %w", err)
	}
	deleteCtx, deleteCancel := dependencyContext(ctx)
	err = s.writer.DeleteMediaServer(deleteCtx, id)
	deleteCancel()
	if err != nil {
		return core.MediaServer{}, fmt.Errorf("delete media server: %w", err)
	}
	s.adapters.remove(id)
	return record.MediaServer, nil
}

// CloseIdleConnections releases every registered server's pooled connections.
func (s *Service) CloseIdleConnections() { s.adapters.closeIdleConnections() }

func (s *Service) probeRegistration(ctx context.Context, adapter core.MediaServerAdapter) (core.ServerInfo, error) {
	select {
	case s.registrationSlots <- struct{}{}:
	default:
		return core.ServerInfo{}, saturationError("register")
	}
	callCtx, cancel := dependencyContext(ctx)
	info, err := adapter.Probe(callCtx)
	cancel()
	<-s.registrationSlots
	return info, err
}

func saturationError(operation string) error {
	return &core.MediaServerError{
		Kind: core.MediaServerSaturated, Operation: operation,
		Retryable: true, RetryAfter: bulkheadRetryAfter,
	}
}

func credentialContext(server core.MediaServer) secrets.Context {
	return secrets.Context{
		Purpose: credentialPurpose, RecordID: server.ID,
		Kind: string(server.Kind), BaseURL: server.BaseURL,
	}
}

func dependencyContext(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(parent, dependencyCallTime)
}
