// Package cmd wires the pipefitter subcommands together and dispatches to them.
package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"sort"
	"text/tabwriter"

	"github.com/spf13/pflag"
)

var (
	// ErrHelp signals that the user asked for help. The usage text has already been
	// written to Host.ErrOut, and this is not a failure.
	ErrHelp = errors.New("help requested")

	// ErrUsage signals that the invocation was malformed.
	ErrUsage = errors.New("usage")
)

// Host is everything a subcommand needs from outside the program.
//
// Passing these in rather than reaching for os.DirFS and os.Environ is what
// makes the subcommands testable: a test supplies an fstest.MapFS and a plain
// map, with no working-directory changes and no process environment to restore.
// Every bug this package has had lived in the code that built these values
// inline, where no test could reach it.
//
// It is a parameter rather than a receiver on purpose. On a receiver, methods
// quietly reach for fields until the struct is a god object; as a parameter,
// every dependency stays visible in the signature.
type Host struct {
	// FS is the tree bundles and values files are read from, rooted at the
	// working directory.
	FS fs.FS
	// Environ is the process environment, which becomes .Env in a template.
	Environ map[string]string
	// Out receives the generated pipeline, and nothing else.
	Out io.Writer
	// ErrOut receives usage, diagnostics and validation findings.
	ErrOut io.Writer
}

// OSHost builds a Host from the current process.
//
// This is the only place pipefitter touches ambient process state, which is
// what keeps everything below it a function of its inputs.
func OSHost() Host {
	return Host{
		FS:      os.DirFS("."),
		Environ: environMap(),
		Out:     os.Stdout,
		ErrOut:  os.Stderr,
	}
}

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
	// Run executes the subcommand with the remaining positional arguments.
	// A subcommand with a payload writes it to host.Out; diagnostics and
	// findings go to host.ErrOut. A subcommand with no payload, such as
	// validate, must leave host.Out untouched.
	Run(ctx context.Context, host Host, args []string) error
}

// registerValuesFlag declares --values on fs, binding it to files.
//
// generate and validate both take it and must agree on its name, shorthand and
// description, since they accept the same inputs and differ only in what they do
// with the result. Declared here rather than embedded in a shared struct so each
// subcommand's Flags method stays explicit and can add flags of its own.
func registerValuesFlag(fs *pflag.FlagSet, files *[]string) {
	fs.StringSliceVarP(files, "values", "f", nil,
		"values file layered over each bundle's own defaults; repeatable, applied left to right")
}

// commands is the subcommand registry, keyed by Name.
func commands() map[string]command {
	cmds := []command{
		&versionCmd{},
		&generateCmd{},
		&validateCmd{},
	}

	reg := make(map[string]command, len(cmds))
	for _, c := range cmds {
		reg[c.Name()] = c
	}

	return reg
}

// Run parses args and dispatches to the named subcommand.
//
// Run returns ErrUsage for help requests and malformed invocations, having
// already written the relevant usage text to host.ErrOut.
func Run(ctx context.Context, host Host, args []string) error {
	reg := commands()

	if len(args) == 0 {
		usage(host.ErrOut, reg)

		return ErrHelp
	}

	name := args[0]

	switch name {
	case "-h", "--help", "help":
		usage(host.ErrOut, reg)

		return ErrHelp
	}

	cmd, ok := reg[name]
	if !ok {
		fmt.Fprintf(host.ErrOut, "pipefitter: unknown command %q\n\n", name)
		usage(host.ErrOut, reg)

		return ErrUsage
	}

	fs := pflag.NewFlagSet("pipefitter "+name, pflag.ContinueOnError)
	fs.SetOutput(host.ErrOut)

	var logFile string
	lfHelp := "also write everything sent to stderr to this file. File is truncated before writes."
	fs.StringVar(&logFile, "log-file", "", lfHelp)
	cmd.Flags(fs)

	if err := fs.Parse(args[1:]); err != nil {
		// pflag stays silent under ContinueOnError, so we report it ourselves.
		if !errors.Is(err, pflag.ErrHelp) {
			fmt.Fprintf(host.ErrOut, "pipefitter: %s %v\n", name, err)

			return ErrUsage
		}

		commandUsage(host.ErrOut, cmd, fs)

		return ErrHelp
	}

	if logFile != "" {
		f, err := os.Create(logFile)
		if err != nil {
			return err
		}
		defer func() {
			// consider writing to a log file as best effort.
			// Stderr should be considered authoritative and
			// will contain the relevant output.
			_ = f.Close()
		}()

		host.ErrOut = io.MultiWriter(host.ErrOut, f)
	}

	if err := cmd.Run(ctx, host, fs.Args()); err != nil {
		fmt.Fprintf(host.ErrOut, "pipefitter: %v\n", err)

		return err
	}

	return nil
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
