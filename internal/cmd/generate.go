package cmd

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"strings"

	"github.com/goccy/go-yaml"
	"github.com/spf13/pflag"

	"github.com/akijowski/pipefitter/internal/bkenv"
	"github.com/akijowski/pipefitter/internal/pipeline"
	"github.com/akijowski/pipefitter/internal/render"
	"github.com/akijowski/pipefitter/internal/source"
	"github.com/akijowski/pipefitter/internal/validate"
	"github.com/akijowski/pipefitter/internal/values"
)

const defaultBundleDir = ".buildkite/pipefitter"

// generateCmd implements:
//
//	pipefitter generate [flags] [bundle-dir...]
//
// Positional arguments are bundle directories, each merged into the output in
// the order given. With no arguments the conventional location is used, so a
// repository following the convention needs no arguments at all:
//
//	pipefitter generate | buildkite-agent pipeline upload
//
// Flags:
//
//	--values, -f   a values file; repeatable, layered left to right
//
// Values precedence for each bundle, lowest to highest:
//
//	the bundle's own values.yaml   <-   -f base.yaml   <-   -f prod.yaml
//
// A bundle's values.yaml is always the base and is never disabled by --values.
// It is the bundle's declared interface: templates render with missingkey=error,
// so a template can only read keys that file declares. The --values files layer
// on top and are shared by every bundle in the invocation, while each bundle
// starts its own chain from its own defaults.
type generateCmd struct {
	valuesFiles []string
}

// pipelinePatch is a patch value that applies to every bundle in a pipeline.
// It contains an attribution source for error-handling.
type pipelinePatch struct {
	patch  map[string]any
	source string
}

// checkedPipeline is the result of the stages both subcommands share: the
// merged document, and whatever validation had to say about it.
//
// The two travel together because a caller generally needs both — validate
// reports the findings, generate serializes the document only if there are
// none — and keeping them in one value means the shared stages run once.
type checkedPipeline struct {
	doc      pipeline.Document
	findings []validate.Finding
}

func (g *generateCmd) Name() string { return "generate" }

func (g *generateCmd) Description() string { return "generate a pipeline from sources to standard out" }

func (g *generateCmd) Flags(fs *pflag.FlagSet) {
	registerValuesFlag(fs, &g.valuesFiles)
}

func (g *generateCmd) Run(_ context.Context, host Host, args []string) error {
	checked, err := checkPipeline(host.FS, host.Environ, args, g.valuesFiles)
	if err != nil {
		return err
	}

	// Fail closed: a pipeline with findings must not reach stdout. The findings
	// travel in the error because generate, unlike validate, has not printed
	// them — and "N problems" without saying what they are is not actionable.
	if len(checked.findings) > 0 {
		b, err := marshalFindings(checked.findings)
		if err != nil {
			return err
		}

		return fmt.Errorf("pipeline is not valid: %s", strings.TrimSpace(string(b)))
	}

	b, err := pipeline.Marshal(checked.doc)
	if err != nil {
		return err
	}
	_, err = host.Out.Write(b)

	return err
}

// checkPipeline renders every bundle in dirs and combines them into one
// pipeline document.
//
// It takes the filesystem and the environment as arguments rather than reading
// os.DirFS and os.Environ itself, which keeps the whole flow a function of its
// inputs and lets tests drive it with fstest.MapFS and a plain map. Run is the
// only place that touches process state.
//
// The values chain is rebuilt from scratch for each bundle. That single detail
// is what makes bundles isolated: one bundle's defaults are never in scope while
// another renders, so bumping a shared bundle's version cannot silently change
// an unrelated bundle's behavior through a colliding key name.
//
// Findings come back as data rather than an error, because the two subcommands
// treat them differently: validate reports them, generate refuses to emit a
// document. Only a genuine failure — an unreadable bundle, a template that will
// not render, a duplicate step key — is returned as an error.
//
// Nothing is written anywhere and nothing is serialized. Serialization is
// generate's own last step, which is what lets validate share these stages
// without producing a document, and what lets Run keep stdout empty when any
// stage fails.
func checkPipeline(fsys fs.FS, vars map[string]string, dirs, valuesFiles []string) (checkedPipeline, error) {
	checked := checkedPipeline{}
	merged, err := mergePipeline(fsys, vars, dirs, valuesFiles)
	if err != nil {
		return checked, err
	}
	checked.doc = merged
	checked.findings = validate.Run(merged, validate.Rules())

	return checked, nil
}

// mergePipeline runs the stages before validation: it loads each bundle, builds
// that bundle's values, renders and parses every template, and merges the
// resulting documents into one.
func mergePipeline(fsys fs.FS, vars map[string]string, dirs, valuesFiles []string) (pipeline.Document, error) {
	if len(dirs) == 0 {
		dirs = []string{defaultBundleDir}
	}

	patches, err := loadPatchesFromFiles(fsys, valuesFiles)
	if err != nil {
		return nil, err
	}

	env := bkenv.Parse(vars)

	var srcs []pipeline.Source
	for _, dir := range dirs {
		// load the bundle from the directory
		bundle, err := source.LoadDir(fsys, dir)
		if err != nil {
			return nil, err
		}

		// merge bundle values with global pipeline values
		vals, err := mergeValues(dir+" defaults", bundle.Defaults, patches)
		if err != nil {
			return nil, fmt.Errorf("merge values: %w", err)
		}

		renderCtx := render.Context{Values: vals.Tree, Env: env}

		// parse each bundle to a source containing a Document
		for _, name := range bundle.TemplateNames() {
			src := pipeline.Source{
				Name: dir + "/" + name,
			}
			doc, err := compileBundleToDocument(name, bundle, renderCtx)
			if err != nil {
				return nil, fmt.Errorf("bundle %q: %w", src.Name, err)
			}
			src.Doc = doc

			srcs = append(srcs, src)
		}
	}

	return pipeline.Merge(srcs)
}

// loadPatchesFromFiles reads and parses the --values files, once, in the order
// given.
//
// They are read before any bundle is loaded: every bundle layers the same
// patches, so reading them once avoids re-parsing per bundle, and a missing or
// malformed file fails before any rendering work happens.
func loadPatchesFromFiles(fsys fs.FS, patchFiles []string) ([]pipelinePatch, error) {
	patches := make([]pipelinePatch, 0, len(patchFiles))
	for _, patchFile := range patchFiles {
		patchData, err := fs.ReadFile(fsys, patchFile)
		if err != nil {
			return nil, fmt.Errorf("unable to read file %q: %w", patchFile, err)
		}
		var patch map[string]any
		err = yaml.Unmarshal(patchData, &patch)
		if err != nil {
			return nil, fmt.Errorf("unable to parse YAML from values file %q: %w", patchFile, err)
		}
		patches = append(patches, pipelinePatch{patch: patch, source: patchFile})
	}

	return patches, nil
}

// mergeValues builds one bundle's values: its own defaults first, then each
// patch in order, so a later patch wins.
//
// The chain starts from an empty Values every time it is called, which is what
// keeps bundles isolated — one bundle's defaults are never in scope while
// another renders.
func mergeValues(defaultSrc string, defaults map[string]any, patches []pipelinePatch) (values.Values, error) {
	vals := values.Values{}
	vals, err := values.Merge(vals, defaults, defaultSrc)
	if err != nil {
		return vals, fmt.Errorf("unable to merge values from %q: %w", defaultSrc, err)
	}
	for _, p := range patches {
		vals, err = values.Merge(vals, p.patch, p.source)
		if err != nil {
			return vals, fmt.Errorf("unable to merge values from %q: %w", p.source, err)
		}
	}

	return vals, nil
}

// compileBundleToDocument renders one template from a bundle and parses the
// result, which is the text-to-structured transition: everything before is
// strings, everything after is a Document.
func compileBundleToDocument(name string, bundle source.Bundle, rCtx render.Context) (pipeline.Document, error) {
	text, err := render.Render(bundle.TemplateSet(), name, rCtx, render.NoAgent{})
	if err != nil {
		return pipeline.Document{}, fmt.Errorf("unable to render template: %w", err)
	}

	return pipeline.Parse(text)
}

// environMap converts the process environment into the map bkenv.Parse wants.
//
// This is the only place pipefitter reads os.Environ, which is what keeps every
// package below cmd testable with a plain map.
//
// Entries without an "=" are skipped rather than indexed into: os.Environ
// normally yields "key=value", but a malformed entry would otherwise panic.
func environMap() map[string]string {
	environ := os.Environ()
	m := make(map[string]string, len(environ))

	for _, e := range environ {
		if k, v, ok := strings.Cut(e, "="); ok {
			m[k] = v
		}
	}

	return m
}
