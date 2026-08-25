package source

import (
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLoadDir covers bundle layout: which files are templates, which are
// helpers, and which are ignored.
//
// LoadDir takes an fs.FS rather than a path, so these tests need no temporary
// directories and can run in parallel.
func TestLoadDir(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		files   fstest.MapFS
		dir     string
		assert  func(t *testing.T, b Bundle)
		wantErr string
	}{
		"loads values, templates and helpers": {
			files: fstest.MapFS{
				"b/values.yaml":  {Data: []byte("tag: v1\n")},
				"b/test.tmpl":    {Data: []byte("steps: []\n")},
				"b/_helpers.tpl": {Data: []byte(`{{ define "x" }}y{{ end }}`)},
			},
			dir: "b",
			assert: func(t *testing.T, b Bundle) {
				assert.Equal(t, map[string]any{"tag": "v1"}, b.Defaults)
				assert.Contains(t, b.Templates, "test.tmpl")
				assert.Contains(t, b.Helpers, "_helpers.tpl")
				assert.NotContains(t, b.Templates, "_helpers.tpl")
				assert.NotContains(t, b.Templates, "values.yaml")
			},
		},
		"the bundle name is the directory": {
			files: fstest.MapFS{"b/test.tmpl": {Data: []byte("steps: []\n")}},
			dir:   "b",
			assert: func(t *testing.T, b Bundle) {
				assert.Equal(t, "b", b.Name)
			},
		},
		"a nested directory path works": {
			files: fstest.MapFS{".buildkite/pipefitter/test.tmpl": {Data: []byte("steps: []\n")}},
			dir:   ".buildkite/pipefitter",
			assert: func(t *testing.T, b Bundle) {
				assert.Equal(t, ".buildkite/pipefitter", b.Name)
				assert.Contains(t, b.Templates, "test.tmpl")
			},
		},

		// --- file classification ---
		"only .tmpl files are templates": {
			// A bundle directory may hold documentation or licences. Rendering
			// those as templates would emit garbage into the pipeline, so
			// templates are opt-in by extension rather than opt-out by denylist.
			files: fstest.MapFS{
				"b/test.tmpl": {Data: []byte("steps: []\n")},
				"b/README.md": {Data: []byte("# notes\n")},
				"b/LICENSE":   {Data: []byte("MIT\n")},
			},
			dir: "b",
			assert: func(t *testing.T, b Bundle) {
				assert.Len(t, b.Templates, 1)
				assert.Contains(t, b.Templates, "test.tmpl")
				assert.NotContains(t, b.Templates, "README.md")
				assert.NotContains(t, b.Templates, "LICENSE")
			},
		},
		"an underscore prefix makes a helper regardless of extension": {
			files: fstest.MapFS{
				"b/test.tmpl":    {Data: []byte("steps: []\n")},
				"b/_helpers.tpl": {Data: []byte(`{{ define "a" }}a{{ end }}`)},
				"b/_more.tmpl":   {Data: []byte(`{{ define "b" }}b{{ end }}`)},
				"b/_notes.txt":   {Data: []byte("scratch\n")},
			},
			dir: "b",
			assert: func(t *testing.T, b Bundle) {
				assert.Len(t, b.Templates, 1, "only test.tmpl renders a document")
				assert.Contains(t, b.Helpers, "_helpers.tpl")
				assert.Contains(t, b.Helpers, "_more.tmpl")
				assert.Contains(t, b.Helpers, "_notes.txt")
			},
		},
		"subdirectories are ignored": {
			files: fstest.MapFS{
				"b/test.tmpl":       {Data: []byte("steps: []\n")},
				"b/nested/x.tmpl":   {Data: []byte("steps: []\n")},
				"b/nested/deep.tpl": {Data: []byte("x\n")},
			},
			dir: "b",
			assert: func(t *testing.T, b Bundle) {
				assert.Len(t, b.Templates, 1)
				assert.Contains(t, b.Templates, "test.tmpl")
			},
		},
		"template content is preserved verbatim": {
			files: fstest.MapFS{
				"b/test.tmpl": {Data: []byte("steps:\n  - label: {{ .Values.tag }}\n")},
			},
			dir: "b",
			assert: func(t *testing.T, b Bundle) {
				assert.Equal(t, "steps:\n  - label: {{ .Values.tag }}\n", b.Templates["test.tmpl"])
			},
		},

		// --- values.yaml ---
		"a missing values.yaml is not an error": {
			files: fstest.MapFS{"b/test.tmpl": {Data: []byte("steps: []\n")}},
			dir:   "b",
			assert: func(t *testing.T, b Bundle) {
				assert.Empty(t, b.Defaults)
				assert.Len(t, b.Templates, 1)
			},
		},
		"an empty values.yaml is not an error": {
			files: fstest.MapFS{
				"b/values.yaml": {Data: []byte("")},
				"b/test.tmpl":   {Data: []byte("steps: []\n")},
			},
			dir: "b",
			assert: func(t *testing.T, b Bundle) {
				assert.Empty(t, b.Defaults)
			},
		},
		"nested values parse as map[string]any all the way down": {
			// values.Merge requires map[string]any at every level. Some YAML
			// libraries hand back map[any]any for nested mappings, which would
			// break the merge, so this is pinned rather than assumed.
			files: fstest.MapFS{
				"b/values.yaml": {Data: []byte("image:\n  repo: acme\n  tag: v1\n")},
				"b/test.tmpl":   {Data: []byte("steps: []\n")},
			},
			dir: "b",
			assert: func(t *testing.T, b Bundle) {
				require.Contains(t, b.Defaults, "image")

				nested, ok := b.Defaults["image"].(map[string]any)
				require.True(t, ok, "nested mappings must be map[string]any, got %T", b.Defaults["image"])
				assert.Equal(t, "acme", nested["repo"])
				assert.Equal(t, "v1", nested["tag"])
			},
		},
		"a malformed values.yaml is an error naming the file": {
			files: fstest.MapFS{
				"b/values.yaml": {Data: []byte("tag: [unclosed\n")},
				"b/test.tmpl":   {Data: []byte("steps: []\n")},
			},
			dir:     "b",
			wantErr: "values.yaml",
		},
		"a values.yaml that is not a mapping is an error": {
			files: fstest.MapFS{
				"b/values.yaml": {Data: []byte("- just\n- a\n- list\n")},
				"b/test.tmpl":   {Data: []byte("steps: []\n")},
			},
			dir:     "b",
			wantErr: "values.yaml",
		},

		// --- errors ---
		"a missing directory is an error naming it": {
			files:   fstest.MapFS{},
			dir:     "nope",
			wantErr: "nope",
		},
		"a directory with no templates is an error": {
			files:   fstest.MapFS{"b/values.yaml": {Data: []byte("tag: v1\n")}},
			dir:     "b",
			wantErr: "no templates",
		},
		"a directory with only helpers is an error": {
			// Helpers emit nothing on their own, so a bundle of only helpers
			// would render an empty pipeline.
			files:   fstest.MapFS{"b/_helpers.tpl": {Data: []byte(`{{ define "x" }}y{{ end }}`)}},
			dir:     "b",
			wantErr: "no templates",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, err := LoadDir(tc.files, tc.dir)

			if tc.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantErr)

				return
			}

			require.NoError(t, err)
			tc.assert(t, got)
		})
	}
}

// TestTemplateNamesIsSorted matters because render parses in this order and the
// merge concatenates steps in it: unsorted names would make output depend on
// map iteration order.
func TestTemplateNamesIsSorted(t *testing.T) {
	t.Parallel()

	files := fstest.MapFS{
		"b/c.tmpl": {Data: []byte("steps: []\n")},
		"b/a.tmpl": {Data: []byte("steps: []\n")},
		"b/b.tmpl": {Data: []byte("steps: []\n")},
	}

	got, err := LoadDir(files, "b")
	require.NoError(t, err)

	assert.Equal(t, []string{"a.tmpl", "b.tmpl", "c.tmpl"}, got.TemplateNames())
}

// TestTemplateNamesIsStable calls it repeatedly: a map-backed implementation
// that forgot to sort would pass a single call by luck.
func TestTemplateNamesIsStable(t *testing.T) {
	t.Parallel()

	files := fstest.MapFS{}
	for _, n := range []string{"z", "m", "a", "q", "b", "y", "c", "x"} {
		files["b/"+n+".tmpl"] = &fstest.MapFile{Data: []byte("steps: []\n")}
	}

	b, err := LoadDir(files, "b")
	require.NoError(t, err)

	first := b.TemplateNames()
	for range 20 {
		assert.Equal(t, first, b.TemplateNames())
	}
}
