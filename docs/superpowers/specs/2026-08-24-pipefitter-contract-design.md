# Pipefitter CLI Contract & Architecture

**Date:** 2026-08-24 (design), 2026-08-28 (revised against the implementation)
**Status:** MVP implemented. Phase 2 work is listed under
[Deferred work](#deferred-work-in-priority-order).

Sections marked **as built** record where the implementation diverged from the
original design, and why. Where the two disagree, the implementation is correct
and this document has been corrected.

## Purpose

Pipefitter is a single binary with multiple subcommands that renders Buildkite
pipeline YAML to stdout, so a repository's checked-in `pipeline.yml` can be a
two-liner:

```yaml
steps:
  - command: pipefitter generate | buildkite-agent pipeline upload
```

Templates are **data pipefitter resolves**, never payload it ships. Nothing is
embedded in the binary. A shared bundle can be versioned and consumed by many
repositories, each supplying its own values.

## Non-goals

- Embedding templates in the binary.
- Being a general-purpose YAML templating tool. The output is a Buildkite
  pipeline document.
- Reimplementing Buildkite's pipeline schema (see Deferred).

## Architecture

Because templates are text but merging and validation need structure, the tool
is a pipeline with a text-to-structured transition in the middle:

```
1. RESOLVE    each source string -> local files          (go-getter)
2. LOAD       per bundle: template files + optional values.yaml defaults
3. AGGREGATE  per bundle: RenderContext{Values, Env}     (RFC 7396 + provenance)
4. RENDER     per bundle: text/template -> YAML text
5. PARSE      per document: YAML text -> structured document
6. MERGE      N documents -> 1 pipeline
7. VALIDATE   rule set over the merged tree              (fail closed)
8. SERIALIZE  once -> stdout or --output
```

Stages 1-5 are per-source and independent, which is what makes values isolation
(below) fall out of the structure rather than needing enforcement. Stages 6-8
are where sources meet.

The three main subcommands are the same pipeline truncated at different stages,
so they cannot drift out of sync:

| Command    | Stops after |
| ---------- | ----------- |
| `values`   | 3           |
| `validate` | 7           |
| `generate` | 8           |

### Package layout

| Package             | Purpose                                         | Depends on     |
| ------------------- | ----------------------------------------------- | -------------- |
| `internal/cmd`      | flag parsing, subcommand dispatch               | everything     |
| `internal/source`   | go-getter wrapper, protocol allowlist           | go-getter      |
| `internal/bkenv`    | `BUILDKITE_*` -> typed struct + raw `Vars`      | -              |
| `internal/values`   | RFC 7396 merge, provenance tracking             | -              |
| `internal/render`   | `RenderContext`, FuncMap, template execution    | values, bkenv  |
| `internal/pipeline` | document merge, serialize                       | yaml           |
| `internal/validate` | rule set over the merged document               | pipeline       |

Deliberate properties:

- `internal/values` and `internal/bkenv` have **no dependencies**. Both are pure
  data-in/data-out, so `values` is testable against RFC 7396's own appendix
  cases and `bkenv` is testable by feeding it a map.
- `internal/pipeline` **hides its representation**. Merge sits behind an
  interface so v1 can use `map[string]any` and a comment-preserving version can
  move to `yaml.Node` later without touching the resolver or renderer.

### Interfaces

Interfaces go only where there is a real second implementation or a need to
fake. `values` and `bkenv` stay concrete functions; an interface there would be
ceremony.

- `source.Fetcher` — fetching, faked in tests.
- `pipeline` merge — hides internal representation.
- `render.AgentClient` — future `buildkite-agent` I/O (see Deferred). Defined in
  v1 even if unused, so the feature is additive.
- `cmd.Host` — ambient process state. **As built:** the design called this `Env`
  and predicted env-var reading would force it into existence. Neither held.
  It is named `Host` because `bkenv.Env` — the *template's* view of the
  environment — appears in the same call chain and the two would be confused.
  And the forcing function was not env vars but the testability of `Run`: the
  subcommands built `os.DirFS(".")` and the environment inline, which is where
  every bug in that package lived, because no test could reach it.

  ```go
  type Host struct {
      FS      fs.FS             // bundles and values files
      Environ map[string]string // becomes .Env in a template
      Out     io.Writer         // the pipeline, and nothing else
      ErrOut  io.Writer         // usage, diagnostics, findings, the failure line
  }

  func OSHost() Host   // the only place pipefitter touches process state
  ```

  A parameter, never a receiver. No clock exists yet; `now` is not registered as
  a template function, so nothing needs one.

## Sources and bundles

A **source** is a bundle: a directory containing an optional `values.yaml` plus
one or more template files. Local and remote are uniform, because go-getter
fetches either.

```
.buildkite/pipefitter/
|- values.yaml        # defaults for this bundle
|- _helpers.tpl       # leading _ -> parsed for {{ define }}, never emitted
|- test.tmpl          # -> a YAML document
`- deploy.tmpl        # -> a YAML document
```

Files beginning with `_` are parsed but produce no document. This is Helm's
convention and it keeps text-level composition (`define`/`include`) cleanly
separate from document-level merging.

One remote repository may contain many bundles, selected with go-getter's
subdirectory syntax and pinned by ref:

```
git::ssh://git@github.com/org/pipelines//bundles/go-service?ref=v1.2.0
```

### Resolution

Sources are resolved with `hashicorp/go-getter` **v1** (v1.8.8). v1 is the
actively maintained line; v2 has been untouched since July 2024 and v1 is what
Terraform itself uses.

Accepted costs, recorded so they are not a surprise:

- Large dependency tree (AWS SDK, GCS) which will grow the binary substantially.
- A CVE history. Reasonable for a library that fetches and unpacks remote
  archives, but it means keeping the dependency patched matters.

Mitigation: the set of permitted protocols is an **explicit allowlist** via
go-getter's `Getters` map, not the permissive default.

## Data model

### Render context

What every template sees:

```go
type RenderContext struct {
	Values map[string]any // merged: bundle defaults <- values file(s)
	Env    Env
}

type Env struct {
	Buildkite Buildkite         // curated, typed
	Vars      map[string]string // long tail, raw
}

type Buildkite struct {
	Branch, Commit, Tag, Message string
	PipelineSlug, Organization   string
	BuildNumber                  int
	BuildURL                     string
	Source                       string // "webhook", "api", "ui", "schedule"
	RetryCount                   int
	PullRequest                  PullRequest
}

type PullRequest struct {
	IsPR       bool   // BUILDKITE_PULL_REQUEST != "false"
	Number     int
	BaseBranch string
	Repo       string
}
```

Design decisions:

- **Values and environment are separate namespaces, not merged layers.** Only
  the YAML sources merge. This keeps the merge chain two links long, so "what is
  `.Values.image.tag`?" never requires knowing the process environment.
- `Env` is a **struct with a typed `Buildkite` field plus a raw `Vars` escape
  hatch.** The deciding factor is failure mode: with a struct,
  `{{ .Env.Buildkite.Brnach }}` is an execution error and the build fails
  loudly. With maps it renders empty, producing a valid-looking pipeline with a
  blank branch condition. Silent-empty is the dangerous default for
  machine-consumed output.
- The **Buildkite set is curated, not exhaustive.** `Vars` covers the long tail;
  vars get promoted into the typed struct as demand appears.
- Name mapping is mechanical: `BUILDKITE_PIPELINE_SLUG` ->
  `.Env.Buildkite.PipelineSlug`.
- `PullRequest` is nested to defuse a real footgun: Buildkite sets
  `BUILDKITE_PULL_REQUEST=false` literally, which is a non-empty and therefore
  truthy string. `{{ if .Env.Buildkite.PullRequest.IsPR }}` is a real bool.

### Values merge

**Semantics are RFC 7396 (JSON Merge Patch)**, which obsoletes RFC 7386 and
pairs with RFC 5789. Adopting a named standard means the behavior is specified
and testable rather than invented:

- Objects merge recursively.
- **Arrays replace wholesale.** The RFC is explicit that partial array patching
  is not possible.
- Type mismatch: the patch value wins entirely.
- **`null` deletes the key.**

**Recursion limits, measured rather than assumed.** `values.Merge` bounds its
recursion because stack overflow in Go is a fatal error that `recover` cannot
catch. Two of the original justifications have since been tested:

- *"YAML aliases can produce a self-referential structure."* **False for
  `goccy/go-yaml`.** It resolves a recursive alias to `nil` rather than building
  a cycle, verified for all three forms (`steps: &a [{steps: *a}]`,
  `a: &x {self: *x}`, `a: &x [*x]`). No cyclic structure can reach a parsed
  document. The guard is still worth keeping — it protects against a
  programmatic caller passing a hand-built cyclic map, which the tests do — but
  it is not defending against YAML.
- *Deep nesting.* Self-limiting, because YAML indentation makes nesting
  quadratically expensive: depth 400 needs 650KB of input, depth 3000 needs 36MB.
  goccy parsed depth 3000 in 381ms, so its own parser recurses deeper than any
  walk of ours before we are reached.

Consequently the recursive walks in `pipeline` and `validate` do **not** carry
depth guards. That is a decision, not an omission.

**An unmitigated gap: alias expansion.** goccy parses a "billion laughs"
document without complaint — seven levels of nine-way aliases expand roughly
300 bytes into ~4.8M elements. A depth limit does not help, because expansion
happens during parsing, before pipefitter sees a document. This matters most for
phase 2, where bundles come from remote sources. Options if it becomes real:
bound the input size before parsing, or check whether goccy grows an expansion
limit.

Array-replace is the right call independent of the standard: append-semantics
would make a template default impossible to remove. `null`-deletes provides the
removal escape hatch.

**Documented sharp edge:** an empty YAML value parses as `null`, so

```yaml
queue:        # this DELETES the bundle's default
```

is a deletion, not a no-op. So do `queue: ~` and `queue: null`.

This interacts with `missingkey=error` in a way that inverts the obvious advice.
A bundle is supposed to declare every key its templates read, but it cannot
declare one as null — that deletes it, and the template then fails with "no entry
for key". Declare with an empty string instead:

```yaml
queue: ""     # present, so {{ .Values.queue | default "x" }} works
```

Verified against the binary: `queue:`, `queue: ~` and `queue: null` all error;
`queue: ""` and `queue: real` both work. `pipefitter values` must render deletions
explicitly (`queue: <deleted by values.yaml>`) so this is visible rather than
mysterious.

YAML-only types (timestamps, non-string keys) are treated as opaque scalars,
which means "replace".

### Values isolation

Each bundle renders against `MergePatch(bundleDefaults, valuesFiles...)`.
Isolation is **per bundle, not per file**: templates within one bundle share
that bundle's `values.yaml`; separate bundles cannot see each other's defaults.
The values file(s) are the single shared override layer.

The single-bundle case therefore degenerates to exactly two layers
(`defaults <- values.yaml`), keeping "render a whole pipeline with minimal
config" simple, and multi-bundle does not change the mental model.

**Why not one global tree.** A global tree couples independently versioned
bundles through a shared namespace. Bumping `deploy` from `v1.2.0` to `v1.3.0`
could change `test`'s behavior if the new version adds a default key that `test`
also reads — nothing in the consuming repo changed except one unrelated ref.
That failure mode is inherent to a shared namespace, not a bug to patch.

**Accepted limitation:** two bundles cannot receive different values for the
same key name. Workaround is distinct key names (`testQueue`, `deployQueue`),
which is also more readable. Namespaced per-source values is the deferred escape
hatch.

### Provenance

Merge carries origins rather than computing them in a second pass, so
`pipefitter values` is always accurate:

```go
type Values struct {
	Tree    map[string]any
	Origins map[string]Origin // "image.tag" -> where it came from
}

type Origin struct {
	Source  string // "bundle default (test@v1.2.0)" or "values.yaml"
	Deleted bool   // set to null per RFC 7396
}

func Merge(base Values, patch map[string]any, source string) Values
```

Folding this over the layers gives the `values` output for free.

## Template functions

Functions are registered through **our own `FuncMap` builder**, not by passing
a third-party map through directly.

Base set: `Masterminds/sprig` v3 (v3.3.0). sprig is stable-but-slow (last
release Aug 2024, repo active Jul 2025, not archived). Low churn in a function
library is acceptable, and the point of choosing it is that Helm users already
know these functions.

**Excluded functions:**

| Excluded                | Reason                                                                                                                              |
| ----------------------- | ----------------------------------------------------------------------------------------------------------------------------------- |
| `env`, `expandenv`      | They read the live process environment at render time, invisible to `pipefitter values`. That silently breaks the provenance guarantee. One path in (`.Env`), one place to inspect it. Helm excludes these too, for security reasons. |
| `getHostByName`         | Performs a live DNS lookup mid-render.                                                                                              |
| `randAlphaNum`, `uuid*` | Non-deterministic: identical inputs would render different output every run, breaking reproducible pipelines and golden tests.       |

`now` is sourced from an injected clock rather than the wall clock, for the same
determinism reason.

## Missing keys

**As built.** Templates render with `missingkey=error`, so reading a key that is
absent fails the render rather than emitting anything. This was not in the
original design and was decided during implementation.

Without it, `text/template` emits the literal string `<no value>` — which is
*valid YAML*. So `tag: {{ .Values.nope }}` would produce `tag: <no value>` and
ship a step carrying a real string of that name to Buildkite. A failed render is
strictly better than a silently wrong pipeline, which is the same reasoning
behind the typed `.Env.Buildkite` struct.

The obvious middle ground does not exist. `missingkey=zero` still prints
`<no value>` for a `map[string]any`, because the zero value of `any` is a nil
interface. It only helps typed maps, verified against the binary:

```
map[string]any      missingkey=zero    "<no value>"     <- .Values
map[string]string   missingkey=zero    ""               <- .Env.Vars
```

**Consequence for `default`.** Sprig's `default` covers a key that is present but
empty. It cannot cover an absent one, because the map index is evaluated before
the pipe and fails first. A bundle must therefore declare every key its templates
read, using `""` and not a bare `key:` — see the sharp edge under Values merge.

The upshot is that a bundle's `values.yaml` becomes its documented interface,
which is the property that makes a shared bundle readable without opening its
templates.

## Document merge

**RFC 7396 is deliberately NOT used at the document level.** Arrays-replace
would mean only the last bundle's `steps` survive, destroying composition. Two
merges with different rules is intentional; this section exists so nobody
"fixes" the inconsistency later.

Buildkite's top-level keys are a small closed set, which makes this tractable.
**Source order is precedence:**

| Key                | Rule                                       |
| ------------------ | ------------------------------------------ |
| `steps`, `notify`  | concatenate in source order                |
| `env`, `agents`    | deep-merge, later sources win on conflict  |

Command-line source order is already an explicit, visible, ordered precedence
mechanism. Priority weights would add a second competing one, where the failure
mode is a pipeline whose behavior does not match the order you read on the line.
Weights remain additive if ordering proves insufficient.

Erroring on any conflict was rejected: shared bundles setting common `env`
defaults that a repo-local bundle overrides **is** the intended workflow.

### Step keys

Keys are **literal rendered strings authored by humans.** No generation.

- Cross-bundle `depends_on` needs stable names a human can type in another
  template. Any generation scheme forces every reference through a helper
  function, which is indirection with no payoff while keys are already unique.
- The one case generation would solve is instantiating the same bundle twice,
  which is already out of scope: per-bundle isolation means two instances cannot
  receive different values. It is the same limitation wearing a different hat,
  and key namespacing should land together with namespaced per-source values as
  one coherent feature.
- An author who wants a parameterized key today already can:
  `key: deploy-{{ .Values.environment }}`.
- Buildkite explicitly forbids keys matching the UUID pattern. Beyond that the
  charset is undocumented, so avoid exotic separators on principle rather than
  relying on permissiveness.

## Validation

Runs on `generate` (fail closed) and is also exposed as `validate` for a
check-only run, so "is my pipeline valid?" does not require piping to
`/dev/null`. Built as a **rule set**, not hardcoded conditionals, so adding
rules is additive.

Validation inspects the merged structured document. It is not text-scraping:
parsing is already required in order to merge, so the tree is in hand.

v1 rules, scoped to errors that only exist because of composition — the ones
nothing else can catch:

1. **Duplicate step keys** across sources. Error names both bundles. Buildkite
   would reject this on upload, but our message is dramatically more useful.
2. **`depends_on` referencing a key that exists nowhere** in the merged
   pipeline. This is the natural failure mode of cross-bundle dependencies, and
   it is cheap for us because we have every key in the tree.

General schema validation is deferred (see below).

## CLI surface

```
pipefitter                                  -> help, exit 0
pipefitter generate  [flags] [source...]    -> YAML on stdout
pipefitter validate  [flags] [source...]    -> diagnostics on stderr, nothing on stdout
pipefitter values    [flags] [source...]    -> effective values + provenance
pipefitter version                          -> version string
```

`[source...]` defaults to `.buildkite/pipefitter` when omitted. Multiple sources
merge in the order given.

### Flags

| Flag              | Commands   | Meaning                                                              |
| ----------------- | ---------- | -------------------------------------------------------------------- |
| `--values`, `-f`  | all three  | values file; repeatable, layered left-to-right                       |
| `--output`, `-o`  | `generate` | write to file instead of stdout; `-` means stdout (default)          |
| `--verbose`, `-v` | all        | debug level and above; default is warn and above                     |
| `--log-file`      | all        | write everything sent to stderr to this file as well                 |

**`--log-file` captures the whole stderr transcript, not only log records.** The
spec originally said it teed `slog`'s writer. That is useless in practice:
nothing logs at warn yet, so the file would always be empty — and the most
common failure, a bundle that cannot be read, produces only the final error
line. It now tees `Host.ErrOut`, so findings, diagnostics and the failure reason
all land in the file. A file containing everything except the reason would be
useless exactly when it was needed.

The file is truncated on open: one run, one transcript, rather than accumulating
across retries of a CI step. Closing it is best-effort — the same bytes already
reached stderr, which is the authoritative copy, so reporting that a duplicate
was truncated is not worth complicating the dispatcher's control flow.

`--verbose` is deliberately not implemented yet. With no `slog` call sites it
would change nothing observable; it becomes meaningful when the first actionable
warning exists.

**Two known inconsistencies in the hand-rolled dispatcher**, both likely to be
resolved by adopting a CLI framework rather than fixed in place:

- Flags are accepted anywhere *after* the subcommand, but not before:
  `pipefitter generate --log-file=x` works, `pipefitter --log-file=x generate`
  reports an unknown command. Supporting both needs a separate pre-parse pass for
  flags that apply to every subcommand.
- `<command> --help` prints the flag list twice. pflag writes its own usage to
  the FlagSet's output on `ErrHelp`, and then `commandUsage` writes ours.

**`--values` layers on top of a bundle's own values.yaml.** Precedence for each
bundle, lowest to highest:

```
the bundle's own values.yaml   <-   -f base.yaml   <-   -f prod.yaml
```

*This supersedes an earlier decision that `--values` would replace
auto-discovery of `values.yaml`.* That rule was written when `values.yaml` was a
repo-level convenience file. The bundle model makes it the bundle's **declared
interface** instead, and templates render with `missingkey=error` — so a
template can only read keys that file declares. Disabling it would make every
template fail on its first key reference. A bundle's `values.yaml` is therefore
always the base and is never disabled.

The concern that motivated "replace" — implicit input making "why did my
pipeline change?" hard to answer — is addressed differently: a bundle's
`values.yaml` is not ambient discovery but part of the bundle being explicitly
named, and `pipefitter values` reports the provenance of every key.

The `-f` files are shared across every bundle in one invocation, while each
bundle starts its own chain from its own defaults.

`--set` is deliberately absent (deferred). There is deliberately **no
`--skip-validate`**: fail-closed is the point, and `validate` already exists for
diagnosis.

### Logging

Quiet by default, because the tool's purpose is YAML on stdout. A single `slog`
handler whose level comes from `--verbose` and whose writer is `os.Stderr` or
`io.MultiWriter(os.Stderr, logFile)`. Constructed in `main` from parsed flags,
which keeps `Env`'s writers the only I/O the packages see.

| Mode              | Level             |
| ----------------- | ----------------- |
| default           | `warn` and above  |
| `--verbose`, `-v` | `debug` and above |

**Default is `warn`, not `error`.** A successful run stays silent — nothing
should emit a warning on the happy path — so the tool is still quiet in normal
use. But warn-by-default means genuinely noisy conditions surface without
needing a flag: a source resolved from a mutable ref, a values key that no
template reads, a deprecated template function. Errors-only would hide exactly
the class of problem that is easiest to fix and hardest to notice.

The corollary is a standard to hold to: **a warning must be actionable.**
Anything logged at warn on a normal successful run is a bug in pipefitter, not
in the user's pipeline, because it trains people to ignore the channel.

Non-fatal findings from validation are a separate matter — those belong in
`validate`'s own output, not in log lines, since they are results rather than
diagnostics.

### Exit codes

| Code | Meaning                                                                    |
| ---- | -------------------------------------------------------------------------- |
| 0    | success — including bare invocation and `--help`                            |
| 1    | operational failure — source resolution, render, parse, merge, or validation |
| 2    | usage error — unknown command, unknown or malformed flag                    |

The boundary is deliberate: **2 covers only argument parsing**, before any work
begins. Everything that happens once pipefitter starts resolving sources is a 1,
including a source path that does not exist. A missing path is indistinguishable
from a fetch failure from the caller's perspective, and splitting them would
make the code decide whether each failure was the user's fault.

This requires splitting the existing `ErrUsage` sentinel: asking for help is a
success, misuse is a 2. Small change to `runMain`'s switch plus a second
sentinel.

### As built: the shared seam

The design said the subcommands are "the same pipeline truncated at different
stages". In the implementation that is two functions in `internal/cmd`:

| Function | Stages | Returns |
| --- | --- | --- |
| `mergePipeline` | 1-6: load, merge values, render, parse, merge documents | `pipeline.Document` |
| `checkPipeline` | 1-7: the above, then validate | `checkedPipeline{doc, findings}` |

`generate` calls `checkPipeline`, refuses on findings, then serializes — stage 8
belongs to it alone. `validate` calls `checkPipeline` and reports. The deferred
`render` command calls `mergePipeline`, which is why the split is drawn there.

**Findings are returned as data, not as an error.** That is what lets the two
commands treat the same result differently: `validate` prints them and exits
non-zero, `generate` refuses to emit. Only a genuine failure — an unreadable
bundle, a template that will not render, a duplicate step key — is an error.

A stage-composition framework (a `[]stage` each command slices) was considered
and rejected: with three commands and a fixed order, named functions are cheaper
and clearer. It becomes worth revisiting at four or five commands, or if the
order ever varies.

### The stdout invariant

**Stdout carries a pipeline document and nothing else.** Logs, usage,
diagnostics, and validation output all go to stderr. On any non-zero exit stdout
is empty, never a partial document.

This means `generate` buffers the serialized output and writes it only after
validation passes, rather than streaming. Small memory cost, and it is what
makes `pipefitter generate | buildkite-agent pipeline upload` safe without a
temp file.

### Output normalization

Because pipefitter parses and re-serializes rather than passing template output
through, **emitted YAML is normalized, not byte-identical to what templates
wrote.** Comments in templates do not survive, and key order comes from the
serializer. This is the cost of being able to merge and validate at all. It
surprises people, so it must be documented.

## Testing strategy

| Layer      | Approach                                                                                  |
| ---------- | ----------------------------------------------------------------------------------------- |
| `values`   | Table tests against RFC 7396's appendix cases, plus provenance assertions. No fixtures.   |
| `bkenv`    | Fed a `map[string]string`, never the real environment. Covers the `"false"` PR case.      |
| `pipeline` | Merge tables: list concat, map precedence, source ordering.                                |
| `validate` | One table per rule; each rule independently testable.                                     |
| `source`   | `Fetcher` faked in tests; one build-tagged integration test against a real local path.    |
| CLI        | `rogpeppe/go-internal/testscript` (v1.16.0).                                              |

`testscript` is the extraction of Go's own `cmd/go` test harness. It exercises
the real contract rather than a function resembling it, and an entire case lives
in one readable txtar file:

```
env BUILDKITE_BRANCH=main
env BUILDKITE_PULL_REQUEST=false

exec pipefitter generate
cmp stdout want.yaml

! exec pipefitter generate dupes/
! stdout .
stderr 'duplicate step key "test"'

-- .buildkite/pipefitter/values.yaml --
goVersion: "1.26"
-- .buildkite/pipefitter/test.tmpl --
steps:
  - label: ":go: test"
    command: go test ./...
    key: test
-- want.yaml --
steps:
  - label: ":go: test"
    command: go test ./...
    key: test
```

Three properties that matter specifically here: `env` sets Buildkite vars per
script, so `.Env.Buildkite` plumbing is tested end-to-end without touching
process state; `! stdout .` asserts the stdout-empty-on-failure invariant
directly; and `--output` / `--log-file` become testable via `cmp`.

Invariant tests remain their own category — stdout empty on every failure path.
`TestRunUsageStaysOffStdout` already exists.

## Deferred work, in priority order

Ranked 2026-08-28. Each entry names the seam that keeps it additive rather than a
refactor, so none of this requires unpicking the MVP.

| # | Item | Seam |
| --- | --- | --- |
| 1 | **Remote bundles via go-getter** | `source.LoadDir` takes an `fs.FS`; resolution happens before it |
| 2 | **Agent integration** — a single feature, see below | `render.AgentClient`, already threaded through `Render` |
| 3 | **`pipefitter values`** — effective values and their provenance | `values.Merge` already records `Origins` |
| 4 | **`pipefitter render`** — merged document, no validation | calls `mergePipeline`, the existing stage-6 seam |
| 5 | **`--set` overrides** | another layer in the `Merge` fold |
| 6 | **`--output` / `-o`** | `Host.Out` is already the only writer |
| 7 | **Buildkite schema validation** | `validate` is a rule set; add a rule |
| 8 | **Namespaced per-source values + step-key namespacing** | one coherent feature; lookup falls back to top-level |

### Item 2: agent integration ships as one feature

Three things that were listed separately during ranking, and are one piece of
work:

- **Threading `ctx`** through `Run`, `checkPipeline`, `mergePipeline`,
  `compileBundleToDocument` and into `render.Render`. It is accepted and dropped
  at every one of those today. This is not a follow-up to the agent functions;
  `AgentClient.MetaData` takes a `context.Context`, so it has to land first or
  as the same feature's opening commit.
- **The template functions themselves**, backed by a real `AgentClient` that
  shells out to `buildkite-agent`. Memoizing and recording, per the design
  decision recorded below.
- **Answer files**, a second `AgentClient` implementation, so a template using
  agent functions can still be rendered locally where no agent exists. Without
  it the feature is unusable outside CI, which is where people write templates.

This is also the likely home of **the first `slog` call sites**. Agent I/O is
where warn-level logging finally has something actionable to say — a lookup that
fell back to an answer file, a key the agent did not have — which is the
condition the logging design has been waiting for.

### Source attribution for findings is explicitly not planned

Validation runs on the merged document, where `pipeline.Source`'s template name
has been discarded, so a finding names the step at fault but not the file that
declared it.

**Item 4 (`render`) is the accepted answer to this**, not a workaround for it.
Being able to read the whole merged document is enough to locate a problem, and
it costs nothing beyond a command that stops at stage 6. Threading provenance
through `Document` would mean every merge carrying attribution that almost
nothing reads — the same objection that defers comment preservation — so it is
ruled out rather than deferred.

*(An earlier revision of this document had the relationship backwards, treating
attribution as the real fix and `render` as a stopgap.)*

**`render` must be a separate command, not `generate --no-validate`.**

```
pipefitter generate   -> validated document, for uploading
pipefitter render     -> merged document, no validation, for reading
```

Fail-closed on `generate` is what makes
`pipefitter generate | buildkite-agent pipeline upload` safe, and an escape hatch
on that same command is what someone reaches for under deadline pressure.
`generate --no-validate` in a checked-in `pipeline.yml` would survive review;
`render` piped into `pipeline upload` would not. The CLI surface section rules
out the flag for this reason, and that still stands.

### Item 1 is the largest

Remote bundles bring a protocol allowlist, a dependency with a CVE history, and
— unlike everything else here — input that is not first-party, which raises the
stakes on the alias-expansion gap below.

### Unprioritized

Equal and low priority as of 2026-08-28.

| Item | Note |
| --- | --- |
| `--verbose` / `-v` | Nothing logs at warn yet, so it would reveal nothing. Meaningful once the first actionable warning exists. |
| Priority weights instead of source order | Source order remains the default and is explicit at the call site. |
| YAML comment preservation | Likely a `yaml.Node` refactor of the merge layer — see the note below. |
| **Duplicate step keys inside groups** | `validate.DependsOn` recurses into group steps; `pipeline.Merge`'s duplicate detection does not. A key duplicated between a group member and a top-level step renders cleanly and is rejected by Buildkite on upload. Verified 2026-08-28. |
| Alias expansion | Unmitigated — see the note below. Rises in importance with item 1. |
| Flags before the subcommand | `pipefitter generate --log-file=x` works; `pipefitter --log-file=x generate` reports an unknown command. Needs a separate pre-parse pass. |
| `<command> --help` prints flags twice | pflag writes its own usage on `ErrHelp`, then `commandUsage` writes ours. |
| CLI framework adoption | See below. |
| `slog` has no call sites | Configured at warn, nothing logs. Likely picked up as part of item 2, where agent I/O gives warn-level logging its first actionable thing to say. Other candidates: a source resolved from a mutable ref, a values key no template reads. |

### CLI framework adoption

Deliberately not done. The hand-rolled dispatcher is ~70 lines for three flat
commands, and `google/subcommands` independently arrived at the same
`Name`/`Description`/`Flags`/`Run` shape, which is reassuring.

Triggers that would change the decision:

| Signal | Why |
| --- | --- |
| Nested subcommands | The dispatcher is one level deep by construction |
| More than about six commands | Usage rendering and flag groups stop being trivial |
| Wanting shell completions | Never worth hand-writing |
| Three or more flags shared by every command | Two is a helper function; more is a system |

If it happens, **kong** is the closer fit than cobra: its dependency injection
would take `Host` directly, where cobra's `*Command`-with-state model works
against the parameter-not-receiver decision. The migration is contained —
`Run(ctx, host, args)` bodies are framework-agnostic, and only `cmd.go` plus
three two-line `Flags` methods mention pflag.

### Notes

**Official schema validation.** `buildkite/pipeline-schema` is official
(Buildkite's own org) and actively maintained, so consuming it is viable rather
than a maintenance trap — we would not author or track a schema. Deferred only
to keep a JSON-schema library dependency out of v1.

**Comment preservation.** Likely requires merging at the `yaml.Node` level,
which carries `HeadComment`/`LineComment`/`FootComment` and round-trips them.
That is meaningfully more code than map merging, and RFC 7396 is defined over
plain JSON-ish data. So this is a possible refactor of the merge layer, not a
cheap template function. Recording it honestly.

**Agent functions.** Design decided ahead of implementation, so the seam is
right the first time.

Template functions call the agent **lazily during render**, consul-template
style, rather than resolving into `.Agent.*` in a stage before rendering. Two
alternatives were considered and rejected:

- **Declared prerequisites** (a bundle lists the keys it needs, fetched before
  render). Rejected: declaring a dependency separately from using it means the
  two drift, and it over-fetches — every declared key is pulled even on a branch
  that never runs.
- **Two-pass discovery** (render with a recording stub, fetch, render for real).
  This is what consul-template actually does internally, but it exists there to
  serve a long-lived process watching for change. Pipefitter runs once and
  exits. It also has a sharp edge: when a conditional depends on agent data,
  pass one may not discover keys inside the branch it did not take, so it needs
  loop-until-stable. Real complexity for no gain here.

Lazy calls mean `render` is no longer pure in the strict sense, but the property
that actually matters is preserved: the client is **injected**, not ambient, so
`Render` stays deterministic given its inputs and tests pass a fake.

Two requirements on the implementation:

1. **Memoize.** A template referencing the same meta-data key from three steps
   is the normal case, not an edge case. The wrapper caches per key, so repeated
   lookups hit the agent once. This recovers the main efficiency argument that
   favored prefetching.
2. **Record.** Keep a log of every key fetched and its value, so agent data gets
   the same introspection story as values provenance — you can see exactly what
   was pulled and what it resolved to.

Rendering gains a new failure mode: agent unreachable. **Errors must name the
key**, not just the transport, or debugging is miserable. Outside CI the agent
is genuinely absent, so the default implementation fails with guidance — which
is what answer files address.

The interface and its no-op implementation land in v1 even though no function
uses them yet, because adding a parameter later churns every call site:

```go
type AgentClient interface {
	MetaData(ctx context.Context, key string) (string, bool, error)
}
```

## New dependencies

| Dependency                         | Purpose         | Notes                                  |
| ---------------------------------- | --------------- | -------------------------------------- |
| `hashicorp/go-getter` v1           | source fetching | large dep tree; CVE history; allowlist |
| `Masterminds/sprig/v3`             | template funcs  | exclusions above                       |
| `goccy/go-yaml`                    | parse/serialize | see below                              |
| `rogpeppe/go-internal` (test-only) | CLI testing     | `testscript`                           |

**YAML library: `goccy/go-yaml`** (v1.19.2). The deciding factor is that
`go-yaml/yaml` — the v3 package most Go code uses — was **archived in April
2025**. `goccy/go-yaml` is actively maintained, and it independently happens to
have the better key-ordering and comment support, which is exactly what the
deferred comment-preservation work needs.
