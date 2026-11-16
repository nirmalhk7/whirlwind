package workflow

import (
	"context"
	"fmt"
	"path/filepath"

	"whirlwind/internal/model"
	"whirlwind/internal/verification"
)

func (e *Engine) validateQltyEvidence(ctx context.Context, task model.ExecutionPlan, report verification.Report) error {
	foundCheck := false
	for index := range report.Results {
		result := &report.Results[index]
		if !isQltyVerification(result) {
			continue
		}
		foundCheck = foundCheck || result.Command[1] == "check"
		if err := e.loadQltyArtifact(ctx, task, result); err != nil {
			return err
		}
		if err := verification.ValidateQltyResult(*result); err != nil {
			return fmt.Errorf("verification result %q has invalid Qlty evidence: %w", result.Name, err)
		}
	}
	if !foundCheck {
		return fmt.Errorf("mandatory Qlty check evidence is required before accepting agent code; configure the mandatory Qlty command and run verification")
	}
	return nil
}

func isQltyVerification(result *verification.Result) bool {
	return isQltyCommand(result.Command)
}

func isQltyCommand(command []string) bool {
	return len(command) > 1 && filepath.Base(command[0]) == "qlty" &&
		(command[1] == "check" || command[1] == "smells")
}

func (e *Engine) loadQltyArtifact(ctx context.Context, task model.ExecutionPlan, result *verification.Result) error {
	if result.OutputArtifact == "" {
		return fmt.Errorf("verification result %q lacks its full Qlty output artifact", result.Name)
	}
	artifact, output, err := e.Store.Artifact(ctx, result.OutputArtifact)
	if err != nil {
		return fmt.Errorf("verification result %q Qlty artifact is unavailable: %w", result.Name, err)
	}
	if !qltyArtifactBelongsToRun(artifact, task, *result) || !qltyArtifactComplete(artifact, output, *result) {
		return fmt.Errorf("verification result %q Qlty artifact is incomplete or belongs to another run", result.Name)
	}
	result.Output = string(output)
	return nil
}

func qltyArtifactBelongsToRun(artifact model.Artifact, task model.ExecutionPlan, result verification.Result) bool {
	wantOwner, wantID, wantKind := expectedQltyArtifactOwner(task, result)
	return artifact.OwnerKind == wantOwner && artifact.AttemptID == wantID && artifact.Kind == wantKind
}

func expectedQltyArtifactOwner(task model.ExecutionPlan, result verification.Result) (string, string, string) {
	if result.ReusedFrom != "" {
		return "verification", result.ReusedFrom, "cached_check:" + result.Name
	}
	if task.LastAttemptID != "" {
		return "sessions", task.LastAttemptID, "check:" + result.Name
	}
	return "verification", task.VerificationID, "check:" + result.Name
}

func qltyArtifactComplete(artifact model.Artifact, output []byte, result verification.Result) bool {
	return qltyArtifactContentComplete(artifact, result) && qltyArtifactSizeMatches(artifact, output)
}

func qltyArtifactContentComplete(artifact model.Artifact, result verification.Result) bool {
	return artifact.ContentType == "text/plain" && !artifact.Truncated && !result.Truncated
}

func qltyArtifactSizeMatches(artifact model.Artifact, output []byte) bool {
	return len(output) == artifact.Bytes && len(output) == artifact.StoredBytes
}
