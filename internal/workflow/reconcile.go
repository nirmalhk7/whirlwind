package workflow

import (
	"context"
	"errors"
	"log"
	"time"
	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

// Reconcile restores owned runtimes and eligible issue state under a supervisor lease.
func (e *Engine) Reconcile(ctx context.Context) {
	if err := e.reconcileStandaloneRuntimes(ctx); err != nil {
		log.Printf("reconcile standalone sandbox runtimes: %v", err)
		return
	}
	if err := e.reconcileRuntimeJobs(ctx, runtimeCheckOwner{}); err != nil {
		log.Printf("reconcile interrupted check runtimes: %v", err)
	}
	lease, err := e.Store.Claim(ctx, "reconcile", e.owner, 4*time.Minute)
	if err != nil {
		return
	}
	defer func() { _ = e.Store.Release(context.Background(), "reconcile", e.owner, lease.Fence) }()
	projects, _ := e.Store.List(ctx, "projects")
	for _, record := range projects {
		p, err := store.Decode[model.Project](record)
		if err == nil {
			e.reconcileProject(ctx, p)
		}
	}
}
func (e *Engine) reconcileProject(ctx context.Context, p model.Project) {
	if e.Agentic(ctx) {
		e.reconcileAgenticProject(ctx, p)
		return
	}
	issues, err := e.GitHub.Issues(ctx, p.Repo)
	if err != nil {
		if ctx.Err() == nil {
			_ = e.Store.Notify(ctx, "", "github", connectionFailureMessage(p, err))
		}
		return
	}
	if err := e.resolveProjectNotices(ctx, p, "github"); err != nil {
		log.Printf("resolve GitHub connection notice: %v", err)
	}
	seen := map[int]bool{}
	for _, issue := range issues {
		seen[issue.Number] = true
		_, _ = e.Ingest(ctx, p, issue)
	}
	tasks, _ := e.Store.List(ctx, "execution_plans")
	for _, record := range tasks {
		task, _ := store.Decode[model.ExecutionPlan](record)
		if task.ProjectID == p.ID {
			e.reconcileTask(ctx, p, task, record.Version, seen)
		}
	}
}
func (e *Engine) reconcileTask(ctx context.Context, p model.Project, t model.ExecutionPlan, version int, seen map[int]bool) {
	if t.Cancelled() {
		return
	}
	if t.Issue.Number > 0 && !seen[t.Issue.Number] {
		matches, err := e.Store.IssueBoundToPlan(ctx, p.Repo, t.Issue.Number, t.ID)
		if err != nil || !matches {
			return
		}
		issue, err := e.GitHub.Issue(ctx, p.Repo, t.Issue.Number)
		if err == nil && t.Stage != "pr_created" && t.Stage != "complete" {
			_, _ = e.Ingest(ctx, p, issue)
		}
	}
	// Existing PR values remain historical data. Canonical runtime no longer
	// polls or changes remote PR state after local delivery.
}

// Ingest cancels tasks affected by issue changes before delegating deduplication and replanning to SQLite storage.
func (e *Engine) Ingest(ctx context.Context, p model.Project, i model.Issue) (model.ExecutionPlan, error) {
	if e.Agentic(ctx) {
		return model.ExecutionPlan{}, errors.New("ready issue intake is disabled in Agentic mode")
	}
	records, _ := e.Store.List(ctx, "execution_plans")
	for _, record := range records {
		task, _ := store.Decode[model.ExecutionPlan](record)
		if affectedByIssue(task, p, i) {
			matches, err := e.Store.IssueBoundToPlan(ctx, p.Repo, i.Number, task.ID)
			if err != nil {
				return model.ExecutionPlan{}, err
			}
			if matches {
				e.Stop(task.ID)
			}
		}
	}
	return e.Store.IngestIssue(ctx, p, i)
}
func affectedByIssue(t model.ExecutionPlan, p model.Project, i model.Issue) bool {
	incoming, ix := time.Parse(time.RFC3339, i.UpdatedAt)
	existing, ex := time.Parse(time.RFC3339, t.Issue.UpdatedAt)
	if ix == nil && ex == nil && incoming.Before(existing) {
		return false
	}
	contentMatches := t.Issue.Body == i.Body && t.Issue.Title == i.Title
	if t.EligibilityOverride && !i.Eligible() && contentMatches {
		return false
	}
	if t.ProjectID != p.ID || t.Issue.Number != i.Number {
		return false
	}
	return !i.Eligible() || !contentMatches || t.Issue.UpdatedAt != i.UpdatedAt
}
