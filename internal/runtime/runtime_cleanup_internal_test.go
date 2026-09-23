package runtime

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	httpapi "github.com/BonzTM/bloom/internal/api/http"
	"github.com/BonzTM/bloom/internal/config"
	"github.com/BonzTM/bloom/internal/core"
	oidcadapter "github.com/BonzTM/bloom/internal/oidc"
	"github.com/BonzTM/bloom/internal/telemetry"
)

type shutdownOrder struct {
	mu     sync.Mutex
	events []string
}

func (o *shutdownOrder) add(event string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.events = append(o.events, event)
}

func (o *shutdownOrder) snapshot() []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]string(nil), o.events...)
}

type recordingTracer struct {
	shutdowns atomic.Int32
	order     *shutdownOrder
}

type recordingMediaCloser struct {
	closes atomic.Int32
	order  *shutdownOrder
}

type recordingPlaybackManager struct {
	starts   atomic.Int32
	stops    atomic.Int32
	startErr error
	errors   chan error
	order    *shutdownOrder
}

func (m *recordingPlaybackManager) Start(context.Context) error {
	m.starts.Add(1)
	return m.startErr
}

func (m *recordingPlaybackManager) Errors() <-chan error {
	if m.errors == nil {
		m.errors = make(chan error)
	}
	return m.errors
}

func (m *recordingPlaybackManager) Stop(context.Context) error {
	m.stops.Add(1)
	if m.order != nil {
		m.order.add("playback")
	}
	return nil
}

func (c *recordingMediaCloser) CloseIdleConnections() {
	c.closes.Add(1)
	if c.order != nil {
		c.order.add("media")
	}
}

func (t *recordingTracer) Shutdown(context.Context) error {
	t.shutdowns.Add(1)
	if t.order != nil {
		t.order.add("tracer")
	}
	return nil
}

type shutdownDBConnector struct {
	order  *shutdownOrder
	closed *atomic.Bool
}

func (c shutdownDBConnector) Connect(context.Context) (driver.Conn, error) {
	return &shutdownDBConnection{order: c.order, closed: c.closed}, nil
}

func (shutdownDBConnector) Driver() driver.Driver { return shutdownDBDriver{} }

type shutdownDBDriver struct{}

func (shutdownDBDriver) Open(string) (driver.Conn, error) {
	return nil, errors.New("shutdown test driver must use its connector")
}

type shutdownDBConnection struct {
	order  *shutdownOrder
	closed *atomic.Bool
}

func (*shutdownDBConnection) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("shutdown test connection does not prepare statements")
}

func (c *shutdownDBConnection) Close() error {
	if c.closed.CompareAndSwap(false, true) {
		c.order.add("database")
	}
	return nil
}

func (*shutdownDBConnection) Begin() (driver.Tx, error) {
	return nil, errors.New("shutdown test connection does not begin transactions")
}

func (c *shutdownDBConnection) Ping(context.Context) error {
	if c.closed.Load() {
		return driver.ErrBadConn
	}
	return nil
}

type blockingServerFixture struct {
	server         *httpapi.Server
	readiness      *telemetry.Readiness
	handlerStarted <-chan struct{}
	handlerExited  <-chan struct{}
	releaseHandler func()
	requestDone    <-chan error
	serveDone      <-chan error
}

func startBlockingServer(t *testing.T, pool *sql.DB) blockingServerFixture {
	t.Helper()
	started, exited, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	web := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(started)
		<-release
		w.WriteHeader(http.StatusNoContent)
		close(exited)
	})
	readiness := telemetry.NewReadiness(true)
	srv := httpapi.New(config.HTTPConfig{
		Addr: "127.0.0.1:0", ReadHeaderTimeout: time.Second, WriteTimeout: time.Minute, MaxBodyBytes: 1 << 20,
	}, httpapi.Deps{
		Logger: slog.New(slog.DiscardHandler), Metrics: telemetry.NopMetrics{},
		Readiness: readiness, Pinger: pool, Web: web,
	})
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	serveDone := make(chan error, 1)
	go func() { serveDone <- srv.Serve(listener) }()
	requestDone := make(chan error, 1)
	go func() {
		response, requestErr := http.Get("http://" + listener.Addr().String() + "/blocked")
		if response != nil {
			requestErr = errors.Join(requestErr, response.Body.Close())
		}
		requestDone <- requestErr
	}()
	releaseHandler := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(func() { releaseHandler(); _ = srv.Close() })
	return blockingServerFixture{srv, readiness, started, exited, releaseHandler, requestDone, serveDone}
}

func TestShutdownForcedCloseIsDeterministicAndOrdered(t *testing.T) {
	plan := newShutdownPlan(time.Date(2099, 1, 2, 3, 4, 5, 0, time.UTC), 100*time.Second)
	ctx, cancel := context.WithDeadline(t.Context(), plan.end())
	defer cancel()
	released := make(chan struct{})
	order := make([]string, 0, 7)
	appendOrder := func(name string) { order = append(order, name) }
	phases := shutdownPhases{
		setReady: func(bool) { appendOrder("unready") },
		drainHTTP: func(context.Context) error {
			appendOrder("drain")
			return context.DeadlineExceeded
		},
		forceCloseHTTP: func(context.Context) error {
			appendOrder("force close")
			close(released)
			return nil
		},
		waitHandlers: func(context.Context) error {
			select {
			case <-released:
				appendOrder("wait handlers")
				return nil
			default:
				return errors.New("force close did not release handlers")
			}
		},
		closeProvider:  func(context.Context) error { appendOrder("close provider"); return nil },
		closeMedia:     func(context.Context) error { appendOrder("close media"); return nil },
		closeDatabase:  func(context.Context) error { appendOrder("close database"); return nil },
		flushTelemetry: func(context.Context) error { appendOrder("flush telemetry"); return nil },
	}
	err := executeShutdown(ctx, plan, phases)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("shutdown error = %v, want drain deadline", err)
	}
	want := []string{
		"unready", "drain", "force close", "wait handlers", "close provider", "close media", "close database", "flush telemetry",
	}
	if strings.Join(order, ",") != strings.Join(want, ",") {
		t.Fatalf("shutdown order = %v, want %v", order, want)
	}
}

func TestRuntimeShutdownWaitsForAdmittedHandlerBeforeClosingDependencies(t *testing.T) {
	order := &shutdownOrder{}
	databaseClosed := &atomic.Bool{}
	pool := sql.OpenDB(shutdownDBConnector{order: order, closed: databaseClosed})
	if err := pool.PingContext(t.Context()); err != nil {
		t.Fatalf("open shutdown test database: %v", err)
	}
	provider := &recordingOIDCProvider{order: order}
	playbackManager := &recordingPlaybackManager{order: order}
	media := &recordingMediaCloser{order: order}
	tracer := &recordingTracer{order: order}
	fixture := startBlockingServer(t, pool)
	<-fixture.handlerStarted
	phases := runtimeShutdownPhases(fixture.server, provider, playbackManager, media, pool, tracer)
	forced := make(chan struct{})
	drainFailure := errors.New("forced drain failure")
	realDrain, realForceClose := phases.drainHTTP, phases.forceCloseHTTP
	phases.drainHTTP = func(context.Context) error {
		cancelled, cancel := context.WithCancel(context.Background())
		cancel()
		return errors.Join(drainFailure, realDrain(cancelled))
	}
	phases.forceCloseHTTP = func(ctx context.Context) error {
		err := realForceClose(ctx)
		close(forced)
		return err
	}
	done := make(chan error, 1)
	plan := newShutdownPlan(time.Now(), time.Minute)
	go func() { done <- executeShutdown(t.Context(), plan, phases) }()
	<-forced
	if provider.closes.Load() != 0 || media.closes.Load() != 0 || databaseClosed.Load() || tracer.shutdowns.Load() != 0 {
		t.Fatalf("dependencies closed while handler blocked: provider %d media %d database %t tracer %d",
			provider.closes.Load(), media.closes.Load(), databaseClosed.Load(), tracer.shutdowns.Load())
	}
	if fixture.readiness.Ready() {
		t.Fatal("server remained ready during shutdown")
	}
	if err := pool.PingContext(t.Context()); err != nil {
		t.Fatalf("database unavailable while handler blocked: %v", err)
	}
	fixture.releaseHandler()
	<-fixture.handlerExited
	if err := <-done; !errors.Is(err, drainFailure) {
		t.Fatalf("shutdown error = %v, want forced drain failure", err)
	}
	<-fixture.requestDone
	if serveErr := <-fixture.serveDone; !errors.Is(serveErr, http.ErrServerClosed) {
		t.Fatalf("Serve error = %v, want http.ErrServerClosed", serveErr)
	}
	want := []string{"playback", "provider", "media", "database", "tracer"}
	if got := order.snapshot(); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("dependency close order = %v, want %v", got, want)
	}
}

func TestServePropagatesPlaybackStartupFailure(t *testing.T) {
	order := &shutdownOrder{}
	databaseClosed := &atomic.Bool{}
	pool := sql.OpenDB(shutdownDBConnector{order: order, closed: databaseClosed})
	if err := pool.PingContext(t.Context()); err != nil {
		t.Fatalf("open shutdown test database: %v", err)
	}
	manager := &recordingPlaybackManager{
		startErr: errors.New("restore failed"), errors: make(chan error), order: order,
	}
	media := &recordingMediaCloser{order: order}
	tracer := &recordingTracer{order: order}
	srv := httpapi.New(config.HTTPConfig{
		Addr: "127.0.0.1:0", ReadHeaderTimeout: time.Second, WriteTimeout: time.Second,
		MaxBodyBytes: 1 << 20,
	}, httpapi.Deps{
		Logger: slog.New(slog.DiscardHandler), Metrics: telemetry.NopMetrics{},
		Readiness: telemetry.NewReadiness(true), Pinger: pool,
	})
	serving, err := serve(
		t.Context(), srv, nil, manager, media, pool, tracer,
		slog.New(slog.DiscardHandler), time.Second, nil,
		func(ctx context.Context, network, address string) (net.Listener, error) {
			return (&net.ListenConfig{}).Listen(ctx, network, address)
		},
	)
	if !serving || err == nil || !strings.Contains(err.Error(), "start playback manager") {
		t.Fatalf("serve = %t, %v; want playback startup failure", serving, err)
	}
	if manager.starts.Load() != 1 || manager.stops.Load() != 1 {
		t.Fatalf("playback lifecycle starts=%d stops=%d, want 1 each", manager.starts.Load(), manager.stops.Load())
	}
	want := []string{"playback", "media", "database", "tracer"}
	if got := order.snapshot(); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("startup failure shutdown order = %v, want %v", got, want)
	}
}

func TestShutdownPhaseDeadlinesStayInsideOneGrace(t *testing.T) {
	start := time.Date(2099, 1, 2, 3, 4, 5, 0, time.UTC)
	plan := newShutdownPlan(start, 100*time.Second)
	ctx, cancel := context.WithDeadline(t.Context(), plan.end())
	defer cancel()
	deadlines := make(chan time.Time, 6)
	capture := func(ctx context.Context) error {
		deadline, ok := ctx.Deadline()
		if !ok {
			return errors.New("shutdown phase has no deadline")
		}
		deadlines <- deadline
		return nil
	}
	forceCalled := atomic.Bool{}
	phases := shutdownPhases{
		setReady:       func(bool) {},
		drainHTTP:      capture,
		forceCloseHTTP: func(context.Context) error { forceCalled.Store(true); return nil },
		waitHandlers:   capture,
		closeProvider:  capture,
		closeMedia:     capture,
		closeDatabase:  capture,
		flushTelemetry: capture,
	}
	if err := executeShutdown(ctx, plan, phases); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	if forceCalled.Load() || len(deadlines) != cap(deadlines) {
		t.Fatalf("force close called = %t, phase deadlines = %d", forceCalled.Load(), len(deadlines))
	}
	want := []time.Time{
		start.Add(50 * time.Second), start.Add(70 * time.Second), start.Add(80 * time.Second),
		start.Add(80 * time.Second), start.Add(90 * time.Second), start.Add(100 * time.Second),
	}
	for index, wantDeadline := range want {
		if got := <-deadlines; !got.Equal(wantDeadline) {
			t.Fatalf("shutdown phase %d deadline = %s, want %s", index, got, wantDeadline)
		}
	}
}

type startupResources struct {
	pool   *sql.DB
	tracer *recordingTracer
}

type recordingOIDCProvider struct {
	closes atomic.Int32
	order  *shutdownOrder
}

func (*recordingOIDCProvider) AuthorizationURL(string, string, string) string {
	return "https://id.example/authorize"
}

func (*recordingOIDCProvider) Exchange(context.Context, string, string, string) (core.OIDCClaims, error) {
	return core.OIDCClaims{}, errors.New("not used")
}

func (p *recordingOIDCProvider) Close(context.Context) error {
	p.closes.Add(1)
	if p.order != nil {
		p.order.add("provider")
	}
	return nil
}

func TestRunClosesResourcesWhenListenerBindFails(t *testing.T) {
	resources := &startupResources{tracer: &recordingTracer{}}
	deps := cleanupTestDependencies(resources)
	deps.listen = func(context.Context, string, string) (net.Listener, error) {
		return nil, errors.New("bind failed")
	}
	err := Run(t.Context(), cleanupTestConfig(t), Streams{Log: io.Discard, Audit: io.Discard}, deps)
	if err == nil || !strings.Contains(err.Error(), "listen HTTP") {
		t.Fatalf("Run error = %v, want listener bind failure", err)
	}
	assertStartupResourcesClosed(t, resources)
}

func TestRunClosesOIDCProviderWhenListenerBindFails(t *testing.T) {
	provider := &recordingOIDCProvider{}
	cfg := cleanupTestConfig(t)
	cfg.PublicURL = "https://bloom.example"
	cfg.OIDC = cleanupTestOIDCConfig("https://id.example")
	resources := &startupResources{tracer: &recordingTracer{}}
	deps := cleanupTestDependencies(resources)
	deps.newOIDCProvider = func(context.Context, config.OIDCConfig, oidcadapter.Dependencies) (oidcLifecycle, error) {
		return provider, nil
	}
	deps.listen = func(context.Context, string, string) (net.Listener, error) {
		return nil, errors.New("bind failed")
	}
	err := Run(t.Context(), cfg, Streams{Log: io.Discard, Audit: io.Discard}, deps)
	if err == nil || !strings.Contains(err.Error(), "listen HTTP") {
		t.Fatalf("Run error = %v, want listener bind failure", err)
	}
	if got := provider.closes.Load(); got != 1 {
		t.Fatalf("provider closes = %d, want 1", got)
	}
	assertStartupResourcesClosed(t, resources)
}

func TestRunClosesOIDCProviderOnNormalShutdown(t *testing.T) {
	provider := &recordingOIDCProvider{}
	cfg := cleanupTestConfig(t)
	cfg.PublicURL = "https://bloom.example"
	cfg.OIDC = cleanupTestOIDCConfig("https://id.example")
	resources := &startupResources{tracer: &recordingTracer{}}
	deps := cleanupTestDependencies(resources)
	deps.newOIDCProvider = func(context.Context, config.OIDCConfig, oidcadapter.Dependencies) (oidcLifecycle, error) {
		return provider, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	deps.ListenerReady = func(net.Addr) { cancel() }
	if err := Run(ctx, cfg, Streams{Log: io.Discard, Audit: io.Discard}, deps); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := provider.closes.Load(); got != 1 {
		t.Fatalf("provider closes = %d, want 1", got)
	}
	assertStartupResourcesClosed(t, resources)
}

func TestRunClosesResourcesWhenOIDCStartupFails(t *testing.T) {
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	t.Cleanup(provider.Close)
	cfg := cleanupTestConfig(t)
	cfg.PublicURL = "https://bloom.example"
	cfg.OIDC = cleanupTestOIDCConfig(provider.URL)
	resources := &startupResources{tracer: &recordingTracer{}}
	deps := cleanupTestDependencies(resources)
	deps.OIDCRandom = func(time.Duration) (time.Duration, error) { return 0, nil }
	deps.OIDCWait = func(context.Context, time.Duration) error { return nil }
	err := Run(t.Context(), cfg, Streams{Log: io.Discard, Audit: io.Discard}, deps)
	if err == nil || !strings.Contains(err.Error(), "initialize OIDC provider") {
		t.Fatalf("Run error = %v, want OIDC startup failure", err)
	}
	assertStartupResourcesClosed(t, resources)
}

func cleanupTestDependencies(resources *startupResources) Dependencies {
	return Dependencies{
		newTracerProvider: func(context.Context, config.TelemetryConfig, string, string) (tracerLifecycle, error) {
			return resources.tracer, nil
		},
		openStore: func(ctx context.Context, cfg config.Config, logger *slog.Logger, metrics *telemetry.PromMetrics) (*sql.DB, error) {
			pool, err := openStore(ctx, cfg, logger, metrics)
			resources.pool = pool
			return pool, err
		},
	}
}

func cleanupTestConfig(t *testing.T) config.Config {
	t.Helper()
	return config.Config{
		HTTP: config.HTTPConfig{
			Addr: "127.0.0.1:0", ReadHeaderTimeout: time.Second, ReadTimeout: time.Second,
			WriteTimeout: time.Second, IdleTimeout: time.Second, MaxBodyBytes: 1 << 20,
		},
		Database: config.DatabaseConfig{
			Driver: config.DriverSQLite,
			DSN: "file:" + filepath.Join(t.TempDir(), "cleanup.db") +
				"?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)",
			MaxOpenConns: 2, MaxIdleConns: 2, ConnMaxLifetime: time.Minute, ConnMaxIdleTime: time.Minute,
			MigrateOnStartup: true,
		},
		Telemetry: config.TelemetryConfig{LogFormat: config.LogFormatJSON},
		Auth: config.AuthConfig{
			SessionCookieSecure: true, SessionLifetime: time.Hour,
			SessionIdleTimeout: 15 * time.Minute, LoginRateRefillInterval: time.Minute,
			LoginRateBurst: 5, LoginRateMaxKeys: 100, LoginMaxConcurrent: 4,
		},
		Playback: config.PlaybackConfig{
			PollActive: 5 * time.Second, PollIdle: 30 * time.Second,
			MissedPolls: 3, ResumeWindow: 5 * time.Minute, StoreTimeout: time.Second,
		},
		SecretKey: config.NewSecret([]byte("0123456789abcdef0123456789abcdef")), ShutdownGrace: time.Second,
	}
}

func cleanupTestOIDCConfig(issuer string) config.OIDCConfig {
	return config.OIDCConfig{
		Enabled: true, DisplayName: "SSO", IssuerURL: issuer, ClientID: "bloom",
		ClientSecret: config.NewSecret([]byte("secret")),
		RedirectURL:  "https://bloom.example/api/v1/auth/oidc/callback",
		Scopes:       []string{"openid"}, UsernameClaim: "preferred_username", AllowInsecureIssuer: true,
		DiscoveryTimeout: 200 * time.Millisecond, TokenExchangeTimeout: time.Second, JWKSFetchTimeout: time.Second,
	}
}

func assertStartupResourcesClosed(t *testing.T, resources *startupResources) {
	t.Helper()
	if resources.pool == nil {
		t.Fatal("database pool was not created")
	}
	if err := resources.pool.PingContext(t.Context()); err == nil {
		t.Fatal("database pool remained open")
	}
	if got := resources.tracer.shutdowns.Load(); got != 1 {
		t.Fatalf("tracer shutdowns = %d, want 1", got)
	}
}
