package cmd

import (
	"bytes"
	"context"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/akijowski/pipefitter/internal/pipeline"
)

// bundleFS is the shape a repository has: one bundle under the default path.
func bundleFS(files map[string]string) fstest.MapFS {
	fsys := fstest.MapFS{}
	for name, body := range files {
		fsys[name] = &fstest.MapFile{Data: []byte(body)}
	}

	return fsys
}

// generateYAML runs the shared stages and then serializes, which is what the
// generate subcommand does. Tests that care about the emitted document use this
// rather than reaching into the intermediate result.
func generateYAML(fsys fstest.MapFS, vars map[string]string, dirs, valuesFiles []string) ([]byte, error) {
	checked, err := checkPipeline(fsys, vars, dirs, valuesFiles)
	if err != nil {
		return nil, err
	}

	return pipeline.Marshal(checked.doc)
}

func TestGenerateRegistered(t *testing.T) {
	t.Parallel()

	reg := commands()

	require.Contains(t, reg, "generate")
	assert.NotEmpty(t, reg["generate"].Description())
}

// TestBuildPipeline exercises the whole flow with no process state: an fs.FS
// stands in for the working tree and a map for the environment.
func TestBuildPipeline(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		files       map[string]string
		vars        map[string]string
		dirs        []string
		valuesFiles []string
		wantIn      []string
		wantNotIn   []string
		wantErr     string
	}{
		"renders a bundle using its own defaults": {
			files: map[string]string{
				defaultBundleDir + "/values.yaml": "goVersion: \"1.26\"\n",
				defaultBundleDir + "/test.tmpl": "steps:\n" +
					"  - key: test\n" +
					"    label: \"go {{ .Values.goVersion }}\"\n" +
					"    command: go test ./...\n",
			},
			wantIn: []string{"key: test", "go 1.26", "go test ./..."},
		},
		"reads the buildkite environment": {
			files: map[string]string{
				defaultBundleDir + "/test.tmpl": "steps:\n" +
					"  - key: test\n" +
					"    branches: \"{{ .Env.Buildkite.Branch }}\"\n",
			},
			vars:   map[string]string{"BUILDKITE_BRANCH": "main"},
			wantIn: []string{"branches: main"},
		},
		"a helper is usable from a template": {
			files: map[string]string{
				defaultBundleDir + "/_helpers.tpl": `{{ define "q" }}big{{ end }}`,
				defaultBundleDir + "/test.tmpl": "agents:\n" +
					"  queue: \"{{ include \"q\" . }}\"\n" +
					"steps:\n  - key: test\n",
			},
			wantIn: []string{"queue: big"},
		},
		"several templates in one bundle merge": {
			files: map[string]string{
				defaultBundleDir + "/a.tmpl": "steps:\n  - key: one\n",
				defaultBundleDir + "/b.tmpl": "steps:\n  - key: two\n",
			},
			wantIn: []string{"key: one", "key: two"},
		},
		"several bundles merge in argument order": {
			files: map[string]string{
				"shared/a.tmpl": "steps:\n  - key: shared\n",
				"local/b.tmpl":  "steps:\n  - key: local\n",
			},
			dirs:   []string{"shared", "local"},
			wantIn: []string{"key: shared", "key: local"},
		},
		"each bundle keeps its own defaults": {
			// Per-bundle isolation: one bundle's defaults must not leak into
			// another's render, even under the same key name.
			files: map[string]string{
				"one/values.yaml": "who: one\n",
				"one/a.tmpl":      "steps:\n  - key: one\n    label: \"{{ .Values.who }}\"\n",
				"two/values.yaml": "who: two\n",
				"two/b.tmpl":      "steps:\n  - key: two\n    label: \"{{ .Values.who }}\"\n",
			},
			dirs:   []string{"one", "two"},
			wantIn: []string{"label: one", "label: two"},
		},
		"a values file overrides bundle defaults": {
			files: map[string]string{
				defaultBundleDir + "/values.yaml": "queue: default\n",
				defaultBundleDir + "/test.tmpl":   "agents:\n  queue: \"{{ .Values.queue }}\"\nsteps:\n  - key: t\n",
				"override.yaml":                   "queue: big\n",
			},
			valuesFiles: []string{"override.yaml"},
			wantIn:      []string{"queue: big"},
			wantNotIn:   []string{"queue: default"},
		},
		"values files layer left to right": {
			files: map[string]string{
				defaultBundleDir + "/values.yaml": "queue: default\n",
				defaultBundleDir + "/test.tmpl":   "agents:\n  queue: \"{{ .Values.queue }}\"\nsteps:\n  - key: t\n",
				"base.yaml":                       "queue: base\n",
				"prod.yaml":                       "queue: prod\n",
			},
			valuesFiles: []string{"base.yaml", "prod.yaml"},
			wantIn:      []string{"queue: prod"},
		},
		"a values file is shared across bundles": {
			files: map[string]string{
				"one/a.tmpl": "steps:\n  - key: one\n    label: \"{{ .Values.shared }}\"\n",
				"two/b.tmpl": "steps:\n  - key: two\n    label: \"{{ .Values.shared }}\"\n",
				"vals.yaml":  "shared: yes\n",
			},
			dirs:        []string{"one", "two"},
			valuesFiles: []string{"vals.yaml"},
			wantIn:      []string{"key: one", "key: two"},
		},

		// --- errors ---
		"a missing bundle is an error": {
			files:   map[string]string{},
			dirs:    []string{"nope"},
			wantErr: "nope",
		},
		"a missing values file is an error": {
			files: map[string]string{
				defaultBundleDir + "/test.tmpl": "steps:\n  - key: t\n",
			},
			valuesFiles: []string{"absent.yaml"},
			wantErr:     "absent.yaml",
		},
		"a template referencing an undeclared key is an error": {
			files: map[string]string{
				defaultBundleDir + "/test.tmpl": "steps:\n  - key: t\n    label: \"{{ .Values.nope }}\"\n",
			},
			wantErr: "nope",
		},
		"a duplicate step key across bundles is an error": {
			files: map[string]string{
				"one/a.tmpl": "steps:\n  - key: dup\n",
				"two/b.tmpl": "steps:\n  - key: dup\n",
			},
			dirs:    []string{"one", "two"},
			wantErr: "dup",
		},
		"a template rendering invalid yaml is an error": {
			files: map[string]string{
				defaultBundleDir + "/test.tmpl": "steps:\n  - [unclosed\n",
			},
			wantErr: "test.tmpl",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, err := generateYAML(bundleFS(tc.files), tc.vars, tc.dirs, tc.valuesFiles)

			if tc.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantErr)

				return
			}

			require.NoError(t, err)

			for _, want := range tc.wantIn {
				assert.Contains(t, string(got), want)
			}

			for _, unwanted := range tc.wantNotIn {
				assert.NotContains(t, string(got), unwanted)
			}
		})
	}
}

// TestBuildPipelineDefaultsToTheConventionalDir means `pipefitter generate` with
// no arguments works in a repository that follows the convention.
func TestBuildPipelineDefaultsToTheConventionalDir(t *testing.T) {
	t.Parallel()

	fsys := bundleFS(map[string]string{
		defaultBundleDir + "/test.tmpl": "steps:\n  - key: t\n",
	})

	got, err := generateYAML(fsys, nil, nil, nil)

	require.NoError(t, err)
	assert.Contains(t, string(got), "key: t")
}

// TestBuildPipelineOutputIsValidYAML checks the payload actually round-trips,
// rather than only containing the right substrings.
func TestBuildPipelineOutputIsValidYAML(t *testing.T) {
	t.Parallel()

	fsys := bundleFS(map[string]string{
		defaultBundleDir + "/a.tmpl": "env:\n  A: \"1\"\nsteps:\n  - key: one\n",
		defaultBundleDir + "/b.tmpl": "env:\n  B: \"2\"\nsteps:\n  - key: two\n",
	})

	got, err := generateYAML(fsys, nil, nil, nil)
	require.NoError(t, err)

	assert.Contains(t, string(got), "A: \"1\"")
	assert.Contains(t, string(got), "B: \"2\"")
	assert.Equal(t, 1, bytes.Count(got, []byte("steps:")), "exactly one steps key")
}

// TestGenerateWritesNothingToStdoutOnFailure is the invariant that makes
// `pipefitter generate | buildkite-agent pipeline upload` safe.
func TestGenerateWritesNothingToStdoutOnFailure(t *testing.T) {
	t.Parallel()

	var out, errOut bytes.Buffer

	host := Host{FS: fstest.MapFS{}, Out: &out, ErrOut: &errOut}

	err := Run(context.Background(), host, []string{"generate", "does-not-exist"})

	require.Error(t, err)
	assert.Empty(t, out.String(), "stdout must stay empty when generate fails")
}
