# Examples

Runnable bundles. Clone the repository and render one:

```bash
pipefitter generate examples/simple
```

No environment variables or flags are needed — each example renders standalone.

## `simple`

The smallest useful bundle: a `values.yaml` and one template, matching the Quick
start in the [README](../README.md).

```bash
pipefitter generate examples/simple            # render it
pipefitter validate examples/simple            # check it without rendering
```

Try these against it:

```bash
# Override a value. The bundle's own defaults still apply to keys you leave out.
printf 'queue: big\n' > /tmp/override.yaml
pipefitter generate examples/simple --values /tmp/override.yaml

# Values from the Buildkite environment reach templates through .Env.
BUILDKITE_BRANCH=release/v2 pipefitter generate examples/simple

# Reading a key the bundle does not declare is an error, naming the key.
printf 'steps:\n  - key: t\n    x: "{{ .Values.nope }}"\n' > examples/simple/broken.tmpl
pipefitter generate examples/simple
rm examples/simple/broken.tmpl
```
