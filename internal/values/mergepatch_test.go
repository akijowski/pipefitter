package values

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMergePatch exercises the recursive RFC 7396 core directly.
//
// mergePatchAt is a pure value-to-value function: it takes the target value and
// the patch value at the same position in the tree and returns the new value.
// It never mutates either argument.
//
// There are only four shapes it has to distinguish:
//
//	patch is a map, target is a map      -> recurse key by key
//	patch is a map, target is not a map  -> treat target as an empty map
//	patch is anything else               -> return the patch (replace)
//	patch value inside a map is nil      -> that key is removed
func TestMergePatch(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		target any
		patch  any
		want   any
	}{
		// --- replacement: patch is not a map ---
		"scalar replaces scalar": {
			target: "a",
			patch:  "b",
			want:   "b",
		},
		"scalar replaces absent target": {
			target: nil,
			patch:  "b",
			want:   "b",
		},
		"scalar replaces a map": {
			target: map[string]any{"a": 1},
			patch:  "z",
			want:   "z",
		},
		"slice replaces slice wholesale": {
			target: []any{"a", "b"},
			patch:  []any{"c"},
			want:   []any{"c"},
		},
		"slice replaces a map": {
			target: map[string]any{"a": 1},
			patch:  []any{"c"},
			want:   []any{"c"},
		},
		"nil patch returns nil": {
			// A nil patch means "delete" — but only a parent map can action a
			// deletion, so here it simply returns nil and the caller decides.
			target: map[string]any{"a": 1},
			patch:  nil,
			want:   nil,
		},

		// --- recursion: patch is a map ---
		"map merges into map": {
			target: map[string]any{"a": 1, "b": 2},
			patch:  map[string]any{"b": 3},
			want:   map[string]any{"a": 1, "b": 3},
		},
		"map adds new keys": {
			target: map[string]any{"a": 1},
			patch:  map[string]any{"b": 2},
			want:   map[string]any{"a": 1, "b": 2},
		},
		"map replaces a scalar target": {
			target: "z",
			patch:  map[string]any{"a": 1},
			want:   map[string]any{"a": 1},
		},
		"map replaces an absent target": {
			target: nil,
			patch:  map[string]any{"a": 1},
			want:   map[string]any{"a": 1},
		},
		"map replaces a slice target": {
			target: []any{"x"},
			patch:  map[string]any{"a": 1},
			want:   map[string]any{"a": 1},
		},
		"nested maps recurse": {
			target: map[string]any{"a": map[string]any{"b": 1, "c": 2}},
			patch:  map[string]any{"a": map[string]any{"b": 9}},
			want:   map[string]any{"a": map[string]any{"b": 9, "c": 2}},
		},
		"three levels deep": {
			target: map[string]any{"a": map[string]any{"b": map[string]any{"c": 1, "d": 2}}},
			patch:  map[string]any{"a": map[string]any{"b": map[string]any{"c": 9}}},
			want:   map[string]any{"a": map[string]any{"b": map[string]any{"c": 9, "d": 2}}},
		},
		"empty map patch changes nothing": {
			target: map[string]any{"a": 1},
			patch:  map[string]any{},
			want:   map[string]any{"a": 1},
		},

		// --- deletion: a nil value inside a map patch ---
		"nil value removes the key": {
			target: map[string]any{"a": 1, "b": 2},
			patch:  map[string]any{"b": nil},
			want:   map[string]any{"a": 1},
		},
		"nil value for an absent key is a no-op": {
			target: map[string]any{"a": 1},
			patch:  map[string]any{"b": nil},
			want:   map[string]any{"a": 1},
		},
		"removing the only key leaves an empty map": {
			target: map[string]any{"a": 1},
			patch:  map[string]any{"a": nil},
			want:   map[string]any{},
		},
		"nil value removes only the nested key": {
			target: map[string]any{"a": map[string]any{"b": 1, "c": 2}},
			patch:  map[string]any{"a": map[string]any{"b": nil}},
			want:   map[string]any{"a": map[string]any{"c": 2}},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, err := mergePatchAt(tc.target, tc.patch, maxDepth)

			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

// TestMergePatchDoesNotMutateTarget is the property that lets Merge pass
// base.Tree straight in without copying it first.
func TestMergePatchDoesNotMutateTarget(t *testing.T) {
	t.Parallel()

	target := map[string]any{
		"keep":   1,
		"drop":   2,
		"nested": map[string]any{"inner": "original"},
	}

	_, err := mergePatchAt(target, map[string]any{
		"drop":   nil,
		"nested": map[string]any{"inner": "changed"},
		"added":  "new",
	}, maxDepth)
	require.NoError(t, err)

	assert.Equal(t, map[string]any{
		"keep":   1,
		"drop":   2,
		"nested": map[string]any{"inner": "original"},
	}, target, "mergePatchAt must not mutate the target, at any depth")
}

// TestMergePatchDoesNotMutatePatch guards the other direction — the patch is
// usually a parsed values file the caller still holds.
func TestMergePatchDoesNotMutatePatch(t *testing.T) {
	t.Parallel()

	patch := map[string]any{"nested": map[string]any{"b": 2}}

	_, err := mergePatchAt(map[string]any{"nested": map[string]any{"a": 1}}, patch, maxDepth)
	require.NoError(t, err)

	assert.Equal(t, map[string]any{"nested": map[string]any{"b": 2}}, patch,
		"mergePatchAt must not mutate the patch, at any depth")
}

// TestMergePatchSharesUntouchedSubtrees documents a deliberate property: a
// subtree the patch never reaches is shared by reference with the target rather
// than deep-copied. That is how persistent data structures work, and it is
// cheaper than copying whole subtrees on every layer of the fold.
//
// The guarantee mergePatchAt makes is that it never mutates its inputs — not that
// callers may mutate its output. Merged trees are read-only data: they are
// folded, then handed to templates.
func TestMergePatchSharesUntouchedSubtrees(t *testing.T) {
	t.Parallel()

	nested := map[string]any{"a": 1}
	target := map[string]any{"nested": nested}

	merged, err := mergePatchAt(target, map[string]any{"other": 2}, maxDepth)
	require.NoError(t, err)

	got, ok := merged.(map[string]any)
	assert.True(t, ok)

	assert.Equal(t, map[string]any{"a": 1}, nested,
		"mergePatchAt itself must leave the subtree alone")

	// Writing through the result reaches the original: the subtree is shared,
	// which is why merged trees must be treated as read-only.
	gotNested, ok := got["nested"].(map[string]any)
	assert.True(t, ok)

	gotNested["a"] = "mutated"

	assert.Equal(t, "mutated", nested["a"],
		"an untouched subtree is shared by reference, not deep-copied")
}
