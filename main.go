package main

import (
	"context"
	"errors"
	"io"
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

	os.Exit(runMain(ctx, os.Stdout, os.Stderr, os.Args[1:]))
}

// runMain is the testable entrypoint. Generated pipeline YAML goes to out;
// everything else — logs, usage, diagnostics — goes to errOut.
func runMain(ctx context.Context, out, errOut io.Writer, args []string) int {
	err := cmd.Run(ctx, out, errOut, args)

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
