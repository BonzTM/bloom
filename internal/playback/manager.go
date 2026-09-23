package playback

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/BonzTM/bloom/internal/core"
)

const (
	serverRefreshInterval = time.Minute
	serverListPageSize    = 100
	maxServerListPages    = 11
)

type serverLister interface {
	List(ctx context.Context, afterNameKey string, pageSize int) ([]core.MediaServerConnection, error)
}

type refreshObserver interface {
	IncPlaybackRefreshFailure()
}

// SourceFactory constructs a polling source for one registered server.
type SourceFactory func(core.MediaServer) Source

type managedCollector struct {
	cancel context.CancelFunc
	done   chan struct{}
}

// ManagerOptions contains deterministic lifecycle triggers used by tests.
type ManagerOptions struct {
	Refresh     <-chan time.Time
	Wait        func(context.Context, time.Duration) error
	RandomInt64 func(int64) int64
	NewID       func() (string, error)
}

// Manager keeps exactly one collector running for each registered media server.
type Manager struct {
	servers        serverLister
	store          core.PlaybackStore
	config         Config
	factory        SourceFactory
	clock          core.Clock
	logger         *slog.Logger
	metrics        Observer
	refreshMetrics refreshObserver
	refreshTrigger <-chan time.Time
	wait           func(context.Context, time.Duration) error
	randomInt64    func(int64) int64
	newID          func() (string, error)

	operationMu sync.Mutex
	mu          sync.Mutex
	collectors  map[string]managedCollector
	deleting    map[string]bool
	cancel      context.CancelFunc
	done        chan struct{}
	errors      chan error
	started     bool
}

// NewManager validates dependencies and returns an idle manager.
func NewManager(
	servers serverLister,
	store core.PlaybackStore,
	config Config,
	factory SourceFactory,
	clock core.Clock,
	logger *slog.Logger,
	metrics Observer,
	options ManagerOptions,
) (*Manager, error) {
	if servers == nil || store == nil || factory == nil || clock == nil || logger == nil || !validConfig(config) {
		return nil, fmt.Errorf("playback manager: %w", core.ErrInvalidArgument)
	}
	manager := &Manager{
		servers: servers, store: store, config: config, factory: factory,
		clock: clock, logger: logger, metrics: metrics, refreshTrigger: options.Refresh,
		wait: options.Wait, randomInt64: options.RandomInt64, newID: options.NewID,
		collectors: make(map[string]managedCollector), deleting: make(map[string]bool),
		errors: make(chan error, 1),
	}
	if refreshMetrics, ok := metrics.(refreshObserver); ok {
		manager.refreshMetrics = refreshMetrics
	}
	return manager, nil
}

// Start loads registered servers after HTTP readiness and starts their collectors.
func (m *Manager) Start(ctx context.Context) error {
	m.mu.Lock()
	if m.started {
		m.mu.Unlock()
		return errors.New("playback manager already started")
	}
	runCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	m.cancel = cancel
	m.done = make(chan struct{})
	m.started = true
	m.mu.Unlock()
	if err := m.refresh(ctx, runCtx); err != nil && ctx.Err() == nil {
		m.reportRefreshFailure(ctx, err)
	}
	go m.run(runCtx)
	return nil
}

// Errors reports the first fatal manager or collector failure.
func (m *Manager) Errors() <-chan error { return m.errors }

// Stop cancels all collectors and waits within ctx.
func (m *Manager) Stop(ctx context.Context) error {
	m.mu.Lock()
	if !m.started {
		m.mu.Unlock()
		return nil
	}
	cancel, done := m.cancel, m.done
	m.mu.Unlock()
	cancel()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (m *Manager) run(ctx context.Context) {
	defer m.markStopped()
	if m.refreshTrigger != nil {
		m.runRefreshLoop(ctx, m.refreshTrigger)
		return
	}
	ticker := time.NewTicker(serverRefreshInterval)
	defer ticker.Stop()
	m.runRefreshLoop(ctx, ticker.C)
}

func (m *Manager) runRefreshLoop(ctx context.Context, refresh <-chan time.Time) {
	for {
		select {
		case <-ctx.Done():
			m.operationMu.Lock()
			m.stopAll()
			m.operationMu.Unlock()
			return
		case _, ok := <-refresh:
			if !ok {
				m.operationMu.Lock()
				m.stopAll()
				m.operationMu.Unlock()
				return
			}
			if err := m.refresh(ctx, ctx); err != nil && ctx.Err() == nil {
				m.reportRefreshFailure(ctx, err)
			}
		}
	}
}

func (m *Manager) refresh(queryCtx, runCtx context.Context) error {
	m.operationMu.Lock()
	defer m.operationMu.Unlock()
	servers, err := m.listServers(queryCtx)
	present := make(map[string]bool, len(servers))
	for _, server := range servers {
		present[server.ID] = true
		if startErr := m.startCollector(runCtx, server); startErr != nil {
			return errors.Join(err, startErr)
		}
	}
	if err != nil {
		return err
	}
	m.stopDeleted(present)
	return nil
}

func (m *Manager) listServers(ctx context.Context) ([]core.MediaServer, error) {
	servers := make([]core.MediaServer, 0)
	after := ""
	for range maxServerListPages {
		connections, err := m.servers.List(ctx, after, serverListPageSize)
		if err != nil {
			return servers, fmt.Errorf("list media servers: %w", err)
		}
		for _, connection := range connections {
			servers = append(servers, connection.Server)
		}
		if len(connections) < serverListPageSize {
			return servers, nil
		}
		after = core.MediaServerNameKey(connections[len(connections)-1].Server.Name)
	}
	return servers, errors.New("registered media server count exceeds collector limit")
}

func (m *Manager) reportRefreshFailure(ctx context.Context, err error) {
	if m.refreshMetrics != nil {
		m.refreshMetrics.IncPlaybackRefreshFailure()
	}
	m.logger.WarnContext(ctx, "playback manager refresh failed", "error", err)
}

func (m *Manager) startCollector(runCtx context.Context, server core.MediaServer) error {
	m.mu.Lock()
	_, exists := m.collectors[server.ID]
	deleting := m.deleting[server.ID]
	m.mu.Unlock()
	if exists || deleting {
		return nil
	}
	collector, err := NewCollector(server, m.config, Dependencies{
		Store: m.store, Source: m.factory(server), Clock: m.clock,
		Logger: m.logger, Observer: m.metrics, Wait: m.wait,
		RandomInt64: m.randomInt64, NewID: m.newID,
	})
	if err != nil {
		return err
	}
	collectorCtx, cancel := context.WithCancel(runCtx)
	managed := managedCollector{cancel: cancel, done: make(chan struct{})}
	m.mu.Lock()
	m.collectors[server.ID] = managed
	m.mu.Unlock()
	go m.runCollector(collectorCtx, server.ID, collector, managed.done)
	return nil
}

func (m *Manager) runCollector(
	ctx context.Context,
	serverID string,
	collector *Collector,
	done chan struct{},
) {
	defer m.collectorDone(serverID, done)
	if err := collector.Run(ctx); err != nil && ctx.Err() == nil {
		m.fail(fmt.Errorf("playback collector %s: %w", serverID, err))
		m.mu.Lock()
		cancel := m.cancel
		m.mu.Unlock()
		cancel()
	}
}

func (m *Manager) collectorDone(serverID string, done chan struct{}) {
	close(done)
	m.mu.Lock()
	if managed, ok := m.collectors[serverID]; ok && managed.done == done {
		delete(m.collectors, serverID)
	}
	m.mu.Unlock()
}

// StopServer prevents refresh from starting a collector and awaits the current one.
func (m *Manager) StopServer(ctx context.Context, id string) error {
	m.operationMu.Lock()
	defer m.operationMu.Unlock()
	m.mu.Lock()
	m.deleting[id] = true
	collector, exists := m.collectors[id]
	m.mu.Unlock()
	if !exists {
		return nil
	}
	collector.cancel()
	select {
	case <-collector.done:
		return nil
	case <-ctx.Done():
		m.mu.Lock()
		delete(m.deleting, id)
		m.mu.Unlock()
		return ctx.Err()
	}
}

// FinishServerDelete releases the refresh exclusion after the delete attempt.
func (m *Manager) FinishServerDelete(id string, _ bool) {
	m.mu.Lock()
	delete(m.deleting, id)
	m.mu.Unlock()
}

func (m *Manager) stopDeleted(present map[string]bool) {
	m.mu.Lock()
	deleted := make([]managedCollector, 0)
	for id, collector := range m.collectors {
		if !present[id] {
			delete(m.collectors, id)
			deleted = append(deleted, collector)
		}
	}
	m.mu.Unlock()
	for _, collector := range deleted {
		collector.cancel()
		<-collector.done
	}
}

func (m *Manager) stopAll() {
	m.mu.Lock()
	collectors := make([]managedCollector, 0, len(m.collectors))
	for id, collector := range m.collectors {
		delete(m.collectors, id)
		collectors = append(collectors, collector)
	}
	m.mu.Unlock()
	for _, collector := range collectors {
		collector.cancel()
	}
	for _, collector := range collectors {
		<-collector.done
	}
}

func (m *Manager) fail(err error) {
	select {
	case m.errors <- err:
	default:
	}
}

func (m *Manager) markStopped() {
	m.mu.Lock()
	if m.started {
		m.started = false
		close(m.done)
	}
	m.mu.Unlock()
}
