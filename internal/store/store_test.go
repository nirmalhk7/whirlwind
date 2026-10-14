package store

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"whirlwind/internal/model"
)

// openTest opens a temporary SQLite store and registers cleanup with the test runner.
func openTest(t *testing.T) *Store {
	t.Helper()
	s, e := Open(filepath.Join(t.TempDir(), "state.db"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// TestIssueDeduplicationAndReplan verifies duplicate issue ingestion reuses a task, edits replan it, and label withdrawal pauses it.
func TestIssueDeduplicationAndReplan(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	p := model.Project{ID: "p", Name: "p", Repo: "owner/repo", Path: "/tmp/repo"}
	if e := s.Put(ctx, "projects", p.ID, p, 0); e != nil {
		t.Fatal(e)
	}
	issue := model.Issue{Number: 7, Title: "Add endpoint", Body: "first", UpdatedAt: "1", State: "OPEN", Labels: []string{"whirlwind:plan"}}
	a, e := s.IngestIssue(ctx, p, issue)
	if e != nil {
		t.Fatal(e)
	}
	b, e := s.IngestIssue(ctx, p, issue)
	if e != nil || a.ID != b.ID {
		t.Fatalf("duplicate: %+v %v", b, e)
	}
	issue.Body = "second"
	issue.UpdatedAt = "2"
	b, e = s.IngestIssue(ctx, p, issue)
	if e != nil || b.PlanVersion != 2 || b.Stage != "planning" {
		t.Fatalf("replan: %+v %v", b, e)
	}
	issue.Labels = nil
	b, e = s.IngestIssue(ctx, p, issue)
	if e != nil || b.Stage != "paused" {
		t.Fatalf("withdrawal: %+v %v", b, e)
	}
	events, e := s.Events(ctx, 0, 100)
	if e != nil || len(events) != 4 {
		t.Fatalf("events %d %v", len(events), e)
	}
}

// TestLeaseFencingAndConcurrentClaim verifies concurrent claims select one owner and only that owner can release the fenced lease.
func TestLeaseFencingAndConcurrentClaim(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	var wg sync.WaitGroup
	var mu sync.Mutex
	wins := 0
	var fence int64
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			l, e := s.Claim(ctx, "unit", "owner", time.Minute)
			if e == nil {
				mu.Lock()
				wins++
				fence = l.Fence
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if wins != 1 {
		t.Fatalf("writers=%d", wins)
	}
	if e := s.Release(ctx, "unit", "other", fence); e == nil {
		t.Fatal("foreign release accepted")
	}
	if e := s.Release(ctx, "unit", "owner", fence); e != nil {
		t.Fatal(e)
	}
	l, e := s.Claim(ctx, "unit", "next", time.Minute)
	if e != nil || l.Fence <= fence {
		t.Fatalf("fencing: %+v %v", l, e)
	}
	if e := s.Renew(ctx, "unit", "owner", fence, time.Minute); e == nil {
		t.Fatal("stale writer renewed")
	}
}

// TestOptimisticConcurrencyAndPersistence verifies stale writes fail and current resource state survives reopening SQLite.
func TestOptimisticConcurrencyAndPersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	s, e := Open(path)
	if e != nil {
		t.Fatal(e)
	}
	ctx := context.Background()
	if e = s.Put(ctx, "settings", "global", map[string]int{"slots": 4}, 0); e != nil {
		t.Fatal(e)
	}
	if e = s.Put(ctx, "settings", "global", map[string]int{"slots": 8}, 0); e == nil {
		t.Fatal("lost update accepted")
	}
	s.Close()
	s, e = Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	r, e := s.Get(ctx, "settings", "global")
	if e != nil || r.Version != 1 {
		t.Fatalf("reopen: %+v %v", r, e)
	}
}

// TestReplanInvalidatesOldWorktreeAndAcceptance verifies issue edits clear worktree and acceptance evidence for the next plan version.
func TestReplanInvalidatesOldWorktreeAndAcceptance(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	p := model.Project{ID: "p", Repo: "o/r"}
	issue := model.Issue{Number: 1, Body: "first", State: "OPEN", Labels: []string{"whirlwind:plan"}, UpdatedAt: "1"}
	task, e := s.IngestIssue(ctx, p, issue)
	if e != nil {
		t.Fatal(e)
	}
	task.Worktree = "old"
	task.Branch = "old"
	task.Base = "old"
	task.Approved = true
	task.ChecksPassed = true
	r, _ := s.Get(ctx, "tasks", task.ID)
	s.Put(ctx, "tasks", task.ID, task, r.Version)
	issue.Body = "updated"
	issue.UpdatedAt = "2"
	task, e = s.IngestIssue(ctx, p, issue)
	if e != nil || task.Worktree != "" || task.Base != "" || task.Branch != "" || task.ChecksPassed || task.Approved {
		t.Fatalf("stale state %+v %v", task, e)
	}
}

// TestExpiredLeaseRejectsOldFence verifies an expired lease cannot be renewed using its previous fencing token.
func TestExpiredLeaseRejectsOldFence(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	old, e := s.Claim(ctx, "w", "old", time.Millisecond)
	if e != nil {
		t.Fatal(e)
	}
	time.Sleep(3 * time.Millisecond)
	next, e := s.Claim(ctx, "w", "next", time.Minute)
	if e != nil || next.Fence <= old.Fence {
		t.Fatalf("%+v %v", next, e)
	}
	if s.Renew(ctx, "w", "old", old.Fence, time.Minute) == nil {
		t.Fatal("expired owner renewed")
	}
}

// TestOldWebhookCannotRevertNewerPlan verifies an older issue timestamp cannot overwrite the current plan snapshot.
func TestOldWebhookCannotRevertNewerPlan(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	p := model.Project{ID: "p", Repo: "o/r"}
	latest := model.Issue{Number: 2, Body: "new", State: "OPEN", Labels: []string{"whirlwind:plan"}, UpdatedAt: "2026-10-12T10:00:00Z"}
	task, e := s.IngestIssue(ctx, p, latest)
	if e != nil {
		t.Fatal(e)
	}
	old := latest
	old.Body = "old"
	old.UpdatedAt = "2026-10-12T09:00:00Z"
	task, e = s.IngestIssue(ctx, p, old)
	if e != nil || task.Issue.Body != "new" || task.PlanVersion != 1 {
		t.Fatalf("replayed old issue: %+v %v", task, e)
	}
}

// TestPlanIssueWebhookBindsExistingPlannerTask verifies a published plan marker binds its existing planner task instead of creating another.
func TestPlanIssueWebhookBindsExistingPlannerTask(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	p := model.Project{ID: "p", Repo: "o/r"}
	task := model.Task{ID: "direction-one", ProjectID: "p", PlanVersion: 1, Stage: "plan_ready", Plan: &model.Plan{Objective: "feature"}}
	s.Put(ctx, "tasks", task.ID, task, 0)
	issue := model.Issue{Number: 9, Body: "feature\n<!-- whirlwind-plan:direction-one -->", State: "OPEN", Labels: []string{"whirlwind:plan"}}
	bound, e := s.IngestIssue(ctx, p, issue)
	if e != nil || bound.ID != task.ID || bound.Plan == nil || bound.Stage != "coding" {
		t.Fatalf("planner duplicated: %+v %v", bound, e)
	}
}

// TestForeignKeyRejectsOrphanIssueIdentity verifies SQLite rejects repository/issue mappings without a stored task.
func TestForeignKeyRejectsOrphanIssueIdentity(t *testing.T) {
	s := openTest(t)
	if _, e := s.DB.Exec("INSERT INTO issue_tasks(repo,issue,task_id) VALUES('o/r',1,'missing')"); e == nil {
		t.Fatal("orphan task reference accepted")
	}
}

// TestRestoredEligibilityResumesEvenWithSameTimestamp verifies restoring an issue label resumes its paused stage even when the source timestamp is unchanged.
func TestRestoredEligibilityResumesEvenWithSameTimestamp(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	p := model.Project{ID: "p", Repo: "o/r"}
	issue := model.Issue{Number: 3, Body: "work", State: "OPEN", Labels: []string{"whirlwind:plan"}, UpdatedAt: "2026-10-12T10:00:00Z"}
	_, e := s.IngestIssue(ctx, p, issue)
	if e != nil {
		t.Fatal(e)
	}
	issue.Labels = nil
	s.IngestIssue(ctx, p, issue)
	issue.Labels = []string{"whirlwind:plan"}
	restored, e := s.IngestIssue(ctx, p, issue)
	if e != nil || restored.Stage != "planning" {
		t.Fatalf("eligibility not resumed: %+v %v", restored, e)
	}
}
