package values

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMergeRFC7396 pins the merge semantics to RFC 7396 (JSON Merge Patch).
// The cases are drawn from the RFC's own appendix so the behavior is specified
// rather than invented.
func TestMergeRFC7396(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		base  map[string]any
		patch map[string]any
		want  map[string]any
	}{
		"replaces a scalar": {
			base:  map[string]any{"a": "b"},
			patch: map[string]any{"a": "c"},
			want:  map[string]any{"a": "c"},
		},
		"adds a key": {
			base:  map[string]any{"a": "b"},
			patch: map[string]any{"b": "c"},
			want:  map[string]any{"a": "b", "b": "c"},
		},
		"null deletes a key": {
			base:  map[string]any{"a": "b"},
			patch: map[string]any{"a": nil},
			want:  map[string]any{},
		},
		"null on a missing key is a no-op": {
			base:  map[string]any{"a": "b"},
			patch: map[string]any{"c": nil},
			want:  map[string]any{"a": "b"},
		},
		"merges nested objects recursively": {
			base:  map[string]any{"a": map[string]any{"b": "c", "keep": "yes"}},
			patch: map[string]any{"a": map[string]any{"b": "d"}},
			want:  map[string]any{"a": map[string]any{"b": "d", "keep": "yes"}},
		},
		"array replaces wholesale": {
			base:  map[string]any{"a": []any{"b", "c"}},
			patch: map[string]any{"a": []any{"d"}},
			want:  map[string]any{"a": []any{"d"}},
		},
		"object replaces array": {
			base:  map[string]any{"a": []any{"b"}},
			patch: map[string]any{"a": map[string]any{"c": "d"}},
			want:  map[string]any{"a": map[string]any{"c": "d"}},
		},
		"scalar replaces object": {
			base:  map[string]any{"a": map[string]any{"b": "c"}},
			patch: map[string]any{"a": "z"},
			want:  map[string]any{"a": "z"},
		},
		"object replaces scalar": {
			base:  map[string]any{"a": "z"},
			patch: map[string]any{"a": map[string]any{"b": "c"}},
			want:  map[string]any{"a": map[string]any{"b": "c"}},
		},
		"empty patch changes nothing": {
			base:  map[string]any{"a": "b"},
			patch: map[string]any{},
			want:  map[string]any{"a": "b"},
		},
		"patch onto an empty base": {
			base:  map[string]any{},
			patch: map[string]any{"a": "b"},
			want:  map[string]any{"a": "b"},
		},
		"nested null deletes only the inner key": {
			base:  map[string]any{"a": map[string]any{"b": "c", "d": "e"}},
			patch: map[string]any{"a": map[string]any{"b": nil}},
			want:  map[string]any{"a": map[string]any{"d": "e"}},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, err := Merge(Values{Tree: tc.base}, tc.patch, "patch")

			require.NoError(t, err)
			assert.Equal(t, tc.want, got.Tree)
		})
	}
}

// TestMergeDoesNotMutateBase matters because Merge is folded over several
// layers; mutating the base would corrupt the caller's data mid-fold.
func TestMergeDoesNotMutateBase(t *testing.T) {
	t.Parallel()

	base := map[string]any{"a": map[string]any{"b": "c"}}

	_, err := Merge(Values{Tree: base}, map[string]any{"a": map[string]any{"b": "changed"}}, "patch")
	require.NoError(t, err)

	assert.Equal(t, map[string]any{"a": map[string]any{"b": "c"}}, base,
		"Merge must not mutate its input")
}

// TestMergeDoesNotMutatePatch guards the other direction: the patch is often a
// parsed values file that the caller may still hold a reference to.
func TestMergeDoesNotMutatePatch(t *testing.T) {
	t.Parallel()

	patch := map[string]any{"a": map[string]any{"b": "new"}}

	_, err := Merge(Values{Tree: map[string]any{"a": map[string]any{"keep": "yes"}}}, patch, "patch")
	require.NoError(t, err)

	assert.Equal(t, map[string]any{"a": map[string]any{"b": "new"}}, patch,
		"Merge must not mutate the patch")
}

// TestMergeZeroValueIsUsable lets callers start a fold from Values{} without
// pre-allocating a tree.
func TestMergeZeroValueIsUsable(t *testing.T) {
	t.Parallel()

	got, err := Merge(Values{}, map[string]any{"a": "b"}, "defaults")

	require.NoError(t, err)
	assert.Equal(t, map[string]any{"a": "b"}, got.Tree)
}
