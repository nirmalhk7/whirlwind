package httpapi

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestLocalPlanInspectionReturnsSavedMarkdown(t *testing.T) {
	s, plan := workflowLocalPlan(t)
	response, err := s.ExecuteLocal(context.Background(), LocalCommand{Operation: "inspect.plan", PlanID: plan.ID})
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		Content string `json:"content"`
		Hash    string `json:"hash"`
	}
	if err := json.Unmarshal(response, &document); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(document.Content, "# ") || document.Hash == "" {
		t.Fatalf("inspection omitted Markdown or content hash: %s", response)
	}
}

func TestParityPlanDocumentCommandsSaveAndReplayActualContent(t *testing.T) {
	s, plan := workflowLocalPlan(t)
	ctx := context.Background()
	view, err := s.Engine.ReadLocalPlan(ctx, plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	edited := strings.Replace(view.Content, "# ", "# Revised ", 1)
	payload, _ := json.Marshal(map[string]string{"content": edited})
	command := LocalCommand{Operation: "plan.document.save", PlanID: plan.ID, ProjectID: plan.ProjectID, ExpectedVersion: 1, ContentHash: view.Hash, IdempotencyKey: "save", Payload: payload}
	first, err := s.ExecuteLocal(ctx, command)
	if err != nil {
		t.Fatalf("edited Markdown save unavailable: %v", err)
	}
	second, err := s.ExecuteLocal(ctx, command)
	if err != nil || string(first) != string(second) {
		t.Fatalf("save receipt changed: %v", err)
	}
	after, err := s.Engine.ReadLocalPlan(ctx, plan.ID)
	if err != nil || after.Content != edited || after.ApprovedHash != "" {
		t.Fatalf("document was not saved independently: %+v %v", after, err)
	}
	command.PlanID = "another"
	if _, err := s.ExecuteLocal(ctx, command); err == nil {
		t.Fatal("receipt replayed across different plans")
	}
	if record, err := s.Store.Get(ctx, "execution_plans", plan.ID); err != nil || record.Version != 2 {
		t.Fatalf("replay changed resource: %+v %v", record, err)
	}
}
