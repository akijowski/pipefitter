// Package pipeline parses, merges and serializes Buildkite pipeline documents.
//
// Documents are untyped: a template renders text, and YAML is parsed into plain
// Go maps and slices rather than a modeled schema. That is deliberate.
// Buildkite steps are polymorphic — command, wait, block, group and trigger
// steps share no common shape — so modeling them would mean tracking
// Buildkite's schema forever, and a document pipefitter cannot represent would
// become a document pipefitter cannot emit.
//
// The cost is that the shapes are not visible in the type system, so this
// package states them in documentation and confines the type assertions to
// small named helpers instead of scattering them through the merge.
package pipeline

import (
	"fmt"
	"maps"
	"slices"

	"github.com/goccy/go-yaml"
)

// Document is a parsed Buildkite pipeline: the top level of a YAML mapping.
//
// Values are whatever YAML produced. A mapping is a map[string]any, a sequence
// is a []any, and a scalar is a string, int, float64, bool or nil. Nested
// mappings are map[string]any at every level.
//
// A document with two steps and some environment looks like this:
//
//	Document{
//	    "env": map[string]any{
//	        "REGION": "us-east-1",
//	    },
//	    "steps": []any{
//	        map[string]any{
//	            "key":     "test",
//	            "label":   ":go: test",
//	            "command": "go test ./...",
//	        },
//	        map[string]any{
//	            "key":        "deploy",
//	            "label":      "deploy",
//	            "depends_on": "test",
//	        },
//	    },
//	}
//
// Only four top-level keys carry meaning for merging — steps, notify, env and
// agents — and Merge documents how each is combined. Everything else is passed
// through.
//
// Note the shapes that are easy to get wrong: "steps" is a []any whose elements
// are map[string]any, not a []map[string]any; and "depends_on" may be either a
// single string or a []any of strings, because Buildkite accepts both.
type Document map[string]any

// Source is one rendered document together with where it came from.
//
// Name exists only for diagnostics: a Document cannot say which template
// produced it, and "duplicate step key" is far more useful when it can name both
// files involved.
type Source struct {
	Name string // "bundle/template.tmpl", for error messages
	Doc  Document
}

func (d Document) steps() []any {
	steps, ok := asSequence(d["steps"])
	if !ok {
		return []any{}
	}

	return steps
}

func (d Document) notifications() []any {
	n, ok := asSequence(d["notify"])
	if !ok {
		return []any{}
	}

	return n
}

// Parse decodes a rendered template into a Document.
//
// The document must be a YAML mapping; a sequence or scalar at the top level is
// an error, since a Buildkite pipeline is always a mapping. An empty or
// whitespace-only input yields an empty non-nil Document, which lets a template
// that renders nothing take part in a merge without special-casing.
func Parse(b []byte) (Document, error) {
	var d Document

	err := yaml.Unmarshal(b, &d)
	if err != nil {
		return nil, fmt.Errorf("failed to parse yaml: %w", err)
	}

	if d == nil {
		d = Document{}
	}

	return d, err
}

// Marshal serializes d as YAML.
//
// IndentSequence matches the style Buildkite pipelines are conventionally
// written in, with sequence items indented under their key. Keeping the option
// here rather than at the call site means every document pipefitter emits is
// formatted the same way.
//
// Note that serializing normalizes: mapping keys come out sorted alphabetically
// rather than in the order a template wrote them, and comments do not survive.
func Marshal(d Document) ([]byte, error) {
	b, err := yaml.MarshalWithOptions(d, yaml.IndentSequence(true))
	if err != nil {
		return nil, fmt.Errorf("failed to marshal yaml: %w", err)
	}

	return b, nil
}

// Merge combines rendered documents into one pipeline, in source order.
//
// Each top-level key is combined according to what it means to Buildkite:
//
//	steps, notify    concatenated in source order
//	env, agents      merged as mappings, later sources winning per key
//	anything else    last source to set it wins
//
// Note that this is deliberately not RFC 7396, which internal/values uses for
// configuration. There, sequences replace wholesale; here they concatenate,
// because replacing would discard every bundle's steps but the last. Two merges
// with different rules is the intent, not an inconsistency.
//
// Step keys must be unique across every source. A duplicate is an error naming
// both templates involved — Buildkite would reject the upload anyway, but not
// in a way that says where the collision came from.
//
// Merge does not modify the sources.
func Merge(srcs []Source) (Document, error) {
	out := Document{}
	stepMemo := make(map[string]string)
	for _, src := range srcs {
		for key, val := range src.Doc {
			switch key {
			case "steps":
				steps, ok := asSequence(val)
				if !ok {
					return out, fmt.Errorf("invalid steps value type %T for %q", val, src.Name)
				}
				temp := slices.Clone(out.steps())
				for _, step := range steps {
					sk, hasStepKey, err := stepKey(step)
					if err != nil {
						return out, fmt.Errorf("invalid step in %q: %w", src.Name, err)
					}

					if hasStepKey {
						if err := recordStep(stepMemo, sk, src.Name); err != nil {
							return out, err
						}
					}

					temp = append(temp, step)
				}
				out["steps"] = temp

			case "notify":
				notifications, ok := asSequence(val)
				if !ok {
					return out, fmt.Errorf("invalid notify value type %T for %q", val, src.Name)
				}
				temp := slices.Clone(out.notifications())
				temp = append(temp, notifications...)
				out["notify"] = temp
			case "env", "agents":
				curr, _ := asMapping(out[key])
				in, ok := asMapping(val)
				if !ok {
					return out, fmt.Errorf("invalid %q value type %T for %q", key, val, src.Name)
				}
				temp := make(map[string]any, len(curr)+len(in))
				maps.Copy(temp, curr)
				maps.Copy(temp, in)
				out[key] = temp
			default:
				out[key] = val
			}
		}
	}

	return out, nil
}

func recordStep(memo map[string]string, key, owner string) error {
	if conflict, found := memo[key]; found {
		return fmt.Errorf("duplicate step %q in %q, conflicts with %q", key, owner, conflict)
	}
	memo[key] = owner

	return nil
}

func asMapping(val any) (map[string]any, bool) {
	m, ok := val.(map[string]any)

	return m, ok
}

func asSequence(val any) ([]any, bool) {
	s, ok := val.([]any)

	return s, ok
}

// stepKey returns a step's "key" field.
//
// The bool reports whether the step has a key at all. Absence is legitimate in
// two ways: a step may simply not declare one, and a step need not be a mapping
// — "steps: [wait]" is valid Buildkite — so neither case is an error.
//
// A key that is present but not a string is an error rather than a silent
// absence. An unquoted numeric key parses as a number, and treating that as
// "no key" would quietly exclude the step from duplicate detection.
func stepKey(step any) (string, bool, error) {
	stepMapping, ok := asMapping(step)
	if !ok {
		return "", false, nil
	}

	key, ok := stepMapping["key"]
	if !ok {
		return "", false, nil
	}

	k, ok := key.(string)
	if !ok {
		return "", false, fmt.Errorf("step key must be a string, got %T (%v)", key, key)
	}

	return k, true, nil
}
