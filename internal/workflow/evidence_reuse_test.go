package workflow

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"whirlwind/internal/model"
	"whirlwind/internal/verification"
)

func TestEvidenceReuseRejectsChangedInputsOrExpiredEvidence(t *testing.T) {
	now := time.Date(2026, 10, 22, 0, 0, 0, 0, time.UTC)
	key := evidenceKey{Tree: "tree", Base: "base", Policy: "policy", PlanVersion: 1, Command: "command", Environment: "env", Executable: "tool"}
	original := cachedCheck{Key: key, VerificationID: "verified", FinishedAt: now.Format(time.RFC3339Nano), Result: verification.Result{Passed: true}}
	if !original.valid(key, now.Add(time.Second), 60) {
		t.Fatal("matching fresh evidence rejected")
	}
	for _, change := range []func(*evidenceKey){func(k *evidenceKey) { k.Tree = "changed" }, func(k *evidenceKey) { k.Base = "changed" }, func(k *evidenceKey) { k.Policy = "changed" }, func(k *evidenceKey) { k.PlanVersion++ }, func(k *evidenceKey) { k.Command = "changed" }, func(k *evidenceKey) { k.Environment = "changed" }, func(k *evidenceKey) { k.Executable = "changed" }} {
		changed := key
		change(&changed)
		if original.valid(changed, now, 60) {
			t.Fatal("changed evidence input reused")
		}
	}
	if original.valid(key, now.Add(61*time.Second), 60) || original.valid(key, now, 0) {
		t.Fatal("expired or undeclared freshness reused")
	}
	original.Result.Passed = false
	if original.valid(key, now, 60) {
		t.Fatal("failure reused as pass")
	}
	original.Result.Passed = true
	original.VerificationID = ""
	if original.valid(key, now, 60) {
		t.Fatal("missing provenance reused")
	}
}

func TestReusableEvidenceKeepsLargeOutputInArtifact(t *testing.T) {
	e, task, repo := testEngine(t)
	task.Worktree = repo
	run, err := e.startVerification(context.Background(), model.Project{}, &task)
	if err != nil {
		t.Fatal(err)
	}
	key := evidenceKey{Tree: "tree"}
	if err = run.cacheResult(context.Background(), key, verification.Result{Name: "output", Passed: true, Output: strings.Repeat("large", 30000)}); err != nil {
		t.Fatal(err)
	}
	record, err := e.Store.Get(context.Background(), "check_cache", task.ID+":"+evidenceDigest(key))
	if err != nil {
		t.Fatal(err)
	}
	if len(record.Data) > 8192 {
		t.Fatal("cache inlined full logs into resource events")
	}
}

func TestReusableCheckUsesEvidenceButDirtySourceForcesExecution(t *testing.T) {
	e, task, repo := testEngine(t)
	task.Worktree = repo
	policy := model.Policy{Checks: []model.Check{{Name: "pure", Command: []string{"/usr/bin/true"}, Reusable: true, FreshnessSeconds: 60}}}
	project := model.Project{Policy: policy, PolicyHash: "policy"}
	first, err := e.startVerification(context.Background(), project, &task)
	if err != nil {
		t.Fatal(err)
	}
	report, err := first.runChecks(policy, policy.Checks, func(verification.Result) error { return nil })
	if err != nil || !report.Passed {
		t.Fatalf("initial check: %+v %v", report, err)
	}
	// A saved check is not reusable until the containing verification completes.
	key, _ := first.checkKey(policy, policy.Checks[0])
	if _, reused := first.cachedResult(key, policy.Checks[0]); reused {
		t.Fatal("incomplete verification reused")
	}
	if err = first.finish(report, report, nil); err != nil {
		t.Fatal(err)
	}
	second, err := e.startVerification(context.Background(), project, &task)
	if err != nil {
		t.Fatal(err)
	}
	report, err = second.runChecks(policy, policy.Checks, func(verification.Result) error { return nil })
	if err != nil || report.Results[0].ReusedFrom != first.id {
		t.Fatalf("missing reuse provenance: %+v %v", report, err)
	}
	if err = os.WriteFile(filepath.Join(repo, "untracked.txt"), []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	report, err = second.runChecks(policy, policy.Checks, func(verification.Result) error { return nil })
	if err != nil || report.Results[0].ReusedFrom != "" {
		t.Fatalf("dirty source reused: %+v %v", report, err)
	}
}
