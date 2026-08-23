package cmd

import (
	"context"
	"fmt"
	"log/slog"

	flag "github.com/spf13/pflag"
)

func Main(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("pipefitter", flag.ContinueOnError)

	if err := flags.Parse(args); err != nil {
		return fmt.Errorf("unable to parse flags: %w", err)
	}

	slog.InfoContext(ctx, "hello there")

	return nil
}
