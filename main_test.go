package main

import (
	"bytes"
	"context"
	"testing"
)

func TestRunMain(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		args     []string
		wantCode int
	}{
		"version succeeds":       {args: []string{"version"}, wantCode: exitCodeSuccess},
		"no args is a usage err": {args: nil, wantCode: exitCodeErr},
		"unknown command errs":   {args: []string{"bogus"}, wantCode: exitCodeErr},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var out, errOut bytes.Buffer

			if got := runMain(context.Background(), &out, &errOut, tc.args); got != tc.wantCode {
				t.Errorf("runMain() = %d, want %d (stderr: %q)", got, tc.wantCode, errOut.String())
			}
		})
	}
}
