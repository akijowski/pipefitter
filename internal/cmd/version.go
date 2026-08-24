package cmd

import (
	"context"
	"fmt"
	"io"
	"runtime/debug"

	flag "github.com/spf13/pflag"
)

// version is overridden at link time by GoReleaser. When pipefitter is built
// with plain `go build` it stays empty and we fall back to the module's build
// info.
var version string

type versionCmd struct{}

func (*versionCmd) Name() string { return "version" }

func (*versionCmd) Description() string { return "print the pipefitter version" }

func (*versionCmd) Flags(*flag.FlagSet) {}

func (*versionCmd) Run(_ context.Context, out io.Writer, _ []string) error {
	_, err := fmt.Fprintln(out, buildVersion())

	return err
}

func buildVersion() string {
	if version != "" {
		return version
	}

	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" {
		return info.Main.Version
	}

	return "unknown"
}
