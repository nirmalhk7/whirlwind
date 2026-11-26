package workflow

import (
	"context"
	"errors"
	"reflect"

	"whirlwind/internal/execution"
)

// Standalone jobs own their journal and lifecycle evidence. Never manufacture
// legacy in-process runtime rows to make a standalone result look accepted.
func (e *Engine) validateStandaloneCheckProof(ctx context.Context, input runtimeProofInput) error {
	project, err := e.Project(ctx, input.task.ProjectID)
	if err != nil {
		return err
	}
	step := taskStep{Engine: e, ctx: ctx, task: input.task, project: project}
	qualified, err := step.validatedReviewRunnerReport(input.provenance.Head)
	if err != nil {
		return err
	}
	for _, result := range qualified.Results {
		if result.Name != input.check.Name {
			continue
		}
		owned := runtimeIdentityComplete(result.Runtime)
		if project.Policy.OfflineDockerImage != "" {
			owned = result.Runtime.Kind == execution.OfflineDockerKind && result.Runtime.CleanupVerified &&
				execution.IsOfflineDockerIdentity(result.Runtime.SandboxID, result.Runtime.ImageID) &&
				result.ExecutionProfile != "" && result.ExecutionProfile == input.result.ExecutionProfile
		}
		if !owned || !reflect.DeepEqual(result.Runtime, input.result.Runtime) || !reflect.DeepEqual(result.Command, input.result.Command) || !reflect.DeepEqual(result.ExitCode, input.result.ExitCode) {
			return errors.New("standalone check proof does not match retained command result")
		}
		if result.Passed != input.result.Passed || result.Error != input.result.Error || result.Output != input.result.Output || result.Diagnostics != input.result.Diagnostics || result.Truncated != input.result.Truncated {
			return errors.New("standalone check outcome or content changed after verification")
		}
		return nil
	}
	return errors.New("standalone check missing from complete runner report")
}
