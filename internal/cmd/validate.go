package cmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	"github.com/spf13/pflag"

	"github.com/akijowski/pipefitter/internal/validate"
)

// errPipelineInvalid reports that validation found problems. The findings
// themselves have already been written to Host.ErrOut.
var errPipelineInvalid = errors.New("pipeline is not valid")

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

	if len(checked.findings) > 0 {
		b, err := marshalFindings(checked.findings)
		if err != nil {
			return err
		}
		if _, err = host.ErrOut.Write(b); err != nil {
			return err
		}

		// The findings are already on ErrOut, so the error only has to signal
		// failure — restating a count here would report the same problem twice
		// in different words.
		return errPipelineInvalid
	}

	return nil
}

// marshalFindings renders findings for a human reading stderr.
func marshalFindings(findings []validate.Finding) ([]byte, error) {
	var buf bytes.Buffer
	for _, finding := range findings {
		if _, err := fmt.Fprintf(&buf, "%s: %s\n", finding.Rule, finding.Message); err != nil {
			return nil, err
		}
	}

	return buf.Bytes(), nil
}
