// Package runtime assembles the process from its configured parts so that
// cmd/bloom/main.go stays thin, per the handbook's
// foundations/shared-constructs.md: it owns wiring, startup ordering, and the
// bounded, ordered shutdown path. It holds no business logic.
package runtime

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"slices"
	"time"

	"github.com/alexedwards/scs/v2"
	"golang.org/x/sync/errgroup"

	httpapi "github.com/BonzTM/bloom/internal/api/http"
	"github.com/BonzTM/bloom/internal/api/web"
	"github.com/BonzTM/bloom/internal/buildinfo"
	"github.com/BonzTM/bloom/internal/config"
	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/db"
	inviteapp "github.com/BonzTM/bloom/internal/invite"
	"github.com/BonzTM/bloom/internal/mediaserver"
	"github.com/BonzTM/bloom/internal/metadata"
	"github.com/BonzTM/bloom/internal/metadata/tmdb"
	oidcadapter "github.com/BonzTM/bloom/internal/oidc"
	"github.com/BonzTM/bloom/internal/playback"
	requestapp "github.com/BonzTM/bloom/internal/request"
	"github.com/BonzTM/bloom/internal/secrets"
	"github.com/BonzTM/bloom/internal/telemetry"
)

// Streams are the process output sinks. They are injected so tests can capture
// logs; production passes os.Stdout (app log) and os.Stderr (audit stream).
type Streams struct {
	// Log receives the application and access log.
	Log io.Writer
	// Audit receives the dedicated audit stream (ADR 0006 item 8).
	Audit io.Writer
	// Console receives interactive prompts and command diagnostics.
	Console io.Writer
}

// Dependencies are process facilities injected for deterministic startup and
// retry tests. Production Run supplies their system implementations.
type Dependencies struct {
	Clock              core.Clock
	OIDCRandom         func(time.Duration) (time.Duration, error)
	OIDCWait           func(context.Context, time.Duration) error
	ListenerReady      func(net.Addr)
	newTracerProvider  func(context.Context, config.TelemetryConfig, string, string) (tracerLifecycle, error)
	openStore          func(context.Context, config.Config, *slog.Logger, *telemetry.PromMetrics) (*sql.DB, error)
	listen             func(context.Context, string, string) (net.Listener, error)
	newOIDCProvider    func(context.Context, config.OIDCConfig, oidcadapter.Dependencies) (oidcLifecycle, error)
	newPlaybackManager func(playbackManagerDependencies) (*playback.Manager, error)
}

type tracerLifecycle interface {
	Shutdown(context.Context) error
}

type oidcLifecycle interface {
	core.OIDCProvider
	Close(context.Context) error
}

type mediaConnectionCloser interface {
	CloseIdleConnections()
}

type playbackLifecycle interface {
	Start(context.Context) error
	Stop(context.Context) error
	Errors() <-chan error
}

// systemClock is the production core.Clock. No core package reads the wall
// clock directly.
type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now() }

const oidcRoleValidationTimeout = 5 * time.Second

// Run wires the service and blocks until ctx is cancelled (a signal) or a
// component fails, then performs the ordered shutdown. It returns nil on a
// clean stop. In -migrate mode it applies migrations and returns without
// serving.
func Run(ctx context.Context, cfg config.Config, streams Streams, supplied ...Dependencies) error {
	deps := runtimeDependencies(supplied)
	logger := telemetry.NewLogger(streams.Log, cfg.Telemetry)
	logger.Info("starting",
		"service", buildinfo.Name,
		"version", buildinfo.Version,
		"commit", buildinfo.Commit,
		"db_driver", cfg.Database.Driver,
	)
	warnTrustedProxyMode(logger, cfg.Auth)

	// One-shot migration mode: apply the embedded goose migrations and exit.
	// This is the production path for schema changes; a deployment runs the
	// SAME image with -migrate ahead of the rollout.
	if cfg.Migrate {
		return Migrate(ctx, cfg, logger)
	}
	return runService(ctx, cfg, streams.Audit, logger, deps)
}

func runService(
	ctx context.Context, cfg config.Config, auditSink io.Writer, logger *slog.Logger, deps Dependencies,
) (retErr error) {
	metrics := telemetry.NewPromMetrics(buildinfo.Name)
	tracerProvider, err := deps.newTracerProvider(ctx, cfg.Telemetry, buildinfo.Name, buildinfo.Version)
	if err != nil {
		return fmt.Errorf("init tracing: %w", err)
	}
	ownership := startupOwnership{tracer: tracerProvider, grace: cfg.ShutdownGrace}
	defer ownership.cleanup(&retErr)
	pool, err := deps.openStore(ctx, cfg, logger, metrics)
	if err != nil {
		return err
	}
	ownership.pool = pool
	clock := deps.Clock
	if bootstrapErr := bootstrapFirstAdmin(ctx, cfg, auditSink, pool, logger, clock); bootstrapErr != nil {
		return bootstrapErr
	}
	wiring, err := wireServiceDependencies(ctx, pool, cfg, logger, metrics, deps, &ownership)
	if err != nil {
		return err
	}
	srv, err := assembleHTTPServer(
		cfg, auditSink, logger, metrics, pool,
		wiring.accounts, wiring.localIdentities, wiring.authorizer, wiring.roles, wiring.sessions,
		wiring.mediaServers, wiring.invites, wiring.playbackStore, clock,
		wiring.metadata, wiring.requests,
		wiring.oidcProvider, wiring.oidcAccounts, wiring.oidcFlows,
	)
	if err != nil {
		return err
	}

	serving, err := serve(
		ctx, srv, wiring.oidcProvider, wiring.playbackManager,
		connectionGroup{wiring.mediaServers, wiring.metadata},
		pool, tracerProvider, logger, cfg.ShutdownGrace,
		deps.ListenerReady, deps.listen,
	)
	if serving {
		ownership.transferred = true
	}
	return err
}

type serviceWiring struct {
	accounts        core.AccountStore
	localIdentities core.LocalIdentityStore
	authorizer      core.Authorizer
	roles           core.RoleReader
	sessions        *scs.SessionManager
	mediaServers    *mediaserver.Service
	metadata        *metadata.Service
	requests        *requestapp.Service
	invites         *inviteapp.Service
	playbackStore   core.PlaybackStore
	playbackManager *playback.Manager
	oidcProvider    oidcLifecycle
	oidcAccounts    core.OIDCAccountStore
	oidcFlows       core.OIDCFlowStore
}

func wireServiceDependencies(
	ctx context.Context, pool *sql.DB, cfg config.Config, logger *slog.Logger,
	metrics *telemetry.PromMetrics, deps Dependencies, ownership *startupOwnership,
) (serviceWiring, error) {
	accounts, identities, authorizer, roles, sessions, err := authDependencies(
		pool, cfg, metrics, logger, deps.Clock,
	)
	if err != nil {
		return serviceWiring{}, err
	}
	mediaServers, err := mediaServerDependencies(pool, cfg, metrics, deps.Clock)
	if err != nil {
		return serviceWiring{}, err
	}
	ownership.media = mediaServers
	metadataService, requestService, err := requestDependencies(pool, cfg, metrics, deps.Clock)
	if err != nil {
		return serviceWiring{}, err
	}
	ownership.media = connectionGroup{mediaServers, metadataService}
	invites, err := inviteDependencies(pool, cfg, mediaServers, deps.Clock)
	if err != nil {
		return serviceWiring{}, err
	}
	playbackStore, playbackManager, err := playbackDependencies(
		pool, cfg, mediaServers, metrics, logger, deps.Clock, deps.newPlaybackManager,
	)
	if err != nil {
		return serviceWiring{}, err
	}
	ownership.playback = playbackManager
	if roleErr := validateOIDCRoles(ctx, roles, cfg.OIDC); roleErr != nil {
		return serviceWiring{}, roleErr
	}
	provider, oidcAccounts, oidcFlows, err := oidcDependencies(ctx, pool, cfg, metrics, deps)
	if err != nil {
		return serviceWiring{}, err
	}
	ownership.provider = provider
	return serviceWiring{
		accounts: accounts, localIdentities: identities, authorizer: authorizer, roles: roles,
		sessions: sessions, mediaServers: mediaServers, invites: invites,
		playbackStore: playbackStore, playbackManager: playbackManager,
		metadata: metadataService, requests: requestService,
		oidcProvider: provider, oidcAccounts: oidcAccounts, oidcFlows: oidcFlows,
	}, nil
}

type startupOwnership struct {
	tracer      tracerLifecycle
	pool        io.Closer
	provider    oidcLifecycle
	playback    playbackLifecycle
	media       mediaConnectionCloser
	grace       time.Duration
	transferred bool
}

func (o *startupOwnership) cleanup(retErr *error) {
	if o.transferred {
		return
	}
	*retErr = errors.Join(*retErr, stopPlayback(o.playback, o.grace))
	*retErr = errors.Join(*retErr, closeOIDCProvider(o.provider, o.grace))
	closeMediaConnections(o.media)
	if o.pool != nil {
		*retErr = errors.Join(*retErr, o.pool.Close())
	}
	*retErr = errors.Join(*retErr, shutdownTracer(o.tracer, o.grace))
}

func runtimeDependencies(supplied []Dependencies) Dependencies {
	deps := Dependencies{Clock: systemClock{}}
	if len(supplied) > 0 {
		deps = supplied[0]
	}
	if deps.Clock == nil {
		deps.Clock = systemClock{}
	}
	if deps.newTracerProvider == nil {
		deps.newTracerProvider = func(
			ctx context.Context, cfg config.TelemetryConfig, name, version string,
		) (tracerLifecycle, error) {
			return telemetry.NewTracerProvider(ctx, cfg, name, version)
		}
	}
	if deps.openStore == nil {
		deps.openStore = openStore
	}
	if deps.listen == nil {
		deps.listen = func(ctx context.Context, network, address string) (net.Listener, error) {
			return (&net.ListenConfig{}).Listen(ctx, network, address)
		}
	}
	if deps.newOIDCProvider == nil {
		deps.newOIDCProvider = func(
			ctx context.Context, cfg config.OIDCConfig, adapterDeps oidcadapter.Dependencies,
		) (oidcLifecycle, error) {
			return oidcadapter.New(ctx, cfg, adapterDeps)
		}
	}
	if deps.newPlaybackManager == nil {
		deps.newPlaybackManager = func(input playbackManagerDependencies) (*playback.Manager, error) {
			return playback.NewManager(
				input.servers, input.store, input.config, input.factory, input.clock,
				input.logger, input.metrics, playback.ManagerOptions{},
			)
		}
	}
	return deps
}

func shutdownTracer(provider tracerLifecycle, grace time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), grace)
	defer cancel()
	return provider.Shutdown(ctx)
}

func closeOIDCProvider(provider oidcLifecycle, grace time.Duration) error {
	if provider == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), shutdownPhaseBudget(grace, 5*time.Second))
	defer cancel()
	return wrapShutdownError("close OpenID Connect provider", provider.Close(ctx))
}

func closeMediaConnections(media mediaConnectionCloser) {
	if media != nil {
		media.CloseIdleConnections()
	}
}

func stopPlayback(manager playbackLifecycle, grace time.Duration) error {
	if manager == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), shutdownPhaseBudget(grace, 5*time.Second))
	defer cancel()
	return wrapShutdownError("stop playback collectors", manager.Stop(ctx))
}

func bootstrapFirstAdmin(
	ctx context.Context,
	cfg config.Config,
	auditSink io.Writer,
	pool *sql.DB,
	logger *slog.Logger,
	clock core.Clock,
) error {
	store, err := db.NewBootstrapAccountStore(pool, cfg.Database.Driver)
	if err != nil {
		return fmt.Errorf("build bootstrap account store: %w", err)
	}
	audit := telemetry.NewAuditLogger(auditSink, clock)
	return bootstrapAdmin(ctx, cfg.Bootstrap, store, logger, audit, clock)
}

func assembleHTTPServer(
	cfg config.Config,
	auditSink io.Writer,
	logger *slog.Logger,
	metrics *telemetry.PromMetrics,
	pool *sql.DB,
	accounts core.AccountStore,
	localIdentities core.LocalIdentityStore,
	authorizer core.Authorizer,
	roles core.RoleReader,
	sessions *scs.SessionManager,
	mediaServers *mediaserver.Service,
	invites *inviteapp.Service,
	playbackStore core.PlaybackStore,
	clock core.Clock,
	metadataService *metadata.Service,
	requestService *requestapp.Service,
	oidcProvider core.OIDCProvider,
	oidcAccounts core.OIDCAccountStore,
	oidcFlows core.OIDCFlowStore,
) (*httpapi.Server, error) {
	dist, err := web.Dist()
	if err != nil {
		return nil, fmt.Errorf("runtime: web assets: %w", err)
	}
	audit := telemetry.NewAuditLogger(auditSink, clock)
	return httpapi.New(cfg.HTTP, httpapi.Deps{
		Logger:              logger,
		Metrics:             metrics,
		Readiness:           telemetry.NewReadiness(false),
		Pinger:              pool,
		Web:                 web.Handler(dist, logger),
		Identity:            core.NewLocalIdentityProvider(localIdentities),
		Accounts:            accounts,
		Authorizer:          authorizer,
		Roles:               roles,
		MediaServerReader:   mediaServers,
		MediaServerManager:  mediaServers,
		InviteReader:        invites,
		InviteManager:       invites,
		PlaybackReader:      playbackStore,
		MetadataReader:      metadataService,
		MetadataManager:     metadataService,
		RequestService:      requestService,
		Sessions:            sessions,
		Audit:               audit,
		AuditCorrelationKey: cfg.SecretKey.Bytes(),
		Clock:               clock,
		Auth:                cfg.Auth,
		OIDC:                oidcProvider,
		OIDCAccounts:        oidcAccounts,
		OIDCFlows:           oidcFlows,
		OIDCConfig:          cfg.OIDC,
		PublicURL:           cfg.PublicURL,
	}), nil
}

type connectionGroup []mediaConnectionCloser

func (g connectionGroup) CloseIdleConnections() {
	for _, closer := range g {
		if closer != nil {
			closer.CloseIdleConnections()
		}
	}
}

func requestDependencies(pool *sql.DB, cfg config.Config, metrics *telemetry.PromMetrics, clock core.Clock) (*metadata.Service, *requestapp.Service, error) {
	providerReader, providerWriter, err := db.NewMetadataProviderStores(pool, cfg.Database.Driver)
	if err != nil {
		return nil, nil, fmt.Errorf("build metadata provider stores: %w", err)
	}
	cipher, err := secrets.New(cfg.SecretKey.Bytes())
	if err != nil {
		return nil, nil, fmt.Errorf("build metadata credential cipher: %w", err)
	}
	registry := metadata.NewRegistry(tmdb.Dependencies{Metrics: metrics, Clock: clock})
	metadataService, err := metadata.NewService(providerReader, providerWriter, cipher, registry, clock)
	if err != nil {
		return nil, nil, fmt.Errorf("build metadata service: %w", err)
	}
	profileReader, profileWriter, err := db.NewRequestProfileStores(pool, cfg.Database.Driver)
	if err != nil {
		return nil, nil, fmt.Errorf("build request profile stores: %w", err)
	}
	requestReader, requestWriter, quotaReader, quotaWriter, quotaDeleter, err := db.NewRequestStores(pool, cfg.Database.Driver)
	if err != nil {
		return nil, nil, fmt.Errorf("build request stores: %w", err)
	}
	service, err := requestapp.NewService(requestapp.Dependencies{
		Profiles: profileReader, ProfileWriter: profileWriter,
		Requests: requestReader, RequestWriter: requestWriter, QuotaReader: quotaReader, QuotaWriter: quotaWriter,
		QuotaDeleter: quotaDeleter, Metadata: metadataService, Clock: clock, Metrics: metrics,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("build request service: %w", err)
	}
	return metadataService, service, nil
}

func inviteDependencies(
	pool *sql.DB, cfg config.Config, mediaServers *mediaserver.Service, clock core.Clock,
) (*inviteapp.Service, error) {
	reader, store, err := db.NewInviteStores(pool, cfg.Database.Driver)
	if err != nil {
		return nil, fmt.Errorf("build invite stores: %w", err)
	}
	service, err := inviteapp.NewService(reader, store, mediaServers, mediaServers, clock)
	if err != nil {
		return nil, fmt.Errorf("build invite service: %w", err)
	}
	return service, nil
}

func playbackDependencies(
	pool *sql.DB,
	cfg config.Config,
	mediaServers *mediaserver.Service,
	metrics *telemetry.PromMetrics,
	logger *slog.Logger,
	clock core.Clock,
	newManager func(playbackManagerDependencies) (*playback.Manager, error),
) (core.PlaybackStore, *playback.Manager, error) {
	store, err := db.NewPlaybackStore(pool, cfg.Database.Driver)
	if err != nil {
		return nil, nil, fmt.Errorf("build playback store: %w", err)
	}
	collectorConfig := playback.Config{
		ActiveInterval: cfg.Playback.PollActive, IdleInterval: cfg.Playback.PollIdle,
		MissedPolls: cfg.Playback.MissedPolls, ResumeWindow: cfg.Playback.ResumeWindow,
		StoreTimeout: cfg.Playback.StoreTimeout,
	}
	factory := func(server core.MediaServer) playback.Source {
		return playback.SourceFunc(func(ctx context.Context) ([]core.PlaybackSession, error) {
			return mediaServers.ListSessions(ctx, server.ID)
		})
	}
	manager, err := newManager(playbackManagerDependencies{
		servers: mediaServers, store: store, config: collectorConfig, factory: factory,
		clock: clock, logger: logger, metrics: metrics,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("build playback manager: %w", err)
	}
	mediaServers.SetPlaybackLifecycle(manager)
	return store, manager, nil
}

type playbackManagerDependencies struct {
	servers *mediaserver.Service
	store   core.PlaybackStore
	config  playback.Config
	factory playback.SourceFactory
	clock   core.Clock
	logger  *slog.Logger
	metrics playback.Observer
}

func mediaServerDependencies(
	pool *sql.DB,
	cfg config.Config,
	metrics *telemetry.PromMetrics,
	clock core.Clock,
) (*mediaserver.Service, error) {
	reader, writer, err := db.NewMediaServerStores(pool, cfg.Database.Driver)
	if err != nil {
		return nil, fmt.Errorf("build media server stores: %w", err)
	}
	cipher, err := secrets.New(cfg.SecretKey.Bytes())
	if err != nil {
		return nil, fmt.Errorf("build credential cipher: %w", err)
	}
	deviceID, err := secrets.DeviceID(cfg.SecretKey.Bytes())
	if err != nil {
		return nil, fmt.Errorf("derive Jellyfin device id: %w", err)
	}
	registry := mediaserver.NewRegistry(buildinfo.Version, deviceID, metrics)
	service, err := mediaserver.NewService(reader, writer, cipher, registry, clock)
	if err != nil {
		return nil, fmt.Errorf("build media server service: %w", err)
	}
	return service, nil
}

func oidcDependencies(
	ctx context.Context,
	pool *sql.DB,
	cfg config.Config,
	metrics oidcadapter.Metrics,
	deps Dependencies,
) (oidcLifecycle, core.OIDCAccountStore, core.OIDCFlowStore, error) {
	if !cfg.OIDC.Enabled {
		return nil, nil, nil, nil
	}
	accounts, err := db.NewOIDCAccountStore(pool, cfg.Database.Driver)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("build OIDC account store: %w", err)
	}
	flows, err := db.NewOIDCFlowStore(pool)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("build OIDC flow store: %w", err)
	}
	provider, err := deps.newOIDCProvider(ctx, cfg.OIDC, oidcadapter.Dependencies{
		Metrics: metrics, Clock: deps.Clock, Random: deps.OIDCRandom, Wait: deps.OIDCWait,
	})
	if err != nil {
		return nil, nil, nil, fmt.Errorf("initialize OIDC provider: %w", err)
	}
	return provider, accounts, flows, nil
}

func validateOIDCRoles(ctx context.Context, roles core.RoleReader, cfg config.OIDCConfig) error {
	if !cfg.Enabled {
		return nil
	}
	validationCtx, cancel := context.WithTimeout(ctx, oidcRoleValidationTimeout)
	defer cancel()
	targets := make([]string, 0, len(cfg.RoleMap)+1)
	if cfg.DefaultRole != "" {
		targets = append(targets, cfg.DefaultRole)
	}
	for _, role := range cfg.RoleMap {
		targets = append(targets, role)
	}
	slices.Sort(targets)
	for _, role := range slices.Compact(targets) {
		exists, err := roles.RoleExists(validationCtx, role)
		if err != nil {
			return fmt.Errorf("validate configured OIDC role %q: %w", role, err)
		}
		if !exists {
			return fmt.Errorf("validate configured OIDC role %q: role does not exist", role)
		}
	}
	return nil
}

func authDependencies(
	pool *sql.DB,
	cfg config.Config,
	metrics db.SessionCleanupMetrics,
	logger *slog.Logger,
	clock core.Clock,
) (core.AccountStore, core.LocalIdentityStore, core.Authorizer, core.RoleReader, *scs.SessionManager, error) {
	accounts, localIdentities, err := db.NewAccountStores(pool, cfg.Database.Driver)
	if err != nil {
		return nil, nil, nil, nil, nil, fmt.Errorf("build account stores: %w", err)
	}
	authorizer, roles, err := db.NewAuthorizationStores(pool, cfg.Database.Driver)
	if err != nil {
		return nil, nil, nil, nil, nil, fmt.Errorf("build authorization stores: %w", err)
	}
	sessionStore, err := db.NewSessionStore(pool, cfg.Database.Driver, metrics, logger, clock)
	if err != nil {
		return nil, nil, nil, nil, nil, fmt.Errorf("build session store: %w", err)
	}
	return accounts, localIdentities, authorizer, roles, configureSessions(cfg.Auth, sessionStore), nil
}

func warnTrustedProxyMode(logger *slog.Logger, cfg config.AuthConfig) {
	if len(cfg.TrustedProxyCIDRs) == 0 {
		return
	}
	logger.Warn("trusted proxy mode enabled",
		"trusted_proxy_cidr_count", len(cfg.TrustedProxyCIDRs),
	)
}

func configureSessions(cfg config.AuthConfig, store scs.Store) *scs.SessionManager {
	manager := scs.New()
	manager.Store = store
	manager.Lifetime = cfg.SessionLifetime
	manager.IdleTimeout = cfg.SessionIdleTimeout
	manager.HashTokenInStore = true
	manager.Cookie.Name = "bloom_session"
	manager.Cookie.Path = "/"
	manager.Cookie.HttpOnly = true
	manager.Cookie.SameSite = http.SameSiteLaxMode
	manager.Cookie.Secure = cfg.SessionCookieSecure
	return manager
}

// openStore opens the configured engine's pool, optionally self-migrates, and
// registers the pool statistics collector.
func openStore(ctx context.Context, cfg config.Config, logger *slog.Logger, metrics *telemetry.PromMetrics) (*sql.DB, error) {
	pool, err := db.Open(ctx, cfg.Database, logger)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	if cfg.Database.MigrateOnStartup {
		if merr := db.MigrateWithLogger(ctx, pool, cfg.Database.Driver, logger); merr != nil {
			_ = pool.Close()
			return nil, fmt.Errorf("migrate: %w", merr)
		}
	}
	if err := metrics.RegisterDBStats(pool, string(cfg.Database.Driver)); err != nil {
		_ = pool.Close()
		return nil, err
	}
	logger.Info("database opened", "driver", cfg.Database.Driver, "migrate_on_startup", cfg.Database.MigrateOnStartup)
	return pool, nil
}

// serve runs the listener and the shutdown supervisor under one errgroup bound
// to the root context: if either returns, the other observes the cancellation.
func serve(
	ctx context.Context,
	srv *httpapi.Server,
	oidcProvider oidcLifecycle,
	playbackManager playbackLifecycle,
	media mediaConnectionCloser,
	pool *sql.DB,
	tp tracerLifecycle,
	logger *slog.Logger,
	grace time.Duration,
	listenerReady func(net.Addr),
	listen func(context.Context, string, string) (net.Listener, error),
) (bool, error) {
	listener, err := listen(ctx, "tcp", srv.Addr())
	if err != nil {
		return false, fmt.Errorf("listen HTTP: %w", err)
	}
	listenerOwned := true
	defer func() {
		if listenerOwned {
			_ = listener.Close()
		}
	}()
	g, gctx := errgroup.WithContext(ctx)
	serving := make(chan struct{})

	g.Go(func() error {
		srv.SetReady(true)
		logger.Info("http listening", "addr", listener.Addr().String())
		if listenerReady != nil {
			listenerReady(listener.Addr())
		}
		close(serving)
		if err := srv.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("http serve: %w", err)
		}
		return nil
	})

	g.Go(func() error {
		<-gctx.Done()
		return shutdown(srv, oidcProvider, playbackManager, media, pool, tp, logger, grace)
	})

	g.Go(func() error {
		return supervisePlayback(gctx, serving, playbackManager)
	})

	<-serving
	listenerOwned = false
	if err := g.Wait(); err != nil {
		return true, fmt.Errorf("run: %w", err)
	}
	logger.Info("stopped")
	return true, nil
}

func supervisePlayback(
	ctx context.Context,
	serving <-chan struct{},
	manager playbackLifecycle,
) error {
	<-serving
	if err := manager.Start(ctx); err != nil {
		if playbackStartWasCanceled(ctx) {
			return nil
		}
		return fmt.Errorf("start playback manager: %w", err)
	}
	select {
	case err := <-manager.Errors():
		return err
	case <-ctx.Done():
		return nil
	}
}

func playbackStartWasCanceled(ctx context.Context) bool { return ctx.Err() != nil }

// Migrate is the one-shot -migrate mode: open the pool, apply all pending
// embedded goose migrations for the configured engine, close the pool. main
// maps a nil return to exit 0 and any error to a logged failure with exit 1,
// which is exactly the contract a migration Job needs.
func Migrate(ctx context.Context, cfg config.Config, logger *slog.Logger) error {
	pool, err := db.Open(ctx, cfg.Database, logger)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	if merr := db.MigrateWithLogger(ctx, pool, cfg.Database.Driver, logger); merr != nil {
		_ = pool.Close()
		return fmt.Errorf("migrate: %w", merr)
	}
	if err := pool.Close(); err != nil {
		return fmt.Errorf("close database: %w", err)
	}
	logger.Info("migrations applied", "driver", cfg.Database.Driver)
	return nil
}

// shutdown drains and releases resources in reverse dependency order under
// one absolute grace deadline.
func shutdown(
	srv *httpapi.Server, oidcProvider oidcLifecycle, playbackManager playbackLifecycle,
	media mediaConnectionCloser, pool *sql.DB,
	tp tracerLifecycle, logger *slog.Logger, grace time.Duration,
) error {
	logger.Info("shutting down", "grace", grace)
	plan := newShutdownPlan(time.Now(), grace)
	ctx, cancel := context.WithDeadline(context.Background(), plan.end())
	defer cancel()
	return executeShutdown(ctx, plan, runtimeShutdownPhases(srv, oidcProvider, playbackManager, media, pool, tp))
}

type shutdownPhases struct {
	setReady       func(bool)
	drainHTTP      func(context.Context) error
	forceCloseHTTP func(context.Context) error
	waitHandlers   func(context.Context) error
	stopPlayback   func(context.Context) error
	closeProvider  func(context.Context) error
	closeMedia     func(context.Context) error
	closeDatabase  func(context.Context) error
	flushTelemetry func(context.Context) error
}

type shutdownPhase uint8

const (
	shutdownDrain shutdownPhase = iota
	shutdownHandlerWait
	shutdownProviderClose
	shutdownDatabaseClose
	shutdownTelemetryFlush
)

type shutdownPlan struct {
	start time.Time
	grace time.Duration
}

func newShutdownPlan(start time.Time, grace time.Duration) shutdownPlan {
	return shutdownPlan{start: start, grace: grace}
}

func (p shutdownPlan) end() time.Time { return p.deadline(shutdownTelemetryFlush) }

func (p shutdownPlan) deadline(phase shutdownPhase) time.Time {
	units := int64(10)
	switch phase {
	case shutdownDrain:
		units = 5
	case shutdownHandlerWait:
		units = 7
	case shutdownProviderClose:
		units = 8
	case shutdownDatabaseClose:
		units = 9
	case shutdownTelemetryFlush:
		units = 10
	}
	offset := p.grace/10*time.Duration(units) + p.grace%10*time.Duration(units)/10
	return p.start.Add(offset)
}

func runtimeShutdownPhases(
	srv *httpapi.Server, provider oidcLifecycle, playbackManager playbackLifecycle, media mediaConnectionCloser,
	pool *sql.DB, tracer tracerLifecycle,
) shutdownPhases {
	return shutdownPhases{
		setReady:       srv.SetReady,
		drainHTTP:      srv.Shutdown,
		forceCloseHTTP: func(context.Context) error { return srv.Close() },
		waitHandlers:   srv.WaitHandlers,
		stopPlayback: func(ctx context.Context) error {
			if playbackManager == nil {
				return nil
			}
			return playbackManager.Stop(ctx)
		},
		closeProvider: func(ctx context.Context) error {
			if provider == nil {
				return nil
			}
			return provider.Close(ctx)
		},
		closeMedia: func(context.Context) error {
			closeMediaConnections(media)
			return nil
		},
		closeDatabase:  func(context.Context) error { return pool.Close() },
		flushTelemetry: tracer.Shutdown,
	}
}

func executeShutdown(ctx context.Context, plan shutdownPlan, phases shutdownPhases) error {
	phases.setReady(false)
	var shutdownErr error
	drainErr := runShutdownPhase(ctx, plan, shutdownDrain, phases.drainHTTP)
	shutdownErr = errors.Join(shutdownErr, wrapShutdownError("http shutdown", drainErr))
	if drainErr != nil {
		forceErr := runShutdownPhase(ctx, plan, shutdownHandlerWait, phases.forceCloseHTTP)
		shutdownErr = errors.Join(shutdownErr, wrapShutdownError("force close HTTP", forceErr))
	}
	waitErr := runShutdownPhase(ctx, plan, shutdownHandlerWait, phases.waitHandlers)
	shutdownErr = errors.Join(shutdownErr, wrapShutdownError("wait for HTTP handlers", waitErr))
	if waitErr != nil {
		return shutdownErr
	}
	if phases.stopPlayback != nil {
		playbackErr := runShutdownPhase(ctx, plan, shutdownProviderClose, phases.stopPlayback)
		shutdownErr = errors.Join(shutdownErr, wrapShutdownError("stop playback collectors", playbackErr))
	}
	providerErr := runShutdownPhase(ctx, plan, shutdownProviderClose, phases.closeProvider)
	shutdownErr = errors.Join(shutdownErr, wrapShutdownError("close OpenID Connect provider", providerErr))
	mediaErr := runShutdownPhase(ctx, plan, shutdownProviderClose, phases.closeMedia)
	shutdownErr = errors.Join(shutdownErr, wrapShutdownError("close media-server connections", mediaErr))
	databaseErr := runShutdownPhase(ctx, plan, shutdownDatabaseClose, phases.closeDatabase)
	shutdownErr = errors.Join(shutdownErr, wrapShutdownError("close database", databaseErr))
	telemetryErr := runShutdownPhase(ctx, plan, shutdownTelemetryFlush, phases.flushTelemetry)
	shutdownErr = errors.Join(shutdownErr, wrapShutdownError("telemetry flush", telemetryErr))
	return shutdownErr
}

func runShutdownPhase(
	ctx context.Context, plan shutdownPlan, phase shutdownPhase, operation func(context.Context) error,
) error {
	phaseCtx, cancel := context.WithDeadline(ctx, plan.deadline(phase))
	defer cancel()
	return runBoundedShutdownOperation(phaseCtx, operation)
}

func runBoundedShutdownOperation(ctx context.Context, operation func(context.Context) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- operation(ctx) }()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func shutdownPhaseBudget(grace, maximum time.Duration) time.Duration {
	if grace < maximum {
		return grace
	}
	return maximum
}

func wrapShutdownError(operation string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", operation, err)
}
