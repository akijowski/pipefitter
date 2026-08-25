// Package values merges layered configuration trees using RFC 7396
// (JSON Merge Patch) semantics, recording where each value came from.
//
// A pipefitter bundle supplies defaults which the caller's values files then
// override. Both are untyped trees parsed from YAML, so merging happens over
// map[string]any rather than a schema.
package values

import (
	"errors"
	"fmt"
	"maps"
	"strings"
)

// ErrMaxDepth is returned when a patch structure is overly nested and exceeds maxDepth
var ErrMaxDepth = errors.New("max depth exceeded")

const (
	maxDepth = 64
	// originPathSeparator is how Values.Origins denotes nested keys.
	// Strictly speaking a valid YAML key can also have a "." in the key name
	// but this is rare for a Buildkite pipeline. In this case however, recordOrigins
	// could overly truncate the Origins map during cleanup and is worth noting.
	originPathSeparator = "."
)

// Values is a merged configuration tree together with the provenance of every
// value in it.
//
// Tree is what templates read as .Values. Origins is keyed by dotted path
// ("image.tag"), so it is flat even though Tree is nested, and it holds an
// entry only for leaves — never for the intermediate maps along the way.
type Values struct {
	Tree    map[string]any
	Origins map[string]Origin
}

// Origin records which layer last wrote a given path.
type Origin struct {
	// Source is the label passed to Merge for the layer that wrote this path,
	// such as a values file path or "bundle defaults".
	Source string
	// Deleted reports that the layer removed the path by setting it to null,
	// rather than assigning it a value. The path is absent from Tree, and this
	// distinguishes "explicitly removed here" from "never set".
	Deleted bool
}

// Merge applies patch to base and returns the result, following RFC 7396
// (JSON Merge Patch): objects merge recursively, arrays and scalars replace
// wholesale, and a nil value deletes its key.
//
// source names where patch came from — a file path, or a label such as
// "bundle defaults" — and is recorded against each key the patch writes so
// callers can report provenance.
//
// Neither base nor patch is modified, so Merge can be folded over an ordered
// chain of layers with later layers winning. The zero Values is a valid
// starting point:
//
//	v := Values{}
//	for _, l := range layers {
//		v = Merge(v, l.patch, l.source)
//	}
//
// The returned tree shares any subtree the patch did not reach with base, so
// merged trees must be treated as read-only.
func Merge(base Values, patch map[string]any, source string) (Values, error) {
	v := Values{}
	// mergePatchAt returns a non-map only when the patch itself is not a map, and
	// this signature types patch as a map, so the assertion holds. Left
	// unchecked on purpose: if that coupling ever breaks, a panic beats
	// silently assigning a nil Tree and rendering blank values everywhere.
	// An error is returned if the patch nests deeper than maxDepth; the base tree's
	// depth is irrelevant, since only the patch drives descent.
	tree, err := mergePatchAt(base.Tree, patch, maxDepth)
	if err != nil {
		return v, err
	}
	v.Tree = tree.(map[string]any)

	v.Origins = recordOrigins(base.Origins, patch, source)

	return v, nil
}

// recordOrigins allocates a new map from tree, then walks the patch recursively, updating the copied tree.
//
// NOTE: recordOrigins does not impose a depth guard on the recursive walkOrigins function. It is protected because
// the order these functions are called in Merge guarantees safety. The mergePatchAt function protects against
// recursive overflow and precedes this call. If the order is changed then this function will need an appropriate
// guard installed.
func recordOrigins(tree map[string]Origin, patch map[string]any, source string) map[string]Origin {
	m := make(map[string]Origin, len(tree))
	maps.Copy(m, tree)
	walkOrigins(m, patch, "", source)
	return m
}

// walkOrigins recursively walks patch and updates m with path and source information.
//
// if patch.value is nil, the Origin at that location in m is marked as Deleted. All descendants in that chain are
// deleted.
//
// if patch.value is a map[string]any, the Origin at that location is removed and the map is walked recursively until
// a leaf is found.
//
// if patch.value is any other type then the Origin is updated at that location. All descendants in that chain are
// deleted.
func walkOrigins(m map[string]Origin, patch map[string]any, path, source string) {
	// recursively walk patch when the value is a map, and update the origin in all other cases
	for k, v := range patch {
		prefix := k
		if path != "" {
			prefix = path + originPathSeparator + k
		}
		switch p := v.(type) {
		case map[string]any:
			// clean up any existing record here since we are not on a leaf
			delete(m, prefix)
			walkOrigins(m, p, prefix, source)
		case nil:
			// overwrite existing record and purge descendents
			m[prefix] = Origin{Source: source, Deleted: true}
			purgeOriginDescendants(m, prefix)
		default:
			// overwrite existing record and purge descendents
			m[prefix] = Origin{Source: source}
			purgeOriginDescendants(m, prefix)
		}
	}
}

// purgeOriginDescendants walks the keys of m and removes each entry with prefix + originPathSeparator
func purgeOriginDescendants(m map[string]Origin, prefix string) {
	for k := range m {
		if strings.HasPrefix(k, prefix+originPathSeparator) {
			delete(m, k)
		}
	}
}

// mergePatchAt is the recursive core of RFC 7396, applying the patch value to the
// target value at the same position in the tree and returning the new value.
//
// It never mutates either argument: where both sides are maps it allocates a
// new map rather than writing into target. That is why the RFC's "if Target is
// not an Object, Target = {}" step is absent here — that step exists only to
// give the pseudocode something writable, and a non-map target simply
// contributes no entries.
//
// A nil patch value means "delete", which only a parent map can act on, so the
// map branch removes such keys and returning nil is left to the caller.
//
// A depth counter is kept to avoid runaway recursion due to poor YAML anchors
// or highly nested patches. An ErrMaxDepth is returned in this case.
func mergePatchAt(target, patch any, depth int) (any, error) {
	if depth <= 0 {
		return nil, fmt.Errorf("nesting exceeded at depth %d: %w", depth, ErrMaxDepth)
	}
	// A non-map target yields a nil map, which is safe to read: copying from it
	// and indexing it both behave as empty.
	t, _ := target.(map[string]any)
	var out any
	switch p := patch.(type) {
	case map[string]any:
		// make a new map m and copy target (t) to it. Nil map is a no-op
		m := make(map[string]any)
		maps.Copy(m, t)
		for k, v := range p {
			if v == nil {
				delete(m, k)
			} else {
				// merge the subtree whether it is a map or scalar
				merged, err := mergePatchAt(t[k], v, depth-1)
				if err != nil {
					return nil, err
				}
				m[k] = merged
			}
		}
		out = m

	default:
		out = patch
	}

	return out, nil
}
