package verification

import (
	"encoding/json"
	"errors"
	"fmt"
)

// Smell findings default to advisory output in Qlty. Treat the report itself as
// evidence: a successful process exit alone does not prove a clean project.
func qltySmellsError(output string) error {
	var report struct {
		Version string `json:"version"`
		Runs    []struct {
			Results     []json.RawMessage `json:"results"`
			Invocations []struct {
				ExecutionSuccessful *bool `json:"executionSuccessful"`
			} `json:"invocations"`
		} `json:"runs"`
	}
	if err := json.Unmarshal([]byte(output), &report); err != nil || report.Version != "2.1.0" || len(report.Runs) == 0 {
		return errors.New("Qlty smells requires a valid SARIF report; run with --sarif")
	}
	findings := 0
	for _, run := range report.Runs {
		for _, invocation := range run.Invocations {
			if invocation.ExecutionSuccessful != nil && !*invocation.ExecutionSuccessful {
				return errors.New("Qlty smells analysis did not complete successfully")
			}
		}
		findings += len(run.Results)
	}
	if findings > 0 {
		return fmt.Errorf("Qlty reported %d code smells; repair findings before review", findings)
	}
	return nil
}
