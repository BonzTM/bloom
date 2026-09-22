// Command bloom is the service entrypoint. It mirrors the handbook's thin-main
// template: main does nothing but wire process lifecycle. Config is loaded and
// validated first (fail-fast), the root context is cancelled on SIGINT/SIGTERM,
// and internal/runtime owns assembly and the ordered, bounded shutdown.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/BonzTM/bloom/internal/config"
	"github.com/BonzTM/bloom/internal/runtime"
)

func main() {
	// One root context for the whole process. Cancelled on the first signal;
	// stop() restores default signal handling so a second signal kills hard.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)

	err := run(ctx)
	// Restore default signal handling before any os.Exit so the deferred-cleanup
	// trap (gocritic exitAfterDefer) does not apply: stop() always runs here.
	stop()
	if err != nil {
		// Boundary log: errors are wrapped with %w on the way up and logged
		// exactly once, here, before the process exits non-zero.
		slog.Error("startup failed", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	cfg, err := config.Load(os.Args[1:])
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	return runtime.Run(ctx, cfg, runtime.Streams{Log: os.Stdout, Audit: os.Stderr})
}
