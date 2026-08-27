package cmd

import (
	"bytes"
	"context"
	"testing"

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
			wantErr:     ErrUsage,
			wantErrOut:  "Usage:",
			wantOutEmpt: true,
		},
		"help flag prints usage": {
			args:        []string{"--help"},
			wantErr:     ErrUsage,
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

// TestRunUsageStaysOffStdout guards the invariant that stdout carries only the
// generated pipeline, so `pipefitter ... | buildkite-agent pipeline upload`
// never sees help text.
func TestRunUsageStaysOffStdout(t *testing.T) {
	t.Parallel()

	var out, errOut bytes.Buffer

	err := Run(context.Background(), Host{Out: &out, ErrOut: &errOut}, []string{"help"})

	require.ErrorIs(t, err, ErrUsage)
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
