package cmd

import (
	"context"

	"github.com/spf13/pflag"
)

// validateCmd implements:
//
//	pipefitter validate [flags] [bundle-dir...]
//
// It takes the same arguments and flags as generate and runs the same stages,
// stopping before serialization. Findings go to Host.ErrOut and it exits
// non-zero when there are any; nothing is ever written to Host.Out, because
// validate has no document to emit.
//
// It exists so "is my pipeline valid?" does not require generating one and
// discarding the output — and, since findings are reported against the merged
// document where source attribution is gone, so that a failing generate has a
// companion command to run.
type validateCmd struct {
	valuesFiles []string
}

func (v *validateCmd) Name() string { return "validate" }

func (v *validateCmd) Description() string { return "validate bundle sources for correctness" }

func (v *validateCmd) Flags(fs *pflag.FlagSet) {
	registerValuesFlag(fs, &v.valuesFiles)
}

func (v *validateCmd) Run(_ context.Context, host Host, args []string) error {
	checked, err := checkPipeline(host.FS, host.Environ, args, v.valuesFiles)
	if err != nil {
		return err
	}

	return emitFindings(host, checked.findings)
}
