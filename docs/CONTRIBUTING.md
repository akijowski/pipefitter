# Contributing to Pipefitter

Everything a developer needs. For using pipefitter, see the [README](../README.md).

## Prerequisites

- [Go](https://go.dev/dl/) 1.26 or later
- [Just](https://just.systems/man/en/) for the build and test targets

### Optional: Nix environment

For a reproducible environment with pinned dependencies:

- [Nix](https://nixos.org/download.html)
- [direnv](https://direnv.net/)

```bash
eval "$(direnv hook bash)"  # or zsh, fish, etc.
direnv allow               # from the repository root
```

The devenv environment provides a pinned Go toolchain, `golangci-lint`, `gopls`,
and pre-commit hooks (`golangci-lint`, `gotest`, `govet`, `shellcheck`).

## Build and run

```bash
just build   # compile via a GoReleaser snapshot, into dist/
just run     # go run .
just clean   # remove build artifacts
```

## Testing

```bash
just test              # every test
just unit-test         # everything except the CLI scripts
just test-race         # every test under the race detector, as CI does
just update-scripts    # regenerate the CLI scripts' golden output
```

All three test targets pass extra flags through to `go test`:

```bash
just test -v -count=1
just unit-test -run TestMerge -v
```

### Why `unit-test` exists

`testdata/script/*.txtar` drives the real binary through
[testscript](https://pkg.go.dev/github.com/rogpeppe/go-internal/testscript).
Under `-v` those tests log every script line, which drowns out everything else —
about 22KB of trace. `unit-test` skips them with `-skip TestScripts`.

They stay silent unless they fail, so plain `just test` is unaffected. When one
does fail, the full transcript is exactly what you want.

### The two test layers

They cover deliberately different things and should not overlap:

| Layer | Covers |
| --- | --- |
| Package tests | Logic, driven with `fstest.MapFS` and plain maps. No process state, all parallel. |
| `testscript` | What `Host` abstracts away: `OSHost`, the real working directory, real environment variables, process exit status, stdout and stderr as real streams. |

`internal/cmd` tests reach `Run` — the dispatcher — by constructing a `Host` over
an in-memory filesystem. Every bug found in that package has lived in code that
built its inputs inline, where no test could reach it, so prefer injecting over
reaching for `os`.

### Golden output

The `.txtar` files embed expected output. It is generated, not predicted, since
the serialized form is the YAML library's to decide:

```bash
just update-scripts
```

Review the diff before committing — that is the point at which an unintended
change to the output becomes visible.

## Linting

```bash
just lint
```

`.golangci.yml` pins the linter set so a local run and CI agree; devenv does not
pin a `golangci-lint` version and CI pins one explicitly in the workflow. Keep
the two in step when bumping.

The config excludes `errcheck` for the `fmt.Fprint` family. A failed write of
usage text has nowhere to be reported and nothing to be done about it. Writes
carrying the pipeline document stay checked — a truncated pipeline must not look
like a successful one.

## Design documents

Decisions and their reasoning live in `docs/superpowers/`:

- [The CLI contract and architecture](superpowers/specs/2026-08-24-pipefitter-contract-design.md),
  including the deferred work and why each item was deferred
- [The MVP implementation plan](superpowers/plans/2026-08-24-pipefitter-mvp.md)

Worth reading before changing behaviour: several decisions that look arbitrary
have a recorded reason, and a few record measurements rather than assumptions.

## CI

Three independent jobs, so one failure does not mask another.

### `ci.yml` (push and pull requests to `main`)

- **Test** — `gofmt -s` check, a `go mod tidy` check, `go vet`, then `just test-race`
- **Lint** — `golangci-lint` at the version pinned in the workflow
- **Build** — a GoReleaser snapshot build

The Test job runs `just test-race`, the same command you run locally, so the two
cannot drift. `just` is installed with a commit-pinned action rather than a
`curl` of a release tarball — an earlier version of this workflow used the
latter, and it silently installed nothing while reporting success.

Lint and build use their official actions directly, since neither tool is on the
runner and both actions handle their own installation.

Go's version comes from `go-version-file: go.mod`, so CI cannot drift from the
module.

### `release.yml` (on a `v*` tag)

- **Validate** — `goreleaser check`
- **Release** — builds Linux and Darwin binaries and publishes a GitHub release

### Pinned actions

Every action is pinned to a commit for supply-chain safety:

- `actions/checkout@11d5960a` (v4)
- `actions/setup-go@40f1582b` (v5)
- `golangci/golangci-lint-action@ba0d7d2e` (v9.3.0)
- `goreleaser/goreleaser-action@f06c13b6` (v7.2.3)
- `taiki-e/install-action@37f7c578` (v2.87.0)

When adding one, resolve the tag to a **commit** SHA. Annotated tags point at a
tag object whose SHA is not a commit, and a workflow pinned to it fails to
resolve — that has happened in this repository before.

## Releasing

```bash
just build                      # snapshot, to check the config
git tag -a v1.0.0 -m "Release v1.0.0"
git push origin v1.0.0          # triggers release.yml
```

The version is injected at link time into `internal/cmd.version`; a plain
`go build` falls back to the module's build info, so `pipefitter version` works
either way.

## Code style

- Follow [Effective Go](https://go.dev/doc/effective_go)
- Run `just lint` and `just test` before committing
- Document exported symbols, and record *why* where the reason is not obvious
  from the code

Ambient process state — the filesystem, the environment, the output streams —
arrives as a `cmd.Host` parameter rather than being reached for directly, and
never as a method receiver. Receivers accumulate state until the struct is a god
object; as a parameter every dependency stays visible in the signature.
