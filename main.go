package main

import (
	"context"
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

	os.Exit(runMain(ctx, os.Args[1:]))
}

func runMain(ctx context.Context, args []string) int {
	if err := cmd.Main(ctx, args); err != nil {
		slog.ErrorContext(ctx, "error with command", slog.Any("error", err))

		return exitCodeErr
	}

	return exitCodeSuccess
}
