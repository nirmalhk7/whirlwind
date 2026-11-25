package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"whirlwind/internal/model"
	"whirlwind/internal/provider"
	"whirlwind/internal/store"
)

func TestApprovedIdeaPlanUsesStablePrefixedUUID(t *testing.T) {
	e, idea := approvedIdeaPlannerFixture(t)
	ctx := context.Background()
	e.intakeApprovedIdeas(ctx)
	records, err := e.Store.List(ctx, "execution_plans")
	if err != nil {
		t.Fatal(err)
	}
	var id string
	for _, record := range records {
		plan, err := store.Decode[model.ExecutionPlan](record)
		if err != nil {
			t.Fatal(err)
		}
		if plan.IdeaID == idea.ID {
			id = plan.ID
		}
	}
	if !strings.HasPrefix(id, "plan_") || uuid.Validate(strings.TrimPrefix(id, "plan_")) != nil {
		t.Fatalf("plan must have a prefixed UUID: %q", id)
	}
	e.intakeApprovedIdeas(ctx)
	after, err := e.Store.List(ctx, "execution_plans")
	if err != nil || len(after) != len(records) {
		t.Fatalf("polling duplicated plan: %d %v", len(after), err)
	}
	if _, err := e.Store.Get(ctx, "execution_plans", id); err != nil {
		t.Fatal(err)
	}
}

func TestParityApprovedIdeaDispatchesRealMarkdownPlanner(t *testing.T) {
	e, idea := approvedIdeaPlannerFixture(t)
	ctx := context.Background()
	var launches atomic.Int32
	e.Adapter = func(model.Provider) provider.Adapter { return markdownPlannerAdapter(t, &launches) }
	e.dispatch(ctx)
	e.background.Wait()
	record, err := e.Store.Get(ctx, "ideas", idea.ID)
	requireWorkflowSuccess(t, err)
	saved, _ := store.Decode[model.Idea](record)
	if launches.Load() != 1 || saved.DecisionStatus != "complete" || saved.PlanID == "" {
		t.Fatalf("approved backlog not planned once: launches=%d idea=%+v", launches.Load(), saved)
	}
	planRecord, err := e.Store.Get(ctx, "execution_plans", saved.PlanID)
	requireWorkflowSuccess(t, err)
	plan, _ := store.Decode[model.ExecutionPlan](planRecord)
	if plan.IdeaID != idea.ID || plan.IdeationRunID != "source" || plan.Stage != "plan_approval" || plan.Approved || plan.PlanDocument == nil {
		t.Fatalf("authoring and approval conflated: %+v", plan)
	}
	content, err := os.ReadFile(plan.PlanDocument.Path)
	requireWorkflowSuccess(t, err)
	if _, err := ParsePlanDocument(string(content)); err != nil {
		t.Fatal(err)
	}
	var runID, status string
	if err := e.Store.DB.QueryRow("SELECT planner_run_id,status FROM planner_runs WHERE idea_id=?", idea.ID).Scan(&runID, &status); err != nil || runID != plan.LastAttemptID || status != "completed" {
		t.Fatalf("missing attempt linkage %s %s %v", runID, status, err)
	}
	e.dispatch(ctx)
	e.background.Wait()
	if launches.Load() != 1 {
		t.Fatal("completed idea launched twice")
	}
	requireWorkflowSuccess(t, e.ReviewLocalPlan(ctx, plan.ID, planRecord.Version, "revise", plan.PlanDocument.Hash, "Clarify the acceptance evidence"))
	revisedRecord, err := e.Store.Get(ctx, "execution_plans", plan.ID)
	requireWorkflowSuccess(t, err)
	revised, _ := store.Decode[model.ExecutionPlan](revisedRecord)
	if revised.Stage != "plan_approval" || revised.PlanDocument == nil {
		t.Fatalf("requesting changes reopened completed Planner: %+v", revised)
	}
	e.dispatch(ctx)
	e.background.Wait()
	if launches.Load() != 1 {
		t.Fatal("revision relaunched completed Planner")
	}

}

func approvedIdeaPlannerFixture(t *testing.T, checks ...model.Check) (*Engine, model.Idea) {
	t.Helper()
	e, existing, _ := testEngine(t)
	ctx := context.Background()
	r, _ := e.Store.Get(ctx, "execution_plans", existing.ID)
	existing.Stage = "complete"
	if err := e.Store.Put(ctx, "execution_plans", existing.ID, existing, r.Version); err != nil {
		t.Fatal(err)
	}
	configurePlannerFixtureChecks(t, e, existing.ProjectID, checks)
	if err := e.Store.Put(ctx, "operations", "source", model.Operation{Kind: "ideation", Subject: "p", Stage: "complete"}, 0); err != nil {
		t.Fatal(err)
	}
	idea := model.Idea{Title: "Invoice export", ID: "idea", ProjectID: "p", ProjectName: "Repository", RunID: "source", Direction: "expand", What: "Add result file", Why: "Result is missing", Where: "result.txt", HowItHelps: "Provide useful output", DecisionStatus: "draft", NoveltyMethod: "legacy_llm"}
	if err := e.Store.Put(ctx, "ideas", idea.ID, idea, 0); err != nil {
		t.Fatal(err)
	}
	idea.DecisionStatus = "approved"
	if err := e.Store.Put(ctx, "ideas", idea.ID, idea, 1); err != nil {
		t.Fatal(err)
	}

	return e, idea
}

func TestParityPlannerMissingOutputRequiresExplicitRetry(t *testing.T) {
	e, idea := approvedIdeaPlannerFixture(t)
	ctx := context.Background()
	var launches atomic.Int32
	e.Adapter = func(model.Provider) provider.Adapter {
		return connectionAdapterFunc(func(context.Context, provider.Input, provider.Hooks) (provider.Output, error) {
			launches.Add(1)
			return provider.Output{Text: "Done"}, nil
		})
	}
	e.dispatch(ctx)
	e.background.Wait()
	record, err := e.Store.Get(ctx, "ideas", idea.ID)
	if err != nil {
		t.Fatal(err)
	}
	saved, _ := store.Decode[model.Idea](record)
	if saved.DecisionStatus != "error" || saved.ErrorReason == "" || saved.PlanID != "" {
		t.Fatalf("missing Markdown did not fail closed: %+v", saved)
	}
	var id string
	requireWorkflowSuccess(t, e.Store.DB.QueryRow("SELECT planner_run_id FROM planner_runs WHERE idea_id=?", idea.ID).Scan(&id))
	attempt := plannerTestAttempt(t, e, id)
	if !attempt.Finished || attempt.Status != "failed" || attempt.Success == nil || *attempt.Success || attempt.Error != saved.ErrorReason {
		t.Fatalf("plan missing but Runs did not report the same failure as the idea: status=%q success=%v attempt error=%q idea error=%q", attempt.Status, attempt.Success, attempt.Error, saved.ErrorReason)
	}
	if !strings.Contains(saved.ErrorReason, "did not write the assigned plan.md") || !strings.Contains(saved.ErrorReason, "retry") {
		t.Fatalf("missing plan did not provide actionable recovery: %q", saved.ErrorReason)
	}
	assertPlannerMetric(t, e, id, false, false)
	e.dispatch(ctx)
	e.background.Wait()
	if launches.Load() != 1 {
		t.Fatal("error automatically retried without human decision")
	}
	var count int
	if err := e.Store.DB.QueryRow("SELECT count(*) FROM planner_runs WHERE idea_id=? AND status='error' AND error_reason<>''", idea.ID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("missing output lost attempt error: %d %v", count, err)
	}
}

type plannerCanceller interface {
	CancelPlanner(context.Context, string, int) error
}

func TestParityPlannerCancellationFencesCompletionAndRequiresHumanRetry(t *testing.T) {
	e, idea := approvedIdeaPlannerFixture(t)
	ctx := context.Background()
	canceller, ok := any(e).(plannerCanceller)
	if !ok {
		t.Fatal("active Planner cancellation unavailable")
	}
	started := make(chan struct{})
	var launches atomic.Int32
	e.Adapter = func(model.Provider) provider.Adapter {
		return connectionAdapterFunc(func(ctx context.Context, _ provider.Input, _ provider.Hooks) (provider.Output, error) {
			launches.Add(1)
			close(started)
			<-ctx.Done()
			return provider.Output{Text: "late completion"}, nil
		})
	}
	e.dispatch(ctx)
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("Planner never started")
	}
	record, err := e.Store.Get(ctx, "ideas", idea.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := canceller.CancelPlanner(ctx, idea.ID, record.Version); err != nil {
		t.Fatal(err)
	}
	e.background.Wait()
	record, err = e.Store.Get(ctx, "ideas", idea.ID)
	if err != nil {
		t.Fatal(err)
	}
	saved, _ := store.Decode[model.Idea](record)
	if saved.DecisionStatus != "error" || saved.ErrorReason != "user_cancelled" || saved.PlanID != "" {
		t.Fatalf("cancelled output published: %+v", saved)
	}
	var status, reason string
	if err := e.Store.DB.QueryRow("SELECT status,error_reason FROM planner_runs WHERE idea_id=?", idea.ID).Scan(&status, &reason); err != nil || status != "error" || reason != "user_cancelled" {
		t.Fatalf("cancellation evidence missing %s %s %v", status, reason, err)
	}
	e.dispatch(ctx)
	e.background.Wait()
	if launches.Load() != 1 {
		t.Fatal("cancelled idea automatically requeued")
	}
}

func markdownPlannerAdapter(t *testing.T, launches *atomic.Int32) provider.Adapter {
	return connectionAdapterFunc(func(ctx context.Context, in provider.Input, h provider.Hooks) (provider.Output, error) {
		launches.Add(1)
		if in.Role != "plan" || !in.ReadOnly || !strings.Contains(in.Prompt, "source") || !strings.Contains(in.Prompt, "Provide useful output") {
			t.Errorf("Planner lost full idea context: %+v", in)
		}
		out, err := (fakeAgent{}).Run(ctx, in, h)
		if err != nil {
			return out, err
		}
		plan, err := decodePlan(out.Text)
		if err != nil {
			return out, err
		}
		if in.Execution == nil || len(in.Execution.WriteRoots) != 1 {
			t.Errorf("Planner missing assigned output grant")
			return provider.Output{}, nil
		}
		raw, _ := json.Marshal(map[string]string{"path": filepath.Join(in.Execution.WriteRoots[0], "plan.md"), "content": RenderPlanDocument(plan)})
		client := provider.NewClient(in, h)
		defer func() { _ = client.Close() }()
		if _, err := client.Handle(ctx, provider.Message{Method: "fs/write_text_file", Params: raw}); err != nil {
			return provider.Output{}, err
		}
		return provider.Output{Text: "Assigned Markdown written."}, nil
	})
}

func TestParityPlannerExplicitRetryResetsFailedAuthoring(t *testing.T) {
	e, idea := approvedIdeaPlannerFixture(t)
	ctx := context.Background()
	e.Adapter = func(model.Provider) provider.Adapter {
		return connectionAdapterFunc(func(context.Context, provider.Input, provider.Hooks) (provider.Output, error) {
			return provider.Output{Text: "Done"}, nil
		})
	}
	e.dispatch(ctx)
	e.background.Wait()
	r, err := e.Store.Get(ctx, "ideas", idea.ID)
	if err != nil {
		t.Fatal(err)
	}
	saved, _ := store.Decode[model.Idea](r)
	if saved.DecisionStatus != "error" {
		t.Fatalf("not failed: %+v", saved)
	}
	saved.DecisionStatus, saved.ErrorReason = "approved", ""
	if err := e.Store.Put(ctx, "ideas", idea.ID, saved, r.Version); err != nil {
		t.Fatal(err)
	}
	var launches atomic.Int32
	e.Adapter = func(model.Provider) provider.Adapter { return markdownPlannerAdapter(t, &launches) }
	e.dispatch(ctx)
	e.background.Wait()
	r, err = e.Store.Get(ctx, "ideas", idea.ID)
	if err != nil {
		t.Fatal(err)
	}
	saved, _ = store.Decode[model.Idea](r)
	if saved.DecisionStatus != "complete" || launches.Load() != 1 {
		t.Fatalf("human retry did not run Planner: %+v launches=%d", saved, launches.Load())
	}
	var attempts int
	if err := e.Store.DB.QueryRow("SELECT count(*) FROM planner_runs WHERE idea_id=?", idea.ID).Scan(&attempts); err != nil || attempts != 2 {
		t.Fatalf("retry discarded prior ACP attempt: %d %v", attempts, err)
	}
}

func TestParityPlannerReconcilesPublishedFileAfterInterruptedCommit(t *testing.T) {
	e, idea := approvedIdeaPlannerFixture(t)
	ctx := context.Background()
	var launches atomic.Int32
	e.Adapter = func(model.Provider) provider.Adapter { return markdownPlannerAdapter(t, &launches) }
	e.intakeApprovedIdeas(ctx)
	step, err := e.prepareStep(ctx, model.PlanID("idea:"+idea.ID))
	if err != nil {
		t.Fatal(err)
	}
	content, err := step.prepareIdeaPlanPublication()
	requireWorkflowSuccess(t, err)
	interrupted := errors.New("publication interrupted after writing Markdown")
	err = e.Store.CommitPlannerPublication(ctx, step.task, step.record.Version, func() error {
		if _, err := step.writeLocalPlanFile(step.task, content); err != nil {
			return err
		}
		return interrupted
	})
	if !errors.Is(err, interrupted) {
		t.Fatalf("expected interruption after writing Markdown: %v", err)
	}
	published := step.task.PlanDocument.Path
	before, err := os.ReadFile(published)
	if err != nil {
		t.Fatal(err)
	}
	pending := plannerTestAttempt(t, e, step.task.LastAttemptID)
	if !pending.Finished || pending.Status != model.AttemptAwaitingPlanPublication || pending.Success != nil {
		t.Fatalf("provider success was reported as Planner success before the publication commit: status=%q success=%v", pending.Status, pending.Success)
	}
	assertPlannerMetric(t, e, step.task.LastAttemptID, true, nil)

	ideaRecord, err := e.Store.Get(ctx, "ideas", idea.ID)
	requireWorkflowSuccess(t, err)
	if err := e.CancelPlanner(ctx, idea.ID, ideaRecord.Version); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("cancellation discarded published recovery evidence: %v", err)
	}

	// Simulate process loss between filesystem publication and the final transaction.
	// No authoring result is committed to the ExecutionPlan resource.
	replacement := New(e.Store, e.GitHub, e.Root)
	replacement.PlansRoot = e.PlansRoot
	replacement.Adapter = func(model.Provider) provider.Adapter {
		return connectionAdapterFunc(func(context.Context, provider.Input, provider.Hooks) (provider.Output, error) {
			t.Error("recovery launched duplicate Planner")
			return provider.Output{}, nil
		})
	}
	if err := replacement.Step(ctx, model.PlanID("idea:"+idea.ID)); err != nil {
		t.Fatal(err)
	}
	r, err := e.Store.Get(ctx, "ideas", idea.ID)
	if err != nil {
		t.Fatal(err)
	}
	saved, _ := store.Decode[model.Idea](r)
	after, err := os.ReadFile(published)
	if err != nil {
		t.Fatal(err)
	}
	if saved.DecisionStatus != "complete" || string(before) != string(after) || launches.Load() != 1 {
		t.Fatalf("publication recovery lost identity/content: %+v launches=%d", saved, launches.Load())
	}
	completed := plannerTestAttempt(t, e, step.task.LastAttemptID)
	if completed.Status != "complete" || completed.Success == nil || !*completed.Success {
		t.Fatalf("verified publication did not finish the original attempt: %+v", completed)
	}
	assertPlannerMetric(t, e, step.task.LastAttemptID, false, true)
}

func assertPlannerMetric(t *testing.T, e *Engine, id string, pending bool, success any) {
	t.Helper()
	record, err := e.Store.Get(t.Context(), "metrics", id)
	requireWorkflowSuccess(t, err)
	metric, err := store.Decode[map[string]any](record)
	requireWorkflowSuccess(t, err)
	awaiting, _ := metric["awaiting_plan_publication"].(bool)
	if awaiting != pending || metric["success"] != success {
		t.Fatalf("inconsistent Planner metric: awaiting=%v success=%v; want %v/%v", awaiting, metric["success"], pending, success)
	}
}

func TestParityPlannerRejectsInvalidMarkdownWithoutRecordingSuccess(t *testing.T) {
	e, idea := approvedIdeaPlannerFixture(t)
	e.Adapter = func(model.Provider) provider.Adapter {
		return connectionAdapterFunc(func(_ context.Context, in provider.Input, _ provider.Hooks) (provider.Output, error) {
			return provider.Output{Text: "Draft written"}, os.WriteFile(filepath.Join(in.PlannerOutputDirectory, "plan.md"), []byte("Not a valid plan."), 0600)
		})
	}
	e.dispatch(t.Context())
	e.background.Wait()
	record, err := e.Store.Get(t.Context(), "ideas", idea.ID)
	requireWorkflowSuccess(t, err)
	saved, err := store.Decode[model.Idea](record)
	requireWorkflowSuccess(t, err)
	if saved.DecisionStatus != "error" || saved.PlanID != "" || !strings.Contains(saved.ErrorReason, "assigned plan.md is not publishable") {
		t.Fatalf("invalid document did not block publication with a specific recovery reason: %+v", saved)
	}
	var id, intended string
	requireWorkflowSuccess(t, e.Store.DB.QueryRowContext(t.Context(), "SELECT planner_run_id,markdown_path FROM planner_runs WHERE idea_id=?", idea.ID).Scan(&id, &intended))
	if _, err := os.Stat(intended); !os.IsNotExist(err) {
		t.Fatalf("invalid plan ended up at the publication destination %q: %v", intended, err)
	}
	attempt := plannerTestAttempt(t, e, id)
	if attempt.Status != "failed" || attempt.Success == nil || *attempt.Success || attempt.Error != saved.ErrorReason {
		t.Fatalf("attempt result did not retain the invalid plan error: %+v", attempt)
	}
	assertPlannerMetric(t, e, id, false, false)
}

func plannerTestAttempt(t *testing.T, e *Engine, id string) model.Attempt {
	t.Helper()
	record, err := e.Store.Get(t.Context(), "sessions", id)
	requireWorkflowSuccess(t, err)
	attempt, err := store.Decode[model.Attempt](record)
	requireWorkflowSuccess(t, err)
	return attempt
}
