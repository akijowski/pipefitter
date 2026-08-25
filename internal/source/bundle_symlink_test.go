package source

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLoadDirFollowsSymlinks pins a deliberate choice that fstest.MapFS cannot
// express, so it runs against a real filesystem.
//
// fs.ReadDir reports an entry's type without following symlinks, so a symlinked
// template has IsRegular() == false even though fs.ReadFile reads it fine.
// Filtering on IsRegular would therefore drop symlinked templates silently.
// Sharing a template into a bundle by symlink is a reasonable thing to do, so
// LoadDir skips only directories.
func TestLoadDirFollowsSymlinks(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	bundle := filepath.Join(root, "bundle")
	shared := filepath.Join(root, "shared")
	require.NoError(t, os.MkdirAll(bundle, 0o755))
	require.NoError(t, os.MkdirAll(shared, 0o755))

	require.NoError(t, os.WriteFile(
		filepath.Join(shared, "shared.tmpl"),
		[]byte("steps:\n  - label: shared\n"), 0o644))
	require.NoError(t, os.WriteFile(
		filepath.Join(bundle, "local.tmpl"),
		[]byte("steps:\n  - label: local\n"), 0o644))

	// A symlinked template, and a symlinked helper.
	require.NoError(t, os.Symlink(
		filepath.Join(shared, "shared.tmpl"),
		filepath.Join(bundle, "linked.tmpl")))
	require.NoError(t, os.WriteFile(
		filepath.Join(shared, "_helpers.tpl"),
		[]byte(`{{ define "x" }}y{{ end }}`), 0o644))
	require.NoError(t, os.Symlink(
		filepath.Join(shared, "_helpers.tpl"),
		filepath.Join(bundle, "_linked.tpl")))

	got, err := LoadDir(os.DirFS(root), "bundle")
	require.NoError(t, err)

	assert.Len(t, got.Templates, 2, "a symlinked template must be loaded")
	assert.Contains(t, got.Templates, "local.tmpl")
	assert.Contains(t, got.Templates, "linked.tmpl")
	assert.Equal(t, "steps:\n  - label: shared\n", got.Templates["linked.tmpl"],
		"content must be read through the symlink")

	assert.Contains(t, got.Helpers, "_linked.tpl", "a symlinked helper must be loaded")
}

// TestLoadDirSkipsSymlinkedDirectories keeps the directory rule intact: a
// symlink pointing at a directory must still be skipped rather than read.
func TestLoadDirSkipsSymlinkedDirectories(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	bundle := filepath.Join(root, "bundle")
	other := filepath.Join(root, "other")
	require.NoError(t, os.MkdirAll(bundle, 0o755))
	require.NoError(t, os.MkdirAll(other, 0o755))

	require.NoError(t, os.WriteFile(
		filepath.Join(bundle, "local.tmpl"), []byte("steps: []\n"), 0o644))
	require.NoError(t, os.WriteFile(
		filepath.Join(other, "nested.tmpl"), []byte("steps: []\n"), 0o644))

	// A symlink to a directory, named so it would otherwise look like a template.
	require.NoError(t, os.Symlink(other, filepath.Join(bundle, "dirlink.tmpl")))

	got, err := LoadDir(os.DirFS(root), "bundle")
	require.NoError(t, err)

	assert.Len(t, got.Templates, 1, "a symlinked directory is not a template")
	assert.Contains(t, got.Templates, "local.tmpl")
}
