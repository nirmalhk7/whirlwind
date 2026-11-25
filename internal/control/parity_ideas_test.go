package control

import (
	"context"
	"path/filepath"
	"testing"

	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

func TestParityInspectionIncludesIdeasAndTheirSourceRuns(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	ctx := context.Background()
	if err := db.Put(ctx, "operations", "source", model.Operation{Kind: "ideation", Subject: "p", Stage: "complete", StartedAt: "now"}, 0); err != nil {
		t.Fatal(err)
	}
	if err := db.Put(ctx, "projects", "p", model.Project{ID: "p", Name: "Project", Path: "/work/project"}, 0); err != nil {
		t.Fatal(err)
	}
	i := model.Idea{Title: "Invoice export", ID: "idea", ProjectID: "p", RunID: "source", Direction: "expand", What: "Export", Why: "Reconcile", Where: "billing", HowItHelps: "Retain records", DecisionStatus: "draft", NoveltyMethod: "legacy_llm"}
	if err := db.Put(ctx, "ideas", i.ID, i, 0); err != nil {
		t.Fatal(err)
	}
	snapshot, err := Inspect(ctx, db)
	if err != nil || len(snapshot["ideas"]) != 1 || len(snapshot["operations"]) != 1 {
		t.Fatalf("registry/provenance missing from inspection: %v", err)
	}
	dispatcher := SupervisorDispatcher{DB: db.DB}
	response, err := dispatcher.Handle(ctx, Request{RequestID: "ideas", Operation: "inspect.records", ResourceID: "ideas", ProjectID: "p", Limit: 100})
	if err != nil {
		t.Fatalf("online registry inspection unavailable: %v", err)
	}
	page, ok := response.Result.(RecordPage)
	if !ok || len(page.Records) != 1 || page.Records[0].ID != i.ID {
		t.Fatalf("registry page missing: %+v", response)
	}
}
