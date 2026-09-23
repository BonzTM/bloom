package runtime

import (
	"context"
	"errors"
	"io"
	"log/slog"
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
