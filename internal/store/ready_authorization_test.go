package store

import (
	"context"
	"path/filepath"
	"testing"
	"whirlwind/internal/model"
)

const (
	authorizationStateDB    = "state.db"
	authorizationRepository = "owner/repo"
	readyIssueLabel         = "whirlwind:ready"
	storeCloseError         = "close store: %v"
)

func TestReadyLabelAuthorizesWithoutInboxSelection(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), authorizationStateDB))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Errorf(storeCloseError, err)
		}
	})
	task, err := s.IngestIssue(context.Background(), model.Project{ID: "p", Repo: authorizationRepository, PolicyHash: "policy"}, model.Issue{Number: 7, Title: "Fix search", Body: "Guard stale responses", State: "OPEN", Labels: []string{readyIssueLabel}})
	if err != nil {
		t.Fatal(err)
	}
	if !task.HasSelection() || task.Stage != "planning" {
		t.Fatalf("ready task held for selection: %+v", task)
	}
}

func TestReadyIssueRevisionKeepsAuthorizationAndReplacesPlan(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), authorizationStateDB))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Errorf(storeCloseError, err)
		}
	})
	ctx := context.Background()
	p := model.Project{ID: "p", Repo: authorizationRepository, PolicyHash: "policy"}
	issue := model.Issue{Number: 7, Title: "Fix search", Body: "Old behavior", State: "OPEN", Labels: []string{readyIssueLabel}}
	first, err := s.IngestIssue(ctx, p, issue)
	if err != nil {
		t.Fatal(err)
	}
	issue.Body = "New authoritative behavior"
	next, err := s.IngestIssue(ctx, p, issue)
	if err != nil {
		t.Fatal(err)
	}
	if next.ID != first.ID || next.Stage != "planning" || !next.HasSelection() || next.Selection.SourceFingerprint != model.IssueFingerprint(issue) {
		t.Fatalf("updated ready issue held for selection: %+v", next)
	}
}

func TestCancelledIssueCannotRestartFromOlderChangedWebhook(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), authorizationStateDB))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Errorf(storeCloseError, err)
		}
	})
	ctx := context.Background()
	p := model.Project{ID: "p", Repo: authorizationRepository}
	issue := model.Issue{Number: 7, Title: "Fix", Body: "Current", UpdatedAt: "2026-10-22T20:00:00Z", State: "OPEN", Labels: []string{readyIssueLabel}}
	task, err := s.IngestIssue(ctx, p, issue)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.ActivateMode(ctx, model.AgenticMode); err != nil {
		t.Fatal(err)
	}
	issue.Body = "Old content"
	issue.UpdatedAt = "2026-10-22T19:00:00Z"
	next, err := s.IngestIssue(ctx, p, issue)
	if err != nil {
		t.Fatal(err)
	}
	if next.ID != task.ID || !next.Cancelled() {
		t.Fatalf("older source resurrected cancelled task: %+v", next)
	}
	issue.Body = "New content"
	issue.UpdatedAt = "2026-10-22T21:00:00Z"
	next, err = s.IngestIssue(ctx, p, issue)
	if err != nil {
		t.Fatal(err)
	}
	if next.ID == task.ID || next.Cancelled() {
		t.Fatal("new source revision cannot create new work")
	}
}
