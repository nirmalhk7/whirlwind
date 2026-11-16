package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

func TestParityIdeaUpdateRejectsNonDraftWithoutLaunchingACP(t *testing.T) {
	s := setup(t)
	ctx := context.Background()
	if err := s.Store.Put(ctx, "projects", "p", model.Project{ID: "p", Name: "Project", Path: "/work/project"}, 0); err != nil {
		t.Fatal(err)
	}
	if err := s.Store.Put(ctx, "operations", "source", model.Operation{Kind: "ideation", Subject: "p", Stage: "complete", StartedAt: "now"}, 0); err != nil {
		t.Fatal(err)
	}
	i := model.Idea{Title: "Invoice export", ID: "idea", ProjectID: "p", RunID: "source", Direction: "expand", What: "Export invoices", Why: "Reconcile totals", Where: "billing", HowItHelps: "Keep monthly records", NoveltyMethod: "legacy_llm", DecisionStatus: "draft"}
	if err := s.Store.Put(ctx, "ideas", i.ID, i, 0); err != nil {
		t.Fatal(err)
	}
	i.DecisionStatus = "approved"
	if err := s.Store.Put(ctx, "ideas", i.ID, i, 1); err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(model.IdeaDraft{What: "Different content", Why: i.Why, Where: i.Where, HowItHelps: i.HowItHelps})
	_, err := s.ExecuteLocal(ctx, LocalCommand{Operation: "idea.update", ResourceID: i.ID, ProjectID: i.ProjectID, ExpectedVersion: 2, IdempotencyKey: "edit-approved", Payload: payload})
	if !errors.Is(err, store.ErrConflict) {
		t.Fatalf("read-only idea update was not versioned domain rejection: %v", err)
	}
	sessions, err := s.Store.List(ctx, "sessions")
	if err != nil || len(sessions) != 0 {
		t.Fatalf("read-only edit launched a model: %d %v", len(sessions), err)
	}
}
