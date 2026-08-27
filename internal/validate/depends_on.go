package validate

import (
	"fmt"
	"maps"

	"github.com/akijowski/pipefitter/internal/pipeline"
)

type stepDependency struct {
	owner string // who declared the step
	key   string // the step key being referenced
}

type DependsOn struct{}

func (d DependsOn) Name() string { return "depends_on" }

func (d DependsOn) Check(doc pipeline.Document) []Finding {
	var f []Finding
	steps, ok := asSequence(doc["steps"])
	if !ok {
		return f
	}

	declaredKeys := collectKeys(steps)
	for _, dep := range dependingKeys(steps) {
		if _, found := declaredKeys[dep.key]; !found {
			msg := fmt.Sprintf("%s declares a dependency on missing step with key %q", dep.owner, dep.key)
			f = append(f, Finding{
				Rule:    d.Name(),
				Message: msg,
			})
		}
	}

	return f
}

// collectKeys returns a set of all steps that define a "key" field
// A step can also declare steps as part of a group, so it will recursively walk the tree
func collectKeys(seq []any) map[string]struct{} {
	collection := map[string]struct{}{}
	for _, item := range seq {
		mapping, isMap := asMapping(item)
		if !isMap {
			// a step that is not a YAML object is still valid,
			// but we do not need to collect it
			continue
		}
		// collect top level key if it exists
		if key, ok := mapping["key"].(string); ok {
			collection[key] = struct{}{}
		}
		// collect nested steps if they exist
		if nested, ok := asSequence(mapping["steps"]); ok {
			maps.Copy(collection, collectKeys(nested))
		}
	}

	return collection
}

// dependingKeys returns a slice of tuples that relate a depends_on relationship between steps
// A step can also declare steps as part of a group, so it will recursively walk the tree
func dependingKeys(seq []any) []stepDependency {
	// a slice maintains seq order, while also reporting duplicate key dependencies
	var deps []stepDependency
	for _, item := range seq {
		mapping, isMap := asMapping(item)
		if !isMap {
			// skip scalar items
			continue
		}
		owner := stepOwner(mapping)

		// collect top level depends_on if it exists
		for _, dep := range dependsOnRefs(mapping["depends_on"]) {
			deps = append(deps, stepDependency{owner: owner, key: dep})
		}
		// collect nest steps if they exist
		if nested, ok := asSequence(mapping["steps"]); ok {
			deps = append(deps, dependingKeys(nested)...)
		}
	}

	return deps
}

func asSequence(val any) ([]any, bool) {
	s, ok := val.([]any)

	return s, ok
}

func asMapping(val any) (map[string]any, bool) {
	m, ok := val.(map[string]any)

	return m, ok
}

// stepOwner returns the provided step "key", "label", or sentinel "unnamed step" value in order.
func stepOwner(step map[string]any) string {
	const fallback = "unnamed step"
	if key, hasKey := step["key"]; hasKey {
		if validKey, ok := key.(string); ok {
			return validKey
		}
	}
	if label, hasLabel := step["label"]; hasLabel {
		if validLabel, ok := label.(string); ok {
			return validLabel
		}
	}

	return fallback
}

// dependsOnRefs walks the provided depends_on value val and returns a slice of dependency names
// A valid depends_on value is either nil (no dependency), a slice of strings, or a slice of step
// objects. A val that does not meet this shape will return a nil slice
func dependsOnRefs(val any) []string {
	switch v := val.(type) {
	case nil:
		return nil
	case string:
		return []string{v}
	case []any:
		// either depends_on: ["a", "b"] or [{"step": "a"},...]
		var out []string
		for _, entry := range v {
			switch e := entry.(type) {
			case string:
				out = append(out, e)
			case map[string]any:
				// append step only if a valid string type
				if s, ok := e["step"].(string); ok {
					out = append(out, s)
				}
			}
		}

		return out
	}
	// unknown, no dependency
	return nil
}
