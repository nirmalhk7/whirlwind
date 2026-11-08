package verification

import (
	"encoding/json"
	"errors"
	"strings"
)

const baselineAdvisoryMessage = "Baseline comparison accepted preexisting findings as advisory; raw process evidence retained"

type baselineAdvisoryComparison struct {
	Status              string `json:"status"`
	PreexistingFindings int    `json:"preexisting_findings"`
	BaselineFindings    int    `json:"baseline_findings"`
	RawExitCode         int    `json:"raw_exit_code"`
}

// AcceptedQltyBaselineAdvisory recognizes complete Qlty findings accepted by baseline comparison.
func AcceptedQltyBaselineAdvisory(result Result) bool {
	if !baselineAdvisoryCandidate(result) {
		return false
	}
	comparison, ok := baselineAdvisoryMetadata(result)
	if !ok {
		return false
	}
	findings, err := baselineSARIF(baselineAdvisoryOutput(result.Output))
	return err == nil && len(findings) == comparison.PreexistingFindings
}

func baselineAdvisoryCandidate(result Result) bool {
	if !baselineQlty(result) || result.Truncated || result.ExitCode == nil || !result.Passed {
		return false
	}
	if *result.ExitCode != 0 && *result.ExitCode != 1 {
		return false
	}
	return strings.Contains(result.Error, baselineAdvisoryMessage)
}

func baselineAdvisoryMetadata(result Result) (baselineAdvisoryComparison, bool) {
	start := strings.LastIndex(result.Output, "\n\n{")
	if start < 0 {
		return baselineAdvisoryComparison{}, false
	}
	var metadata struct {
		Comparison baselineAdvisoryComparison `json:"baseline_comparison"`
	}
	if json.Unmarshal([]byte(result.Output[start+2:]), &metadata) != nil {
		return baselineAdvisoryComparison{}, false
	}
	comparison := metadata.Comparison
	valid := comparison.Status == "preexisting_advisory" && comparison.PreexistingFindings > 0 &&
		comparison.PreexistingFindings <= comparison.BaselineFindings && comparison.RawExitCode == *result.ExitCode
	return comparison, valid
}

func baselineAdvisoryOutput(output string) string {
	return output[:strings.LastIndex(output, "\n\n{")]
}

// ValidateQltyResult proves clean SARIF or complete findings accepted by baseline comparison.
func ValidateQltyResult(result Result) error {
	if !baselineQlty(result) {
		return nil
	}
	if !completeQltyProcessEvidence(result) {
		return errors.New("qlty evidence is incomplete")
	}
	if AcceptedQltyBaselineAdvisory(result) {
		return nil
	}
	if !cleanQltyProcess(result) {
		return errors.New("qlty process did not complete successfully")
	}
	return validateCleanQltySARIF(result.Output)
}

func completeQltyProcessEvidence(result Result) bool {
	return !result.Truncated && result.ExitCode != nil
}

func cleanQltyProcess(result Result) bool {
	return *result.ExitCode == 0 && result.Passed && result.Error == ""
}

func validateCleanQltySARIF(output string) error {
	findings, err := baselineSARIF(output)
	if err != nil {
		return err
	}
	if len(findings) != 0 {
		return errors.New("qlty reported findings without an accepted baseline comparison")
	}
	return nil
}
