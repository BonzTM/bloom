// Command bloom is the service entrypoint. It mirrors the handbook's thin-main
// template: main does nothing but wire process lifecycle. Config is loaded and
// validated first (fail-fast), the root context is cancelled on SIGINT/SIGTERM,
// and internal/runtime owns assembly and the ordered, bounded shutdown.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"golang.org/x/term"

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
	return runCommand(ctx, os.Args[1:], resolveBootstrapPassword)
}

type bootstrapPasswordReader func(io.Writer) ([]byte, error)

func runCommand(ctx context.Context, args []string, readPassword bootstrapPasswordReader) error {
	streams := runtime.Streams{Log: os.Stdout, Audit: os.Stderr, Console: os.Stdout}
	input := runtime.CommandInput{ReadPassword: func() ([]byte, error) {
		return readPassword(streams.Console)
	}}
	return runtime.Execute(ctx, args, streams, input)
}

func resolveBootstrapPassword(console io.Writer) ([]byte, error) {
	if password, ok := os.LookupEnv("BLOOM_BOOTSTRAP_PASSWORD"); ok && password != "" {
		return []byte(password), nil
	}
	if term.IsTerminal(int(os.Stdin.Fd())) {
		return readTerminalPassword(console)
	}
	return nil, errors.New("BLOOM_BOOTSTRAP_PASSWORD is unset and stdin is not a terminal")
}

func readTerminalPassword(console io.Writer) ([]byte, error) {
	return promptForPassword(console, func() ([]byte, error) {
		return term.ReadPassword(int(os.Stdin.Fd()))
	})
}

func promptForPassword(console io.Writer, read func() ([]byte, error)) ([]byte, error) {
	if _, err := fmt.Fprint(console, "Password: "); err != nil {
		return nil, fmt.Errorf("write password prompt: %w", err)
	}
	password, err := read()
	if _, writeErr := fmt.Fprintln(console); writeErr != nil {
		return password, fmt.Errorf("write password prompt newline: %w", errors.Join(err, writeErr))
	}
	return password, err
}
