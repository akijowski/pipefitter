package main

import (
	"bytes"
	"context"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"

	"github.com/akijowski/pipefitter/internal/cmd"
)

// TestRunMainReportsFailuresOnErrOut pins that a failure explains itself on the
// Host's error writer rather than through the package logger.
//
// Routing it through slog put the message on a handler wired to os.Stderr inside
// main, so no test could see what a failure actually said — and it rendered a
// terminal message as a structured record, timestamp and escaped quotes
// included.
func TestRunMainReportsFailuresOnErrOut(t *testing.T) {
	t.Parallel()

	var out, errOut bytes.Buffer

	host := cmd.Host{FS: fstest.MapFS{}, Out: &out, ErrOut: &errOut}

	got := runMain(context.Background(), host, []string{"generate", "does-not-exist"})

	assert.Equal(t, exitCodeErr, got)
	assert.Contains(t, errOut.String(), "does-not-exist",
		"the failure must say what went wrong")
	assert.Contains(t, errOut.String(), "pipefitter:",
		"and identify which program is speaking")
	assert.NotContains(t, errOut.String(), "level=ERROR",
		"a terminal message is not a log record")
	assert.Empty(t, out.String())
}

// TestRunMainHelpIsNotAnError keeps help from being reported as a failure: usage
// goes to stderr, but no error line follows it.
func TestRunMainHelpIsNotAnError(t *testing.T) {
	t.Parallel()

	var out, errOut bytes.Buffer

	host := cmd.Host{FS: fstest.MapFS{}, Out: &out, ErrOut: &errOut}

	got := runMain(context.Background(), host, nil)

	assert.Equal(t, exitCodeSuccess, got)
	assert.NotContains(t, errOut.String(), "pipefitter:",
		"asking for help must not produce an error line")
}

func TestRunMain(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		args     []string
		wantCode int
	}{
		// 0 — asking for help is not a failure.
		"version succeeds":            {args: []string{"version"}, wantCode: exitCodeSuccess},
		"bare invocation prints help": {args: nil, wantCode: exitCodeSuccess},
		"help subcommand":             {args: []string{"help"}, wantCode: exitCodeSuccess},
		"--help flag":                 {args: []string{"--help"}, wantCode: exitCodeSuccess},
		"-h flag":                     {args: []string{"-h"}, wantCode: exitCodeSuccess},
		"subcommand --help":           {args: []string{"generate", "--help"}, wantCode: exitCodeSuccess},

		// 2 — the user got the invocation wrong.
		"unknown command":      {args: []string{"bogus"}, wantCode: exitCodeUsage},
		"unknown flag":         {args: []string{"version", "--nope"}, wantCode: exitCodeUsage},
		"malformed flag value": {args: []string{"generate", "--values"}, wantCode: exitCodeUsage},

		// 1 — the invocation was fine, the work failed.
		"missing bundle": {args: []string{"generate", "does-not-exist"}, wantCode: exitCodeErr},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var out, errOut bytes.Buffer

			host := cmd.Host{FS: fstest.MapFS{}, Out: &out, ErrOut: &errOut}

			got := runMain(context.Background(), host, tc.args)

			assert.Equal(t, tc.wantCode, got, "stderr: %s", errOut.String())
		})
	}
}
