package verification

import (
	"fmt"
	"slices"

	"whirlwind/internal/model"
)

// AdvisoryFailureAllowed uses the approved command policy, never a flag in a
// reported result. Ordinary failures remain visible; incomplete execution,
// analyzer findings and pending source fixes cannot use this exception.
func AdvisoryFailureAllowed(check model.Check, result Result) bool {
	if check.Required == nil || *check.Required || check.ReportFormat == "sarif" || baselineQlty(result) || result.Autofix != nil {
		return false
	}
	if check.Name != result.Name || !slices.Equal(check.Command, result.Command) {
		return false
	}
	if result.Passed || result.Truncated || result.ExitCode == nil || *result.ExitCode <= 0 {
		return false
	}
	return result.Error == "" || result.Error == fmt.Sprintf("exit status %d", *result.ExitCode)
}

func advisoryFailureForChecks(checks []model.Check, result Result) bool {
	for _, check := range checks {
		if AdvisoryFailureAllowed(check, result) {
			return true
		}
	}
	return false
}
