package cmd

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRun(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		args        []string
		wantErr     error
		wantErrOut  string
		wantOutEmpt bool
	}{
		"no args prints usage": {
			args:        nil,
			wantErr:     ErrHelp,
			wantErrOut:  "Usage:",
			wantOutEmpt: true,
		},
		"help flag prints usage": {
			args:        []string{"--help"},
			wantErr:     ErrHelp,
			wantErrOut:  "Usage:",
			wantOutEmpt: true,
		},
		"unknown command names the offender": {
			args:        []string{"bogus"},
			wantErr:     ErrUsage,
			wantErrOut:  `unknown command "bogus"`,
			wantOutEmpt: true,
		},
		"unknown flag does not double-report": {
			args:        []string{"version", "--nope"},
			wantErr:     ErrUsage,
			wantErrOut:  "unknown flag",
			wantOutEmpt: true,
		},
		"version writes to out": {
			args: []string{"version"},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var out, errOut bytes.Buffer

			err := Run(context.Background(), Host{Out: &out, ErrOut: &errOut}, tc.args)

			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
			} else {
				require.NoError(t, err)
			}

			if tc.wantErrOut != "" {
				assert.Contains(t, errOut.String(), tc.wantErrOut)
			}

			if tc.wantOutEmpt {
				assert.Empty(t, out.String())
			}
		})
	}
}

// TestRunSentinels pins which sentinel each kind of bad or help invocation
// returns, because that is what main maps to an exit code: asking for help is a
// success, getting the invocation wrong is not.
func TestRunSentinels(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		args    []string
		wantErr error
	}{
		"bare invocation":    {args: nil, wantErr: ErrHelp},
		"help subcommand":    {args: []string{"help"}, wantErr: ErrHelp},
		"--help":             {args: []string{"--help"}, wantErr: ErrHelp},
		"-h":                 {args: []string{"-h"}, wantErr: ErrHelp},
		"subcommand --help":  {args: []string{"generate", "--help"}, wantErr: ErrHelp},
		"unknown command":    {args: []string{"bogus"}, wantErr: ErrUsage},
		"unknown flag":       {args: []string{"version", "--nope"}, wantErr: ErrUsage},
		"flag missing value": {args: []string{"generate", "--values"}, wantErr: ErrUsage},
		"version":            {args: []string{"version"}, wantErr: nil},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var out, errOut bytes.Buffer

			err := Run(context.Background(), Host{FS: fstest.MapFS{}, Out: &out, ErrOut: &errOut}, tc.args)

			if tc.wantErr == nil {
				require.NoError(t, err)

				return
			}

			require.ErrorIs(t, err, tc.wantErr)
			assert.NotEmpty(t, errOut.String(), "usage or the reason must reach stderr")
		})
	}
}

// TestRunHelpAndUsageStayOffStdout guards the invariant that stdout carries only the
// generated pipeline, so `pipefitter ... | buildkite-agent pipeline upload`
// never sees help text.
func TestRunHelpAndUsageStayOffStdout(t *testing.T) {
	t.Parallel()

	var out, errOut bytes.Buffer

	err := Run(context.Background(), Host{Out: &out, ErrOut: &errOut}, []string{"help"})

	require.ErrorIs(t, err, ErrHelp)
	assert.Empty(t, out.String(), "stdout must carry only pipeline output")
	assert.NotEmpty(t, errOut.String(), "usage text must go to stderr")
}

func TestCommandsRegistryKeysMatchNames(t *testing.T) {
	t.Parallel()

	for key, c := range commands() {
		assert.Equal(t, key, c.Name(), "registry key must match command Name()")
		assert.NotEmpty(t, c.Description(), "command %q needs a Description", key)
	}
}

// TestLogFileTeesEverythingOnErrOut covers --log-file: it captures the whole
// stderr transcript, not just diagnostics.
//
// The transcript has to include the final error line, because for the most
// common failure — a bundle that cannot be read — that line is the only output
// there is. A file containing everything except the reason would be useless
// exactly when you needed it.
func TestLogFileTeesEverythingOnErrOut(t *testing.T) {
	t.Parallel()

	dangling := map[string]string{
		defaultBundleDir + "/test.tmpl": "steps:\n  - key: deploy\n    depends_on: nope\n",
	}

	tests := map[string]struct {
		files   map[string]string
		args    []string
		wantIn  []string
		wantErr bool
	}{
		"validate findings and the error line": {
			files:   dangling,
			args:    []string{"validate"},
			wantIn:  []string{"nope", "pipefitter:"},
			wantErr: true,
		},
		"generate findings and the error line": {
			files:   dangling,
			args:    []string{"generate"},
			wantIn:  []string{"nope", "pipefitter:"},
			wantErr: true,
		},
		"a failure with no findings still records the reason": {
			files:   map[string]string{},
			args:    []string{"generate", "does-not-exist"},
			wantIn:  []string{"does-not-exist", "pipefitter:"},
			wantErr: true,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			logPath := filepath.Join(t.TempDir(), "pipefitter.log")

			var out, errOut bytes.Buffer

			host := hostWith(tc.files, &out, &errOut)
			args := append(tc.args, "--log-file", logPath)

			err := Run(context.Background(), host, args)

			if tc.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}

			logged, readErr := os.ReadFile(logPath)
			require.NoError(t, readErr, "the log file must exist")

			assert.Equal(t, errOut.String(), string(logged),
				"the file is a tee of stderr, not a different rendering")

			for _, want := range tc.wantIn {
				assert.Contains(t, string(logged), want)
			}

			assert.Empty(t, out.String(), "stdout carries only a pipeline")
		})
	}
}

// TestLogFileWorksAlongsideSubcommandFlags pins the aesthetic: --log-file is
// accepted wherever a subcommand's own flags are, so its position never matters.
func TestLogFileWorksAlongsideSubcommandFlags(t *testing.T) {
	t.Parallel()

	logPath := filepath.Join(t.TempDir(), "pipefitter.log")

	var out, errOut bytes.Buffer

	host := hostWith(map[string]string{
		defaultBundleDir + "/values.yaml": "queue: default\n",
		defaultBundleDir + "/test.tmpl":   "agents:\n  queue: \"{{ .Values.queue }}\"\nsteps:\n  - key: t\n",
		"over.yaml":                       "queue: big\n",
	}, &out, &errOut)

	err := Run(context.Background(), host,
		[]string{"generate", "--log-file", logPath, "--values", "over.yaml"})

	require.NoError(t, err, "stderr: %s", errOut.String())
	assert.Contains(t, out.String(), "queue: big")

	logged, readErr := os.ReadFile(logPath)
	require.NoError(t, readErr)
	assert.Empty(t, string(logged), "a successful run says nothing on stderr")
}

// TestLogFileTruncates keeps one run to one transcript, rather than accumulating
// across retries of a CI step.
func TestLogFileTruncates(t *testing.T) {
	t.Parallel()

	logPath := filepath.Join(t.TempDir(), "pipefitter.log")
	require.NoError(t, os.WriteFile(logPath, []byte("stale content from a previous run\n"), 0o644))

	var out, errOut bytes.Buffer

	host := hostWith(map[string]string{}, &out, &errOut)

	_ = Run(context.Background(), host, []string{"generate", "nope", "--log-file", logPath})

	logged, readErr := os.ReadFile(logPath)
	require.NoError(t, readErr)
	assert.NotContains(t, string(logged), "stale content")
}

// TestLogFileUnwritablePathIsAnError reports a bad path rather than silently
// carrying on without a transcript.
func TestLogFileUnwritablePathIsAnError(t *testing.T) {
	t.Parallel()

	var out, errOut bytes.Buffer

	host := hostWith(map[string]string{}, &out, &errOut)

	err := Run(context.Background(), host,
		[]string{"version", "--log-file", filepath.Join(t.TempDir(), "no", "such", "dir", "x.log")})

	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrUsage, "an unopenable path is an operational failure, not misuse")
}
