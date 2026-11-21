package store

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"whirlwind/internal/model"
	"whirlwind/internal/taskqueue"
)

func TestNativeRoleTasksFIFOConcurrentPopAndRestart(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "state.db")
	s, err := Open(path)
	requireQueueSuccess(t, err)
	project := model.Project{ID: "queue-project", Path: "/queue-project", PolicyHash: "policy"}
	requireQueueSuccess(t, s.Put(ctx, "projects", project.ID, project, 0))
	first, second := queueFixturePlan("first", project.ID), queueFixturePlan("second", project.ID)
	requireQueueSuccess(t, s.Put(ctx, "execution_plans", first.ID, first, 0))
	requireQueueSuccess(t, s.Put(ctx, "execution_plans", second.ID, second, 0))
	role := taskqueue.Planner
	requireQueueSuccess(t, s.PushRoleTasks(ctx, role, taskqueue.Push{PlanID: second.ID, Version: 1}))
	if d, err := s.PopRoleTask(ctx, role); d != nil || err != nil {
		t.Fatalf("a targeted push overtook earlier staged data: %+v %v", d, err)
	}
	requireQueueSuccess(t, s.PushRoleTasks(ctx, role, taskqueue.Push{}))
	popped, errs := make(chan *taskqueue.Delivery, 12), make(chan error, 12)
	var wg sync.WaitGroup
	for range 12 {
		wg.Go(func() {
			d, err := s.PopRoleTask(ctx, role)
			errs <- err
			if d != nil {
				popped <- d
			}
		})
	}
	wg.Wait()
	close(popped)
	close(errs)
	for err := range errs {
		requireQueueSuccess(t, err)
	}
	old := <-popped
	if old == nil || old.PlanID != first.ID || len(popped) != 0 {
		t.Fatalf("concurrent consumers duplicated or misordered the head: %v, pending=%d", old, len(popped))
	}
	requireQueueSuccess(t, s.Close())
	s, err = Open(path)
	requireQueueSuccess(t, err)
	defer func() { _ = s.Close() }()
	requireQueueSuccess(t, s.BackfillRoleTasks(ctx))
	if d, err := s.PopRoleTask(ctx, role); d != nil || err != nil {
		t.Fatalf("restart duplicated live work: %+v %v", d, err)
	}
	_, err = s.DB.Exec("UPDATE role_tasks SET expires_at=? WHERE id=?", time.Now().Add(-time.Second).UnixMilli(), old.ID)
	requireQueueSuccess(t, err)
	replayed, err := s.PopRoleTask(ctx, role)
	requireQueueSuccess(t, err)
	if replayed == nil || replayed.ID != old.ID || replayed.Token == old.Token {
		t.Fatalf("expired work was not reclaimed with a fresh lease: %+v", replayed)
	}
	for _, action := range []string{"ack", "renew", "release"} {
		if err := s.SettleRoleTask(ctx, role, old.Receipt, action); !errors.Is(err, taskqueue.ErrConflict) {
			t.Fatalf("stale token could %s: %v", action, err)
		}
	}
	if err := s.SettleRoleTask(ctx, role, replayed.Receipt, "ack"); !errors.Is(err, taskqueue.ErrConflict) {
		t.Fatalf("ack discarded current work: %v", err)
	}
	first.Stage = "plan_approval"
	requireQueueSuccess(t, s.Put(ctx, "execution_plans", first.ID, first, 1))
	requireQueueSuccess(t, s.SettleRoleTask(ctx, role, replayed.Receipt, "ack"))
	if err := s.PushRoleTasks(ctx, role, taskqueue.Push{PlanID: first.ID, Version: 1}); !errors.Is(err, taskqueue.ErrConflict) {
		t.Fatalf("stale push did not fail: %v", err)
	}
	d, err := s.PopRoleTask(ctx, role)
	requireQueueSuccess(t, err)
	if d == nil || d.PlanID != second.ID {
		t.Fatalf("next item missing: %+v", d)
	}
}

func TestNativeRoleTasksBackfillPreservesCurrentStateAndPause(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "state.db")
	s, err := Open(path)
	requireQueueSuccess(t, err)
	p := model.Project{ID: "project", Path: "/existing", PolicyHash: "hash", Paused: true}
	requireQueueSuccess(t, s.Put(ctx, "projects", p.ID, p, 0))
	item := queueFixturePlan("plan", p.ID)
	requireQueueSuccess(t, s.Put(ctx, "execution_plans", item.ID, item, 0))
	// Emulate a current pre-queue schema omitting only the additive queue schema.
	_, err = s.DB.Exec("DROP TRIGGER role_tasks_delete_plan; DROP TABLE role_tasks")
	requireQueueSuccess(t, err)
	requireQueueSuccess(t, s.Close())
	s, err = Open(path)
	requireQueueSuccess(t, err)
	defer func() { _ = s.Close() }()
	requireQueueSuccess(t, s.BackfillRoleTasks(ctx))
	requireQueueSuccess(t, s.PushRoleTasks(ctx, taskqueue.Planner, taskqueue.Push{}))
	if d, err := s.PopRoleTask(ctx, taskqueue.Planner); d != nil || err != nil {
		t.Fatalf("paused project popped: %+v %v", d, err)
	}
	p.Paused = false
	requireQueueSuccess(t, s.Put(ctx, "projects", p.ID, p, 1))
	requireQueueSuccess(t, s.Put(ctx, "settings", "global", map[string]bool{"stopped": true}, 0))
	if d, err := s.PopRoleTask(ctx, taskqueue.Planner); d != nil || err != nil {
		t.Fatalf("globally stopped workflow popped: %+v %v", d, err)
	}
	requireQueueSuccess(t, s.Put(ctx, "settings", "global", map[string]bool{"stopped": false}, 1))
	d, err := s.PopRoleTask(ctx, taskqueue.Planner)
	requireQueueSuccess(t, err)
	if d == nil || d.PlanID != item.ID || d.Version != 1 {
		t.Fatalf("old plan was not restored on resume: %+v", d)
	}
}

func TestNativeRoleTasksRetireOutdatedCriticalApprovalHead(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	for i, hash := range []string{"current", "other"} {
		project := model.Project{ID: fmt.Sprintf("project-%d", i), Path: fmt.Sprintf("/queue-%d", i), PolicyHash: hash}
		requireQueueSuccess(t, s.Put(ctx, "projects", project.ID, project, 0))
		p := queueFixturePlan(fmt.Sprintf("delivery-%d", i), project.ID)
		p.Stage, p.Risk, p.Approved = "submitting", "critical", true
		p.Plan = &model.PlanSpecification{}
		p.PlanDocument = &model.PlanDocument{ApprovedHash: "frozen"}
		p.Score, p.ChecksPassed = 81, true
		p.Base, p.ReviewedCommit, p.ReviewID, p.VerificationID = "base", "commit", "review", "verification"
		p.Approval = &model.Provenance{Task: p.ID, Head: p.ReviewedCommit, Base: p.Base, PlanVersion: 1, PolicyHash: hash, ReviewID: p.ReviewID}
		requireQueueSuccess(t, s.Put(ctx, "execution_plans", p.ID, p, 0))
		if i == 0 {
			project.PolicyHash = "changed"
			requireQueueSuccess(t, s.Put(ctx, "projects", project.ID, project, 1))
		}
	}
	requireQueueSuccess(t, s.PushRoleTasks(ctx, taskqueue.Finalizer, taskqueue.Push{}))
	d, err := s.PopRoleTask(ctx, taskqueue.Finalizer)
	requireQueueSuccess(t, err)
	if d == nil || d.PlanID != "delivery-1" {
		t.Fatalf("outdated critical approval stalled or bypassed the queue: %+v", d)
	}
}

func queueFixturePlan(id, project string) model.ExecutionPlan {
	return model.ExecutionPlan{ID: id, ProjectID: project, Stage: "planning", PlanVersion: 1,
		Selection: &model.SelectionAuthorization{CandidateID: "approved", SelectedAt: time.Now().Format(time.RFC3339)}}
}

func requireQueueSuccess(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
