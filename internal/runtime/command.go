package runtime

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"

	"github.com/BonzTM/bloom/internal/config"
	"github.com/BonzTM/bloom/internal/telemetry"
)

// CommandInput supplies password input resolved at the process boundary from
// the bootstrap environment secret or a terminal. A nil reader means neither
// source is available.
type CommandInput struct {
	ReadPassword func() ([]byte, error)
}

// Execute dispatches operator subcommands or starts the service.
func Execute(ctx context.Context, args []string, streams Streams, input CommandInput) error {
	if len(args) > 0 {
		switch args[0] {
		case "create-admin":
			return executeCreateAdmin(ctx, args[1:], streams, input)
		case "grant-role":
			return executeGrantRole(ctx, args[1:], streams)
		}
	}
	cfg, err := config.Load(args)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	return Run(ctx, cfg, streams)
}

func consoleStream(streams Streams) io.Writer {
	if streams.Console != nil {
		return streams.Console
	}
	return streams.Log
}

func closeCommandDatabase(result *error, closer io.Closer, logger *slog.Logger, command string) {
	if result == nil || closer == nil || logger == nil {
		return
	}
	err := closer.Close()
	if err == nil {
		return
	}
	wrapped := fmt.Errorf("close %s database: %w", command, err)
	if *result != nil {
		*result = errors.Join(*result, wrapped)
		return
	}
	logger.Error("close "+command+" database", "error", err)
}

type auditEmitter interface {
	Emit(context.Context, telemetry.AuditEvent) error
}
