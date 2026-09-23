package runtime

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/BonzTM/bloom/internal/config"
	"github.com/BonzTM/bloom/internal/telemetry"
)

type recordingTracer struct {
	called      bool
	hasDeadline bool
}

func (t *recordingTracer) Shutdown(ctx context.Context) error {
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

type orderedShutdownServer struct{ order *[]string }

func (s orderedShutdownServer) SetReady(ready bool) {
	if !ready {
		*s.order = append(*s.order, "unready")
	}
}

func (s orderedShutdownServer) Shutdown(context.Context) error {
	*s.order = append(*s.order, "shutdown http")
	return nil
}

type orderedMediaCloser struct{ order *[]string }

func (c orderedMediaCloser) CloseIdleConnections() {
	*c.order = append(*c.order, "close media")
}

func TestShutdownClosesMediaBeforeDatabaseAndTracer(t *testing.T) {
	order := make([]string, 0, 5)
	err := shutdown(
		orderedShutdownServer{order: &order},
		orderedMediaCloser{order: &order},
		orderedFailingCloser{order: &order},
		orderedFailingTracer{order: &order},
		slog.New(slog.DiscardHandler),
		time.Second,
	)
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

func TestCleanupStartupFailureReturnsOrderedCleanupErrors(t *testing.T) {
	startupErr := errors.New("startup failed")
	closeErr := errors.New("close database")
	traceErr := errors.New("shutdown tracer")
	order := make([]string, 0, 2)
	err := cleanupStartupFailure(startupErr,
		nil,
		orderedFailingCloser{order: &order, err: closeErr},
		orderedFailingTracer{order: &order, err: traceErr},
		time.Second)
	if !errors.Is(err, startupErr) || !errors.Is(err, closeErr) || !errors.Is(err, traceErr) {
		t.Fatalf("cleanup error = %v; want startup, close, and tracer errors", err)
	}
	if len(order) != 2 || order[0] != "close database" || order[1] != "shutdown tracer" {
		t.Fatalf("cleanup order = %v; want database then tracer", order)
	}
}

func TestRunInitializedShutsDownTracerAfterStoreFailure(t *testing.T) {
	cfg := config.Config{
		Database: config.DatabaseConfig{
			Driver:       config.DriverPostgres,
			DSN:          "postgres://bloom:bloom@127.0.0.1:1/bloom?sslmode=disable&connect_timeout=1",
			MaxOpenConns: 1, MaxIdleConns: 1,
			ConnMaxLifetime: time.Minute, ConnMaxIdleTime: time.Minute,
		},
		ShutdownGrace: 50 * time.Millisecond,
	}
	tracer := &recordingTracer{}
	err := runInitialized(t.Context(), cfg, Streams{Audit: io.Discard}, slog.New(slog.DiscardHandler),
		telemetry.NewPromMetrics("bootstrap-startup-test"), tracer)
	if err == nil {
		t.Fatal("runInitialized succeeded with unreachable database")
	}
	if !tracer.called || !tracer.hasDeadline {
		t.Fatalf("tracer shutdown = called %t, bounded %t", tracer.called, tracer.hasDeadline)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("startup error was replaced by cleanup error: %v", err)
	}
}

func TestRunInitializedShutsDownTracerAfterMediaServerFailure(t *testing.T) {
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
	tracer := &recordingTracer{}
	err := runInitialized(t.Context(), cfg, Streams{Audit: io.Discard}, slog.New(slog.DiscardHandler),
		telemetry.NewPromMetrics("media-startup-test"), tracer)
	if err == nil || !strings.Contains(err.Error(), "build credential cipher") {
		t.Fatalf("runInitialized error = %v, want credential cipher failure from an empty secret key", err)
	}
	if !tracer.called || !tracer.hasDeadline {
		t.Fatalf("tracer shutdown = called %t, bounded %t", tracer.called, tracer.hasDeadline)
	}
}
