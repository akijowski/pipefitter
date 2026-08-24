# Pipefitter MVP Implementation Plan

> **Working style:** Paired TDD, red/green. Claude writes the test and runs it
> red. Adam writes the implementation. Green is verified together, then commit.
> Steps are labelled **[C]** (Claude) or **[A]** (Adam). Steps use checkbox
> (`- [ ]`) syntax for tracking.

**Goal:** A working `pipefitter generate` that renders one or more local template
bundles into a single validated Buildkite pipeline on stdout.

**Architecture:** Per-bundle: load → merge values (RFC 7396) → render
(text/template + sprig) → parse YAML. Then across bundles: merge documents →
validate → serialize once. Pure packages (`values`, `bkenv`) have no
dependencies and are tested against the RFC's own cases.

**Tech Stack:** Go 1.26, `spf13/pflag`, `goccy/go-yaml`, `Masterminds/sprig/v3`,
`stretchr/testify`, `rogpeppe/go-internal/testscript`.

**Spec:** `docs/superpowers/specs/2026-08-24-pipefitter-contract-design.md`

---

## MVP scope

**In:**

- One or more **local** bundle directories as positional args; default
  `.buildkite/pipefitter`
- `values.yaml` defaults ← `--values` files (repeatable), RFC 7396, provenance
  recorded
- `.Values` + `.Env` (curated Buildkite subset + raw `Vars`)
- sprig minus the excluded functions
- Multiple templates per bundle; `_`-prefixed files are helpers only
- Document merge: `steps`/`notify` concatenate, `env`/`agents` deep-merge
- Validation: duplicate step keys, dangling `depends_on`
- `pipefitter generate` and `pipefitter validate`
- testscript harness

**Deferred to phase 2+ (all additive, seams per the spec):** go-getter remote
resolution, `pipefitter values` (provenance is *recorded* in MVP, just not
surfaced), `--output`, `--log-file`, `--verbose`, agent-backed template
functions, schema validation, comment preservation.

**Present in MVP but unused:** the `render.AgentClient` interface and
`render.NoAgent`, plus the `agent` parameter on `render.Render`. No template
function calls it yet. Carrying the parameter now costs one unused argument;
adding it later would churn every call site and test.

**One deliberate refinement to the spec:** duplicate-step-key detection lives in
`pipeline.Merge` rather than in the validation rule set, because that is the only
place where per-source names are still in scope. `validate` remains a rule set
and owns dangling `depends_on`. The spec's intent (a clear error naming both
bundles) is preserved.

## File structure

| File | Responsibility |
| --- | --- |
| `internal/values/values.go` | RFC 7396 merge + provenance |
| `internal/values/values_test.go` | RFC appendix cases, provenance |
| `internal/bkenv/bkenv.go` | `map[string]string` → typed `Env` |
| `internal/bkenv/bkenv_test.go` | field mapping, `PULL_REQUEST=false` |
| `internal/render/render.go` | `Context`, `FuncMap`, `Render`, `AgentClient` |
| `internal/render/render_test.go` | rendering, helpers, excluded funcs |
| `internal/pipeline/pipeline.go` | `Document`, `Parse`, `Merge`, `Marshal` |
| `internal/pipeline/pipeline_test.go` | concat/merge rules, dup keys |
| `internal/validate/validate.go` | `Rule`, `Finding`, `Run`, depends_on rule |
| `internal/validate/validate_test.go` | one table per rule |
| `internal/source/bundle.go` | `Bundle`, `LoadDir` over `fs.FS` |
| `internal/source/bundle_test.go` | layout, `_` helpers, missing values.yaml |
| `internal/cmd/generate.go` | `generate` subcommand wiring |
| `internal/cmd/validate.go` | `validate` subcommand wiring |
| `internal/cmd/script_test.go` | testscript entrypoint |
| `internal/cmd/testdata/script/*.txtar` | CLI contract cases |

## Test conventions

Applies to every task, stated once (DRY):

- Table tests keyed by a descriptive name: `map[string]struct{...}`.
- `t.Parallel()` on the outer test **and** each subtest.
- Go 1.26, so **no `tc := tc` capture** is needed.
- `require` for anything that must halt the subtest (errors, nil checks);
  `assert` for value comparisons that can continue.
- Never read the real process environment or filesystem — pass maps and
  `fstest.MapFS`.

---

## Task 1: Add testify and establish conventions

**Files:**
- Modify: `go.mod`
- Modify: `internal/cmd/cmd_test.go`
- Modify: `main_test.go`

- [ ] **Step 1 [A]: Add the dependency**

```bash
go get github.com/stretchr/testify@v1.12.1
go mod tidy
```

- [ ] **Step 2 [C]: Convert the existing tests to testify**

Rewrite the assertion bodies in `internal/cmd/cmd_test.go` and `main_test.go` to
use `require`/`assert`, keeping the existing cases and names unchanged. Example
of the target shape for the `TestRun` body:

```go
err := Run(context.Background(), &out, &errOut, tc.args)

if tc.wantErr != nil {
	require.ErrorIs(t, err, tc.wantErr)
} else {
	require.NoError(t, err)
}

if tc.wantErrOut != "" {
	assert.Contains(t, errOut.String(), tc.wantErrOut)
}

if tc.wantOutEmpt {
	assert.Empty(t, out.String())
}
```

- [ ] **Step 3 [A/C]: Verify nothing regressed**

Run: `go test ./...`
Expected: PASS, same cases as before.

- [ ] **Step 4: Commit**

```bash
git add go.mod go.sum internal/cmd/cmd_test.go main_test.go
git commit -m "test: adopt testify for assertions"
```

---

## Task 2: RFC 7396 merge

**Files:**
- Create: `internal/values/values.go`
- Test: `internal/values/values_test.go`

- [ ] **Step 1 [C]: Write the failing test**

Cases taken from RFC 7396's own appendix so the behavior is spec-verified.

```go
package values

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMergeRFC7396(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		base  map[string]any
		patch map[string]any
		want  map[string]any
	}{
		"replaces a scalar": {
			base:  map[string]any{"a": "b"},
			patch: map[string]any{"a": "c"},
			want:  map[string]any{"a": "c"},
		},
		"adds a key": {
			base:  map[string]any{"a": "b"},
			patch: map[string]any{"b": "c"},
			want:  map[string]any{"a": "b", "b": "c"},
		},
		"null deletes a key": {
			base:  map[string]any{"a": "b"},
			patch: map[string]any{"a": nil},
			want:  map[string]any{},
		},
		"null on a missing key is a no-op": {
			base:  map[string]any{"a": "b"},
			patch: map[string]any{"c": nil},
			want:  map[string]any{"a": "b"},
		},
		"merges nested objects recursively": {
			base:  map[string]any{"a": map[string]any{"b": "c", "keep": "yes"}},
			patch: map[string]any{"a": map[string]any{"b": "d"}},
			want:  map[string]any{"a": map[string]any{"b": "d", "keep": "yes"}},
		},
		"array replaces wholesale": {
			base:  map[string]any{"a": []any{"b", "c"}},
			patch: map[string]any{"a": []any{"d"}},
			want:  map[string]any{"a": []any{"d"}},
		},
		"object replaces array": {
			base:  map[string]any{"a": []any{"b"}},
			patch: map[string]any{"a": map[string]any{"c": "d"}},
			want:  map[string]any{"a": map[string]any{"c": "d"}},
		},
		"scalar replaces object": {
			base:  map[string]any{"a": map[string]any{"b": "c"}},
			patch: map[string]any{"a": "z"},
			want:  map[string]any{"a": "z"},
		},
		"empty patch changes nothing": {
			base:  map[string]any{"a": "b"},
			patch: map[string]any{},
			want:  map[string]any{"a": "b"},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := Merge(Values{Tree: tc.base}, tc.patch, "patch")

			assert.Equal(t, tc.want, got.Tree)
		})
	}
}

func TestMergeDoesNotMutateBase(t *testing.T) {
	t.Parallel()

	base := map[string]any{"a": map[string]any{"b": "c"}}

	Merge(Values{Tree: base}, map[string]any{"a": map[string]any{"b": "changed"}}, "patch")

	assert.Equal(t, map[string]any{"a": map[string]any{"b": "c"}}, base,
		"Merge must not mutate its input")
}
```

- [ ] **Step 2 [C]: Run it red**

Run: `go test ./internal/values/ -run TestMerge -v`
Expected: FAIL — `undefined: Merge`, `undefined: Values`.

- [ ] **Step 3 [A]: Implement**

Define `Values`, `Origin`, and `Merge` per the spec. `Merge` must deep-copy
rather than mutate `base` (the last test pins this). The algorithm is the RFC's
pseudocode: if the patch value is a map, recurse; if it is nil, delete; else
replace.

- [ ] **Step 4 [A/C]: Run it green**

Run: `go test ./internal/values/ -v`
Expected: PASS, all subtests.

- [ ] **Step 5: Commit**

```bash
git add internal/values/
git commit -m "feat(values): RFC 7396 merge patch semantics"
```

---

## Task 3: Merge provenance

**Files:**
- Modify: `internal/values/values.go`
- Test: `internal/values/values_test.go`

- [ ] **Step 1 [C]: Write the failing test**

```go
func TestMergeOrigins(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		layers []struct {
			patch  map[string]any
			source string
		}
		want map[string]Origin
	}{
		"records the source of each leaf": {
			layers: []struct {
				patch  map[string]any
				source string
			}{
				{map[string]any{"a": "1", "b": "2"}, "defaults"},
				{map[string]any{"b": "3"}, "values.yaml"},
			},
			want: map[string]Origin{
				"a": {Source: "defaults"},
				"b": {Source: "values.yaml"},
			},
		},
		"records nested paths with dots": {
			layers: []struct {
				patch  map[string]any
				source string
			}{
				{map[string]any{"img": map[string]any{"tag": "v1"}}, "defaults"},
			},
			want: map[string]Origin{
				"img.tag": {Source: "defaults"},
			},
		},
		"records a deletion": {
			layers: []struct {
				patch  map[string]any
				source string
			}{
				{map[string]any{"q": "default"}, "defaults"},
				{map[string]any{"q": nil}, "values.yaml"},
			},
			want: map[string]Origin{
				"q": {Source: "values.yaml", Deleted: true},
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := Values{}
			for _, l := range tc.layers {
				got = Merge(got, l.patch, l.source)
			}

			assert.Equal(t, tc.want, got.Origins)
		})
	}
}
```

- [ ] **Step 2 [C]: Run it red**

Run: `go test ./internal/values/ -run TestMergeOrigins -v`
Expected: FAIL — origins empty or nil.

- [ ] **Step 3 [A]: Implement**

Record `Origins[dottedPath] = Origin{Source: source}` for each scalar leaf
written, and `Origin{Source: source, Deleted: true}` when a key is deleted.
Only leaves get entries — intermediate maps do not.

- [ ] **Step 4 [A/C]: Run it green**

Run: `go test ./internal/values/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/values/
git commit -m "feat(values): track provenance during merge"
```

---

## Task 4: Buildkite environment parsing

**Files:**
- Create: `internal/bkenv/bkenv.go`
- Test: `internal/bkenv/bkenv_test.go`

- [ ] **Step 1 [C]: Write the failing test**

```go
package bkenv

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParse(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		vars   map[string]string
		assert func(t *testing.T, env Env)
	}{
		"maps scalar fields": {
			vars: map[string]string{
				"BUILDKITE_BRANCH":        "main",
				"BUILDKITE_COMMIT":        "abc123",
				"BUILDKITE_PIPELINE_SLUG": "my-app",
				"BUILDKITE_MESSAGE":       "fix: thing",
			},
			assert: func(t *testing.T, env Env) {
				assert.Equal(t, "main", env.Buildkite.Branch)
				assert.Equal(t, "abc123", env.Buildkite.Commit)
				assert.Equal(t, "my-app", env.Buildkite.PipelineSlug)
				assert.Equal(t, "fix: thing", env.Buildkite.Message)
			},
		},
		"parses build number as int": {
			vars: map[string]string{"BUILDKITE_BUILD_NUMBER": "42"},
			assert: func(t *testing.T, env Env) {
				assert.Equal(t, 42, env.Buildkite.BuildNumber)
			},
		},
		"unparseable build number is zero": {
			vars: map[string]string{"BUILDKITE_BUILD_NUMBER": "not-a-number"},
			assert: func(t *testing.T, env Env) {
				assert.Zero(t, env.Buildkite.BuildNumber)
			},
		},
		"pull request false is not a PR": {
			vars: map[string]string{"BUILDKITE_PULL_REQUEST": "false"},
			assert: func(t *testing.T, env Env) {
				assert.False(t, env.Buildkite.PullRequest.IsPR)
				assert.Zero(t, env.Buildkite.PullRequest.Number)
			},
		},
		"pull request number is a PR": {
			vars: map[string]string{
				"BUILDKITE_PULL_REQUEST":             "123",
				"BUILDKITE_PULL_REQUEST_BASE_BRANCH": "main",
			},
			assert: func(t *testing.T, env Env) {
				assert.True(t, env.Buildkite.PullRequest.IsPR)
				assert.Equal(t, 123, env.Buildkite.PullRequest.Number)
				assert.Equal(t, "main", env.Buildkite.PullRequest.BaseBranch)
			},
		},
		"missing pull request var is not a PR": {
			vars: map[string]string{},
			assert: func(t *testing.T, env Env) {
				assert.False(t, env.Buildkite.PullRequest.IsPR)
			},
		},
		"non-buildkite vars land in Vars": {
			vars: map[string]string{"MY_CUSTOM": "hello", "BUILDKITE_BRANCH": "main"},
			assert: func(t *testing.T, env Env) {
				assert.Equal(t, "hello", env.Vars["MY_CUSTOM"])
			},
		},
		"Vars also contains buildkite vars unmodified": {
			vars: map[string]string{"BUILDKITE_BRANCH": "main"},
			assert: func(t *testing.T, env Env) {
				assert.Equal(t, "main", env.Vars["BUILDKITE_BRANCH"])
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			tc.assert(t, Parse(tc.vars))
		})
	}
}
```

- [ ] **Step 2 [C]: Run it red**

Run: `go test ./internal/bkenv/ -v`
Expected: FAIL — `undefined: Parse`, `undefined: Env`.

- [ ] **Step 3 [A]: Implement**

Define `Env`, `Buildkite`, `PullRequest` per the spec, and
`func Parse(vars map[string]string) Env`. `Vars` is the full input map,
unfiltered — the typed struct is a convenience view, not a partition. Note the
last two cases pin that `Vars` contains everything.

- [ ] **Step 4 [A/C]: Run it green**

Run: `go test ./internal/bkenv/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/bkenv/
git commit -m "feat(bkenv): parse Buildkite environment into typed struct"
```

---

## Task 5: Template rendering

**Files:**
- Create: `internal/render/render.go`
- Test: `internal/render/render_test.go`

- [ ] **Step 1 [A]: Add sprig**

```bash
go get github.com/Masterminds/sprig/v3@v3.3.0
go mod tidy
```

- [ ] **Step 2 [C]: Write the failing test**

```go
package render

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/akijowski/pipefitter/internal/bkenv"
)

func TestRender(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		tmpls   map[string]string
		entry   string
		ctx     Context
		want    string
		wantErr string
	}{
		"renders values": {
			tmpls: map[string]string{"a.tmpl": `tag: {{ .Values.tag }}`},
			entry: "a.tmpl",
			ctx:   Context{Values: map[string]any{"tag": "v1"}},
			want:  "tag: v1",
		},
		"renders buildkite env": {
			tmpls: map[string]string{"a.tmpl": `branch: {{ .Env.Buildkite.Branch }}`},
			entry: "a.tmpl",
			ctx:   Context{Env: bkenv.Env{Buildkite: bkenv.Buildkite{Branch: "main"}}},
			want:  "branch: main",
		},
		"uses a sprig function": {
			tmpls: map[string]string{"a.tmpl": `{{ .Values.x | default "fallback" }}`},
			entry: "a.tmpl",
			ctx:   Context{Values: map[string]any{}},
			want:  "fallback",
		},
		"includes a helper define": {
			tmpls: map[string]string{
				"_helpers.tpl": `{{ define "label" }}:go: test{{ end }}`,
				"a.tmpl":       `label: {{ template "label" }}`,
			},
			entry: "a.tmpl",
			want:  "label: :go: test",
		},
		"unknown struct field is an error": {
			tmpls:   map[string]string{"a.tmpl": `{{ .Env.Buildkite.Brnach }}`},
			entry:   "a.tmpl",
			wantErr: "Brnach",
		},
		"env function is not available": {
			tmpls:   map[string]string{"a.tmpl": `{{ env "HOME" }}`},
			entry:   "a.tmpl",
			wantErr: "env",
		},
		"expandenv function is not available": {
			tmpls:   map[string]string{"a.tmpl": `{{ expandenv "$HOME" }}`},
			entry:   "a.tmpl",
			wantErr: "expandenv",
		},
		"uuidv4 function is not available": {
			tmpls:   map[string]string{"a.tmpl": `{{ uuidv4 }}`},
			entry:   "a.tmpl",
			wantErr: "uuidv4",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, err := Render(tc.tmpls, tc.entry, tc.ctx, NoAgent{})

			if tc.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantErr)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestNoAgentErrorsWithGuidance(t *testing.T) {
	t.Parallel()

	_, found, err := NoAgent{}.MetaData(context.Background(), "deploy-target")

	assert.False(t, found)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "deploy-target",
		"the error must name the key, or debugging is miserable")
}
```

- [ ] **Step 3 [C]: Run it red**

Run: `go test ./internal/render/ -v`
Expected: FAIL — `undefined: Render`, `undefined: Context`.

- [ ] **Step 4 [A]: Implement**

```go
type Context struct {
	Values map[string]any
	Env    bkenv.Env
}

// AgentClient is the seam for buildkite-agent I/O during render. MVP registers
// no functions that use it, but the parameter exists from the start so adding
// them later does not churn every call site and test.
type AgentClient interface {
	MetaData(ctx context.Context, key string) (string, bool, error)
}

// NoAgent is the default outside CI: every lookup fails with guidance.
type NoAgent struct{}

func (NoAgent) MetaData(_ context.Context, key string) (string, bool, error) {
	return "", false, fmt.Errorf("no buildkite-agent available for meta-data key %q", key)
}
```

`FuncMap()` starts from `sprig.TxtFuncMap()` and **deletes** `env`,
`expandenv`, `getHostByName`, `uuidv4`, and the `rand*` family. Deleting rather
than shadowing is what makes the "not available" cases fail at parse time.

`Render(tmpls map[string]string, entry string, ctx Context, agent AgentClient) (string, error)`
parses every entry in `tmpls` into one template set (so `define` blocks in
helpers are visible), then executes `entry`. Parse template names in sorted
order for determinism. `agent` is accepted and stored but unused in MVP — do
**not** register any agent-backed template functions yet.

- [ ] **Step 5 [A/C]: Run it green**

Run: `go test ./internal/render/ -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add go.mod go.sum internal/render/
git commit -m "feat(render): template execution with curated sprig FuncMap"
```

---

## Task 6: Parse and serialize YAML documents

**Files:**
- Create: `internal/pipeline/pipeline.go`
- Test: `internal/pipeline/pipeline_test.go`

- [ ] **Step 1 [A]: Add the YAML library**

```bash
go get github.com/goccy/go-yaml@v1.19.2
go mod tidy
```

- [ ] **Step 2 [C]: Write the failing test**

```go
package pipeline

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParse(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		in      string
		want    Document
		wantErr bool
	}{
		"parses steps": {
			in: "steps:\n  - label: test\n",
			want: Document{"steps": []any{
				map[string]any{"label": "test"},
			}},
		},
		"parses an empty document as empty": {
			in:   "",
			want: Document{},
		},
		"rejects malformed yaml": {
			in:      "steps:\n  - [unclosed\n",
			wantErr: true,
		},
		"rejects a non-mapping document": {
			in:      "- just\n- a\n- list\n",
			wantErr: true,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, err := Parse([]byte(tc.in))

			if tc.wantErr {
				require.Error(t, err)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestMarshalRoundTrip(t *testing.T) {
	t.Parallel()

	doc := Document{"steps": []any{map[string]any{"label": "test", "key": "t"}}}

	b, err := Marshal(doc)
	require.NoError(t, err)

	got, err := Parse(b)
	require.NoError(t, err)

	assert.Equal(t, doc, got)
}
```

- [ ] **Step 3 [C]: Run it red**

Run: `go test ./internal/pipeline/ -v`
Expected: FAIL — `undefined: Document`, `Parse`, `Marshal`.

- [ ] **Step 4 [A]: Implement**

`type Document map[string]any`. `Parse` unmarshals into a `Document` and returns
an error if the document is not a mapping. An empty input yields an empty
non-nil `Document`. `Marshal` serializes with `goccy/go-yaml`.

- [ ] **Step 5 [A/C]: Run it green**

Run: `go test ./internal/pipeline/ -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add go.mod go.sum internal/pipeline/
git commit -m "feat(pipeline): parse and serialize pipeline documents"
```

---

## Task 7: Document merge

**Files:**
- Modify: `internal/pipeline/pipeline.go`
- Test: `internal/pipeline/pipeline_test.go`

- [ ] **Step 1 [C]: Write the failing test**

```go
func TestMerge(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		srcs    []Source
		want    Document
		wantErr string
	}{
		"concatenates steps in source order": {
			srcs: []Source{
				{Name: "a", Doc: Document{"steps": []any{map[string]any{"key": "one"}}}},
				{Name: "b", Doc: Document{"steps": []any{map[string]any{"key": "two"}}}},
			},
			want: Document{"steps": []any{
				map[string]any{"key": "one"},
				map[string]any{"key": "two"},
			}},
		},
		"concatenates notify": {
			srcs: []Source{
				{Name: "a", Doc: Document{"notify": []any{"one"}}},
				{Name: "b", Doc: Document{"notify": []any{"two"}}},
			},
			want: Document{"notify": []any{"one", "two"}},
		},
		"deep-merges env with later winning": {
			srcs: []Source{
				{Name: "a", Doc: Document{"env": map[string]any{"A": "1", "B": "1"}}},
				{Name: "b", Doc: Document{"env": map[string]any{"B": "2"}}},
			},
			want: Document{"env": map[string]any{"A": "1", "B": "2"}},
		},
		"deep-merges agents with later winning": {
			srcs: []Source{
				{Name: "a", Doc: Document{"agents": map[string]any{"queue": "default"}}},
				{Name: "b", Doc: Document{"agents": map[string]any{"queue": "big"}}},
			},
			want: Document{"agents": map[string]any{"queue": "big"}},
		},
		"single source passes through": {
			srcs: []Source{
				{Name: "a", Doc: Document{"steps": []any{map[string]any{"key": "one"}}}},
			},
			want: Document{"steps": []any{map[string]any{"key": "one"}}},
		},
		"duplicate step key across sources is an error": {
			srcs: []Source{
				{Name: "shared/test", Doc: Document{"steps": []any{map[string]any{"key": "test"}}}},
				{Name: "local/test", Doc: Document{"steps": []any{map[string]any{"key": "test"}}}},
			},
			wantErr: "test",
		},
		"steps without keys do not collide": {
			srcs: []Source{
				{Name: "a", Doc: Document{"steps": []any{map[string]any{"label": "x"}}}},
				{Name: "b", Doc: Document{"steps": []any{map[string]any{"label": "y"}}}},
			},
			want: Document{"steps": []any{
				map[string]any{"label": "x"},
				map[string]any{"label": "y"},
			}},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, err := Merge(tc.srcs)

			if tc.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantErr)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestMergeDuplicateKeyErrorNamesBothSources(t *testing.T) {
	t.Parallel()

	_, err := Merge([]Source{
		{Name: "shared/test", Doc: Document{"steps": []any{map[string]any{"key": "test"}}}},
		{Name: "local/test", Doc: Document{"steps": []any{map[string]any{"key": "test"}}}},
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "shared/test")
	assert.Contains(t, err.Error(), "local/test")
}
```

- [ ] **Step 2 [C]: Run it red**

Run: `go test ./internal/pipeline/ -run TestMerge -v`
Expected: FAIL — `undefined: Source`, `undefined: Merge`.

- [ ] **Step 3 [A]: Implement**

```go
type Source struct {
	Name string
	Doc  Document
}

func Merge(srcs []Source) (Document, error)
```

`steps` and `notify` concatenate in slice order. `env` and `agents` deep-merge
with later sources winning. Any other top-level key: last writer wins. While
concatenating `steps`, track `key` → source name; on a second sighting return an
error naming both sources and the key. Steps with no `key` are exempt.

- [ ] **Step 4 [A/C]: Run it green**

Run: `go test ./internal/pipeline/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/pipeline/
git commit -m "feat(pipeline): merge documents with duplicate key detection"
```

---

## Task 8: Validation rule set

**Files:**
- Create: `internal/validate/validate.go`
- Test: `internal/validate/validate_test.go`

- [ ] **Step 1 [C]: Write the failing test**

```go
package validate

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/akijowski/pipefitter/internal/pipeline"
)

func TestDependsOnRule(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		doc       pipeline.Document
		wantCount int
		wantIn    string
	}{
		"resolved dependency passes": {
			doc: pipeline.Document{"steps": []any{
				map[string]any{"key": "test"},
				map[string]any{"key": "deploy", "depends_on": "test"},
			}},
			wantCount: 0,
		},
		"dangling dependency is reported": {
			doc: pipeline.Document{"steps": []any{
				map[string]any{"key": "deploy", "depends_on": "nope"},
			}},
			wantCount: 1,
			wantIn:    "nope",
		},
		"dependency as a list is checked": {
			doc: pipeline.Document{"steps": []any{
				map[string]any{"key": "test"},
				map[string]any{"key": "deploy", "depends_on": []any{"test", "missing"}},
			}},
			wantCount: 1,
			wantIn:    "missing",
		},
		"no steps is fine": {
			doc:       pipeline.Document{},
			wantCount: 0,
		},
		"no depends_on is fine": {
			doc: pipeline.Document{"steps": []any{
				map[string]any{"key": "test"},
			}},
			wantCount: 0,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := DependsOn{}.Check(tc.doc)

			assert.Len(t, got, tc.wantCount)

			if tc.wantIn != "" {
				assert.Contains(t, got[0].Message, tc.wantIn)
			}
		})
	}
}

func TestRunAggregatesRules(t *testing.T) {
	t.Parallel()

	doc := pipeline.Document{"steps": []any{
		map[string]any{"key": "deploy", "depends_on": "nope"},
	}}

	findings := Run(doc, Rules())

	assert.NotEmpty(t, findings)
}
```

- [ ] **Step 2 [C]: Run it red**

Run: `go test ./internal/validate/ -v`
Expected: FAIL — `undefined: DependsOn`, `Run`, `Rules`.

- [ ] **Step 3 [A]: Implement**

```go
type Finding struct {
	Rule    string
	Message string
}

type Rule interface {
	Name() string
	Check(pipeline.Document) []Finding
}

type DependsOn struct{}

func Rules() []Rule
func Run(doc pipeline.Document, rules []Rule) []Finding
```

`DependsOn.Check` collects every step `key`, then reports any `depends_on`
reference not in that set. `depends_on` may be a string or a list of strings.
`Rules()` returns the default set — just `DependsOn{}` for MVP. `Run` calls each
rule and concatenates findings.

- [ ] **Step 4 [A/C]: Run it green**

Run: `go test ./internal/validate/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/validate/
git commit -m "feat(validate): rule set with dangling depends_on detection"
```

---

## Task 9: Bundle loading

**Files:**
- Create: `internal/source/bundle.go`
- Test: `internal/source/bundle_test.go`

- [ ] **Step 1 [C]: Write the failing test**

Uses `fstest.MapFS` so no temp directories are involved.

```go
package source

import (
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadDir(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		files   fstest.MapFS
		dir     string
		assert  func(t *testing.T, b Bundle)
		wantErr string
	}{
		"loads values, templates and helpers": {
			files: fstest.MapFS{
				"b/values.yaml":  {Data: []byte("tag: v1\n")},
				"b/test.tmpl":    {Data: []byte("steps: []\n")},
				"b/_helpers.tpl": {Data: []byte(`{{ define "x" }}y{{ end }}`)},
			},
			dir: "b",
			assert: func(t *testing.T, b Bundle) {
				assert.Equal(t, map[string]any{"tag": "v1"}, b.Defaults)
				assert.Contains(t, b.Templates, "test.tmpl")
				assert.Contains(t, b.Helpers, "_helpers.tpl")
				assert.NotContains(t, b.Templates, "_helpers.tpl")
				assert.NotContains(t, b.Templates, "values.yaml")
			},
		},
		"missing values.yaml is not an error": {
			files: fstest.MapFS{"b/test.tmpl": {Data: []byte("steps: []\n")}},
			dir:   "b",
			assert: func(t *testing.T, b Bundle) {
				assert.Empty(t, b.Defaults)
				assert.Len(t, b.Templates, 1)
			},
		},
		"bundle name is the directory": {
			files: fstest.MapFS{"b/test.tmpl": {Data: []byte("steps: []\n")}},
			dir:   "b",
			assert: func(t *testing.T, b Bundle) {
				assert.Equal(t, "b", b.Name)
			},
		},
		"missing directory is an error": {
			files:   fstest.MapFS{},
			dir:     "nope",
			wantErr: "nope",
		},
		"directory with no templates is an error": {
			files:   fstest.MapFS{"b/values.yaml": {Data: []byte("tag: v1\n")}},
			dir:     "b",
			wantErr: "no templates",
		},
		"malformed values.yaml is an error": {
			files: fstest.MapFS{
				"b/values.yaml": {Data: []byte("tag: [unclosed\n")},
				"b/test.tmpl":   {Data: []byte("steps: []\n")},
			},
			dir:     "b",
			wantErr: "values.yaml",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, err := LoadDir(tc.files, tc.dir)

			if tc.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantErr)

				return
			}

			require.NoError(t, err)
			tc.assert(t, got)
		})
	}
}

func TestLoadDirTemplateOrderIsDeterministic(t *testing.T) {
	t.Parallel()

	files := fstest.MapFS{
		"b/c.tmpl": {Data: []byte("steps: []\n")},
		"b/a.tmpl": {Data: []byte("steps: []\n")},
		"b/b.tmpl": {Data: []byte("steps: []\n")},
	}

	got, err := LoadDir(files, "b")
	require.NoError(t, err)

	assert.Equal(t, []string{"a.tmpl", "b.tmpl", "c.tmpl"}, got.TemplateNames())
}
```

- [ ] **Step 2 [C]: Run it red**

Run: `go test ./internal/source/ -v`
Expected: FAIL — `undefined: Bundle`, `LoadDir`.

- [ ] **Step 3 [A]: Implement**

```go
type Bundle struct {
	Name      string
	Defaults  map[string]any
	Templates map[string]string
	Helpers   map[string]string
}

func (b Bundle) TemplateNames() []string // sorted
func LoadDir(fsys fs.FS, dir string) (Bundle, error)
```

Read the directory once. `values.yaml` becomes `Defaults` (absent is fine,
malformed is an error mentioning the filename). Files beginning with `_` go to
`Helpers`; all other regular files except `values.yaml` go to `Templates`.
Subdirectories are ignored. Error if `Templates` ends up empty.

- [ ] **Step 4 [A/C]: Run it green**

Run: `go test ./internal/source/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/source/
git commit -m "feat(source): load a template bundle from a directory"
```

---

## Task 10: Wire the generate subcommand

**Files:**
- Create: `internal/cmd/generate.go`
- Modify: `internal/cmd/cmd.go` (add to the `commands()` registry)
- Test: `internal/cmd/generate_test.go`

- [ ] **Step 1 [C]: Write the failing test**

```go
package cmd

import (
	"bytes"
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGenerateRegistered(t *testing.T) {
	t.Parallel()

	reg := commands()

	require.Contains(t, reg, "generate")
	assert.NotEmpty(t, reg["generate"].Description())
}

func TestGenerateMissingBundleFails(t *testing.T) {
	t.Parallel()

	var out, errOut bytes.Buffer

	err := Run(context.Background(), &out, &errOut, []string{"generate", "does-not-exist"})

	require.Error(t, err)
	assert.Empty(t, out.String(), "stdout must stay empty on failure")
}
```

- [ ] **Step 2 [C]: Run it red**

Run: `go test ./internal/cmd/ -run TestGenerate -v`
Expected: FAIL — registry has no `"generate"`.

- [ ] **Step 3 [A]: Implement**

Add `generateCmd` satisfying the existing `command` interface, registered in
`commands()`. `Flags` registers `--values`/`-f` as a repeatable string slice.
`Run` performs, for each positional source (defaulting to
`.buildkite/pipefitter`):

1. `source.LoadDir(os.DirFS("."), dir)`
2. `vals := values.Values{}`; merge `bundle.Defaults` with source label
   `"<name> defaults"`, then each `--values` file in order
3. for each `bundle.TemplateNames()`:
   `render.Render(tmpls, name, ctx, render.NoAgent{})`, where `tmpls` is
   `bundle.Templates` and `bundle.Helpers` combined into one map so `define`
   blocks resolve
4. `pipeline.Parse` each rendered document into a `pipeline.Source` named
   `"<bundle>/<template>"`

Two small helpers this needs, defined in `internal/cmd`:

```go
// environMap converts the process environment into the map bkenv.Parse wants.
func environMap() map[string]string {
	vars := make(map[string]string)

	for _, kv := range os.Environ() {
		if k, v, ok := strings.Cut(kv, "="); ok {
			vars[k] = v
		}
	}

	return vars
}

// templateSet combines a bundle's templates and helpers into one parse set.
func templateSet(b source.Bundle) map[string]string {
	set := make(map[string]string, len(b.Templates)+len(b.Helpers))

	for k, v := range b.Templates {
		set[k] = v
	}

	for k, v := range b.Helpers {
		set[k] = v
	}

	return set
}
```

`environMap` is the one place the real environment is read, which keeps every
package below `cmd` testable with plain maps.

Then across all sources: `pipeline.Merge`, `validate.Run` (any findings → error),
`pipeline.Marshal`, and only then write to `out`.

Buffer the output and write it after validation passes — the stdout invariant.

- [ ] **Step 4 [A/C]: Run it green**

Run: `go test ./internal/cmd/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/cmd/
git commit -m "feat(cmd): add generate subcommand"
```

---

## Task 11: Wire the validate subcommand

**Files:**
- Create: `internal/cmd/validate.go`
- Modify: `internal/cmd/cmd.go`
- Test: `internal/cmd/validate_test.go`

- [ ] **Step 1 [C]: Write the failing test**

```go
package cmd

import (
	"bytes"
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateRegistered(t *testing.T) {
	t.Parallel()

	reg := commands()

	require.Contains(t, reg, "validate")
	assert.NotEmpty(t, reg["validate"].Description())
}

func TestValidateNeverWritesStdout(t *testing.T) {
	t.Parallel()

	var out, errOut bytes.Buffer

	// Missing bundle: the command must fail without emitting a document.
	err := Run(context.Background(), &out, &errOut, []string{"validate", "does-not-exist"})

	require.Error(t, err)
	assert.Empty(t, out.String())
}
```

- [ ] **Step 2 [C]: Run it red**

Run: `go test ./internal/cmd/ -run TestValidate -v`
Expected: FAIL — registry has no `"validate"`.

- [ ] **Step 3 [A]: Implement**

`validateCmd` runs the identical flow as `generate` through validation, then
returns. Extract the shared stages into one unexported helper in
`internal/cmd` returning the merged `pipeline.Document`, so the two commands
cannot drift. `validate` writes findings to `errOut` and never touches `out`.

- [ ] **Step 4 [A/C]: Run it green**

Run: `go test ./internal/cmd/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/cmd/
git commit -m "feat(cmd): add validate subcommand"
```

---

## Task 12: Exit code split

**Files:**
- Modify: `internal/cmd/cmd.go`
- Modify: `main.go`
- Test: `main_test.go`

- [ ] **Step 1 [C]: Write the failing test**

```go
func TestRunMainExitCodes(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		args     []string
		wantCode int
	}{
		"help is success":            {args: []string{"--help"}, wantCode: exitCodeSuccess},
		"bare invocation is success": {args: nil, wantCode: exitCodeSuccess},
		"version is success":         {args: []string{"version"}, wantCode: exitCodeSuccess},
		"unknown command is usage":   {args: []string{"bogus"}, wantCode: exitCodeUsage},
		"unknown flag is usage":      {args: []string{"version", "--nope"}, wantCode: exitCodeUsage},
		"missing bundle is error":    {args: []string{"generate", "nope"}, wantCode: exitCodeErr},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var out, errOut bytes.Buffer

			got := runMain(context.Background(), &out, &errOut, tc.args)

			assert.Equal(t, tc.wantCode, got, "stderr: %s", errOut.String())
		})
	}
}
```

- [ ] **Step 2 [C]: Run it red**

Run: `go test . -run TestRunMainExitCodes -v`
Expected: FAIL — `undefined: exitCodeUsage`; bare invocation returns 1.

- [ ] **Step 3 [A]: Implement**

Add `exitCodeUsage = 2` in `main.go`. Split the sentinel in `internal/cmd`:
keep `ErrUsage` for misuse (unknown command, bad flag) and add `ErrHelp` for a
help request. `runMain` maps `ErrHelp` → 0, `ErrUsage` → 2, any other error → 1.
Replace the existing `TestRunMain` with this table.

Also set the default log level to **warn** per the spec — `main.go` currently
passes `nil` handler options, which defaults to info:

```go
slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
	Level: slog.LevelWarn,
})))
```

`--verbose` is deferred, so warn is the only level in MVP. The corollary from
the spec applies from day one: nothing may log at warn on a successful run.

- [ ] **Step 4 [A/C]: Run it green**

Run: `go test ./...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add main.go main_test.go internal/cmd/
git commit -m "feat(cmd): separate help from usage-error exit codes"
```

---

## Task 13: testscript CLI harness

**Files:**
- Create: `internal/cmd/script_test.go`
- Create: `internal/cmd/testdata/script/generate.txtar`
- Create: `internal/cmd/testdata/script/duplicate_key.txtar`

- [ ] **Step 1 [A]: Add the dependency**

```bash
go get github.com/rogpeppe/go-internal@v1.16.0
go mod tidy
```

- [ ] **Step 2 [C]: Write the harness and the failing scripts**

`internal/cmd/script_test.go`:

```go
package cmd

import (
	"context"
	"os"
	"testing"

	"github.com/rogpeppe/go-internal/testscript"
)

func TestMain(m *testing.M) {
	os.Exit(testscript.RunMain(m, map[string]func() int{
		"pipefitter": func() int {
			if err := Run(context.Background(), os.Stdout, os.Stderr, os.Args[1:]); err != nil {
				return 1
			}

			return 0
		},
	}))
}

func TestScripts(t *testing.T) {
	t.Parallel()

	testscript.Run(t, testscript.Params{Dir: "testdata/script"})
}
```

`internal/cmd/testdata/script/generate.txtar`:

```
env BUILDKITE_BRANCH=main
env BUILDKITE_PULL_REQUEST=false

exec pipefitter generate
cmp stdout want.yaml

-- .buildkite/pipefitter/values.yaml --
goVersion: "1.26"
-- .buildkite/pipefitter/test.tmpl --
steps:
  - label: ":go: test {{ .Values.goVersion }}"
    command: go test ./...
    key: test
    branches: "{{ .Env.Buildkite.Branch }}"
-- want.yaml --
steps:
- branches: main
  command: go test ./...
  key: test
  label: ':go: test 1.26'
```

`internal/cmd/testdata/script/duplicate_key.txtar`:

```
! exec pipefitter generate a b
! stdout .
stderr 'test'

-- a/test.tmpl --
steps:
  - label: one
    key: test
-- b/test.tmpl --
steps:
  - label: two
    key: test
```

- [ ] **Step 3 [C]: Run it red**

Run: `go test ./internal/cmd/ -run TestScripts -v`
Expected: FAIL — `want.yaml` will not match until serialization is confirmed.

- [ ] **Step 4 [A]: Reconcile**

Capture the serializer's real output and paste it into the txtar's `want.yaml`
section verbatim:

```bash
mkdir -p /tmp/pf-want/.buildkite/pipefitter && cd /tmp/pf-want
printf 'goVersion: "1.26"\n' > .buildkite/pipefitter/values.yaml
cat > .buildkite/pipefitter/test.tmpl <<'TMPL'
steps:
  - label: ":go: test {{ .Values.goVersion }}"
    command: go test ./...
    key: test
    branches: "{{ .Env.Buildkite.Branch }}"
TMPL
BUILDKITE_BRANCH=main BUILDKITE_PULL_REQUEST=false \
  go run github.com/akijowski/pipefitter generate
```

Key order and indentation come from `goccy/go-yaml`, not from us — so the
`want.yaml` in Step 2 is a best guess. This step exists because the exact
serialized form is an *output* of Task 6, not something to predict. Replace the
guess with the captured bytes.

- [ ] **Step 5 [A/C]: Run it green**

Run: `go test ./...`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add go.mod go.sum internal/cmd/script_test.go internal/cmd/testdata/
git commit -m "test(cmd): add testscript CLI contract harness"
```

---

## Task 14: Update the README

**Files:**
- Modify: `README.md`

- [ ] **Step 1 [A]: Replace the aspirational description**

The current README describes a product that does not exist. Rewrite the
Description and add a Usage section covering: the bundle layout, `generate` and
`validate`, `--values` (including that it **replaces** auto-discovery), the
`.Values`/`.Env` namespaces, RFC 7396 merge semantics with the empty-YAML-value
`null` sharp edge, and output normalization (comments do not survive).

- [ ] **Step 2 [A]: Commit**

```bash
git add README.md
git commit -m "docs: describe actual pipefitter behavior"
```

---

## Verification

Run the full suite and confirm the invariants hold:

```bash
go test ./...
just lint
just build
```

Manual smoke test of the real contract:

```bash
mkdir -p /tmp/pf/.buildkite/pipefitter && cd /tmp/pf
printf 'steps:\n  - label: test\n    key: t\n    command: go test ./...\n' \
  > .buildkite/pipefitter/test.tmpl
pipefitter generate                    # YAML on stdout, exit 0
pipefitter generate | head -1          # pipeable
pipefitter generate nope; echo $?      # 1, nothing on stdout
pipefitter bogus; echo $?              # 2
pipefitter; echo $?                    # help, 0
```
