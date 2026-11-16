package workflow

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"go.yaml.in/yaml/v3"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"whirlwind/internal/execution"
	"whirlwind/internal/model"
	"whirlwind/internal/policy"
	"whirlwind/internal/process"
	"whirlwind/internal/provider"
	"whirlwind/internal/verification"
)

const (
	autofixResultFixturePath = "result.txt"
)

func approvedAutofixEvidenceFixture(t *testing.T) (*Engine, model.ExecutionPlan, model.Project, *verificationRun, verification.Report, []byte) {
	t.Helper()
	e, idea := approvedIdeaPlannerFixture(t)
	ctx := context.Background()
	project, err := e.Project(ctx, idea.ProjectID)
	requireWorkflowSuccess(t, err)
	raw, _ := json.Marshal(project.Policy)
	var values map[string]any
	requireWorkflowSuccess(t, json.Unmarshal(raw, &values))
	values["checks"] = append(values["checks"].([]any), map[string]any{"name": "fix", "mode": "autofix", "command": []string{"/bin/sh", "-c", "printf 'corrected result\\n' > result.txt"}, "timeout_seconds": 5})
	data, err := yaml.Marshal(values)
	requireWorkflowSuccess(t, err)
	requireWorkflowSuccess(t, os.WriteFile(filepath.Join(project.Path, ".whirlwind.yaml"), data, 0600))
	submissionGit(t, project.Path, "add", ".whirlwind.yaml")
	submissionGit(t, project.Path, "commit", "-m", "human approved autofix command mode")
	project.Policy, project.PolicyHash, err = policy.Load(project.Path)
	if err != nil {
		t.Fatalf("human autofix configuration unsupported: %v", err)
	}
	row, _ := e.Store.Get(ctx, "projects", project.ID)
	requireWorkflowSuccess(t, e.Store.Put(ctx, "projects", project.ID, project, row.Version))
	var launches atomic.Int32
	e.Adapter = func(model.Provider) provider.Adapter { return markdownPlannerAdapter(t, &launches) }
	e.intakeApprovedIdeas(ctx)
	requireWorkflowSuccess(t, e.Step(ctx, model.PlanID("idea:"+idea.ID)))
	plan, version := localStoredTask(t, e, model.PlanID("idea:"+idea.ID))
	requireWorkflowSuccess(t, e.ReviewLocalPlan(ctx, plan.ID, version, "approve", plan.PlanDocument.Hash, ""))
	plan, _ = localStoredTask(t, e, plan.ID)
	installNativeParityVerifier(t)
	plan.Worktree = project.Path
	plan.Base = submissionGit(t, project.Path, "rev-parse", "HEAD")
	requireWorkflowSuccess(t, os.WriteFile(filepath.Join(project.Path, autofixResultFixturePath), []byte("submitted result\n"), 0600))
	submissionGit(t, project.Path, "add", autofixResultFixturePath)
	submissionGit(t, project.Path, "commit", "-m", "submitted immutable code version")
	run, err := e.startVerification(ctx, project, &plan)
	requireWorkflowSuccess(t, err)
	report, err := run.runChecks(project.Policy, project.Policy.Checks, nil)
	requireWorkflowSuccess(t, err)
	complete, ok := run.value["runner_report"].(verification.JobReport)
	if !ok || len(complete.Results) != 2 || complete.Results[1].Status != "pass" || complete.Results[1].Autofix == nil {
		t.Fatalf("human source-write intent lost before runner: %+v", complete)
	}
	if report.Passed || len(report.Results) != 2 || report.Results[1].Passed || !strings.Contains(report.Results[1].Error, "autofix") {
		t.Fatalf("pending patch certified or missing Coding feedback: %+v", report)
	}
	if raw, err := os.ReadFile(filepath.Join(project.Path, autofixResultFixturePath)); err != nil || string(raw) != "submitted result\n" {
		t.Fatalf("autofix changed submitted source: %q %v", raw, err)
	}
	if err := run.finish(report, report, nil); err == nil {
		t.Fatal("pending autofix accepted before a new code version")
	}
	encoded, _ := json.Marshal(plan.Handoff)
	var handoff struct {
		Proposals []struct {
			ArtifactID      string `json:"artifact_id"`
			VerificationID  string `json:"verification_id"`
			CandidateCommit string `json:"candidate_commit"`
		} `json:"autofix_proposals"`
	}
	requireWorkflowSuccess(t, json.Unmarshal(encoded, &handoff))
	if len(handoff.Proposals) != 1 || handoff.Proposals[0].ArtifactID == "" || handoff.Proposals[0].VerificationID != run.id || handoff.Proposals[0].CandidateCommit != run.head {
		t.Fatalf("Coding lacks durable candidate-bound patch handoff: %s", encoded)
	}
	_, patch, err := e.Store.Artifact(ctx, handoff.Proposals[0].ArtifactID)
	if err != nil || !strings.Contains(string(patch), "+corrected result") {
		t.Fatalf("Coding patch artifact unavailable: %q %v", patch, err)
	}
	return e, plan, project, run, report, patch
}

func TestParityHumanAutofixModeReachesRunnerAndCodingFeedback(t *testing.T) {
	e, plan, project, run, report, patch := approvedAutofixEvidenceFixture(t)
	ctx := context.Background()
	replayed, err := run.runChecks(project.Policy, project.Policy.Checks, nil)
	if err != nil || replayed.Results[1].AutofixArtifact != report.Results[1].AutofixArtifact || replayed.Results[1].StartedAt != report.Results[1].StartedAt {
		t.Fatalf("completed fix job or retained artifact duplicated after recovery: %+v %v", replayed, err)
	}
	e.Adapter = func(model.Provider) provider.Adapter {
		return connectionAdapterFunc(func(ctx context.Context, in provider.Input, _ provider.Hooks) (provider.Output, error) {
			return inspectScopedAutofixPatch(ctx, in, patch)
		})
	}
	if _, err := e.agent(ctx, project, plan, provider.Input{Role: "code", CWD: project.Path, Prompt: "Inspect proposed analyzer fix without applying it."}); err != nil {
		t.Fatal(err)
	}
	// Coding incorporates the proposed patch in another checkout and submits a
	// new immutable commit; every mandatory command must run for that version.
	revised := plan
	revised.Worktree = filepath.Join(t.TempDir(), "coding-revision")
	if output, err := exec.Command("git", "clone", "--no-hardlinks", "--", project.Path, revised.Worktree).CombinedOutput(); err != nil {
		t.Fatalf("revision clone: %v %s", err, output)
	}
	apply := exec.Command("git", "-C", revised.Worktree, "apply", "--binary", "-")
	apply.Stdin = bytes.NewReader(patch)
	if output, err := apply.CombinedOutput(); err != nil {
		t.Fatalf("Coding cannot incorporate retained fix: %v %s", err, output)
	}
	submissionGit(t, revised.Worktree, "add", autofixResultFixturePath)
	submissionGit(t, revised.Worktree, "-c", "user.name=Autofix acceptance", "-c", "user.email=autofix@example.invalid", "commit", "-m", "incorporate proposed analyzer fix")
	newCommit := submissionGit(t, revised.Worktree, "rev-parse", "HEAD")
	if newCommit == run.head {
		t.Fatal("applied fix reused submitted code identity")
	}
	newRun, err := e.startVerification(ctx, project, &revised)
	requireWorkflowSuccess(t, err)
	if newRun.id == run.id {
		t.Fatal("new source reused previous verification identity")
	}
	verified, err := newRun.runChecks(project.Policy, project.Policy.Checks, nil)
	if err != nil || !verified.Passed || len(verified.Results) != 2 || verified.Results[0].StartedAt == report.Results[0].StartedAt || verified.Results[1].Autofix != nil {
		t.Fatalf("applied fix bypassed complete fresh verification: %+v %v", verified, err)
	}
	requireWorkflowSuccess(t, newRun.finish(verified, verified, nil))
	step := taskStep{Engine: e, ctx: ctx, project: project, task: revised}
	if err := step.validateReviewRunnerReport(newCommit); err != nil {
		t.Fatalf("new version lacks complete Reviewer handoff: %v", err)
	}
	step.task.VerificationID = run.id
	if err := step.validateReviewRunnerReport(newCommit); err == nil {
		t.Fatal("new code could be reviewed using previous verification evidence")
	}
}

func inspectScopedAutofixPatch(ctx context.Context, in provider.Input, patch []byte) (provider.Output, error) {
	parts := strings.SplitN(in.Prompt, "Versioned handoff:\n", 2)
	if len(parts) != 2 {
		return provider.Output{}, fmt.Errorf("Coding handoff missing")
	}
	var proposed model.Handoff
	if err := json.NewDecoder(strings.NewReader(parts[1])).Decode(&proposed); err != nil {
		return provider.Output{}, err
	}
	if len(proposed.AutofixProposals) != 1 || proposed.AutofixProposals[0].Path == "" {
		return provider.Output{}, fmt.Errorf("Coding has no scoped read-only patch path")
	}
	sibling := filepath.Join(filepath.Dir(proposed.AutofixProposals[0].Path), "unapproved.patch")
	if err := os.WriteFile(sibling, []byte("unapproved sibling content"), 0600); err != nil {
		return provider.Output{}, err
	}
	command, err := execution.Command(ctx, []string{"/bin/sh", "-c", `cat "$1"; chmod u+w "$1" 2>/dev/null || :; if printf tampered >> "$1"; then exit 91; fi; if cat "$2" 2>/dev/null; then exit 92; fi`, "read-proposed-patch", proposed.AutofixProposals[0].Path, sibling}, *in.Execution)
	if err != nil {
		return provider.Output{}, err
	}
	var output, diagnostic bytes.Buffer
	command.Stdout, command.Stderr = &output, &diagnostic
	if err := process.Run(command); err != nil {
		return provider.Output{}, fmt.Errorf("scoped patch read/write protection: %w %s", err, diagnostic.String())
	}
	if !bytes.Equal(output.Bytes(), patch) {
		return provider.Output{}, fmt.Errorf("Coding received a different patch")
	}
	return provider.Output{Text: "Complete proposed patch inspected; new source version still required."}, nil
}
