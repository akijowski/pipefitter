package values

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mergeAll folds the layers in order, failing the test on the first error.
func mergeAll(t *testing.T, layers ...struct {
	patch  map[string]any
	source string
},
) Values {
	t.Helper()

	got := Values{}

	for _, l := range layers {
		var err error

		got, err = Merge(got, l.patch, l.source)
		require.NoError(t, err)
	}

	return got
}

type layer = struct {
	patch  map[string]any
	source string
}

// TestMergeOrigins pins provenance: every leaf records which layer last wrote
// it, keyed by dotted path.
func TestMergeOrigins(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		layers []layer
		want   map[string]Origin
	}{
		"records the source of each leaf": {
			layers: []layer{
				{map[string]any{"a": "1", "b": "2"}, "defaults"},
				{map[string]any{"b": "3"}, "values.yaml"},
			},
			want: map[string]Origin{
				"a": {Source: "defaults"},
				"b": {Source: "values.yaml"},
			},
		},
		"records nested paths with dots": {
			layers: []layer{
				{map[string]any{"img": map[string]any{"tag": "v1"}}, "defaults"},
			},
			want: map[string]Origin{
				"img.tag": {Source: "defaults"},
			},
		},
		"intermediate maps get no entry of their own": {
			layers: []layer{
				{map[string]any{"a": map[string]any{"b": map[string]any{"c": 1}}}, "defaults"},
			},
			want: map[string]Origin{
				"a.b.c": {Source: "defaults"},
			},
		},
		"a later layer only overrides the leaves it touches": {
			layers: []layer{
				{map[string]any{"img": map[string]any{"tag": "v1", "repo": "acme"}}, "defaults"},
				{map[string]any{"img": map[string]any{"tag": "v2"}}, "values.yaml"},
			},
			want: map[string]Origin{
				"img.tag":  {Source: "values.yaml"},
				"img.repo": {Source: "defaults"},
			},
		},
		"records a deletion": {
			layers: []layer{
				{map[string]any{"q": "default"}, "defaults"},
				{map[string]any{"q": nil}, "values.yaml"},
			},
			want: map[string]Origin{
				"q": {Source: "values.yaml", Deleted: true},
			},
		},
		"a value written after a deletion is live again": {
			layers: []layer{
				{map[string]any{"q": "default"}, "defaults"},
				{map[string]any{"q": nil}, "values.yaml"},
				{map[string]any{"q": "restored"}, "prod.yaml"},
			},
			want: map[string]Origin{
				"q": {Source: "prod.yaml"},
			},
		},
		"a subtree records every leaf, not just one": {
			layers: []layer{
				{map[string]any{"img": map[string]any{"tag": "v1", "repo": "acme"}}, "defaults"},
			},
			want: map[string]Origin{
				"img.tag":  {Source: "defaults"},
				"img.repo": {Source: "defaults"},
			},
		},
		"deleting a subtree purges the origins beneath it": {
			layers: []layer{
				{map[string]any{"a": map[string]any{"b": 1, "c": 2}}, "defaults"},
				{map[string]any{"a": nil}, "values.yaml"},
			},
			want: map[string]Origin{
				"a": {Source: "values.yaml", Deleted: true},
			},
		},
		"replacing a leaf with a map drops the leaf's origin": {
			layers: []layer{
				{map[string]any{"a": "z"}, "defaults"},
				{map[string]any{"a": map[string]any{"b": 1}}, "values.yaml"},
			},
			want: map[string]Origin{
				"a.b": {Source: "values.yaml"},
			},
		},
		"replacing a map with a leaf purges the origins beneath it": {
			layers: []layer{
				{map[string]any{"a": map[string]any{"b": 1, "c": 2}}, "defaults"},
				{map[string]any{"a": "z"}, "values.yaml"},
			},
			want: map[string]Origin{
				"a": {Source: "values.yaml"},
			},
		},
		"a sibling path that merely shares a prefix is not purged": {
			layers: []layer{
				{map[string]any{"ab": 1, "a": map[string]any{"b": 2}}, "defaults"},
				{map[string]any{"a": nil}, "values.yaml"},
			},
			want: map[string]Origin{
				"ab": {Source: "defaults"},
				"a":  {Source: "values.yaml", Deleted: true},
			},
		},
		"a list leaf records one entry, not one per element": {
			layers: []layer{
				{map[string]any{"plugins": []any{"docker", "artifacts"}}, "defaults"},
			},
			want: map[string]Origin{
				"plugins": {Source: "defaults"},
			},
		},
		"an empty patch records nothing": {
			layers: []layer{
				{map[string]any{}, "defaults"},
			},
			want: map[string]Origin{},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := mergeAll(t, tc.layers...)

			assert.Equal(t, tc.want, got.Origins)
		})
	}
}

// TestMergeDoesNotMutateBaseOrigins is the Origins counterpart to the tree
// test: the fold must not write into the caller's origins map.
func TestMergeDoesNotMutateBaseOrigins(t *testing.T) {
	t.Parallel()

	base, err := Merge(Values{}, map[string]any{"a": "1"}, "defaults")
	require.NoError(t, err)

	_, err = Merge(base, map[string]any{"a": "2", "b": "3"}, "values.yaml")
	require.NoError(t, err)

	assert.Equal(t, map[string]Origin{"a": {Source: "defaults"}}, base.Origins,
		"Merge must not mutate the base's Origins")
}

// nest returns a patch nested depth levels deep: {"k":{"k":{...:"leaf"}}}.
func nest(depth int) map[string]any {
	out := map[string]any{"k": "leaf"}

	for range depth {
		out = map[string]any{"k": out}
	}

	return out
}

// TestMergeDepthLimit bounds the recursion. Stack overflow is a fatal error in
// Go that recover() cannot catch, so the recursion has to stop itself.
func TestMergeDepthLimit(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		depth   int
		wantErr bool
	}{
		"a realistic depth is fine":        {depth: 8},
		"just under the limit is fine":     {depth: maxDepth - 2},
		"beyond the limit is an error":     {depth: maxDepth + 5, wantErr: true},
		"far beyond the limit is an error": {depth: maxDepth * 10, wantErr: true},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := Merge(Values{}, nest(tc.depth), "deep.yaml")

			if tc.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "depth",
					"the error must say what went wrong")

				return
			}

			require.NoError(t, err)
		})
	}
}

// TestMergeCyclicPatchDoesNotCrash is the case the limit exists for: a
// self-referential tree, which YAML aliases can produce. Without a depth limit
// this is a fatal stack overflow rather than a test failure.
func TestMergeCyclicPatchDoesNotCrash(t *testing.T) {
	t.Parallel()

	cyclic := map[string]any{}
	cyclic["self"] = cyclic

	_, err := Merge(Values{}, cyclic, "cyclic.yaml")

	require.Error(t, err)
}

// TestMergeCyclicBaseTerminates documents that only the patch drives descent.
// Merging recurses where the patch has a map, so a cycle reachable only through
// the base is harmless: a finite patch stops after finitely many levels.
func TestMergeCyclicBaseTerminates(t *testing.T) {
	t.Parallel()

	cyclic := map[string]any{}
	cyclic["self"] = cyclic

	got, err := Merge(Values{Tree: cyclic}, map[string]any{"self": map[string]any{"x": 1}}, "p.yaml")

	require.NoError(t, err)

	self, ok := got.Tree["self"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, 1, self["x"])
}

// TestMergeWideShallowTreeIsFine guards against counting breadth as depth: a
// flat config with many keys is nowhere near the depth limit.
func TestMergeWideShallowTreeIsFine(t *testing.T) {
	t.Parallel()

	wide := make(map[string]any, maxDepth*4)
	for i := range maxDepth * 4 {
		wide[fmt.Sprintf("key%d", i)] = i
	}

	got, err := Merge(Values{}, wide, "wide.yaml")

	require.NoError(t, err, "breadth is not depth")
	assert.Len(t, got.Tree, maxDepth*4)
}

// TestMergeNestedWideTreeIsFine combines the two: several levels deep, each
// level wide. Total node count far exceeds maxDepth; actual depth does not.
func TestMergeNestedWideTreeIsFine(t *testing.T) {
	t.Parallel()

	level := func() map[string]any {
		m := make(map[string]any, 20)
		for i := range 20 {
			m[fmt.Sprintf("k%d", i)] = i
		}

		return m
	}

	patch := map[string]any{}
	for i := range 20 {
		patch[fmt.Sprintf("branch%d", i)] = level()
	}

	_, err := Merge(Values{}, patch, "nested.yaml")

	require.NoError(t, err)
}
