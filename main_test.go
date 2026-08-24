package main

import (
	"bytes"
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
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

			got := runMain(context.Background(), &out, &errOut, tc.args)

			assert.Equal(t, tc.wantCode, got, "stderr: %s", errOut.String())
		})
	}
}
