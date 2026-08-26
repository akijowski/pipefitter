// Package cmd wires the pipefitter subcommands together and dispatches to them.
package cmd

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"sort"
	"text/tabwriter"

	"github.com/spf13/pflag"
)

// ErrUsage signals that the user invoked pipefitter incorrectly, or asked for
// help. Callers should not log it as an error; the usage text has already been
// written to stderr.
var ErrUsage = errors.New("usage")

// command is a single pipefitter subcommand.
//
// Flags registers the subcommand's flags on the given FlagSet, returning
// nothing; Run is called with the positional arguments left after parsing.
type command interface {
	// Name is the word the user types, e.g. "generate".
	Name() string
	// Description is a one-line description shown in the top-level help.
	Description() string
	// Flags registers subcommand-specific flags.
	Flags(fs *pflag.FlagSet)
	// Run executes the subcommand with the remaining positional arguments,
	// writing its payload (pipeline YAML) to out.
	Run(ctx context.Context, out io.Writer, args []string) error
}

// commands is the subcommand registry, keyed by Name.
func commands() map[string]command {
	cmds := []command{
		&versionCmd{},
		&generateCmd{},
	}

	reg := make(map[string]command, len(cmds))
	for _, c := range cmds {
		reg[c.Name()] = c
	}

	return reg
}

// Run parses args and dispatches to the named subcommand.
//
// out receives command output (the generated pipeline); errOut receives usage
// and diagnostics. Run returns ErrUsage for help requests and malformed
// invocations, having already written the relevant usage text to errOut.
func Run(ctx context.Context, out, errOut io.Writer, args []string) error {
	reg := commands()

	if len(args) == 0 {
		usage(errOut, reg)

		return ErrUsage
	}

	name := args[0]

	switch name {
	case "-h", "--help", "help":
		usage(errOut, reg)

		return ErrUsage
	}

	cmd, ok := reg[name]
	if !ok {
		fmt.Fprintf(errOut, "pipefitter: unknown command %q\n\n", name)
		usage(errOut, reg)

		return ErrUsage
	}

	fs := pflag.NewFlagSet("pipefitter "+name, pflag.ContinueOnError)
	fs.SetOutput(errOut)
	cmd.Flags(fs)

	if err := fs.Parse(args[1:]); err != nil {
		// pflag stays silent under ContinueOnError, so we report it ourselves.
		if !errors.Is(err, flag.ErrHelp) {
			fmt.Fprintf(errOut, "pipefitter %s: %v\n", name, err)
		}

		commandUsage(errOut, cmd, fs)

		return ErrUsage
	}

	return cmd.Run(ctx, out, fs.Args())
}

func commandUsage(w io.Writer, cmd command, fs *pflag.FlagSet) {
	fmt.Fprintf(w, "\n%s\n\nUsage:\n\n\tpipefitter %s [flags]\n", cmd.Description(), cmd.Name())

	if usages := fs.FlagUsages(); usages != "" {
		fmt.Fprintf(w, "\nFlags:\n\n%s", usages)
	}
}

func usage(w io.Writer, reg map[string]command) {
	fmt.Fprint(w, "pipefitter generates Buildkite pipeline YAML on stdout.\n\n")
	fmt.Fprint(w, "Usage:\n\n\tpipefitter <command> [flags]\n\nCommands:\n\n")

	names := make([]string, 0, len(reg))
	for name := range reg {
		names = append(names, name)
	}
	sort.Strings(names)

	tw := tabwriter.NewWriter(w, 0, 8, 2, ' ', 0)
	for _, name := range names {
		fmt.Fprintf(tw, "\t%s\t%s\n", name, reg[name].Description())
	}
	tw.Flush()

	fmt.Fprint(w, "\nRun \"pipefitter <command> --help\" for details on a command.\n")
}
