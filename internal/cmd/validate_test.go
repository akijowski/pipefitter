package cmd

import (
	"bytes"
	"context"
	"io"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateRegistered(t *testing.T) {
	t.Parallel()

	reg := commands()

	require.Contains(t, reg, "validate")
	assert.NotEmpty(t, reg["validate"].Description())
}

// TestBuildPipelineFindings covers the shared stages: both commands run these,
// and the findings come back as data so each can decide what to do with them —
// validate reports, generate refuses.
func TestBuildPipelineFindings(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		files      map[string]string
		dirs       []string
		wantFindin int
		wantIn     []string
		wantErr    string
	}{
		"a sound pipeline has no findings": {
			files: map[string]string{
				defaultBundleDir + "/test.tmpl": "steps:\n" +
					"  - key: test\n" +
					"  - key: deploy\n    depends_on: test\n",
			},
		},
		"a dangling dependency is reported": {
			files: map[string]string{
				defaultBundleDir + "/test.tmpl": "steps:\n" +
					"  - key: deploy\n    depends_on: nope\n",
			},
			wantFindin: 1,
			wantIn:     []string{"nope", "deploy"},
		},
		"a dependency across bundles resolves": {
			files: map[string]string{
				"one/a.tmpl": "steps:\n  - key: test\n",
				"two/b.tmpl": "steps:\n  - key: deploy\n    depends_on: test\n",
			},
			dirs: []string{"one", "two"},
		},
		"every dangling dependency is reported": {
			files: map[string]string{
				defaultBundleDir + "/test.tmpl": "steps:\n" +
					"  - key: a\n    depends_on: x\n" +
					"  - key: b\n    depends_on: y\n",
			},
			wantFindin: 2,
		},

		// Failures before validation surface as errors, not findings.
		"a missing bundle is an error, not a finding": {
			files:   map[string]string{},
			dirs:    []string{"nope"},
			wantErr: "nope",
		},
		"a duplicate step key is an error, not a finding": {
			// Caught in pipeline.Merge, which is the only place source names
			// are still in scope.
			files: map[string]string{
				"one/a.tmpl": "steps:\n  - key: dup\n",
				"two/b.tmpl": "steps:\n  - key: dup\n",
			},
			dirs:    []string{"one", "two"},
			wantErr: "dup",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			fsys := fstest.MapFS{}
			for n, body := range tc.files {
				fsys[n] = &fstest.MapFile{Data: []byte(body)}
			}

			checked, err := checkPipeline(fsys, nil, tc.dirs, nil)

			if tc.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantErr)

				return
			}

			require.NoError(t, err)
			assert.Len(t, checked.findings, tc.wantFindin)

			var joined string
			for _, f := range checked.findings {
				joined += f.Message + "\n"
			}

			for _, want := range tc.wantIn {
				assert.Contains(t, joined, want)
			}
		})
	}
}

// TestValidateNeverWritesStdout is the invariant: validate produces diagnostics,
// never a document, so nothing may reach stdout on any path.
func TestValidateNeverWritesStdout(t *testing.T) {
	t.Parallel()

	cases := map[string][]string{
		"missing bundle":   {"validate", "does-not-exist"},
		"default bundle":   {"validate"},
		"multiple missing": {"validate", "nope-a", "nope-b"},
	}

	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var out, errOut bytes.Buffer

			_ = Run(context.Background(), Host{FS: fstest.MapFS{}, Out: &out, ErrOut: &errOut}, args)

			assert.Empty(t, out.String(), "validate must never write to stdout")
		})
	}
}

// TestBuildPipelineReportsFindingsWithoutFailing pins that the shared stages do
// not decide policy: a dangling dependency is a finding, and no error, because
// generate and validate treat it differently.
func TestBuildPipelineReportsFindingsWithoutFailing(t *testing.T) {
	t.Parallel()

	fsys := fstest.MapFS{
		defaultBundleDir + "/test.tmpl": &fstest.MapFile{
			Data: []byte("steps:\n  - key: deploy\n    depends_on: nope\n"),
		},
	}

	checked, err := checkPipeline(fsys, nil, nil, nil)

	require.NoError(t, err, "findings are data, not an error")
	require.Len(t, checked.findings, 1)
	assert.NotEmpty(t, checked.doc, "the document is still returned so it can be reported on")
}

// TestBuildPipelineSoundPipelineHasNoFindings guards against the validation
// wiring rejecting valid input.
func TestBuildPipelineSoundPipelineHasNoFindings(t *testing.T) {
	t.Parallel()

	fsys := fstest.MapFS{
		defaultBundleDir + "/test.tmpl": &fstest.MapFile{
			Data: []byte("steps:\n  - key: test\n  - key: deploy\n    depends_on: test\n"),
		},
	}

	checked, err := checkPipeline(fsys, nil, nil, nil)

	require.NoError(t, err)
	assert.Empty(t, checked.findings)
	assert.NotEmpty(t, checked.doc)
}

// hostWith builds a Host over an in-memory bundle, which is what lets these
// tests drive Run — the real dispatch path, where every bug in this package has
// hidden — without touching the working directory or the process environment.
//
// Before Host existed these needed t.Chdir and a temp directory, and so could
// not be parallel.
func hostWith(files map[string]string, out, errOut io.Writer) Host {
	fsys := fstest.MapFS{}
	for name, body := range files {
		fsys[name] = &fstest.MapFile{Data: []byte(body)}
	}

	return Host{FS: fsys, Out: out, ErrOut: errOut}
}

// TestValidateExitsNonZeroOnFindings is the whole point of the subcommand: a
// validation step that prints problems and then succeeds is worse than no
// validation at all, because CI goes green.
func TestValidateExitsNonZeroOnFindings(t *testing.T) {
	t.Parallel()

	var out, errOut bytes.Buffer

	host := hostWith(map[string]string{
		defaultBundleDir + "/test.tmpl": "steps:\n  - key: deploy\n    depends_on: nope\n",
	}, &out, &errOut)

	err := Run(context.Background(), host, []string{"validate"})

	require.Error(t, err, "validate must fail when it reports findings")
	assert.Empty(t, out.String(), "validate never writes a document to stdout")
	assert.Contains(t, errOut.String(), "nope", "the finding must be reported to stderr")
}

// TestValidateExitsZeroOnASoundPipeline is the counterpart: a clean pipeline
// must not be reported as a failure.
func TestValidateExitsZeroOnASoundPipeline(t *testing.T) {
	t.Parallel()

	var out, errOut bytes.Buffer

	host := hostWith(map[string]string{
		defaultBundleDir + "/test.tmpl": "steps:\n" +
			"  - key: test\n" +
			"  - key: deploy\n    depends_on: test\n",
	}, &out, &errOut)

	err := Run(context.Background(), host, []string{"validate"})

	require.NoError(t, err)
	assert.Empty(t, out.String(), "validate never writes a document to stdout")
}

// TestGenerateExitsNonZeroOnFindings pins fail-closed in the binary rather than
// only in checkPipeline, and that the reason reaches the user.
func TestGenerateExitsNonZeroOnFindings(t *testing.T) {
	t.Parallel()

	var out, errOut bytes.Buffer

	host := hostWith(map[string]string{
		defaultBundleDir + "/test.tmpl": "steps:\n  - key: deploy\n    depends_on: nope\n",
	}, &out, &errOut)

	err := Run(context.Background(), host, []string{"generate"})

	require.Error(t, err)
	assert.Empty(t, out.String(), "a pipeline that fails validation must not reach stdout")
	assert.Contains(t, err.Error(), "nope",
		"the error must say what is wrong, not just how many problems there are")
}

// TestGenerateWritesADocumentOnSuccess is the happy path through Run, which the
// unit tests around checkPipeline never exercise.
func TestGenerateWritesADocumentOnSuccess(t *testing.T) {
	t.Parallel()

	var out, errOut bytes.Buffer

	host := hostWith(map[string]string{
		defaultBundleDir + "/values.yaml": "goVersion: \"1.26\"\n",
		defaultBundleDir + "/test.tmpl": "steps:\n" +
			"  - key: test\n    label: \"go {{ .Values.goVersion }}\"\n",
	}, &out, &errOut)

	err := Run(context.Background(), host, []string{"generate"})

	require.NoError(t, err, "stderr: %s", errOut.String())
	assert.Contains(t, out.String(), "key: test")
	assert.Contains(t, out.String(), "go 1.26")
}
