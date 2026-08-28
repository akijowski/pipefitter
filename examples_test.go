package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/akijowski/pipefitter/internal/cmd"
)

// TestExamplesRender renders every bundle under examples/ the way the README
// tells a reader to.
//
// Documentation that is executed cannot rot: an example that stops working
// fails the build rather than wasting someone's first ten minutes with the tool.
// Discovering the bundles rather than listing them means a new example is
// covered the moment it is added.
func TestExamplesRender(t *testing.T) {
	t.Parallel()

	entries, err := os.ReadDir("examples")
	require.NoError(t, err)

	var bundles []string

	for _, e := range entries {
		if e.IsDir() {
			bundles = append(bundles, filepath.Join("examples", e.Name()))
		}
	}

	require.NotEmpty(t, bundles, "examples/ must hold at least one bundle")

	for _, dir := range bundles {
		t.Run(dir, func(t *testing.T) {
			t.Parallel()

			var out, errOut bytes.Buffer

			// No Environ: an example must render standalone, outside a
			// Buildkite agent, or the README's instructions do not work.
			host := cmd.Host{FS: os.DirFS("."), Out: &out, ErrOut: &errOut}

			require.NoError(t,
				cmd.Run(context.Background(), host, []string{"generate", dir}),
				"stderr: %s", errOut.String())

			assert.Contains(t, out.String(), "steps:", "an example renders a pipeline")

			// And it must pass its own validation, since the README suggests
			// running validate against it.
			var vOut, vErrOut bytes.Buffer
			vHost := cmd.Host{FS: os.DirFS("."), Out: &vOut, ErrOut: &vErrOut}

			require.NoError(t,
				cmd.Run(context.Background(), vHost, []string{"validate", dir}),
				"stderr: %s", vErrOut.String())
			assert.Empty(t, vOut.String(), "validate writes no document")
		})
	}
}

// TestSimpleExampleMatchesTheReadme keeps the Quick start honest: the output
// printed there is what the example actually produces.
func TestSimpleExampleMatchesTheReadme(t *testing.T) {
	t.Parallel()

	var out, errOut bytes.Buffer

	host := cmd.Host{
		FS:      os.DirFS("."),
		Environ: map[string]string{"BUILDKITE_BRANCH": "main"},
		Out:     &out,
		ErrOut:  &errOut,
	}

	require.NoError(t,
		cmd.Run(context.Background(), host, []string{"generate", "examples/simple"}),
		"stderr: %s", errOut.String())

	for _, want := range []string{
		"queue: default",
		"branches: main",
		"command: go test ./...",
		"key: test",
		`label: ":go: test"`,
		`GO_VERSION: "1.26"`,
	} {
		assert.Contains(t, out.String(), want)
	}
}
