package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"whirlwind/internal/model"
	"whirlwind/internal/store"
	"whirlwind/internal/verification"
)

type runtimeProofInput struct {
	task       model.Task
	provenance model.Provenance
	check      model.Check
	result     verification.Result
}

type runtimeCheckProof struct {
	job            runtimeCheckJob
	input          runtimeProofInput
	identity       verification.RuntimeIdentity
	verificationID string
}

func (e *Engine) validateApprovedCheckRuntime(ctx context.Context, input runtimeProofInput) error {
	proof, err := e.loadRuntimeCheckProof(ctx, input)
	if err != nil {
		return err
	}
	if !runtimeJobMatchesResult(proof) {
		return fmt.Errorf("verification check %q runtime proof does not match its persisted execution", input.check.Name)
	}
	return nil
}

func (e *Engine) loadRuntimeCheckProof(ctx context.Context, input runtimeProofInput) (runtimeCheckProof, error) {
	identity := input.result.Runtime
	if !runtimeIdentityComplete(identity) {
		return runtimeCheckProof{}, fmt.Errorf("verification check %q lacks a complete, cleaned sandbox runtime proof", input.check.Name)
	}
	job, err := e.loadRuntimeCheckJob(ctx, input.check, identity)
	if err != nil {
		return runtimeCheckProof{}, err
	}
	if err := e.validateReusedCheckIfPresent(ctx, input); err != nil {
		return runtimeCheckProof{}, err
	}
	return runtimeCheckProof{job: job, input: input, identity: identity, verificationID: runtimeVerificationID(input)}, nil
}

func runtimeIdentityComplete(identity verification.RuntimeIdentity) bool {
	return runtimeIdentityExecutable(identity) && runtimeIdentitySandboxed(identity) && runtimeIdentityClean(identity)
}

func runtimeIdentityExecutable(identity verification.RuntimeIdentity) bool {
	return hasOpenShellKind(identity) && hasLaunchIdentity(identity) && hasExecutionIdentity(identity)
}

func hasOpenShellKind(identity verification.RuntimeIdentity) bool {
	return identity.Kind == "openshell" || identity.Kind == "nono"
}

func hasLaunchIdentity(identity verification.RuntimeIdentity) bool { return identity.Launcher != "" }

func hasExecutionIdentity(identity verification.RuntimeIdentity) bool {
	return identity.ExecutionID != "" && identity.Generation > 0
}

func runtimeIdentitySandboxed(identity verification.RuntimeIdentity) bool {
	if identity.Kind == "nono" {
		return hasSandboxIdentity(identity) && identity.LifecycleFile != ""
	}
	return hasSandboxIdentity(identity) && hasGatewayIdentity(identity) && hasLifecycleIdentity(identity)
}

func hasSandboxIdentity(identity verification.RuntimeIdentity) bool {
	return identity.SandboxName != "" && identity.SandboxID != ""
}

func hasGatewayIdentity(identity verification.RuntimeIdentity) bool {
	return identity.GatewayName != "" && identity.GatewayEndpoint != ""
}

func hasLifecycleIdentity(identity verification.RuntimeIdentity) bool {
	return identity.Workspace != "" && identity.LifecycleFile != ""
}

func runtimeIdentityClean(identity verification.RuntimeIdentity) bool {
	return identity.CleanupVerified
}

func (e *Engine) loadRuntimeCheckJob(ctx context.Context, check model.Check, identity verification.RuntimeIdentity) (runtimeCheckJob, error) {
	record, err := e.Store.Get(ctx, "runtime_jobs", identity.ExecutionID)
	if err != nil {
		return runtimeCheckJob{}, fmt.Errorf("verification check %q runtime proof is not persisted: %w", check.Name, err)
	}
	job, err := store.Decode[runtimeCheckJob](record)
	if err != nil {
		return runtimeCheckJob{}, fmt.Errorf("verification check %q runtime proof is invalid: %w", check.Name, err)
	}
	return job, nil
}

func (e *Engine) validateReusedCheckIfPresent(ctx context.Context, input runtimeProofInput) error {
	if input.result.ReusedFrom == "" {
		return nil
	}
	return e.validateReusedCheck(ctx, input.task, input.provenance, input.check, input.result)
}

func runtimeVerificationID(input runtimeProofInput) string {
	if input.result.ReusedFrom != "" {
		return input.result.ReusedFrom
	}
	return input.task.VerificationID
}

func (e *Engine) validateReusedCheck(ctx context.Context, task model.Task, provenance model.Provenance, check model.Check, reused verification.Result) error {
	record, err := e.Store.Get(ctx, "verification", reused.ReusedFrom)
	if err != nil {
		return fmt.Errorf("reused verification %q is unavailable: %w", check.Name, err)
	}
	var source struct {
		Stage      string              `json:"stage"`
		Report     verification.Report `json:"report"`
		Provenance model.Provenance    `json:"provenance"`
	}
	if err := json.Unmarshal(record.Data, &source); err != nil {
		return fmt.Errorf("reused verification %q is invalid: %w", check.Name, err)
	}
	if !reusedProvenanceMatches(source.Stage, source.Provenance, provenance, task, reused.ReusedFrom) || !source.Report.Passed {
		return fmt.Errorf("reused verification %q is not bound to the current candidate and policy", check.Name)
	}
	if !sourceResultMatches(source.Report, check, reused) {
		return fmt.Errorf("reused verification %q does not contain the matching passing result", check.Name)
	}
	return nil
}

func reusedProvenanceMatches(stage string, source, current model.Provenance, task model.Task, reusedID string) bool {
	return reusedRecordIdentityMatches(stage, source, task, reusedID) &&
		reusedCandidateMatches(source, current) && source.PolicyHash == current.PolicyHash
}

func reusedRecordIdentityMatches(stage string, source model.Provenance, task model.Task, reusedID string) bool {
	return reusedVerificationMatches(stage, source, reusedID) && reusedTaskMatches(source, task)
}

func reusedVerificationMatches(stage string, source model.Provenance, reusedID string) bool {
	return stage == "complete" && source.VerificationID == reusedID
}

func reusedTaskMatches(source model.Provenance, task model.Task) bool {
	return source.Task == task.ID && source.PlanVersion == task.PlanVersion
}

func reusedCandidateMatches(source, current model.Provenance) bool {
	return reusedPlanMatches(source, current) && reusedRevisionMatches(source, current)
}

func reusedPlanMatches(source, current model.Provenance) bool {
	return source.PlanVersion == current.PlanVersion
}

func reusedRevisionMatches(source, current model.Provenance) bool {
	return source.Head == current.Head && source.Base == current.Base
}

func sourceResultMatches(report verification.Report, check model.Check, reused verification.Result) bool {
	matches := 0
	for _, result := range report.Results {
		if result.Name != check.Name {
			continue
		}
		matches++
		if !sameReusedResult(result, reused) {
			return false
		}
	}
	return matches == 1 && reused.Passed && reused.ExitCode != nil && *reused.ExitCode == 0
}

func sameReusedResult(source, reused verification.Result) bool {
	return reusedExecutionMatches(source, reused) && reusedOutputMatches(source, reused) && reusedRuntimeMatches(source, reused)
}

func reusedExecutionMatches(source, reused verification.Result) bool {
	return source.Passed == reused.Passed && reflect.DeepEqual(source.Command, reused.Command) &&
		reflect.DeepEqual(source.ExitCode, reused.ExitCode)
}

func reusedOutputMatches(source, reused verification.Result) bool {
	return source.Output == reused.Output && source.Truncated == reused.Truncated
}

func reusedRuntimeMatches(source, reused verification.Result) bool {
	return reflect.DeepEqual(source.Runtime, reused.Runtime)
}

func runtimeJobMatchesResult(proof runtimeCheckProof) bool {
	if !runtimeJobOutcomeMatches(proof.job, proof.input.check, proof.input.result) {
		return false
	}
	if !runtimeJobExecutionMatches(proof.job, proof.identity) || !runtimeJobOwnerMatches(proof.job, proof.input.task, proof, proof.input.check) {
		return false
	}
	return runtimeJobSandboxMatches(proof.job, proof.identity) && runtimeJobGatewayMatches(proof.job, proof.identity)
}

func runtimeJobExecutionMatches(job runtimeCheckJob, identity verification.RuntimeIdentity) bool {
	return job.ExecutionID == identity.ExecutionID && job.ExecutionGeneration == identity.Generation && job.LifecycleFile == identity.LifecycleFile
}

func runtimeJobOwnerMatches(job runtimeCheckJob, task model.Task, proof runtimeCheckProof, check model.Check) bool {
	return job.TaskID == task.ID && job.VerificationID == proof.verificationID && job.Role == "verify" && job.CheckID == check.Name
}

func runtimeJobSandboxMatches(job runtimeCheckJob, identity verification.RuntimeIdentity) bool {
	return job.SandboxName == identity.SandboxName && job.SandboxID == identity.SandboxID && job.Workspace == identity.Workspace
}

func runtimeJobGatewayMatches(job runtimeCheckJob, identity verification.RuntimeIdentity) bool {
	return job.GatewayName == identity.GatewayName && job.GatewayEndpoint == identity.GatewayEndpoint
}

func runtimeJobOutcomeMatches(job runtimeCheckJob, check model.Check, result verification.Result) bool {
	if job.Stage != "complete" || !job.CleanupVerified {
		return false
	}
	if isQltyCommand(check.Command) && result.ExitCode != nil && *result.ExitCode == 1 {
		return job.Error == "exit status 1"
	}
	if isQltyCommand(check.Command) && result.ExitCode != nil && *result.ExitCode == 0 && verification.AcceptedQltyBaselineAdvisory(result) {
		const advisory = "; Baseline comparison accepted preexisting findings as advisory; raw process evidence retained"
		rawError, _, found := strings.Cut(result.Error, advisory)
		return found && rawError == job.Error
	}
	return job.Error == ""
}
