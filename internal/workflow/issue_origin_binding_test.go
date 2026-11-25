package workflow

import (
	"context"
	"testing"

	"whirlwind/internal/model"
)

type originIssueSpy struct {
	fakeGitHub
	polled int
}

func (g *originIssueSpy) Issue(context.Context, string, int) (model.Issue, error) {
	g.polled++
	return model.Issue{}, nil
}

func TestReplacementOriginCannotReconcileOrCancelOldOriginIssue(t *testing.T) {
	e, prior, _ := testEngine(t)
	ctx := t.Context()
	p, err := e.Project(ctx, prior.ProjectID)
	requireWorkflowSuccess(t, err)
	before, err := e.Store.Get(ctx, "execution_plans", prior.ID)
	requireWorkflowSuccess(t, err)
	p.Repo, p.RemoteURL = "https://github.com/example/replacement", "https://github.com/example/replacement"
	spy := &originIssueSpy{}
	e.GitHub = spy
	e.reconcileProject(ctx, p)
	if spy.polled != 0 {
		t.Fatal("reconciliation queried the replacement origin for an old-origin issue")
	}
	stopped := false
	e.running[prior.ID] = func() { stopped = true }
	incoming := model.Issue{Number: prior.Issue.Number, Title: "Unrelated same-number issue", State: "OPEN", Labels: []string{"whirlwind:ready"}}
	created, err := e.Ingest(ctx, p, incoming)
	requireWorkflowSuccess(t, err)
	if created.ID == prior.ID || stopped {
		t.Fatalf("replacement origin reused or cancelled old-origin plan %q: created=%q stopped=%t", prior.ID, created.ID, stopped)
	}
	originalVersion, err := e.Store.Get(ctx, "execution_plans", prior.ID)
	requireWorkflowSuccess(t, err)
	if originalVersion.Version != before.Version || string(originalVersion.Data) != string(before.Data) {
		t.Fatal("old-origin plan history was modified")
	}
	e.reconcileProject(ctx, p)
	if spy.polled != 1 {
		t.Fatalf("expected status lookup only for the replacement's own issue, got %d calls", spy.polled)
	}
	restartedOwn := false
	e.running[created.ID] = func() { restartedOwn = true }
	incoming.Title = "Edited issue in the replacement origin"
	incoming.UpdatedAt = "2026-11-24T09:00:00Z"
	reused, err := e.Ingest(ctx, p, incoming)
	requireWorkflowSuccess(t, err)
	if reused.ID != created.ID || !restartedOwn || stopped {
		t.Fatalf("valid same-repository update failed to reset only its own plan: id=%q own=%t old=%t", reused.ID, restartedOwn, stopped)
	}
}
