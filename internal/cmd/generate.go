package cmd

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"

	"github.com/goccy/go-yaml"
	"github.com/spf13/pflag"

	"github.com/akijowski/pipefitter/internal/bkenv"
	"github.com/akijowski/pipefitter/internal/pipeline"
	"github.com/akijowski/pipefitter/internal/render"
	"github.com/akijowski/pipefitter/internal/source"
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

func (g *generateCmd) Name() string { return "generate" }

func (g *generateCmd) Description() string { return "generate a pipeline from sources to standard out" }

func (g *generateCmd) Flags(fs *pflag.FlagSet) {
	fs.StringSliceVarP(&g.valuesFiles, "values", "f", nil, "values files to apply to each bundled source")
}

func (g *generateCmd) Run(ctx context.Context, out io.Writer, args []string) error {
	b, err := buildPipeline(os.DirFS("."), environMap(), args, g.valuesFiles)
	if err != nil {
		return err
	}

	_, err = out.Write(b)

	return err
}

// buildPipeline renders every bundle in dirs and combines them into one
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
// Nothing is written anywhere. The caller receives the finished bytes and
// decides where they go, which is what lets Run keep stdout empty when any stage
// fails.
func buildPipeline(fsys fs.FS, vars map[string]string, dirs, valuesFiles []string) ([]byte, error) {
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

	merged, err := pipeline.Merge(srcs)
	if err != nil {
		return nil, err
	}

	return pipeline.Marshal(merged)
}

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
