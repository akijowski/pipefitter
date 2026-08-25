# Pipefitter - Buildkite Pipelines Made Easy

## Description

Pipefitter is a templating engine for Buildkite CI/CD pipelines. Define pipelines as Go templates with built-in Buildkite functions, then inject project-specific config to generate consistent YAML. Share one template across multiple projects instead of maintaining dozens of nearly-identical files — so your team ships faster with less maintenance overhead.

## Prerequisites

- [Go](https://go.dev/dl/) 1.26.5 or later
- [Just](https://just.systems/man/en/) (for build commands)

### Optional: Nix environment

For a fully reproducible development environment with pinned dependencies:

- [Nix](https://nixos.org/download.html)
- [direnv](https://direnv.net/)

## Getting Started

### Without Nix

1. Install Go 1.26.5 and Just
2. Clone the repository
3. Run `just run` to execute the CLI

### With Nix (devenv.sh)

1. Install Nix and direnv
2. Enable direnv in your shell:
   ```bash
   eval "$(direnv hook bash)"  # or zsh, fish, etc.
   ```
3. Clone the repository and enter the directory
4. Run `direnv allow` to load the devenv environment
5. Run `just run` to execute the CLI

The Nix environment provides:
- Pinned Go 1.26.5
- golangci-lint
- gopls (Go language server)
- Pre-commit hooks (golangci-lint, gotest, govet, shellcheck)

## Development

### Build and Run

```bash
just build   # Compile the binary
just run     # Run the CLI
just clean   # Remove build artifacts
```

### Testing

```bash
just test    # Run tests
```

### Linting

```bash
just lint    # Run golangci-lint
```

## CI

Two workflows run automatically:

### `ci.yml` (push/PR to main)

Three independent jobs, so a lint failure does not hide a test failure:

- **Test** — `gofmt -s` check, `go vet`, then `go test -race ./...`
- **Lint** — `golangci-lint` at the version pinned in the workflow, configured by
  `.golangci.yml`
- **Build** — GoReleaser snapshot build

CI calls the Go toolchain directly rather than going through `just`. The Justfile
stays the convenience layer for local development; CI uses the official pinned
actions for lint and build so there is no third tool to install and keep current.

Go's version comes from `go-version-file: go.mod`, so CI cannot drift from the
module's declared version.

### `release.yml` (on tag push `v*`)
- **Validate** — runs `goreleaser check`
- **Release** — builds binaries for Linux/Darwin and creates GitHub releases

Actions are pinned to commit hashes for supply-chain security:
- `actions/checkout@11d5960a` (v4)
- `actions/setup-go@40f1582b` (v5)
- `golangci/golangci-lint-action@ba0d7d2e` (v9.3.0)
- `goreleaser/goreleaser-action@f06c13b6` (v7.2.3)

## Releases

### Snapshot Build (for testing)

```bash
just build
# or explicitly:
goreleaser build --snapshot --clean
```

Skips validation, builds to `dist/`, uses `0.0.0-SNAPSHOT` version. No GitHub release created.

### Production Release

```bash
# Tag your release
git tag -a v1.0.0 -m "Release v1.0.0"
git push origin v1.0.0

# Or release manually:
goreleaser release --clean
```

Builds binaries for all platforms, creates archives, generates changelog, and publishes a GitHub release.

### Release Workflow

| Stage | Command | When to use |
|-------|---------|-------------|
| **Local dev** | `just build` | Quick iteration, testing builds |
| **CI validation** | `just build` | PR checks, config validation |
| **Pre-release testing** | `goreleaser release --snapshot` | Test full pipeline without publishing |
| **Production** | `git tag vX.Y.Z && git push origin vX.Y.Z` | Triggers `release.yml` workflow |

### Example Release Flow

```bash
# 1. Make changes
# 2. Test with snapshot
just build

# 3. If happy, tag and push
git tag -a v1.0.0 -m "First stable release"
git push origin main --tags

# 4. GitHub Actions triggers release.yml
# 5. Binaries published to GitHub Releases
```

### Best Practices

- Use annotated tags (`git tag -a`) for release metadata
- Run `goreleaser release --snapshot` first to test locally
- Keep `--clean` flag to avoid stale artifacts
- Filter changelog (already configured) to exclude noise
- Pin Go version in workflow (already done)

## Contributing

### Code Style

- Follow [Effective Go](https://go.dev/doc/effective_go) conventions
- Run `just lint` before committing
- Run `just test` to ensure all tests pass

### Pre-commit Hooks (Nix environment)

If using the Nix environment, pre-commit hooks run automatically:
- `golangci-lint` — lints Go code
- `gotest` — runs tests
- `govet` — checks for suspicious constructs
- `shellcheck` — lints shell scripts

To set up pre-commit hooks manually (outside Nix):
```bash
# Install pre-commit
pip install pre-commit

# Install hooks
pre-commit install
```

Note: The pre-commit configuration in this repo is managed by devenv.sh.
For non-Nix contributors, consider setting up equivalent hooks manually.

### Commit Messages

- Use clear, descriptive commit messages
- Reference issue numbers where applicable

## License

See [LICENSE.md](LICENSE.md)
