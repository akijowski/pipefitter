package pipeline

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParse(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		in      string
		want    Document
		wantErr string
	}{
		"parses steps": {
			in: "steps:\n  - label: test\n",
			want: Document{"steps": []any{
				map[string]any{"label": "test"},
			}},
		},
		"parses top-level keys alongside steps": {
			in: "env:\n  FOO: bar\nsteps: []\n",
			want: Document{
				"env":   map[string]any{"FOO": "bar"},
				"steps": []any{},
			},
		},
		"an empty document is empty, not nil": {
			in:   "",
			want: Document{},
		},
		"a whitespace-only document is empty": {
			in:   "\n\n",
			want: Document{},
		},
		"malformed yaml is an error": {
			in:      "steps:\n  - [unclosed\n",
			wantErr: "parse",
		},
		"a top-level list is an error": {
			in:      "- just\n- a\n- list\n",
			wantErr: "parse",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, err := Parse([]byte(tc.in))

			if tc.wantErr != "" {
				require.Error(t, err)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

// TestMarshalRoundTrip avoids asserting on exact serializer output, which is
// goccy's to decide, and pins the property that actually matters.
func TestMarshalRoundTrip(t *testing.T) {
	t.Parallel()

	doc := Document{
		"env": map[string]any{"FOO": "bar"},
		"steps": []any{
			map[string]any{"label": "test", "key": "t", "command": "go test ./..."},
			map[string]any{"wait": nil},
		},
	}

	b, err := Marshal(doc)
	require.NoError(t, err)

	got, err := Parse(b)
	require.NoError(t, err)

	assert.Equal(t, doc, got)
}

func TestMarshalIsDeterministic(t *testing.T) {
	t.Parallel()

	doc := Document{
		"steps": []any{map[string]any{"label": "a", "key": "k", "command": "x"}},
		"env":   map[string]any{"B": "2", "A": "1"},
	}

	first, err := Marshal(doc)
	require.NoError(t, err)

	for range 20 {
		got, err := Marshal(doc)
		require.NoError(t, err)
		assert.Equal(t, string(first), string(got))
	}
}

func TestMerge(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		srcs    []Source
		want    Document
		wantErr string
	}{
		"a single source passes through": {
			srcs: []Source{
				{Name: "a", Doc: Document{"steps": []any{map[string]any{"key": "one"}}}},
			},
			want: Document{"steps": []any{map[string]any{"key": "one"}}},
		},
		"no sources yields an empty document": {
			srcs: nil,
			want: Document{},
		},
		"steps concatenate in source order": {
			srcs: []Source{
				{Name: "a", Doc: Document{"steps": []any{map[string]any{"key": "one"}}}},
				{Name: "b", Doc: Document{"steps": []any{map[string]any{"key": "two"}}}},
			},
			want: Document{"steps": []any{
				map[string]any{"key": "one"},
				map[string]any{"key": "two"},
			}},
		},
		"notify concatenates in source order": {
			srcs: []Source{
				{Name: "a", Doc: Document{"notify": []any{"one"}}},
				{Name: "b", Doc: Document{"notify": []any{"two"}}},
			},
			want: Document{"notify": []any{"one", "two"}},
		},
		"env merges, keeping keys only one source sets": {
			// The later source must win on B while A survives. A replace rather
			// than a merge would drop A, so this distinguishes the two — a case
			// where both sources set the same single key would not.
			srcs: []Source{
				{Name: "a", Doc: Document{"env": map[string]any{"A": "1", "B": "1"}}},
				{Name: "b", Doc: Document{"env": map[string]any{"B": "2", "C": "3"}}},
			},
			want: Document{"env": map[string]any{"A": "1", "B": "2", "C": "3"}},
		},
		"agents merges, keeping keys only one source sets": {
			srcs: []Source{
				{Name: "a", Doc: Document{"agents": map[string]any{"queue": "default", "os": "linux"}}},
				{Name: "b", Doc: Document{"agents": map[string]any{"queue": "big"}}},
			},
			want: Document{"agents": map[string]any{"queue": "big", "os": "linux"}},
		},
		"env and agents merge independently": {
			srcs: []Source{
				{Name: "a", Doc: Document{
					"env":    map[string]any{"A": "1"},
					"agents": map[string]any{"os": "linux"},
				}},
				{Name: "b", Doc: Document{
					"env":    map[string]any{"B": "2"},
					"agents": map[string]any{"queue": "big"},
				}},
			},
			want: Document{
				"env":    map[string]any{"A": "1", "B": "2"},
				"agents": map[string]any{"os": "linux", "queue": "big"},
			},
		},
		"a non-mapping env is an error": {
			srcs: []Source{
				{Name: "a.tmpl", Doc: Document{"env": "oops"}},
			},
			wantErr: "env",
		},
		"a non-mapping agents is an error": {
			srcs: []Source{
				{Name: "a.tmpl", Doc: Document{"agents": []any{"oops"}}},
			},
			wantErr: "agents",
		},
		"a non-string step key is an error": {
			srcs: []Source{
				{Name: "a.tmpl", Doc: Document{"steps": []any{
					map[string]any{"key": 2024, "label": "unquoted"},
				}}},
			},
			wantErr: "a.tmpl",
		},
		"a bare string step is not a mapping and has no key": {
			// "steps: [wait]" is valid Buildkite, so a non-mapping step must be
			// carried through rather than rejected.
			srcs: []Source{
				{Name: "a", Doc: Document{"steps": []any{"wait"}}},
			},
			want: Document{"steps": []any{"wait"}},
		},
		"a source with no steps contributes nothing": {
			srcs: []Source{
				{Name: "a", Doc: Document{"steps": []any{map[string]any{"key": "one"}}}},
				{Name: "b", Doc: Document{"env": map[string]any{"A": "1"}}},
			},
			want: Document{
				"steps": []any{map[string]any{"key": "one"}},
				"env":   map[string]any{"A": "1"},
			},
		},
		"steps without keys never collide": {
			srcs: []Source{
				{Name: "a", Doc: Document{"steps": []any{map[string]any{"label": "x"}}}},
				{Name: "b", Doc: Document{"steps": []any{map[string]any{"label": "y"}}}},
			},
			want: Document{"steps": []any{
				map[string]any{"label": "x"},
				map[string]any{"label": "y"},
			}},
		},
		"a duplicate step key is an error": {
			srcs: []Source{
				{Name: "shared/test.tmpl", Doc: Document{"steps": []any{map[string]any{"key": "test"}}}},
				{Name: "local/test.tmpl", Doc: Document{"steps": []any{map[string]any{"key": "test"}}}},
			},
			wantErr: "test",
		},
		"a duplicate key within one source is an error": {
			srcs: []Source{
				{Name: "a.tmpl", Doc: Document{"steps": []any{
					map[string]any{"key": "dup"},
					map[string]any{"key": "dup"},
				}}},
			},
			wantErr: "dup",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, err := Merge(tc.srcs)

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

// TestMergeDuplicateKeyNamesBothSources is the payoff over letting Buildkite
// reject the upload: the message says where both steps came from.
func TestMergeDuplicateKeyNamesBothSources(t *testing.T) {
	t.Parallel()

	_, err := Merge([]Source{
		{Name: "shared/test.tmpl", Doc: Document{"steps": []any{map[string]any{"key": "test"}}}},
		{Name: "local/test.tmpl", Doc: Document{"steps": []any{map[string]any{"key": "test"}}}},
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "shared/test.tmpl")
	assert.Contains(t, err.Error(), "local/test.tmpl")
}

// TestMergeDoesNotMutateSources matters because Merge runs over documents the
// caller may still hold.
func TestMergeDoesNotMutateSources(t *testing.T) {
	t.Parallel()

	a := Document{"steps": []any{map[string]any{"key": "one"}}}
	b := Document{"steps": []any{map[string]any{"key": "two"}}}

	_, err := Merge([]Source{{Name: "a", Doc: a}, {Name: "b", Doc: b}})
	require.NoError(t, err)

	assert.Equal(t, Document{"steps": []any{map[string]any{"key": "one"}}}, a)
	assert.Equal(t, Document{"steps": []any{map[string]any{"key": "two"}}}, b)
}
