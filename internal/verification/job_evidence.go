package verification

import (
	"errors"
)

func validateJobEvidence(format string, output []byte, truncated, passed bool) error {
	if format == "" || format == "text" {
		return nil
	}
	if truncated {
		return errors.New("structured command report exceeded retained output limit")
	}
	switch format {
	case "go-test-json":
		findings, err := baselineGoTests(string(output))
		if err != nil {
			return err
		}
		if passed && len(findings) > 0 {
			return errors.New("structured test failures contradict successful command exit")
		}
	case "sarif":
		return validateJobSARIFEvidence(output)
	default:
		return errors.New("unsupported command report format")
	}
	return nil
}

func validateJobSARIFEvidence(output []byte) error {
	_, err := baselineSARIF(string(output))
	return err
}
