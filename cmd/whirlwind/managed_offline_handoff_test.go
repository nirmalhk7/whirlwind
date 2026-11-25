package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"whirlwind/internal/model"
	"whirlwind/internal/provider"
	"whirlwind/internal/store"
	"whirlwind/internal/verification"
	"whirlwind/internal/workflow"
)

type seededOfflineAdapter func(context.Context, provider.Input, provider.Hooks) (provider.Output, error)

func (a seededOfflineAdapter) Run(ctx context.Context, input provider.Input, hooks provider.Hooks) (provider.Output, error) {
	return a(ctx, input, hooks)
}

type seededOfflineHandoff struct {
	data, planID, candidate, green, nono string
	project                              model.Project
	git                                  func(string, ...string) string
}

// The supervisor has stopped before this phase. The harness returns only
// deterministic Reviewer outputs. The production engine, store, verifier
// subprocess and immutable snapshots still own the handoffs.
func exerciseSeededOfflineHandoffs(t *testing.T, db *store.Store, fixture seededOfflineHandoff) {
	t.Helper()
	data, project, id := fixture.data, fixture.project, fixture.planID
	candidate, green, nono, git := fixture.candidate, fixture.green, fixture.nono, fixture.git
	ctx := t.Context()
	t.Setenv("WHIRLWIND_NONO_CLI", nono)
	t.Setenv("WHIRLWIND_VERIFY_BIN", filepath.Join(data, "runtime", "whirlwind-verify"))
	registry := filepath.Join(data, "seeded-test-processes")
	mustSeedTerminal(t, os.Mkdir(registry, 0700))
	t.Setenv("WHIRLWIND_PROCESS_REGISTRY", registry)

	getTask := func() (model.ExecutionPlan, int) {
		t.Helper()
		r, err := db.Get(ctx, "execution_plans", id)
		mustSeedTerminal(t, err)
		task, err := store.Decode[model.ExecutionPlan](r)
		mustSeedTerminal(t, err)
		return task, r.Version
	}
	readiness, err := db.Get(ctx, "providers", "seeded")
	mustSeedTerminal(t, err)
	p, err := store.Decode[model.Provider](readiness)
	mustSeedTerminal(t, err)
	// Availability is runtime metadata and leaves the approved queue intact.
	p.Available = true
	used := float64(5)
	p.QuotaSnapshot = &model.QuotaSnapshot{Pool: "seeded", Source: "seeded fixture", ObservedAt: time.Now().UTC().Format(time.RFC3339Nano),
		Windows: []model.QuotaWindow{{LimitID: "fixture", Window: "primary", UsedPercent: &used}}}
	mustSeedTerminal(t, os.WriteFile(p.Command[0], []byte("#!/bin/sh\nexit 88\n"), 0700))
	mustSeedTerminal(t, db.Put(ctx, "providers", p.ID, p, readiness.Version))

	task, version := getTask()
	mustSeedTerminal(t, db.Put(ctx, "review_preparations", fmt.Sprintf("%s:%d", id, task.PlanVersion), map[string]any{
		"plan_id": id, "plan_version": task.PlanVersion, "document_hash": task.PlanDocument.ApprovedHash, "base": task.Base,
		"attempt_id": "seeded-preparation", "checklist": []map[string]string{{"requirement_id": "REQ-RESULT", "inspection": "Read result.txt and inspect the exact regression report."}},
	}, 0))
	resetReview := func(task model.ExecutionPlan, version int) {
		t.Helper()
		task.Stage, task.ResumeStage, task.Error, task.Wait = "review", "", "", nil
		task.ChecksPassed = true
		mustSeedTerminal(t, db.Put(ctx, "execution_plans", id, task, version))
	}
	resetReview(task, version)

	reviewCalls, unexpectedCalls := 0, 0
	accept := false
	expected := green
	newEngine := func() *workflow.Engine {
		e := workflow.New(db, nil, data)
		e.Adapter = func(p model.Provider) provider.Adapter {
			return seededOfflineAdapter(func(_ context.Context, in provider.Input, _ provider.Hooks) (provider.Output, error) {
				if in.Role != "review" {
					unexpectedCalls++
					return provider.Output{}, fmt.Errorf("unexpected seeded role %s", in.Role)
				}
				if p.ID != "seeded" || !in.ReadOnly || in.Execution == nil || !in.Execution.ReadOnly || in.CWD == candidate || in.CWD == project.Path {
					return provider.Output{}, fmt.Errorf("fixture role lacks independent read-only snapshot: %s", in.Role)
				}
				if git(in.CWD, "rev-parse", "HEAD") != expected {
					return provider.Output{}, fmt.Errorf("fixture role received a stale snapshot")
				}
				if !strings.Contains(in.Prompt, `"name":"regression"`) || !strings.Contains(in.Prompt, `\"Test\":\"TestResultReady\"`) {
					return provider.Output{}, fmt.Errorf("fixture Reviewer is missing the named regression evidence")
				}
				reviewCalls++
				status, explanation := "fail", "A review-only acceptance correction is still required."
				if accept {
					status, explanation = "pass", "The repaired result and the isolated test evidence satisfy the requirement."
				}
				review := model.Review{Summary: explanation, Findings: []model.Finding{}, Requirements: []model.RequirementResult{
					{ID: "REQ-RESULT", Status: status, Code: []string{"result.txt"}, Evidence: []string{"Observed current candidate and regression result."}, Explanation: explanation},
				}}
				raw, err := json.Marshal(review)
				return provider.Output{Text: string(raw)}, err
			})
		}
		return e
	}

	e := newEngine()
	if err := e.Step(ctx, id); err != nil {
		t.Fatalf("seeded Reviewer rejection: %v", err)
	}
	rejected, _ := getTask()
	if rejected.Stage != "repair" || rejected.ReviewID == "" || rejected.Delivery != nil || reviewCalls != 1 {
		t.Fatalf("rejected review did not persist repair: %+v", rejected)
	}
	t.Logf("seeded Reviewer rejected %s; durable review=%s stage=%s", green, rejected.ReviewID, rejected.Stage)

	// New connection and engine take over the same durable repair.
	mustSeedTerminal(t, db.Close())
	reopened := openSeededTerminalStore(t, data)
	db = reopened
	e = newEngine()
	recovered, version := getTask()
	if recovered.Stage != "repair" || recovered.ReviewID != rejected.ReviewID {
		t.Fatal("restart lost the rejected review or repair state")
	}
	mustSeedTerminal(t, os.WriteFile(filepath.Join(candidate, "result.txt"), []byte("ready\nreview-correction\n"), 0600))
	git(candidate, "add", "result.txt")
	git(candidate, "commit", "--no-gpg-sign", "-m", "Seed review correction")
	expected, accept = git(candidate, "rev-parse", "HEAD"), true
	resetReview(recovered, version)
	if err := e.Step(ctx, id); err != nil {
		t.Fatalf("seeded accepted Reviewer: %v", err)
	}
	approved, _ := getTask()
	if approved.Stage != "submitting" || approved.ReviewedCommit != expected || approved.ReviewID == rejected.ReviewID || reviewCalls != 2 {
		t.Fatalf("repair lost the exact accepted candidate: %+v", approved)
	}
	assertBlocked := func(reason string) {
		t.Helper()
		if err := e.Step(ctx, id); err == nil || !strings.Contains(err.Error(), reason) {
			t.Fatalf("expected finalization to stop with %q: %v", reason, err)
		}
		current, _ := getTask()
		if current.Stage != "submitting" || current.Delivery != nil || unexpectedCalls != 0 || git(candidate, "for-each-ref", "refs/heads/codex/delivery/") != "" {
			t.Fatalf("blocked finalization attempted a delivery: %+v, unexpected role calls=%d", current.Delivery, unexpectedCalls)
		}
	}
	assertBlocked("mandatory Qlty check evidence is required")

	// Finalization must independently revalidate the retained check output and
	// current checkout before it can stage a local delivery.
	proof, err := db.Get(ctx, "verification", approved.VerificationID)
	mustSeedTerminal(t, err)
	value, err := store.Decode[struct {
		Report verification.JobReport `json:"runner_report"`
	}](proof)
	mustSeedTerminal(t, err)
	var changed map[string]json.RawMessage
	mustSeedTerminal(t, json.Unmarshal(proof.Data, &changed))
	var manifest verification.JobManifest
	mustSeedTerminal(t, json.Unmarshal(changed["runner_manifest"], &manifest))
	manifest.ProjectID = "a-different-project"
	changed["runner_manifest"], err = json.Marshal(manifest)
	mustSeedTerminal(t, err)
	mustSeedTerminal(t, db.Put(ctx, "verification", proof.ID, changed, proof.Version))
	assertBlocked("does not match the managed project, policy and candidate workspace")
	currentProof, err := db.Get(ctx, "verification", proof.ID)
	mustSeedTerminal(t, err)
	mustSeedTerminal(t, db.Put(ctx, "verification", proof.ID, json.RawMessage(proof.Data), currentProof.Version))

	path := value.Report.Results[0].StdoutPath
	original, err := os.ReadFile(path)
	mustSeedTerminal(t, err)
	mustSeedTerminal(t, os.WriteFile(path, []byte("altered evidence"), 0600))
	assertBlocked("streams differ from retained evidence")
	mustSeedTerminal(t, os.WriteFile(path, original, 0600))
	mustSeedTerminal(t, os.WriteFile(filepath.Join(candidate, "result.txt"), []byte("ready\nnew candidate\n"), 0600))
	git(candidate, "add", "result.txt")
	git(candidate, "commit", "--no-gpg-sign", "-m", "Seed newer candidate")
	assertBlocked("stale for the current candidate")
	git(candidate, "checkout", "--detach", expected)
	assertBlocked("mandatory Qlty check evidence is required")
	t.Logf("reopened repair and obtained seeded review for %s; delivery gate refused a different project, altered output, a stale commit and missing Qlty evidence", expected)
}
