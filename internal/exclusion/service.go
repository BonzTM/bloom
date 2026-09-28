// Package exclusion coordinates cached per-server collection settings.
package exclusion

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/BonzTM/bloom/internal/core"
)

const (
	defaultRefreshLease        = time.Minute
	serverMutexCapacity        = 256
	serverMutexOverflowStripes = 64
)

type serverReader interface {
	Get(context.Context, string) (core.MediaServerConnection, error)
}

type cacheEntry struct {
	value     core.MediaServerExclusions
	expiresAt time.Time
}

type serverMutexMap struct {
	mu       sync.Mutex
	indices  map[string]int
	next     int
	locks    [serverMutexCapacity]sync.Mutex
	overflow [serverMutexOverflowStripes]sync.Mutex
}

func newServerMutexMap() *serverMutexMap {
	return &serverMutexMap{indices: make(map[string]int, serverMutexCapacity)}
}

func (m *serverMutexMap) get(serverID string) *sync.Mutex {
	m.mu.Lock()
	defer m.mu.Unlock()
	if index, ok := m.indices[serverID]; ok {
		return &m.locks[index]
	}
	if m.next < len(m.locks) {
		index := m.next
		m.next++
		m.indices[serverID] = index
		return &m.locks[index]
	}
	return &m.overflow[serverMutexIndex(serverID)]
}

func serverMutexIndex(serverID string) int {
	const offset64, prime64 = uint64(14695981039346656037), uint64(1099511628211)
	hash := offset64
	for index := range len(serverID) {
		hash = (hash ^ uint64(serverID[index])) * prime64
	}
	return int(hash % serverMutexOverflowStripes)
}

// Service provides full replacements and lease-bounded cached lookups.
type Service struct {
	store        core.ExclusionStore
	servers      serverReader
	clock        core.Clock
	lease        time.Duration
	mu           sync.Mutex
	entries      map[string]cacheEntry
	generation   map[string]uint64
	invalidators []func(string)
	operations   *serverMutexMap
}

// NewService validates and constructs an exclusion service.
func NewService(
	store core.ExclusionStore, servers serverReader, clock core.Clock, lease time.Duration,
) (*Service, error) {
	if store == nil || servers == nil || clock == nil || lease < 0 {
		return nil, fmt.Errorf("exclusion service: %w", core.ErrInvalidArgument)
	}
	if lease == 0 {
		lease = defaultRefreshLease
	}
	return &Service{
		store: store, servers: servers, clock: clock, lease: lease,
		entries: make(map[string]cacheEntry), generation: make(map[string]uint64),
		operations: newServerMutexMap(),
	}, nil
}

// GetExclusions returns a cloned setting, refreshing it after its lease expires.
func (s *Service) GetExclusions(
	ctx context.Context, serverID string,
) (core.MediaServerExclusions, error) {
	now := s.clock.Now()
	entry, generation, ok := s.cached(serverID)
	if ok && now.Before(entry.expiresAt) {
		return entry.value.Clone(), nil
	}
	value, err := s.store.GetExclusions(ctx, serverID)
	if err != nil {
		return core.MediaServerExclusions{}, err
	}
	s.putIfCurrent(value, now, generation)
	return value.Clone(), nil
}

// Read verifies the media server exists before returning its settings.
func (s *Service) Read(ctx context.Context, serverID string) (core.MediaServerExclusions, error) {
	if _, err := s.servers.Get(ctx, serverID); err != nil {
		return core.MediaServerExclusions{}, err
	}
	return s.GetExclusions(ctx, serverID)
}

// Replace atomically stores the complete setting and refreshes the cache.
func (s *Service) Replace(
	ctx context.Context, value core.MediaServerExclusions,
) (core.MediaServerExclusions, error) {
	if !value.Valid() {
		return core.MediaServerExclusions{}, core.ErrInvalidArgument
	}
	if _, err := s.servers.Get(ctx, value.MediaServerID); err != nil {
		return core.MediaServerExclusions{}, err
	}
	operation := s.operations.get(value.MediaServerID)
	operation.Lock()
	defer operation.Unlock()
	if err := s.store.ReplaceExclusions(ctx, value); err != nil {
		return core.MediaServerExclusions{}, err
	}
	s.replaceCached(value, s.clock.Now())
	s.invalidateReaders(value.MediaServerID)
	return value.Clone(), nil
}

// AddInvalidator registers a cache invalidator invoked after a successful replacement.
func (s *Service) AddInvalidator(invalidate func(string)) {
	if invalidate == nil {
		return
	}
	s.mu.Lock()
	s.invalidators = append(s.invalidators, invalidate)
	s.mu.Unlock()
}

func (s *Service) invalidateReaders(serverID string) {
	s.mu.Lock()
	invalidators := append([]func(string){}, s.invalidators...)
	s.mu.Unlock()
	for _, invalidate := range invalidators {
		invalidate(serverID)
	}
}

func (s *Service) cached(serverID string) (cacheEntry, uint64, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.entries[serverID]
	return entry, s.generation[serverID], ok
}

func (s *Service) putIfCurrent(value core.MediaServerExclusions, now time.Time, generation uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.generation[value.MediaServerID] != generation {
		return
	}
	s.entries[value.MediaServerID] = cacheEntry{value: value.Clone(), expiresAt: now.Add(s.lease)}
}

func (s *Service) replaceCached(value core.MediaServerExclusions, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.generation[value.MediaServerID]++
	s.entries[value.MediaServerID] = cacheEntry{value: value.Clone(), expiresAt: now.Add(s.lease)}
}
