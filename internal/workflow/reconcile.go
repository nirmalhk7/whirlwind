package workflow

import (
	"context"
	"log"
	"strings"
	"time"
	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

// Reconcile leases GitHub reconciliation, ingests eligible or withdrawn issues, and records terminal PR decisions.
func (e *Engine) Reconcile(ctx context.Context) {
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
	issues, err := e.GitHub.Issues(ctx, p.Repo)
	if err != nil {
		_ = e.Store.Notify(ctx, "", "github", err.Error())
		return
	}
	seen := map[int]bool{}
	for _, issue := range issues {
		seen[issue.Number] = true
		_, _ = e.Ingest(ctx, p, issue)
	}
	tasks, _ := e.Store.List(ctx, "tasks")
	for _, record := range tasks {
		task, _ := store.Decode[model.Task](record)
		if task.ProjectID == p.ID {
			e.reconcileTask(ctx, p, task, record.Version, seen)
		}
	}
}
func (e *Engine) reconcileTask(ctx context.Context, p model.Project, t model.Task, version int, seen map[int]bool) {
	if t.Issue.Number > 0 && !seen[t.Issue.Number] {
		issue, err := e.GitHub.Issue(ctx, p.Repo, t.Issue.Number)
		if err == nil && t.Stage != "pr_created" && t.Stage != "complete" {
			_, _ = e.Ingest(ctx, p, issue)
		}
	}
	if t.PR != "" && t.Stage != "complete" {
		e.reconcilePR(ctx, p, t, version)
	}
}
func (e *Engine) reconcilePR(ctx context.Context, p model.Project, t model.Task, version int) {
	decision, err := e.GitHub.PRState(ctx, p.Repo, t.PR)
	if err != nil || (decision != "MERGED" && decision != "CLOSED") {
		return
	}
	t.Stage = "complete"
	if err := e.Store.Write(ctx, []store.Change{{Kind: "tasks", ID: t.ID, Value: t, Expected: version}, {Kind: "outcomes", ID: store.ID(), Value: map[string]any{"task": t.ID, "pr": t.PR, "human_decision": strings.ToLower(decision), "observed_at": time.Now().UTC().Format(time.RFC3339Nano)}}}, nil); err != nil {
		log.Printf("persist PR outcome: %v", err)
	}
}

// Ingest cancels tasks affected by issue changes before delegating deduplication and replanning to SQLite storage.
func (e *Engine) Ingest(ctx context.Context, p model.Project, i model.Issue) (model.Task, error) {
	records, _ := e.Store.List(ctx, "tasks")
	for _, record := range records {
		task, _ := store.Decode[model.Task](record)
		if affectedByIssue(task, p, i) {
			e.Stop(task.ID)
		}
	}
	return e.Store.IngestIssue(ctx, p, i)
}
func affectedByIssue(t model.Task, p model.Project, i model.Issue) bool {
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
