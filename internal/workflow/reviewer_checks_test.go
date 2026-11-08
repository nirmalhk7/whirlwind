package workflow

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"whirlwind/internal/github"
	"whirlwind/internal/model"
	"whirlwind/internal/provider"
	"whirlwind/internal/testutil/openshell"
)

// Allowing arbitrary IDs/commands would turn the reviewer broker into a host shell.
func TestReviewerBrokerRejectsUncommittedCheck(t *testing.T) {
	a := &agentAttempt{agentRun: &agentRun{input: provider.Input{ReadOnly: true, Role: "review"}, project: model.Project{Policy: model.Policy{Checks: []model.Check{{Name: "tests", Command: []string{"true"}}}}}}}
	if _, err := a.runCheck(context.Background(), "sh -c malicious"); err == nil {
		t.Fatal("uncommitted command accepted")
	}
	a.input.ReadOnly = false
	if _, err := a.runCheck(context.Background(), "tests"); err == nil {
		t.Fatal("mutable role accessed reviewer check broker")
	}
}

// A dirty/deleted live file must not change what a reviewer check verifies: only the reviewed commit is used.
func TestReviewerBrokerRunsCommittedSnapshotAndCapturesEvidence(t *testing.T) {
	fixture := openshell.Install(t)
	fixture.EnableCommandExecution()
	t.Setenv("WHIRLWIND_OPENSHELL_CLI", fixture.Binary)
	e, task, cwd := testEngine(t)
	ctx := context.Background()
	if err := os.WriteFile(filepath.Join(cwd, resultFile), []byte("committed"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := github.Run(ctx, cwd, "git", "add", resultFile); err != nil {
		t.Fatal(err)
	}
	if _, err := github.Run(ctx, cwd, "git", "commit", "-m", "fixture result"); err != nil {
		t.Fatal(err)
	}
	head, err := github.Run(ctx, cwd, "git", "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(filepath.Join(cwd, resultFile)); err != nil {
		t.Fatal(err)
	}
	p, err := e.Project(ctx, task.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	task.VerificationID = "reviewed-proof"
	if err = e.Store.Put(ctx, "verification", task.VerificationID, map[string]any{"stage": "complete", "provenance": model.Provenance{Task: task.ID, PlanVersion: task.PlanVersion, Head: head, PolicyHash: p.PolicyHash}}, 0); err != nil {
		t.Fatal(err)
	}
	run := &agentRun{Engine: e, ctx: ctx, project: p, task: task, input: provider.Input{Role: "review", ReadOnly: true, CWD: cwd}}
	a, err := run.newAttempt(model.Provider{ID: "fake", Kind: "acp"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := a.runCheck(ctx, "test")
	if err != nil || !result.Passed || result.Head != head || result.OutputArtifact == "" || result.ExecutionProfile == "" {
		t.Fatalf("snapshot evidence: %+v %v", result, err)
	}
	if _, err = os.Stat(filepath.Join(cwd, resultFile)); !os.IsNotExist(err) {
		t.Fatal("broker changed live worktree")
	}
	if err = e.Store.Put(ctx, "verification", task.VerificationID, map[string]any{"stage": "complete", "provenance": model.Provenance{Task: task.ID, PlanVersion: task.PlanVersion, Head: "different-head", PolicyHash: p.PolicyHash}}, 1); err != nil {
		t.Fatal(err)
	}
	if _, err = a.runCheck(ctx, "test"); err == nil {
		t.Fatal("broker checked a head different from reviewed evidence")
	}
}

// Snapshot extraction must never follow an archive link to overwrite a host file.
func TestReviewSnapshotRejectsArchiveTraversal(t *testing.T) {
	root := t.TempDir()
	if err := extractSnapshot(root, strings.NewReader("not a tar archive")); err == nil {
		t.Fatal("malformed archive accepted")
	}
	if _, err := os.Stat(filepath.Join(root, "unexpected")); !os.IsNotExist(err) {
		t.Fatal("malformed archive wrote files")
	}
}
