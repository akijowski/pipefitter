package cmd

import (
	"bytes"
	"errors"
	"fmt"

	"github.com/akijowski/pipefitter/internal/validate"
)

// errPipelineInvalid reports that validation found problems.
var errPipelineInvalid = errors.New("pipeline is not valid")

// emitFindings checks the findings and returns errPipelineInvalid after writing
// to Host.ErrOut. An empty slice of findings will return nil.
func emitFindings(host Host, findings []validate.Finding) error {
	if len(findings) == 0 {
		return nil
	}

	if _, err := host.ErrOut.Write(marshalFindings(findings)); err != nil {
		return err
	}

	// The findings are already on ErrOut, so the error only has to signal
	// failure — restating a count here would report the same problem twice
	// in different words.
	return errPipelineInvalid
}

// marshalFindings renders findings for a human reading stderr.
func marshalFindings(findings []validate.Finding) []byte {
	var buf bytes.Buffer
	for _, finding := range findings {
		fmt.Fprintf(&buf, "%s: %s\n", finding.Rule, finding.Message)
	}

	return buf.Bytes()
}
