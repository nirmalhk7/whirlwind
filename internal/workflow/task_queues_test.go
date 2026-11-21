package workflow

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"whirlwind/internal/control"
	"whirlwind/internal/model"
	"whirlwind/internal/provider"
	"whirlwind/internal/store"
	"whirlwind/internal/taskqueue"
	"whirlwind/internal/testutil/queuehttp"
)

func TestNativeQueueIdeaAndPlanApprovalHandoffs(t *testing.T) {
	ctx := context.Background()
	e, idea := approvedIdeaPlannerFixture(t, cleanQltyFixtureCheck(t))
	queues := workflowTaskQueueFixture(t, ctx, e)
	var launches atomic.Int32
	e.Adapter = func(model.Provider) provider.Adapter { return markdownPlannerAdapter(t, &launches) }
	e.dispatch(ctx)
	e.background.Wait()
	rec, err := e.Store.Get(ctx, "ideas", idea.ID)
	requireWorkflowSuccess(t, err)
	saved, err := store.Decode[model.Idea](rec)
	requireWorkflowSuccess(t, err)
	pending, version := localStoredTask(t, e, saved.PlanID)
	if launches.Load() != 1 || pending.Stage != "plan_approval" {
		t.Fatalf("wrong planner delivery: count=%d stage=%s", launches.Load(), pending.Stage)
	}
	if e.taskQueueCursor != 1 {
		t.Fatalf("successful planner admission did not rotate role cursor: %d", e.taskQueueCursor)
	}
	if err := e.Step(ctx, saved.PlanID); err == nil {
		t.Fatal("direct step bypassed the supervisor queue")
	}
	e.dispatch(ctx)
	e.background.Wait()
	if launches.Load() != 1 {
		t.Fatalf("unapproved plan ran a second role: %d", launches.Load())
	}
	if d, err := queues.Pop(ctx, taskqueue.Coding); d != nil || err != nil {
		t.Fatalf("coding was delivered before approval: %+v %v", d, err)
	}
	requireWorkflowSuccess(t, e.ReviewLocalPlan(ctx, saved.PlanID, version, "approve", pending.PlanDocument.Hash, ""))
	if err := queues.Push(ctx, taskqueue.Reviewer, taskqueue.Push{PlanID: pending.ID, Version: version + 1}); !errors.Is(err, taskqueue.ErrConflict) {
		t.Fatalf("push to a subsequent phase bypassed coding: %v", err)
	}
	requireWorkflowSuccess(t, queues.Push(ctx, taskqueue.Coding, taskqueue.Push{}))
	d, err := queues.Pop(ctx, taskqueue.Coding)
	requireWorkflowSuccess(t, err)
	if d == nil || d.PlanID != pending.ID || d.Stage != "coding" || d.Version <= version {
		t.Fatalf("confirmed plan did not advance to coding: %+v", d)
	}
	requireWorkflowSuccess(t, queues.Settle(ctx, taskqueue.Coding, d.Receipt, "release"))
}

func TestNativeQueueSelectedIssueUsesExistingPlanApproval(t *testing.T) {
	ctx := context.Background()
	e, task, _ := testEngine(t)
	workflowTaskQueueFixture(t, ctx, e)
	e.dispatch(ctx)
	e.background.Wait()
	pending, _ := localStoredTask(t, e, task.ID)
	if pending.Stage != "plan_approval" || pending.PlanDocument == nil || pending.Issue.Number != task.Issue.Number {
		t.Fatalf("selected issue did not retain its separate plan approval and identity: %+v", pending)
	}
}

func workflowTaskQueueFixture(t *testing.T, ctx context.Context, e *Engine) *taskqueue.Client {
	t.Helper()
	queues, closeQueues, err := control.ListenQueues(ctx, control.QueueSocketPath(e.Root), e.Store)
	if errors.Is(err, syscall.EPERM) || errors.Is(err, syscall.EACCES) {
		t.Logf("Unix socket not permitted in this executor; using real HTTP handlers via in-process transport")
		queues = taskqueue.NewClientWithTransport(queuehttp.Transport{Handler: taskqueue.Handler(e.Store)})
	} else {
		requireWorkflowSuccess(t, err)
		t.Cleanup(closeQueues)
	}
	e.UseTaskQueues(queues)
	return queues
}

func TestNativeTaskQueueStageGatesAndRepairRouting(t *testing.T) {
	task := model.ExecutionPlan{
		ID: "p", ProjectID: "project", PlanVersion: 1, Selection: &model.SelectionAuthorization{CandidateID: "x", SelectedAt: "when"},
		Plan: &model.PlanSpecification{}, PlanDocument: &model.PlanDocument{ApprovedHash: "hash"},
		ReviewID: "review", VerificationID: "verify", ReviewedCommit: "commit", Base: "base", ChecksPassed: true, Score: 80.01,
	}
	for _, tt := range []struct {
		stage  string
		role   taskqueue.Role
		mutate func(*model.ExecutionPlan)
	}{
		{"planning", taskqueue.Planner, nil},
		{"plan_approval", "", nil},
		{"coding", taskqueue.Coding, nil},
		{"coding", "", func(t *model.ExecutionPlan) { t.PlanDocument = nil }},
		{"repair", taskqueue.Coding, nil},
		{"review", taskqueue.Reviewer, nil},
		{"approval", "", nil},
		{"submitting", taskqueue.Finalizer, nil},
		{"submitting", "", func(t *model.ExecutionPlan) { t.ChecksPassed = false }},
		{"submitting", "", func(t *model.ExecutionPlan) { t.Score = 80 }},
		{"submitting", "", func(t *model.ExecutionPlan) { t.ReviewID = "" }},
		{"submitting", "", func(t *model.ExecutionPlan) { t.Risk = "critical" }},
		{"paused", "", nil},
		{"cancelled", "", nil},
		{"waiting_quota", "", nil},
		{"complete", "", nil},
	} {
		candidate := task
		candidate.Stage = tt.stage
		if tt.mutate != nil {
			tt.mutate(&candidate)
		}
		if actual := taskqueue.ForPlan(candidate, "policy"); actual != tt.role {
			t.Errorf("stage %s: got %s, want %s", tt.stage, actual, tt.role)
		}
	}
}

func TestNativeQueueIdeationIntervalUsesDurableDistinctSlots(t *testing.T) {
	e, task, _ := testEngine(t)
	schedule := DefaultSettings().Ideation
	schedule.IntervalMinutes = 60
	base := time.Date(2026, 11, 1, 7, 15, 0, 0, time.UTC)
	day, slot, err := schedule.dueSlot(base)
	requireWorkflowSuccess(t, err)
	_, same, err := schedule.dueSlot(base.Add(25 * time.Minute))
	requireWorkflowSuccess(t, err)
	day2, next, err := schedule.dueSlot(base.Add(time.Hour))
	requireWorkflowSuccess(t, err)
	if day != day2 || slot != same || next == slot {
		t.Fatalf("day/slots are incorrect: %s %s %s %s %s", day, day2, slot, same, next)
	}
	for _, key := range []string{slot, next} {
		ctx := context.WithValue(context.Background(), ideationSlotKey{}, key)
		requireWorkflowSuccess(t, e.saveDirectionsForSchedule(ctx, task.ProjectID, day, nil))
		record, err := e.Store.Get(ctx, "schedules", fmt.Sprintf("%s:%s", task.ProjectID, key))
		requireWorkflowSuccess(t, err)
		if record.Version != 1 {
			t.Fatalf("two same-day slots overwrote one another: %v", record)
		}
	}
}
