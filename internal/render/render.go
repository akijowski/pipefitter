// Package render executes pipefitter templates.
//
// A template sees exactly two namespaces: .Values, the merged configuration
// tree, and .Env, the typed environment. Nothing else is reachable — in
// particular there is no way to read the process environment or the network from
// inside a template, so a render is a function of its inputs.
package render

import (
	"bytes"
	"context"
	"fmt"
	"maps"
	"slices"
	"text/template"

	"github.com/Masterminds/sprig/v3"

	"github.com/akijowski/pipefitter/internal/bkenv"
)

// Context is the data a template renders against.
//
// The two namespaces are deliberately separate rather than merged into one tree:
// keeping them apart means the answer to "where did this value come from?" never
// depends on the process environment.
type Context struct {
	Values map[string]any
	Env    bkenv.Env
}

// AgentClient is the seam for buildkite-agent I/O during a render.
//
// Nothing uses it yet: MVP registers no template function that calls the agent.
// It is threaded through Render from the start so that adding those functions
// later does not churn every call site and test. When they land, the
// implementation should memoize per key — a template referencing the same
// meta-data key from several steps is the normal case — and record what it
// fetched, so agent data gets the same provenance story as values.
type AgentClient interface {
	MetaData(ctx context.Context, key string) (string, bool, error)
}

// NoAgent is the default outside CI, where no agent is reachable. Every lookup
// fails, naming the key that was wanted.
type NoAgent struct{}

func (NoAgent) MetaData(_ context.Context, key string) (string, bool, error) {
	return "", false, fmt.Errorf("no buildkite-agent available for meta-data key %q", key)
}

// FuncMap returns the functions available to templates: sprig, minus the ones
// pipefitter deliberately withholds.
//
// The exclusions are removed rather than shadowed, so a template using one fails
// at parse time with "function not defined" instead of silently misbehaving.
//
//   - env, expandenv: they read the live process environment at render time,
//     which is invisible to provenance reporting. .Env is the one way in.
//   - getHostByName: performs a DNS lookup mid-render.
//   - uuidv4, rand*: non-deterministic, so identical inputs would produce
//     different pipelines on every run.
func FuncMap() template.FuncMap {
	disallowedFuncNames := []string{
		"env",
		"expandenv",
		"getHostByName",
		"uuidv4",
		"randAlpha",
		"randAlphaNum",
		"randNumeric",
		"randAscii",
	}
	m := sprig.TxtFuncMap()
	for _, name := range disallowedFuncNames {
		delete(m, name)
	}

	return m
}

// Render parses every template in tmpls as one set and executes entry against
// renderCtx, returning the rendered text.
//
// All of tmpls is parsed, not just entry, so {{ define }} blocks in helper files
// are visible. Names are parsed in sorted order, which only matters when two
// files define the same name — without it, map iteration order would decide the
// winner and output would not be reproducible.
//
// Missing keys are errors, not empty strings. text/template would otherwise emit
// the literal "<no value>", which is valid YAML and would ship as a real string;
// missingkey=zero does not help, because the zero value of any is a nil
// interface that prints the same way. A bundle must therefore declare every key
// its templates read in values.yaml. Note the consequence for sprig's default:
// it covers a key that is present but empty, not one that is absent, since the
// map index is evaluated before the pipe.
func Render(tmpls map[string]string, entry string, renderCtx Context, agent AgentClient) (string, error) {
	t := template.New("pipefitter")
	fm := FuncMap()
	// include renders a named template to a string, which is what makes helpers
	// usable in pipe chains. It looks circular — the function needs the set,
	// and the set needs its functions registered before parsing — but is not:
	// the closure captures t, and by the time anything executes, every template
	// has been added to it.
	fm["include"] = func(name string, data any) (string, error) {
		var buf bytes.Buffer
		err := t.ExecuteTemplate(&buf, name, data)
		return buf.String(), err
	}
	// Funcs must be registered before Parse: the parser resolves function names
	// as it goes, which is what turns a withheld function into a parse error.
	t.Funcs(fm)
	t.Option("missingkey=error")

	sortedTmplKeys := slices.Sorted(maps.Keys(tmpls))
	for _, tmplKey := range sortedTmplKeys {
		tmpl := tmpls[tmplKey]
		if _, err := t.New(tmplKey).Parse(tmpl); err != nil {
			return "", fmt.Errorf("failed to parse template %s: %w", tmplKey, err)
		}
	}

	if t.Lookup(entry) == nil {
		return "", fmt.Errorf("failed to find template entry %s", entry)
	}

	var buf bytes.Buffer
	if err := t.ExecuteTemplate(&buf, entry, renderCtx); err != nil {
		return "", fmt.Errorf("failed to execute template %s: %w", entry, err)
	}

	return buf.String(), nil
}
