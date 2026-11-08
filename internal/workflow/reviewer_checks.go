package workflow

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"whirlwind/internal/execution"
	"whirlwind/internal/github"
	"whirlwind/internal/model"
	"whirlwind/internal/process"
	"whirlwind/internal/provider"
	"whirlwind/internal/store"
	"whirlwind/internal/verification"
)

// runCheck accepts a committed ID only; reviewers never supply a command or cwd.
func (a *agentAttempt) runCheck(ctx context.Context, id string) (checkResult provider.CheckResult, resultErr error) {
	if a.input.Role == "code" {
		return a.runCodingCheck(ctx, id)
	}
	a.checkMu.Lock()
	defer a.checkMu.Unlock()
	check, current, head, err := a.reviewCheckIdentity(ctx, id)
	if err != nil {
		return provider.CheckResult{}, err
	}
	return a.executeReviewerCheck(ctx, id, check, current, head)
}

func (a *agentAttempt) reviewCheckIdentity(ctx context.Context, id string) (model.Check, model.Project, string, error) {
	if !a.input.ReadOnly || a.input.Role != "review" {
		return model.Check{}, model.Project{}, "", errors.New("check broker requires read-only reviewer role")
	}
	check, err := committedCheck(a.project.Policy, id)
	if err != nil {
		return model.Check{}, model.Project{}, "", err
	}
	current, head, err := a.reviewerProjectHead(ctx)
	if err != nil {
		return model.Check{}, model.Project{}, "", err
	}
	if err := a.validateReviewerEvidence(ctx, current, head); err != nil {
		return model.Check{}, model.Project{}, "", err
	}
	return check, current, head, nil
}

func (a *agentAttempt) reviewerProjectHead(ctx context.Context) (model.Project, string, error) {
	current, err := a.trustedProject(ctx, a.project.ID)
	if err != nil {
		return model.Project{}, "", err
	}
	if current.PolicyHash != a.project.PolicyHash {
		return model.Project{}, "", errors.New("check policy changed")
	}
	head, err := github.Run(ctx, a.input.CWD, "git", "rev-parse", "HEAD")
	if err != nil {
		return model.Project{}, "", err
	}
	return current, head, nil
}

func (a *agentAttempt) validateReviewerEvidence(ctx context.Context, current model.Project, head string) error {
	if a.task.VerificationID == "" {
		return errors.New("reviewed revision evidence required")
	}
	record, err := a.Store.Get(ctx, "verification", a.task.VerificationID)
	if err != nil {
		return err
	}
	proof, err := store.Decode[struct {
		Provenance model.Provenance `json:"provenance"`
		Stage      string           `json:"stage"`
	}](record)
	if err != nil || !reviewProofMatches(proof, current, head, a.task) {
		return errors.New("review check requires the exact verified review revision and policy")
	}
	return nil
}

func reviewProofMatches(proof struct {
	Provenance model.Provenance `json:"provenance"`
	Stage      string           `json:"stage"`
}, current model.Project, head string, task model.Task) bool {
	return proof.Stage == "complete" && reviewProofRevision(proof.Provenance, current, head) && reviewProofTask(proof.Provenance, task)
}

func reviewProofRevision(proof model.Provenance, current model.Project, head string) bool {
	return proof.Head == head && proof.PolicyHash == current.PolicyHash
}

func reviewProofTask(proof model.Provenance, task model.Task) bool {
	return proof.Task == task.ID && proof.PlanVersion == task.PlanVersion
}

func (a *agentAttempt) executeReviewerCheck(ctx context.Context, id string, check model.Check, current model.Project, head string) (checkResult provider.CheckResult, resultErr error) {
	scratch, err := os.MkdirTemp("", "whirlwind-review-check-")
	if err != nil {
		return provider.CheckResult{}, err
	}
	defer func() { resultErr = errors.Join(resultErr, os.RemoveAll(scratch)) }()
	snapshot := filepath.Join(scratch, "source")
	if err = os.Mkdir(snapshot, 0700); err != nil {
		return provider.CheckResult{}, err
	}
	if err = captureReviewSnapshot(ctx, a.input.CWD, head, snapshot); err != nil {
		return provider.CheckResult{}, err
	}
	result, effective, err := runSnapshotCheckOwned(ctx, a.Engine, scratch, snapshot, check, current, runtimeCheckOwner{TaskID: a.task.ID, AttemptID: a.id, VerificationID: a.task.VerificationID, Role: "review"})
	if err != nil {
		return provider.CheckResult{}, err
	}
	return a.persistReviewerResult(ctx, reviewerResultEvidence{id: id, current: current, head: head, effective: effective, result: result})
}

func captureReviewSnapshot(ctx context.Context, cwd, head, snapshot string) error {
	cmd := process.Command(ctx, "git", "-C", cwd, "archive", "--format=tar", head)
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	cmd.Stderr = &bytes.Buffer{}
	if err = process.Start(cmd); err != nil {
		return err
	}
	extractErr := extractSnapshot(snapshot, pipe)
	if extractErr != nil {
		_ = cmd.Cancel()
	} else {
		var drained int64
		drained, extractErr = io.Copy(io.Discard, io.LimitReader(pipe, 1024*1024+1))
		if drained > 1024*1024 {
			extractErr = errors.New("review archive trailing data exceeds limit")
			_ = cmd.Cancel()
		}
	}
	waitErr := process.Wait(cmd)
	if err = errors.Join(extractErr, waitErr); err != nil {
		return fmt.Errorf("review check snapshot: %w", err)
	}
	return nil
}

type reviewerResultEvidence struct {
	id        string
	current   model.Project
	head      string
	effective execution.Profile
	result    verification.Result
}

func (a *agentAttempt) persistReviewerResult(ctx context.Context, evidence reviewerResultEvidence) (provider.CheckResult, error) {
	id, current, head, effective, result := evidence.id, evidence.current, evidence.head, evidence.effective, evidence.result
	artifact, err := a.Store.Capture(ctx, a.id, "review_check:"+id, "text/plain", []byte(result.Output), store.MessagePayloadLimit, false)
	if err != nil {
		return provider.CheckResult{}, a.persistenceError(err)
	}
	output := provider.CheckResult{ExecutionProfile: effective.Digest(), CheckID: id, Head: head, Snapshot: head, Passed: result.Passed, ExitCode: result.ExitCode, Output: string(store.Prefix([]byte(result.Output), 4096)), OutputArtifact: artifact.ID, Truncated: result.Truncated || artifact.Truncated, DurationMS: result.DurationMS}
	if result.Error != "" {
		output.Output += "\n" + result.Error
	}
	value := map[string]any{"execution_profile": effective.Digest(), "task": a.task.ID, "attempt_id": a.id, "plan_version": a.task.PlanVersion, "policy_hash": current.PolicyHash, "head": head, "check_id": id, "result": result, "output_artifact": artifact.ID}
	if err = a.Store.Put(ctx, "review_checks", store.ID(), value, 0); err != nil {
		return output, a.persistenceError(err)
	}
	a.event("whirlwind/review_check", output)
	return output, nil
}

func committedCheck(policy model.Policy, id string) (model.Check, error) {
	if id == "" {
		return model.Check{}, errors.New("committed check ID required")
	}
	checks := append([]model.Check{}, policy.Checks...)
	for _, rule := range policy.RiskRules {
		checks = append(checks, rule.Checks...)
	}
	var found *model.Check
	for _, check := range checks {
		if check.Name != id {
			continue
		}
		if found != nil && !reflect.DeepEqual(*found, check) {
			return model.Check{}, errors.New("ambiguous committed check ID")
		}
		copy := check
		found = &copy
	}
	if found == nil {
		return model.Check{}, fmt.Errorf("unknown committed check ID: %s", id)
	}
	return *found, nil
}
