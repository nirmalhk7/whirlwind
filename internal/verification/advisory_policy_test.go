package verification

import (
	"testing"
	"whirlwind/internal/model"
)

func TestFrozenOptionalPolicyRetainsOrdinaryNonzeroExitEvidence(t *testing.T) {
	optional := false
	command := []string{"sh", "-c", "exit 7"}
	check := model.Check{Name: "advisory", Command: command, Required: &optional}
	zero, seven := 0, 7
	base := Result{Name: "advisory", Command: command, ExitCode: &zero, Passed: true}
	head := Result{Name: "advisory", Command: command, ExitCode: &seven, Error: "exit status 7"}
	report := CompareBaselineForChecks(Report{Passed: true, Results: []Result{base}}, Report{Results: []Result{head}}, []model.Check{check})
	if !report.Passed || report.Results[0].Passed || report.Results[0].Error != "exit status 7" || *report.Results[0].ExitCode != 7 {
		t.Fatalf("optional ordinary failure blocked or rewritten: %+v", report)
	}
}

func TestFrozenOptionalPolicyNeverWaivesIncompleteOrAnalyzerEvidence(t *testing.T) {
	optional := false
	check := model.Check{Name: "advisory", Command: []string{"sh", "-c", "exit 1"}, Required: &optional}
	exit := 1
	complete := Result{Name: check.Name, Command: check.Command, ExitCode: &exit, Error: "exit status 1"}
	for _, name := range []string{"default required", "explicit required", "missing exit", "signal", "truncated", "infrastructure error", "wrong argv", "sarif analyzer", "qlty", "pending fix"} {
		t.Run(name, func(t *testing.T) {
			policy, result := check, complete
			switch name {
			case "default required":
				policy.Required = nil
			case "explicit required":
				required := true
				policy.Required = &required
			case "missing exit":
				result.ExitCode = nil
			case "signal":
				signal := -1
				result.ExitCode = &signal
			case "truncated":
				result.Truncated = true
			case "infrastructure error":
				result.Error = "sandbox failed"
			case "wrong argv":
				result.Command = []string{"sh", "-c", "another command"}
			case "sarif analyzer":
				policy.ReportFormat = "sarif"
			case "qlty":
				policy.Command = []string{"qlty", "check"}
				result.Command = policy.Command
			case "pending fix":
				result.Autofix = &AutofixProposal{}
			default:
				t.Fatalf("unknown case %s", name)
			}
			if AdvisoryFailureAllowed(policy, result) {
				t.Fatal("optional flag waived missing or mandatory quality evidence")
			}
		})
	}
}
