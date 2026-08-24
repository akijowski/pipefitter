package cmd

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
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

			err := Run(context.Background(), &out, &errOut, tc.args)

			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("Run() error = %v, want %v", err, tc.wantErr)
				}
			} else if err != nil {
				t.Fatalf("Run() unexpected error: %v", err)
			}

			if tc.wantErrOut != "" && !strings.Contains(errOut.String(), tc.wantErrOut) {
				t.Errorf("stderr = %q, want it to contain %q", errOut.String(), tc.wantErrOut)
			}

			if tc.wantOutEmpt && out.Len() != 0 {
				t.Errorf("stdout = %q, want empty", out.String())
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

	if err := Run(context.Background(), &out, &errOut, []string{"help"}); !errors.Is(err, ErrUsage) {
		t.Fatalf("Run() error = %v, want ErrUsage", err)
	}

	if out.Len() != 0 {
		t.Errorf("stdout = %q, want empty", out.String())
	}

	if errOut.Len() == 0 {
		t.Error("stderr is empty, want usage text")
	}
}

func TestCommandsRegistryKeysMatchNames(t *testing.T) {
	t.Parallel()

	for key, c := range commands() {
		if key != c.Name() {
			t.Errorf("registry key %q != command Name() %q", key, c.Name())
		}

		if c.Description() == "" {
			t.Errorf("command %q has an empty Description", key)
		}
	}
}
