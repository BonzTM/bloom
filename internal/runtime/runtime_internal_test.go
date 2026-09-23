package runtime

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/BonzTM/bloom/internal/config"
	"github.com/BonzTM/bloom/internal/core"
)

type boundedTracer struct {
	called      bool
	hasDeadline bool
}

func (t *boundedTracer) Shutdown(ctx context.Context) error {
	t.called = true
	_, t.hasDeadline = ctx.Deadline()
	return nil
}

type orderedFailingCloser struct {
	order *[]string
	err   error
}

func (c orderedFailingCloser) Close() error {
	*c.order = append(*c.order, "close database")
	return c.err
}

type orderedFailingTracer struct {
	order *[]string
	err   error
}

type orderedMediaCloser struct{ order *[]string }

func (c orderedMediaCloser) CloseIdleConnections() {
	*c.order = append(*c.order, "close media")
}

func TestShutdownClosesMediaBeforeDatabaseAndTracer(t *testing.T) {
	order := make([]string, 0, 5)
	media := orderedMediaCloser{order: &order}
	phases := shutdownPhases{
		setReady:       func(bool) { order = append(order, "unready") },
		drainHTTP:      func(context.Context) error { order = append(order, "shutdown http"); return nil },
		forceCloseHTTP: func(context.Context) error { return nil },
		waitHandlers:   func(context.Context) error { return nil },
		closeProvider:  func(context.Context) error { return nil },
		closeMedia:     func(context.Context) error { media.CloseIdleConnections(); return nil },
		closeDatabase:  func(context.Context) error { return orderedFailingCloser{order: &order}.Close() },
		flushTelemetry: orderedFailingTracer{order: &order}.Shutdown,
	}
	plan := newShutdownPlan(time.Now(), time.Second)
	err := executeShutdown(t.Context(), plan, phases)
	if err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	want := []string{"unready", "shutdown http", "close media", "close database", "shutdown tracer"}
	if !slices.Equal(order, want) {
		t.Fatalf("shutdown order = %v, want %v", order, want)
	}
}

func (t orderedFailingTracer) Shutdown(context.Context) error {
	*t.order = append(*t.order, "shutdown tracer")
	return t.err
}

type orderedFailingProvider struct {
	order *[]string
	err   error
}

func (orderedFailingProvider) AuthorizationURL(string, string, string) string { return "" }

func (orderedFailingProvider) Exchange(context.Context, string, string, string) (core.OIDCClaims, error) {
	return core.OIDCClaims{}, errors.New("not used")
}

func (p orderedFailingProvider) Close(context.Context) error {
	*p.order = append(*p.order, "close provider")
	return p.err
}

func TestStartupOwnershipCleanupReturnsOrderedErrors(t *testing.T) {
	startupErr := errors.New("startup failed")
	providerErr := errors.New("close provider")
	closeErr := errors.New("close database")
	traceErr := errors.New("shutdown tracer")
	order := make([]string, 0, 4)
	ownership := startupOwnership{
		tracer:   orderedFailingTracer{order: &order, err: traceErr},
		pool:     orderedFailingCloser{order: &order, err: closeErr},
		provider: orderedFailingProvider{order: &order, err: providerErr},
		media:    orderedMediaCloser{order: &order},
		grace:    time.Second,
	}
	err := startupErr
	ownership.cleanup(&err)
	if !errors.Is(err, startupErr) || !errors.Is(err, providerErr) || !errors.Is(err, closeErr) || !errors.Is(err, traceErr) {
		t.Fatalf("cleanup error = %v; want startup, provider, close, and tracer errors", err)
	}
	want := []string{"close provider", "close media", "close database", "shutdown tracer"}
	if !slices.Equal(order, want) {
		t.Fatalf("cleanup order = %v; want %v", order, want)
	}
}

func TestStartupOwnershipCleanupSkipsTransferredResources(t *testing.T) {
	order := make([]string, 0, 2)
	ownership := startupOwnership{
		tracer:      orderedFailingTracer{order: &order},
		pool:        orderedFailingCloser{order: &order},
		grace:       time.Second,
		transferred: true,
	}
	var err error
	ownership.cleanup(&err)
	if err != nil || len(order) != 0 {
		t.Fatalf("cleanup after transfer = err %v order %v; want no cleanup", err, order)
	}
}

func TestRunShutsDownTracerAfterStoreFailure(t *testing.T) {
	cfg := config.Config{
		Database: config.DatabaseConfig{
			Driver:       config.DriverPostgres,
			DSN:          "postgres://bloom:bloom@127.0.0.1:1/bloom?sslmode=disable&connect_timeout=1",
			MaxOpenConns: 1, MaxIdleConns: 1,
			ConnMaxLifetime: time.Minute, ConnMaxIdleTime: time.Minute,
		},
		Bootstrap:     config.BootstrapConfig{Username: "admin"},
		ShutdownGrace: 50 * time.Millisecond,
	}
	tracer := &boundedTracer{}
	deps := Dependencies{
		newTracerProvider: func(context.Context, config.TelemetryConfig, string, string) (tracerLifecycle, error) {
			return tracer, nil
		},
	}
	err := Run(t.Context(), cfg, Streams{Log: io.Discard, Audit: io.Discard}, deps)
	if err == nil {
		t.Fatal("Run succeeded with unreachable database")
	}
	if !tracer.called || !tracer.hasDeadline {
		t.Fatalf("tracer shutdown = called %t, bounded %t", tracer.called, tracer.hasDeadline)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("startup error was replaced by cleanup error: %v", err)
	}
}

func TestRunShutsDownTracerAfterMediaServerFailure(t *testing.T) {
	cfg := config.Config{
		Database: config.DatabaseConfig{
			Driver:       config.DriverSQLite,
			DSN:          "file:" + filepath.Join(t.TempDir(), "media.db") + "?_pragma=foreign_keys(1)",
			MaxOpenConns: 1, MaxIdleConns: 1,
			ConnMaxLifetime: time.Minute, ConnMaxIdleTime: time.Minute,
			MigrateOnStartup: true,
		},
		Auth: config.AuthConfig{
			SessionLifetime: time.Hour, SessionIdleTimeout: time.Minute,
		},
		Bootstrap:     config.BootstrapConfig{Username: "admin"},
		ShutdownGrace: 50 * time.Millisecond,
	}
	tracer := &boundedTracer{}
	deps := Dependencies{
		newTracerProvider: func(context.Context, config.TelemetryConfig, string, string) (tracerLifecycle, error) {
			return tracer, nil
		},
	}
	err := Run(t.Context(), cfg, Streams{Log: io.Discard, Audit: io.Discard}, deps)
	if err == nil || !strings.Contains(err.Error(), "build credential cipher") {
		t.Fatalf("Run error = %v, want credential cipher failure from an empty secret key", err)
	}
	if !tracer.called || !tracer.hasDeadline {
		t.Fatalf("tracer shutdown = called %t, bounded %t", tracer.called, tracer.hasDeadline)
	}
}
