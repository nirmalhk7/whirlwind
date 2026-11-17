package workflow

import (
	"errors"
	"fmt"
	"reflect"
	"strings"

	"whirlwind/internal/model"
	"whirlwind/internal/store"
	"whirlwind/internal/verification"
)

// Revalidate the complete runner handoff at the Reviewer boundary, including
// candidate tree and the commands selected from the frozen human configuration.
func (step *taskStep) validateReviewRunnerReport(commit string) error {
	_, err := step.validatedReviewRunnerReport(commit)
	return err
}

func (step *taskStep) validatedReviewRunnerReport(commit string) (verification.Report, error) {
	r, err := step.Store.Get(step.ctx, "verification", step.task.VerificationID)
	if err != nil {
		return verification.Report{}, fmt.Errorf("complete runner report unavailable: %w", err)
	}
	saved, err := store.Decode[struct {
		Stage      string                   `json:"stage"`
		Provenance model.Provenance         `json:"provenance"`
		Manifest   verification.JobManifest `json:"runner_manifest"`
		Report     verification.JobReport   `json:"runner_report"`
	}](r)
	if err != nil {
		return verification.Report{}, fmt.Errorf("invalid runner report: %w", err)
	}
	m := saved.Manifest
	if saved.Stage != "complete" || m.JobID != r.ID || m.PlanID != step.task.ID || m.PlanVersion != step.task.PlanVersion || m.PlanHash != step.task.PlanDocument.ApprovedHash || m.ConfigurationHash != verificationConfigurationHash(step.task) || m.CandidateCommit != commit || m.BaseCommit != step.task.Base || saved.Provenance.Task != step.task.ID || saved.Provenance.PlanVersion != step.task.PlanVersion || saved.Provenance.Head != commit || saved.Provenance.Base != step.task.Base || saved.Provenance.PolicyHash != step.project.PolicyHash {
		return verification.Report{}, errors.New("runner report does not match the complete approved candidate handoff")
	}
	tree, err := submissionGitRun(step.ctx, step.task.Worktree, "rev-parse", commit+"^{tree}")
	if err != nil || tree != m.CandidateTree {
		return verification.Report{}, errors.New("runner report candidate tree mismatch")
	}
	if err := saved.Report.Validate(m); err != nil {
		return verification.Report{}, fmt.Errorf("invalid complete runner report: %w", err)
	}

	files, patch, err := checkedDiff(step.ctx, step.project.Policy, &step.task)
	if err != nil {
		return verification.Report{}, fmt.Errorf("runner report scope: %w", err)
	}
	_, checks := verification.Select(step.project.Policy, strings.Fields(files), patch)
	if len(m.Commands) != len(checks) {
		return verification.Report{}, errors.New("runner report omits configured commands")
	}
	for i, check := range checks {
		timeout := check.TimeoutSeconds
		if timeout <= 0 {
			timeout = 600
		}
		want := verification.JobCommand{Name: check.Name, Mode: check.Mode, Command: check.Command, TimeoutSeconds: timeout, DependsOn: check.DependsOn, Required: check.Required, OutputPaths: check.OutputPaths, ReportFormat: check.ReportFormat, Environment: check.Environment}
		// JSON equivalence treats empty and omitted slices identically, as the
		// persisted manifest does; command argv/order remain exact.
		if !reflect.DeepEqual(normalizeJobCommand(want), normalizeJobCommand(m.Commands[i])) {
			return verification.Report{}, errors.New("runner report command differs from approved configuration")
		}
	}
	return step.qualifiedRunnerReport(m, saved.Report, checks)
}

func normalizeJobCommand(command verification.JobCommand) verification.JobCommand {
	if len(command.DependsOn) == 0 {
		command.DependsOn = nil
	}
	if len(command.OutputPaths) == 0 {
		command.OutputPaths = nil
	}
	if len(command.Environment) == 0 {
		command.Environment = nil
	}
	return command
}
