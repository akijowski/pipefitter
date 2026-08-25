package render

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/akijowski/pipefitter/internal/bkenv"
)

// TestRender covers template execution: the two namespaces a template can read,
// text-level composition through helpers, and the functions we deliberately do
// not provide.
func TestRender(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		tmpls   map[string]string
		entry   string
		ctx     Context
		want    string
		wantErr string
	}{
		// --- the two namespaces ---
		"renders a value": {
			tmpls: map[string]string{"a.tmpl": `tag: {{ .Values.tag }}`},
			entry: "a.tmpl",
			ctx:   Context{Values: map[string]any{"tag": "v1"}},
			want:  "tag: v1",
		},
		"renders a nested value": {
			tmpls: map[string]string{"a.tmpl": `tag: {{ .Values.image.tag }}`},
			entry: "a.tmpl",
			ctx: Context{Values: map[string]any{
				"image": map[string]any{"tag": "v1"},
			}},
			want: "tag: v1",
		},
		"renders buildkite env": {
			tmpls: map[string]string{"a.tmpl": `branch: {{ .Env.Buildkite.Branch }}`},
			entry: "a.tmpl",
			ctx:   Context{Env: bkenv.Env{Buildkite: bkenv.Buildkite{Branch: "main"}}},
			want:  "branch: main",
		},
		"renders the raw env long tail": {
			tmpls: map[string]string{"a.tmpl": `x: {{ .Env.Vars.MY_CUSTOM }}`},
			entry: "a.tmpl",
			ctx:   Context{Env: bkenv.Env{Vars: map[string]string{"MY_CUSTOM": "hello"}}},
			want:  "x: hello",
		},
		"branches on the typed pull request flag": {
			tmpls: map[string]string{
				"a.tmpl": `pr: {{ if .Env.Buildkite.PullRequest.IsPR }}yes{{ else }}no{{ end }}`,
			},
			entry: "a.tmpl",
			ctx: Context{Env: bkenv.Env{Buildkite: bkenv.Buildkite{
				PullRequest: bkenv.PullRequest{IsPR: false},
			}}},
			want: "pr: no",
		},

		// --- sprig ---
		"a sprig function is available": {
			// default supplies a fallback for a key that is present but empty.
			// It cannot cover an absent key, because the map index is evaluated
			// before the pipe and missingkey=error rejects it first.
			tmpls: map[string]string{"a.tmpl": `{{ .Values.x | default "fallback" }}`},
			entry: "a.tmpl",
			ctx:   Context{Values: map[string]any{"x": ""}},
			want:  "fallback",
		},
		"default passes through a present non-empty value": {
			tmpls: map[string]string{"a.tmpl": `{{ .Values.x | default "fallback" }}`},
			entry: "a.tmpl",
			ctx:   Context{Values: map[string]any{"x": "real"}},
			want:  "real",
		},
		"sprig quote helps with yaml safety": {
			tmpls: map[string]string{"a.tmpl": `msg: {{ .Values.msg | quote }}`},
			entry: "a.tmpl",
			ctx:   Context{Values: map[string]any{"msg": "fix: thing"}},
			want:  `msg: "fix: thing"`,
		},

		// --- text-level composition ---
		"a helper define is usable": {
			tmpls: map[string]string{
				"_helpers.tpl": `{{ define "label" }}:go: test{{ end }}`,
				"a.tmpl":       `label: {{ template "label" }}`,
			},
			entry: "a.tmpl",
			want:  "label: :go: test",
		},
		"a helper define receives context": {
			tmpls: map[string]string{
				"_helpers.tpl": `{{ define "img" }}{{ .Values.repo }}:{{ .Values.tag }}{{ end }}`,
				"a.tmpl":       `image: {{ template "img" . }}`,
			},
			entry: "a.tmpl",
			ctx: Context{Values: map[string]any{
				"repo": "acme/app", "tag": "v1",
			}},
			want: "image: acme/app:v1",
		},
		"a helper is reachable via include": {
			tmpls: map[string]string{
				"_helpers.tpl": `{{ define "q" }}queue{{ end }}`,
				"a.tmpl":       `agents: {{ include "q" . }}`,
			},
			entry: "a.tmpl",
			want:  "agents: queue",
		},

		// --- missing data ---
		"a missing values key is an error": {
			// Without missingkey=error this renders the literal "<no value>",
			// which is valid YAML — so `tag: <no value>` would ship to Buildkite
			// as a real string. Silent-wrong is the failure mode we reject, so a
			// bundle must declare every key it reads in its values.yaml.
			tmpls:   map[string]string{"a.tmpl": `tag: {{ .Values.nope }}`},
			entry:   "a.tmpl",
			ctx:     Context{Values: map[string]any{}},
			wantErr: "nope",
		},
		"a missing nested values key is an error": {
			tmpls:   map[string]string{"a.tmpl": `tag: {{ .Values.image.nope }}`},
			entry:   "a.tmpl",
			ctx:     Context{Values: map[string]any{"image": map[string]any{}}},
			wantErr: "nope",
		},
		"a missing raw env var is an error": {
			// Env.Vars is a map[string]string, so this would render "" under
			// missingkey=zero. It errors for the same reason: a silently empty
			// value in a pipeline is worse than a failed render.
			tmpls:   map[string]string{"a.tmpl": `x: {{ .Env.Vars.NOPE }}`},
			entry:   "a.tmpl",
			ctx:     Context{Env: bkenv.Env{Vars: map[string]string{}}},
			wantErr: "NOPE",
		},
		"a typo in a typed env field is an error": {
			// Env is a struct, so a misspelled field cannot silently render
			// empty. This is the loud-failure half of the story.
			tmpls:   map[string]string{"a.tmpl": `{{ .Env.Buildkite.Brnach }}`},
			entry:   "a.tmpl",
			wantErr: "Brnach",
		},

		// --- excluded functions ---
		"env is not available": {
			tmpls:   map[string]string{"a.tmpl": `{{ env "HOME" }}`},
			entry:   "a.tmpl",
			wantErr: "env",
		},
		"expandenv is not available": {
			tmpls:   map[string]string{"a.tmpl": `{{ expandenv "$HOME" }}`},
			entry:   "a.tmpl",
			wantErr: "expandenv",
		},
		"getHostByName is not available": {
			tmpls:   map[string]string{"a.tmpl": `{{ getHostByName "example.com" }}`},
			entry:   "a.tmpl",
			wantErr: "getHostByName",
		},
		"uuidv4 is not available": {
			tmpls:   map[string]string{"a.tmpl": `{{ uuidv4 }}`},
			entry:   "a.tmpl",
			wantErr: "uuidv4",
		},
		"randAlphaNum is not available": {
			tmpls:   map[string]string{"a.tmpl": `{{ randAlphaNum 8 }}`},
			entry:   "a.tmpl",
			wantErr: "randAlphaNum",
		},

		// --- errors ---
		"an unknown entry is an error": {
			tmpls:   map[string]string{"a.tmpl": `x`},
			entry:   "missing.tmpl",
			wantErr: "missing.tmpl",
		},
		"a malformed template is an error": {
			tmpls:   map[string]string{"a.tmpl": `{{ .Values.tag `},
			entry:   "a.tmpl",
			wantErr: "a.tmpl",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, err := Render(tc.tmpls, tc.entry, tc.ctx, NoAgent{})

			if tc.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantErr)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

// TestRenderIsDeterministic guards the reason the random and time functions are
// excluded: the same inputs must always produce the same pipeline.
func TestRenderIsDeterministic(t *testing.T) {
	t.Parallel()

	tmpls := map[string]string{
		"_helpers.tpl": `{{ define "x" }}{{ .Values.a }}{{ end }}`,
		"a.tmpl":       `{{ template "x" . }}-{{ .Values.b }}`,
	}
	ctx := Context{Values: map[string]any{"a": "1", "b": "2"}}

	first, err := Render(tmpls, "a.tmpl", ctx, NoAgent{})
	require.NoError(t, err)

	for range 20 {
		got, err := Render(tmpls, "a.tmpl", ctx, NoAgent{})
		require.NoError(t, err)
		assert.Equal(t, first, got)
	}
}

// TestRenderDoesNotMutateValues is the cheap guard we promised when documenting
// that merged trees are read-only: rendering must not write into them.
func TestRenderDoesNotMutateValues(t *testing.T) {
	t.Parallel()

	values := map[string]any{"image": map[string]any{"tag": "v1"}}

	_, err := Render(
		map[string]string{"a.tmpl": `{{ .Values.image.tag }}`},
		"a.tmpl",
		Context{Values: values},
		NoAgent{},
	)
	require.NoError(t, err)

	assert.Equal(t, map[string]any{"image": map[string]any{"tag": "v1"}}, values)
}

// TestNoAgentErrorsWithGuidance pins the debuggability requirement: the error
// must name the key that was wanted.
func TestNoAgentErrorsWithGuidance(t *testing.T) {
	t.Parallel()

	got, found, err := NoAgent{}.MetaData(context.Background(), "deploy-target")

	assert.Empty(t, got)
	assert.False(t, found)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "deploy-target",
		"the error must name the key, or debugging is miserable")
}

// TestFuncMapExcludesByDeletion checks the exclusions at the FuncMap level, so a
// future refactor cannot reintroduce them without failing here.
func TestFuncMapExcludesByDeletion(t *testing.T) {
	t.Parallel()

	fm := FuncMap()

	for _, name := range []string{
		"env", "expandenv", "getHostByName",
		"uuidv4", "randAlphaNum", "randAlpha", "randNumeric", "randAscii",
	} {
		assert.NotContains(t, fm, name, "%s must not be registered", name)
	}

	// A sanity check that we did not delete the whole library.
	for _, name := range []string{"default", "quote", "upper", "trim"} {
		assert.Contains(t, fm, name, "%s should be available", name)
	}
}
