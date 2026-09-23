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
	"net/http"
	"time"

	"github.com/alexedwards/scs/v2"
	"golang.org/x/sync/errgroup"

	httpapi "github.com/BonzTM/bloom/internal/api/http"
	"github.com/BonzTM/bloom/internal/api/web"
	"github.com/BonzTM/bloom/internal/buildinfo"
	"github.com/BonzTM/bloom/internal/config"
	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/db"
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

// systemClock is the production core.Clock. No core package reads the wall
// clock directly.
type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now() }

// Run wires the service and blocks until ctx is cancelled (a signal) or a
// component fails, then performs the ordered shutdown. It returns nil on a
// clean stop. In -migrate mode it applies migrations and returns without
// serving.
func Run(ctx context.Context, cfg config.Config, streams Streams) error {
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

	metrics := telemetry.NewPromMetrics(buildinfo.Name)
	tracerProvider, err := telemetry.NewTracerProvider(ctx, cfg.Telemetry, buildinfo.Name, buildinfo.Version)
	if err != nil {
		return fmt.Errorf("init tracing: %w", err)
	}

	pool, err := openStore(ctx, cfg, logger, metrics)
	if err != nil {
		return err
	}
	clock := systemClock{}
	accounts, localIdentities, sessions, err := authDependencies(pool, cfg, metrics, logger, clock)
	if err != nil {
		_ = pool.Close()
		return err
	}
	srv, err := assembleHTTPServer(
		cfg, streams.Audit, logger, metrics, pool,
		accounts, localIdentities, sessions, clock,
	)
	if err != nil {
		_ = pool.Close()
		return err
	}

	return serve(ctx, srv, pool, tracerProvider, logger, cfg.ShutdownGrace)
}

func assembleHTTPServer(
	cfg config.Config,
	auditSink io.Writer,
	logger *slog.Logger,
	metrics *telemetry.PromMetrics,
	pool *sql.DB,
	accounts core.AccountStore,
	localIdentities core.LocalIdentityStore,
	sessions *scs.SessionManager,
	clock core.Clock,
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
		Sessions:            sessions,
		Audit:               audit,
		AuditCorrelationKey: cfg.SecretKey.Bytes(),
		Clock:               clock,
		Auth:                cfg.Auth,
	}), nil
}

func authDependencies(
	pool *sql.DB,
	cfg config.Config,
	metrics db.SessionCleanupMetrics,
	logger *slog.Logger,
	clock core.Clock,
) (core.AccountStore, core.LocalIdentityStore, *scs.SessionManager, error) {
	accounts, localIdentities, err := db.NewAccountStores(pool, cfg.Database.Driver)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("build account stores: %w", err)
	}
	sessionStore, err := db.NewSessionStore(pool, cfg.Database.Driver, metrics, logger, clock)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("build session store: %w", err)
	}
	return accounts, localIdentities, configureSessions(cfg.Auth, sessionStore), nil
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
	pool, err := db.Open(ctx, cfg.Database)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	if cfg.Database.MigrateOnStartup {
		if merr := db.Migrate(ctx, pool, cfg.Database.Driver); merr != nil {
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
func serve(ctx context.Context, srv *httpapi.Server, pool *sql.DB, tp *telemetry.TracerProvider, logger *slog.Logger, grace time.Duration) error {
	g, gctx := errgroup.WithContext(ctx)

	g.Go(func() error {
		// Dependencies are wired and the listener is about to accept: ready.
		srv.SetReady(true)
		logger.Info("http listening", "addr", srv.Addr())
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("http serve: %w", err)
		}
		return nil
	})

	g.Go(func() error {
		<-gctx.Done()
		return shutdown(srv, pool, tp, logger, grace)
	})

	if err := g.Wait(); err != nil {
		return fmt.Errorf("run: %w", err)
	}
	logger.Info("stopped")
	return nil
}

// Migrate is the one-shot -migrate mode: open the pool, apply all pending
// embedded goose migrations for the configured engine, close the pool. main
// maps a nil return to exit 0 and any error to a logged failure with exit 1,
// which is exactly the contract a migration Job needs.
func Migrate(ctx context.Context, cfg config.Config, logger *slog.Logger) error {
	pool, err := db.Open(ctx, cfg.Database)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	if merr := db.Migrate(ctx, pool, cfg.Database.Driver); merr != nil {
		_ = pool.Close()
		return fmt.Errorf("migrate: %w", merr)
	}
	if err := pool.Close(); err != nil {
		return fmt.Errorf("close database: %w", err)
	}
	logger.Info("migrations applied", "driver", cfg.Database.Driver)
	return nil
}

// shutdown drains and releases resources in reverse dependency order under a
// bounded grace budget: flip readiness, drain HTTP with a FRESH deadline,
// close the pool, then flush telemetry last.
func shutdown(srv *httpapi.Server, pool *sql.DB, tp *telemetry.TracerProvider, logger *slog.Logger, grace time.Duration) error {
	logger.Info("shutting down", "grace", grace)

	// Detach from the cancelled root context: shutdown gets its own deadline.
	ctx, cancel := context.WithTimeout(context.Background(), grace)
	defer cancel()

	// 1. Unready so load balancers stop routing new traffic while existing
	//    requests drain. Liveness stays green.
	srv.SetReady(false)

	// 2. Stop accepting connections and wait for in-flight requests to finish.
	if err := srv.Shutdown(ctx); err != nil {
		return fmt.Errorf("http shutdown: %w", err)
	}

	// 3. Close the pool now that no request can still be using it.
	if err := pool.Close(); err != nil {
		return fmt.Errorf("close database: %w", err)
	}

	// 4. Flush telemetry last so the steps above are recorded.
	if err := tp.Shutdown(ctx); err != nil {
		return fmt.Errorf("telemetry flush: %w", err)
	}
	return nil
}
