package main

import (
	"context"
	"errors"
	"log/slog"
	"os"

	"github.com/akijowski/pipefitter/internal/cmd"
)

const (
	exitCodeSuccess = 0
	exitCodeErr     = 1
)

func main() {
	ctx := context.Background()

	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, nil)))

	os.Exit(runMain(ctx, cmd.OSHost(), os.Args[1:]))
}

// runMain is the testable entrypoint. Generated pipeline YAML goes to
// host.Out; everything else — logs, usage, diagnostics — goes to host.ErrOut.
func runMain(ctx context.Context, host cmd.Host, args []string) int {
	err := cmd.Run(ctx, host, args)

	switch {
	case err == nil:
		return exitCodeSuccess
	case errors.Is(err, cmd.ErrUsage):
		// cmd.Run already wrote the usage text to errOut.
		return exitCodeErr
	default:
		slog.ErrorContext(ctx, "command failed", slog.Any("error", err))

		return exitCodeErr
	}
}
