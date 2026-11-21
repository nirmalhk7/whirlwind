package workflow

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"whirlwind/internal/model"
	"whirlwind/internal/provider"
	"whirlwind/internal/rolequeue"
	"whirlwind/internal/store"
)

type roleQueueFixture struct {
	proposed map[rolequeue.Role][]rolequeue.Delivery
	fail     bool
	override *rolequeue.Delivery
}

func (q *roleQueueFixture) Head(_ context.Context, role rolequeue.Role, deliveries []rolequeue.Delivery) (rolequeue.Delivery, bool, error) {
	q.proposed[role] = deliveries
	if q.fail {
		return rolequeue.Delivery{}, false, errors.New("fixture Redis unavailable")
	}
	if q.override != nil {
		return *q.override, true, nil
	}
	if len(deliveries) == 0 {
		return rolequeue.Delivery{}, false, nil
	}
	return deliveries[0], true, nil
}

func TestQueuedApprovalBlocksDispatchDuringOutageAndReplaysOnce(t *testing.T) {
	ctx := context.Background()
	e, idea := approvedIdeaPlannerFixture(t)
	transport := &roleQueueFixture{proposed: make(map[rolequeue.Role][]rolequeue.Delivery), fail: true}
	e.UseRoleQueues(transport)
	var launches atomic.Int32
	e.Adapter = func(model.Provider) provider.Adapter { return markdownPlannerAdapter(t, &launches) }
	e.dispatch(ctx)
	e.background.Wait()
	if launches.Load() != 0 || len(transport.proposed[rolequeue.Planner]) != 1 {
		t.Fatalf("Redis outage bypassed queue or lost approved idea: launches=%d queues=%v", launches.Load(), transport.proposed)
	}
	old := transport.proposed[rolequeue.Planner][0]
	transport.fail = false
	e.dispatch(ctx)
	e.background.Wait()
	current, _ := localStoredTask(t, e, old.ID)
	if launches.Load() != 1 || current.Stage != "plan_approval" {
		t.Fatalf("approval handoff did not run once: launches=%d stage=%q", launches.Load(), current.Stage)
	}
	transport.override = &old
	e.dispatch(ctx)
	e.background.Wait()
	if launches.Load() != 1 || len(transport.proposed[rolequeue.Coding]) != 0 {
		t.Fatal("replayed planner delivery started an agent or bypassed the plan-approval gate")
	}
	if _, err := e.prepareDeliveredStep(ctx, old.ID, &old); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("stale delivery passed the leased revision check: %v (idea %s)", err, idea.ID)
	}
}

func TestRoleRoutingUsesCommittedApprovalReviewAndChecks(t *testing.T) {
	task := model.ExecutionPlan{
		ID: "p", ProjectID: "project", PlanVersion: 1,
		Plan: &model.PlanSpecification{}, PlanDocument: &model.PlanDocument{ApprovedHash: "hash"},
		ReviewID: "review", VerificationID: "verify", ReviewedCommit: "commit", Base: "base", ChecksPassed: true,
		Score: 80.01, Approved: true,
		Approval: &model.Provenance{Task: "p", PlanVersion: 1, ReviewID: "review", Head: "commit", Base: "base", PolicyHash: "policy"},
	}
	for _, tt := range []struct {
		stage  string
		role   rolequeue.Role
		mutate func(*model.ExecutionPlan)
	}{
		{"planning", rolequeue.Planner, nil},
		{"plan_approval", "", nil},
		{"coding", rolequeue.Coding, nil},
		{"coding", "", func(t *model.ExecutionPlan) { t.PlanDocument = nil }},
		{"repair", rolequeue.Coding, nil},
		{"review", rolequeue.Reviewer, nil},
		{"review", "", func(t *model.ExecutionPlan) { t.PlanDocument = nil }},
		{"approval", "", nil},
		{"submitting", rolequeue.Finalizer, nil},
		{"submitting", "", func(t *model.ExecutionPlan) { t.ChecksPassed = false }},
		{"submitting", "", func(t *model.ExecutionPlan) { t.Score = 80 }},
		{"submitting", "", func(t *model.ExecutionPlan) { t.ReviewID = "" }},
		{"submitting", "", func(t *model.ExecutionPlan) { t.Risk, t.Approval = "critical", nil }},
		{"submitting", rolequeue.Finalizer, func(t *model.ExecutionPlan) { t.Risk = "critical" }},
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
		if actual := queuedRole(candidate, "policy"); actual != tt.role {
			t.Errorf("stage %s: got %s, want %s", tt.stage, actual, tt.role)
		}
	}
}

func TestIdeaPlanningThroughRedis(t *testing.T) {
	redisURL := os.Getenv("WHIRLWIND_TEST_REDIS_URL")
	if redisURL == "" {
		t.Skip("set WHIRLWIND_TEST_REDIS_URL to an isolated Redis instance")
	}
	ctx := context.Background()
	e, idea := approvedIdeaPlannerFixture(t, cleanQltyFixtureCheck(t))
	q, err := rolequeue.Open(redisURL, "whirlwind-workflow-test:"+store.ID())
	requireWorkflowSuccess(t, err)
	defer func() { _ = q.Close() }()
	e.UseRoleQueues(q)
	var launches atomic.Int32
	e.Adapter = func(model.Provider) provider.Adapter { return markdownPlannerAdapter(t, &launches) }
	for range 3 {
		e.dispatch(ctx)
		e.background.Wait()
	}
	if launches.Load() != 1 {
		t.Fatalf("wanted 1 planner launch from the Redis head, got %d", launches.Load())
	}
	ideaRecord, err := e.Store.Get(ctx, "ideas", idea.ID)
	requireWorkflowSuccess(t, err)
	saved, err := store.Decode[model.Idea](ideaRecord)
	requireWorkflowSuccess(t, err)
	pending, version := localStoredTask(t, e, saved.PlanID)
	if pending.Stage != "plan_approval" {
		t.Fatalf("expected independent plan approval, got %s", pending.Stage)
	}
	if err := e.Step(ctx, saved.PlanID); err == nil {
		t.Fatal("manual Step bypassed the configured Redis transport")
	}
	requireWorkflowSuccess(t, e.ReviewLocalPlan(ctx, saved.PlanID, version, "approve", pending.PlanDocument.Hash, ""))
	records, err := e.Store.List(ctx, "execution_plans")
	requireWorkflowSuccess(t, err)
	coding := e.collectRoleCandidates(ctx, records)[rolequeue.Coding]
	if len(coding) != 1 {
		t.Fatalf("confirmed plan not eligible for coding queue: %+v", coding)
	}
	delivery, ok, err := q.Head(ctx, rolequeue.Coding, []rolequeue.Delivery{coding[0].delivery})
	requireWorkflowSuccess(t, err)
	if !ok || delivery.ID != saved.PlanID || delivery.Stage != "coding" {
		t.Fatalf("wrong confirmed-plan delivery in Redis: %+v", delivery)
	}
	_, _, err = q.Head(ctx, rolequeue.Coding, nil)
	requireWorkflowSuccess(t, err)
}

func TestIdeationIntervalUsesDurableDistinctSlots(t *testing.T) {
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
