# Pipefitter

Generate Buildkite pipelines from shared templates.

Pipefitter renders Go templates into a Buildkite pipeline on stdout, so a repository's checked-in `pipeline.yml` can be
two lines:

```yaml
steps:
  - command: pipefitter generate | buildkite-agent pipeline upload
```

Templates live in **bundles** — directories that pipefitter reads, not embedded into the binary. A bundle can be
shared across repositories, with each one supplying its own values.

## Install

Download a binary from [releases](../../releases), or:

```bash
go install github.com/akijowski/pipefitter@latest
```

## Quick start

Create a bundle at `.buildkite/pipefitter/`:

```
.buildkite/pipefitter/
├── values.yaml
└── test.tmpl
```

```yaml
# values.yaml
goVersion: "1.26"
queue: default
```

```yaml
# test.tmpl
agents:
  queue: "{{ .Values.queue }}"
steps:
  - key: test
    label: ":go: test"
    command: go test ./...
    branches: "{{ .Env.Buildkite.Branch }}"
```

Then:

```console
$ BUILDKITE_BRANCH=main pipefitter generate
agents:
  queue: default
steps:
  - branches: main
    command: go test ./...
    key: test
    label: ":go: test"
```

### Try it without writing anything

A runnable bundle ships with the repository:

```bash
git clone https://github.com/akijowski/pipefitter && cd pipefitter
pipefitter generate examples/simple
```

See [examples/](examples/) for what else to try against it.

## Bundles

A bundle is a directory containing an optional `values.yaml` and one or more templates:

```
.buildkite/pipefitter/
├── values.yaml        # this bundle's defaults, and its declared interface
├── _helpers.tpl       # parsed for {{ define }}, never emitted on its own
├── test.tmpl          # renders one YAML document
└── deploy.tmpl        # renders another
```

Files are classified by name:

| Name              | Treated as                                                         |
|-------------------|--------------------------------------------------------------------|
| starts with `_`   | a helper — parsed for `{{ define }}`, emits nothing, any extension |
| ends with `.tmpl` | a template — renders one document                                  |
| anything else     | ignored, including `values.yaml`, `README.md`, `LICENSE`           |

Templates are opt-in by extension so a stray file in a bundle can never end up in your pipeline. Subdirectories are
ignored; symlinks to files are followed.

Every template in a bundle renders, and the resulting documents are merged.

### Several bundles

Pass more than one, and they merge in the order given:

```bash
pipefitter generate shared-bundle .buildkite/pipefitter
```

Each bundle keeps its **own** defaults. One bundle's `values.yaml` is never visible while another renders, so upgrading
a shared bundle cannot silently change an unrelated one through a colliding key name.

## Commands

```
pipefitter generate [flags] [bundle-dir...]   # pipeline YAML on stdout
pipefitter validate [flags] [bundle-dir...]   # check without emitting
pipefitter version
```

With no bundle argument, `.buildkite/pipefitter` is used.

| Flag             | Meaning                                                                              |
|------------------|--------------------------------------------------------------------------------------|
| `--values`, `-f` | a values file layered over each bundle's defaults; repeatable, applied left to right |
| `--log-file`     | also write everything sent to stderr to this file; truncated per run                 |

### Exit codes

| Code | Meaning                                                                                           |
|------|---------------------------------------------------------------------------------------------------|
| 0    | success, including `--help`                                                                       |
| 1    | the run failed — a bundle could not be read, a template did not render, validation found problems |
| 2    | the invocation was wrong — unknown command, unknown flag                                          |

**stdout carries a pipeline and nothing else.** Usage, diagnostics and findings all go to stderr, and on any non-zero
exit stdout is empty rather than holding a partial document. That is what makes piping to
`buildkite-agent pipeline upload`
safe without a temporary file.

### validate

`validate` runs everything `generate` does except serialization, so you can check a pipeline without producing one:

```console
$ pipefitter validate
depends_on: deploy declares a dependency on missing step with key "nope"
pipefitter: pipeline is not valid
$ echo $?
1
```

`generate` fails closed on the same problems — a pipeline that does not validate is never emitted.

Two checks run today: a step key used twice across bundles, and a `depends_on`
naming a step that does not exist. Duplicate keys are reported with both template paths, which Buildkite cannot tell
you.

> **Known gap:** duplicate detection only compares top-level steps. A key
> duplicated between a step inside a `group` and one outside it is not caught
> here, and Buildkite rejects it on upload instead. Dependency checking *does*
> see inside groups.

## Values

Values reach templates as `.Values`. Each bundle builds its own, lowest to highest:

```
the bundle's own values.yaml   ←   -f base.yaml   ←   -f prod.yaml
```

A bundle's `values.yaml` is always the base and is never disabled by `--values`. The `-f` files are shared by every
bundle in one invocation; each bundle starts its own chain from its own defaults.

```bash
pipefitter generate --values base.yaml --values prod.yaml
```

### Merge semantics

Layering follows [RFC 7396](https://www.rfc-editor.org/rfc/rfc7396) (JSON Merge Patch):

- mappings merge recursively
- **sequences replace wholesale** — there is no appending to a list
- **`null` deletes the key**

Given a bundle default and an override:

```yaml
# the bundle's values.yaml
image:
  repo: acme
  tag: v1
plugins: [docker, artifacts]
```

```yaml
# -f override.yaml
image:
  tag: v2
plugins: [docker-login]
```

the result is:

```yaml
image:
  repo: acme        # untouched by the override
  tag: v2           # replaced
plugins:
  - docker-login    # the whole sequence replaced, not appended to
```

> **Watch out:** an empty YAML value *is* null, so `queue:` on its own **deletes**
> the key rather than declaring it. So do `queue: ~` and `queue: null`. To
> declare a key with no useful default, give it an empty string: `queue: ""`.

## Templates

Templates are [Go text/template](https://pkg.go.dev/text/template) with
[sprig](https://masterminds.github.io/sprig/) functions available. Two namespaces are in scope, and nothing else:

### `.Values`

The merged configuration for the bundle being rendered.

### `.Env`

```
.Env.Buildkite.Branch          .Env.Buildkite.BuildNumber
.Env.Buildkite.Commit          .Env.Buildkite.BuildURL
.Env.Buildkite.Tag             .Env.Buildkite.Source
.Env.Buildkite.Message         .Env.Buildkite.RetryCount
.Env.Buildkite.PipelineSlug    .Env.Buildkite.Organization

.Env.Buildkite.PullRequest.IsPR         .Env.Buildkite.PullRequest.BaseBranch
.Env.Buildkite.PullRequest.Number       .Env.Buildkite.PullRequest.Repo

.Env.Vars.ANY_OTHER_VARIABLE
```

`.Env.Buildkite` is a curated, typed view; `.Env.Vars` is the whole environment for anything not modeled.

> **Use `PullRequest.IsPR`, never the raw variable.** Buildkite sets
> `BUILDKITE_PULL_REQUEST` to the literal string `"false"` on a non-PR build, and
> that string is *truthy* in a template. `IsPR` is a real boolean.

```
{{ if .Env.Buildkite.PullRequest.IsPR }}...{{ end }}
```

Because `.Env.Buildkite` is typed, a misspelled field is an error rather than an empty string.

### Functions

Sprig, minus five deliberately withheld:

| Withheld           | Why                                                                                    |
|--------------------|----------------------------------------------------------------------------------------|
| `env`, `expandenv` | they read the environment at run time, risking inconsistency; `.Env` is the one way in |
| `getHostByName`    | performs a DNS lookup mid-render                                                       |
| `uuidv4`, `rand*`  | non-deterministic — the same inputs must produce the same pipeline                     |

Using one is a parse error, not a silent surprise.

`include` is also available, for rendering a named template into a pipe:

```
{{ include "my-helper" . | indent 4 }}
```

### Missing keys

**Reading a key that does not exist is an error.** A bundle must declare every key its templates read.

```console
$ pipefitter generate
pipefitter: bundle ".buildkite/pipefitter/test.tmpl": unable to render template:
template execution failed: template: test.tmpl:3:22: executing "test.tmpl"
at <.Values.nope>: map has no entry for key "nope"
```

**Why.** Without this, Go's templating emits the literal string `<no value>` — which is *valid YAML*. So
`tag: {{ .Values.nope }}` would produce
`tag: <no value>` and ship a step with a real string of that name to Buildkite. A failed render is strictly better than
a silently wrong pipeline.

(The obvious middle ground does not exist: `missingkey=zero` still prints
`<no value>` for an untyped map, because the zero value of `any` is a nil interface.)

**What this means for `default`.** Sprig's `default` covers a key that is present but empty. It **cannot** cover an
absent one, because the map lookup happens before the pipe and fails first.

```yaml
# values.yaml — declaring the key, with no useful default
queue: ""
```

```
{{ .Values.queue | default "default-queue" }}   # → default-queue
{{ .Values.notDeclared | default "x" }}         # → error: no entry for key
```

Note `queue: ""` and not `queue:` — an empty YAML value is null, and null deletes the key.

The benefit is that a bundle's `values.yaml` is its **documented interface**:
every key a template reads appears there, so a reader can see what the bundle accepts without reading the templates.

## Output

Pipefitter parses and re-serializes rather than passing template text through, because merging several documents
requires structure. So the output is normalized:

- **mapping keys come out alphabetically**, not in the order a template wrote them
- **comments do not survive**
- **`yes`, `no`, `on`, `off` are quoted**, since YAML 1.1 would otherwise read them as booleans

The result is equivalent YAML, not identical text.

## Not yet supported

Deliberately out of scope for now, with reasoning recorded in
[the design document](docs/superpowers/specs/2026-08-24-pipefitter-contract-design.md):

- remote bundles (git, HTTP, S3) — bundles must be local paths today
- `pipefitter render`, to inspect a document without validating it
- `pipefitter values`, to show where each value came from
- `--output`, `--verbose`, and `--set`
- template functions backed by `buildkite-agent`
- validation against Buildkite's published schema

## Contributing

See [docs/CONTRIBUTING.md](docs/CONTRIBUTING.md) for building, testing, the CI setup, and the design documents.

## License

See [LICENSE.md](LICENSE.md).
