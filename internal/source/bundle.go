// Package source loads template bundles.
//
// A bundle is a directory holding an optional values.yaml alongside one or more
// templates. LoadDir takes an fs.FS rather than a path, which keeps it a pure
// function of its input and lets tests use fstest.MapFS.
package source

import (
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"path"
	"slices"
	"strings"

	"github.com/goccy/go-yaml"
)

// Bundle is a loaded template bundle.
//
// Files in the directory are classified by name, in this order:
//
//   - a "_" prefix makes a helper, whatever its extension. Helpers are parsed
//     for {{ define }} blocks and never emit a document of their own.
//   - a ".tmpl" suffix makes a template. Each one renders a YAML document.
//   - anything else is ignored, including values.yaml, README.md and LICENSE.
//
// Templates are opt-in by extension rather than opt-out by denylist, so a stray
// file in a bundle cannot end up rendered into the pipeline.
//
// Directories are skipped, including symlinks to directories. Symlinks to files
// are followed, so a shared template can be linked into a bundle.
//
// Map keys are base names, not paths, because render uses them as template names.
// Defaults is nil when the bundle has no values.yaml, or an empty one.
type Bundle struct {
	Name      string
	Defaults  map[string]any
	Templates map[string]string
	Helpers   map[string]string
}

// TemplateNames returns the template names in sorted order.
//
// Ordering of names is critical: render parses in it and the document merge
// concatenates steps in it, so leaving it to map iteration would make the
// generated pipeline vary between runs.
func (b Bundle) TemplateNames() []string {
	return slices.Sorted(maps.Keys(b.Templates))
}

func (b Bundle) TemplateSet() map[string]string {
	m := make(map[string]string, len(b.Templates)+len(b.Helpers))
	maps.Copy(m, b.Templates)
	maps.Copy(m, b.Helpers)

	return m
}

// LoadDir reads the bundle in dir.
//
// A missing values.yaml is fine, and so is an empty one — a bundle may exist
// only to supply templates. A malformed values.yaml, or one whose document is
// not a mapping, is an error naming the file.
//
// A directory with no templates is an error: a bundle of only helpers would
// render an empty pipeline, which is more likely a mistake than an intent.
func LoadDir(fsys fs.FS, dir string) (Bundle, error) {
	b := Bundle{
		Name:      dir,
		Defaults:  nil,
		Helpers:   map[string]string{},
		Templates: map[string]string{},
	}

	dirEntries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return b, fmt.Errorf("unable to read directory %s: %w", dir, err)
	}

	valuesFile := path.Join(dir, "values.yaml")
	valuesData, err := fs.ReadFile(fsys, valuesFile)
	switch {
	case err != nil && !errors.Is(err, fs.ErrNotExist):
		return b, fmt.Errorf("unable to read %q: %w", valuesFile, err)
	case !errors.Is(err, fs.ErrNotExist):
		// values.yaml exists so parse data to Defaults
		var defaults map[string]any
		if err = yaml.Unmarshal(valuesData, &defaults); err != nil {
			return b, fmt.Errorf("unable to parse %q: %w", valuesFile, err)
		}
		b.Defaults = defaults

	}

	for _, entry := range dirEntries {
		entryPath := path.Join(dir, entry.Name())
		info, err := fs.Stat(fsys, entryPath)
		if err != nil {
			return b, fmt.Errorf("unable to stat %q: %w", entryPath, err)
		}
		if info.IsDir() {
			continue
		}

		// order matters, check for "_" to find helper files first
		if strings.HasPrefix(entry.Name(), "_") {
			if err := readFSContentToMap(fsys, entryPath, b.Helpers); err != nil {
				return b, err
			}
			continue
		}

		if strings.HasSuffix(entry.Name(), ".tmpl") {
			if err := readFSContentToMap(fsys, entryPath, b.Templates); err != nil {
				return b, err
			}
		}
	}

	if len(b.Templates) == 0 {
		return b, fmt.Errorf("no templates found in %s", dir)
	}

	return b, nil
}

// readFSContentToMap reads p and stores its contents in m under p's base name.
func readFSContentToMap(fsys fs.FS, p string, m map[string]string) error {
	contents, err := fs.ReadFile(fsys, p)
	if err != nil {
		return fmt.Errorf("unable to read %q: %w", p, err)
	}
	m[path.Base(p)] = string(contents)

	return nil
}
