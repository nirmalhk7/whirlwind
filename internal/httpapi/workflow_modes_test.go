package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"whirlwind/internal/model"
	"whirlwind/internal/provider"
	"whirlwind/internal/settingsfile"
	"whirlwind/internal/store"
	"whirlwind/internal/workflow"
)

const (
	workflowResultPath        = "result.txt"
	workflowPlanObjective     = "Improve result"
	workflowPlanChange        = "Change result.txt"
	workflowRevisedPlanChange = "Change result.txt and cover retries"
	workflowPlanDocumentRoute = "tasks/task/plan-document"
	workflowPlanReviewRoute   = "tasks/task/plan-review"
	workflowInventoryRoute    = "providers/native/integrations"
)

// workflowModeRequest exercises the complete local control and mutation boundary.
func workflowModeRequest(t *testing.T, s *Server, method, path, version string, body any) *httptest.ResponseRecorder {
	t.Helper()
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(method, "http://localhost/api/v1/"+path, strings.NewReader(string(encoded)))
	if method != "GET" {
		r.Header.Set(idempotencyKeyHeader, store.ID())
		r.Header.Set(ifMatchHeader, version)
	}
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	return w
}

func TestWorkflowModeCannotChangeThroughSettingsAPI(t *testing.T) {
	for _, field := range []string{"mode", "active_mode", "configured_mode", "restart_required"} {
		t.Run(field, func(t *testing.T) {
			s := setup(t)
			w := workflowModeRequest(t, s, "PATCH", "settings", "0", map[string]any{field: "agentic"})
			if w.Code != 422 || !strings.Contains(w.Body.String(), "mode_requires_restart") {
				t.Fatalf("mode mutation accepted: %d %s", w.Code, w.Body)
			}
			if _, err := s.Store.Get(context.Background(), "settings", "global"); !errors.Is(err, store.ErrNotFound) {
				t.Fatalf("rejected mode mutation persisted settings: %v", err)
			}
		})
	}
}

func TestWorkflowModeEditorRejectsChangeAndDiskEditWaitsForRestart(t *testing.T) {
	s := setup(t)
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "settings.yaml")
	original := "mode: personal\nversion: 1\nworkers: {global_slots: 4}\nproviders: []\nagents: {}\n"
	if err := os.WriteFile(path, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	if err := settingsfile.Attach(ctx, s.Store, path); err != nil {
		t.Fatal(err)
	}
	w := workflowModeRequest(t, s, "GET", "settings/file?view=file", "", nil)
	var file settingsfile.FileView
	if err := json.Unmarshal(w.Body.Bytes(), &file); err != nil || w.Code != 200 {
		t.Fatalf("read settings: %d %s %v", w.Code, w.Body, err)
	}
	next := strings.Replace(original, "personal", "agentic", 1)
	w = workflowModeRequest(t, s, "PATCH", "settings/file", "", map[string]string{"content": next, "revision": file.Revision})
	if w.Code != 422 {
		t.Fatalf("editor changed mode: %d %s", w.Code, w.Body)
	}
	actual, err := os.ReadFile(path)
	if err != nil || string(actual) != original {
		t.Fatalf("rejected editor write changed disk: %s %v", actual, err)
	}
	if err := os.WriteFile(path, []byte(next), 0600); err != nil {
		t.Fatal(err)
	}
	assertWorkflowModes(t, s, "personal", "agentic", true)
	settings, err := s.Store.Get(ctx, "settings", "global")
	if err != nil {
		t.Fatal(err)
	}
	w = workflowModeRequest(t, s, "PATCH", "settings", strconv.Itoa(settings.Version), map[string]int{"global_slots": 7})
	if w.Code != 200 {
		t.Fatalf("unrelated save: %d %s", w.Code, w.Body)
	}
	actual, err = os.ReadFile(path)
	if err != nil || !strings.Contains(string(actual), "mode: agentic") || !strings.Contains(string(actual), "global_slots: 7") {
		t.Fatalf("unrelated save lost pending mode: %s %v", actual, err)
	}
	assertWorkflowModes(t, s, "personal", "agentic", true)
	if err := settingsfile.Attach(ctx, s.Store, path); err != nil {
		t.Fatal(err)
	}
	assertWorkflowModes(t, s, "agentic", "agentic", false)
}

func assertWorkflowModes(t *testing.T, s *Server, active, configured string, restart bool) {
	t.Helper()
	w := workflowModeRequest(t, s, "GET", "workflow_config/global", "", nil)
	var record model.Record
	if err := json.Unmarshal(w.Body.Bytes(), &record); err != nil || w.Code != 200 {
		t.Fatalf("workflow config: %d %s %v", w.Code, w.Body, err)
	}
	value, err := store.Decode[model.WorkflowConfig](record)
	if err != nil || value.ActiveMode != active || value.ConfiguredMode != configured || value.RestartRequired != restart {
		t.Fatalf("workflow config mismatch: %+v %v", value, err)
	}
}

func workflowLocalPlan(t *testing.T) (*Server, model.Task) {
	t.Helper()
	s := setup(t)
	ctx := context.Background()
	s.Engine.PlansRoot = filepath.Join(t.TempDir(), "plans")
	project := model.Project{ID: "project", PolicyHash: "approved-policy", Policy: model.Policy{AllowedPaths: []string{workflowResultPath}}}
	if err := s.Store.Put(ctx, "projects", project.ID, project, 0); err != nil {
		t.Fatal(err)
	}
	task := model.Task{ID: "task", ProjectID: project.ID, Mode: model.AgenticMode, PlanVersion: 1,
		Issue: model.Issue{Title: workflowPlanObjective, Body: "Make result useful"},
		Plan:  &model.Plan{Objective: workflowPlanObjective, What: workflowPlanChange, Why: "Result incomplete", Benefit: "Useful result", Where: []string{workflowResultPath}, AcceptanceCriteria: []string{"Result is correct"}, Units: []model.Unit{{ID: "main", Objective: "Test and implement result", AllowedPaths: []string{workflowResultPath}}}}}
	if err := s.Engine.WriteLocalPlan(ctx, project, &task); err != nil {
		t.Fatal(err)
	}
	if err := s.Store.Put(ctx, "tasks", task.ID, task, 0); err != nil {
		t.Fatal(err)
	}
	return s, task
}

func TestWorkflowPlanAPIApprovalBindsHashAndVersion(t *testing.T) {
	s, task := workflowLocalPlan(t)
	w := workflowModeRequest(t, s, "GET", workflowPlanDocumentRoute, "", nil)
	var view workflow.PlanDocumentView
	if err := json.Unmarshal(w.Body.Bytes(), &view); err != nil || w.Code != 200 || view.Hash == "" || view.Plan.Objective != workflowPlanObjective || view.Path != task.PlanDocument.Path {
		t.Fatalf("plan document: %d %s %v", w.Code, w.Body, err)
	}
	w = workflowModeRequest(t, s, "POST", workflowPlanReviewRoute, "", map[string]string{"action": "approve", "hash": view.Hash})
	if w.Code != 428 {
		t.Fatalf("approval without version accepted: %d %s", w.Code, w.Body)
	}
	for _, tc := range []struct{ version, hash string }{{"2", view.Hash}, {"1", "stale-hash"}} {
		w = workflowModeRequest(t, s, "POST", workflowPlanReviewRoute, tc.version, map[string]string{"action": "approve", "hash": tc.hash})
		if w.Code != 409 {
			t.Fatalf("stale approval accepted: %d %s", w.Code, w.Body)
		}
	}
	w = workflowModeRequest(t, s, "POST", workflowPlanReviewRoute, "1", map[string]string{"action": "approve", "hash": view.Hash})
	var record model.Record
	if err := json.Unmarshal(w.Body.Bytes(), &record); err != nil || w.Code != 200 {
		t.Fatalf("approve: %d %s %v", w.Code, w.Body, err)
	}
	approved, err := store.Decode[model.Task](record)
	if err != nil || approved.Stage != "coding" || approved.PlanDocument.ApprovedHash != view.Hash || approved.PlanDocument.PolicyHash != "approved-policy" || approved.PlanDocument.ApprovedAt == "" {
		t.Fatalf("approval evidence missing: %+v %v", approved, err)
	}
	snapshot, err := s.Store.Get(context.Background(), "plans", "task:1")
	if err != nil {
		t.Fatal(err)
	}
	plan, err := store.Decode[model.Plan](snapshot)
	if err != nil || plan.What != workflowPlanChange {
		t.Fatalf("immutable plan not saved: %+v %v", plan, err)
	}
	w = workflowModeRequest(t, s, "POST", workflowPlanReviewRoute, strconv.Itoa(record.Version), map[string]string{"action": "approve", "hash": view.Hash})
	if w.Code != 409 {
		t.Fatalf("duplicate approval accepted: %d %s", w.Code, w.Body)
	}
}

func TestWorkflowPlanAPIEditsRequireFreshApproval(t *testing.T) {
	s, task := workflowLocalPlan(t)
	changed, err := os.ReadFile(task.PlanDocument.Path)
	if err != nil {
		t.Fatal(err)
	}
	changed = []byte(strings.Replace(string(changed), workflowPlanChange, workflowRevisedPlanChange, 1))
	if err := os.WriteFile(task.PlanDocument.Path, changed, 0600); err != nil {
		t.Fatal(err)
	}
	w := workflowModeRequest(t, s, "POST", workflowPlanReviewRoute, "1", map[string]string{"action": "approve", "hash": task.PlanDocument.Hash})
	if w.Code != 409 {
		t.Fatalf("edited document accepted old hash: %d %s", w.Code, w.Body)
	}
	w = workflowModeRequest(t, s, "GET", workflowPlanDocumentRoute, "", nil)
	var view workflow.PlanDocumentView
	if err := json.Unmarshal(w.Body.Bytes(), &view); err != nil || w.Code != 200 || view.Plan.What != workflowRevisedPlanChange {
		t.Fatalf("edited document not authoritative: %d %s %v", w.Code, w.Body, err)
	}
	w = workflowModeRequest(t, s, "POST", workflowPlanReviewRoute, "1", map[string]string{"action": "approve", "hash": view.Hash})
	if w.Code != 200 {
		t.Fatalf("edited plan approval: %d %s", w.Code, w.Body)
	}
	if err := os.WriteFile(view.Path, []byte(strings.Replace(view.Content, workflowPlanObjective, "Improve revised result", 1)), 0600); err != nil {
		t.Fatal(err)
	}
	w = workflowModeRequest(t, s, "GET", workflowPlanDocumentRoute, "", nil)
	if w.Code != 200 {
		t.Fatalf("read approved edit: %d %s", w.Code, w.Body)
	}
	record, err := s.Store.Get(context.Background(), "tasks", task.ID)
	if err != nil {
		t.Fatal(err)
	}
	invalidated, err := store.Decode[model.Task](record)
	if err != nil || invalidated.Stage != "plan_approval" || invalidated.PlanDocument.ApprovedHash != "" || invalidated.PlanVersion != 2 {
		t.Fatalf("document edit retained approval: %+v %v", invalidated, err)
	}
	snapshot, err := s.Store.Get(context.Background(), "plans", "task:1")
	if err != nil {
		t.Fatal(err)
	}
	approved, err := store.Decode[model.Plan](snapshot)
	if err != nil || approved.Objective != workflowPlanObjective || approved.What != workflowRevisedPlanChange {
		t.Fatalf("manual edit overwrote immutable approval: %+v %v", approved, err)
	}
}

func TestWorkflowPlanAPIRevisionPreservesObjectiveAndFeedback(t *testing.T) {
	s, task := workflowLocalPlan(t)
	w := workflowModeRequest(t, s, "POST", workflowPlanReviewRoute, "1", map[string]string{"action": "revise", "hash": task.PlanDocument.Hash, "feedback": "Cover retry behavior"})
	var record model.Record
	if err := json.Unmarshal(w.Body.Bytes(), &record); err != nil || w.Code != 200 {
		t.Fatalf("request revision: %d %s %v", w.Code, w.Body, err)
	}
	revised, err := store.Decode[model.Task](record)
	if err != nil || revised.Stage != "planning" || revised.PlanVersion != 2 || revised.Plan != nil || revised.PlanDocument != nil || !strings.Contains(revised.Issue.Body, "Cover retry behavior") || !strings.Contains(revised.Issue.Body, "Make result useful") {
		t.Fatalf("revision dropped objective or feedback: %+v %v", revised, err)
	}
	retained, err := os.ReadFile(task.PlanDocument.Path)
	if err != nil || !strings.Contains(string(retained), workflowPlanObjective) {
		t.Fatalf("revision removed original document: %s %v", retained, err)
	}
}

func TestWorkflowPlanAPIRejectIsTerminal(t *testing.T) {
	s, task := workflowLocalPlan(t)
	w := workflowModeRequest(t, s, "POST", workflowPlanReviewRoute, "1", map[string]string{"action": "reject", "hash": task.PlanDocument.Hash})
	var record model.Record
	if err := json.Unmarshal(w.Body.Bytes(), &record); err != nil || w.Code != 200 {
		t.Fatalf("reject: %d %s %v", w.Code, w.Body, err)
	}
	cancelled, err := store.Decode[model.Task](record)
	if err != nil || cancelled.Stage != "cancelled" || cancelled.ResumeStage != "" {
		t.Fatalf("rejection not terminal: %+v %v", cancelled, err)
	}
	for _, action := range []string{"resume", "adopt"} {
		w = workflowModeRequest(t, s, "POST", "tasks/task/"+action, strconv.Itoa(record.Version), map[string]string{})
		if w.Code != 409 || !strings.Contains(w.Body.String(), "task_cancelled") {
			t.Fatalf("cancelled task %s accepted: %d %s", action, w.Code, w.Body)
		}
	}
	w = workflowModeRequest(t, s, "POST", workflowPlanReviewRoute, strconv.Itoa(record.Version), map[string]string{"action": "approve", "hash": task.PlanDocument.Hash})
	if w.Code != 409 {
		t.Fatalf("cancelled plan approved: %d %s", w.Code, w.Body)
	}
}

type workflowInventoryAdapter func(context.Context, provider.Input, provider.Hooks) (provider.Output, error)

func (f workflowInventoryAdapter) Run(ctx context.Context, in provider.Input, hooks provider.Hooks) (provider.Output, error) {
	return f(ctx, in, hooks)
}

func TestWorkflowIntegrationInventoryRefreshAndCachedRead(t *testing.T) {
	s := setup(t)
	ctx := context.Background()
	if err := s.Store.Put(ctx, "providers", "native", model.Provider{ID: "native", Kind: "codex", Command: []string{"never-run"}}, 0); err != nil {
		t.Fatal(err)
	}
	s.Engine.Adapter = func(model.Provider) provider.Adapter {
		return workflowInventoryAdapter(func(_ context.Context, in provider.Input, _ provider.Hooks) (provider.Output, error) {
			if !in.CatalogOnly || !in.ReadOnly || !in.NativeIntegrations || in.Prompt != "" || in.ProviderID != "native" {
				t.Fatalf("inventory attempted model execution: %+v", in)
			}
			return provider.Output{Integrations: []model.Integration{{Provider: "native", Ref: "app://jenkins", Name: "Jenkins", Kind: "app", Enabled: true, Callable: true, Tools: []model.IntegrationTool{{Name: "build_status", ReadOnly: true}}}}}, nil
		})
	}
	w := workflowModeRequest(t, s, "GET", workflowInventoryRoute, "", nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Not checked") {
		t.Fatalf("empty inventory: %d %s", w.Code, w.Body)
	}
	w = workflowModeRequest(t, s, "POST", workflowInventoryRoute, "", map[string]string{})
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"ref":"app://jenkins"`) || !strings.Contains(w.Body.String(), "checked_at") {
		t.Fatalf("inventory refresh: %d %s", w.Code, w.Body)
	}
	refreshed := w.Body.String()
	s.Engine.Adapter = func(model.Provider) provider.Adapter { t.Fatal("cached read invoked provider"); return nil }
	w = workflowModeRequest(t, s, "GET", workflowInventoryRoute, "", nil)
	if w.Code != 200 || w.Body.String() != refreshed {
		t.Fatalf("cached inventory changed: %d %s", w.Code, w.Body)
	}
}

func TestWorkflowIntegrationInventoryDoesNotExposeCredentials(t *testing.T) {
	for _, failed := range []bool{false, true} {
		t.Run(strconv.FormatBool(failed), func(t *testing.T) {
			s := setup(t)
			if err := s.Store.Put(context.Background(), "providers", "native", model.Provider{ID: "native", Kind: "codex"}, 0); err != nil {
				t.Fatal(err)
			}
			s.Engine.Adapter = func(model.Provider) provider.Adapter {
				return workflowCredentialInventoryAdapter(failed)
			}
			w := workflowModeRequest(t, s, "POST", workflowInventoryRoute, "", map[string]string{})
			if w.Code != 200 || strings.Contains(w.Body.String(), "private-inventory-token") {
				t.Fatalf("inventory leaked credential: %d %s", w.Code, w.Body)
			}
			w = workflowModeRequest(t, s, "GET", workflowInventoryRoute, "", nil)
			if w.Code != 200 || strings.Contains(w.Body.String(), "private-inventory-token") {
				t.Fatalf("cached inventory leaked credential: %d %s", w.Code, w.Body)
			}
		})
	}
}

func workflowCredentialInventoryAdapter(failed bool) provider.Adapter {
	return workflowInventoryAdapter(func(context.Context, provider.Input, provider.Hooks) (provider.Output, error) {
		if failed {
			return provider.Output{}, errors.New("connection rejected Authorization: Bearer private-inventory-token")
		}
		return provider.Output{Integrations: []model.Integration{{Provider: "native", Ref: "app://jenkins", Name: "Jenkins", Error: "Authorization: Bearer private-inventory-token"}}}, nil
	})
}

func TestWorkflowSubmitterDisplaysInheritedCodingChoices(t *testing.T) {
	s := setup(t)
	ctx := context.Background()
	chain := []model.AgentChoice{{Provider: "native", Model: "configured-model"}}
	if err := s.Store.Put(ctx, "agents", "code", model.AgentConfig{Role: "code", Chain: chain}, 0); err != nil {
		t.Fatal(err)
	}
	response := workflowModeRequest(t, s, "GET", "agents/submit", "", nil)
	var record model.Record
	if err := json.Unmarshal(response.Body.Bytes(), &record); err != nil || response.Code != 200 {
		t.Fatalf("agent read: %d %s", response.Code, response.Body)
	}
	config, err := store.Decode[model.AgentConfig](record)
	if err != nil || record.Version != 0 || config.Role != "submit" || config.InheritedFrom != "code" || len(config.Chain) != 1 || config.Chain[0] != chain[0] {
		t.Fatalf("incorrect inherited Submitter: %+v %v", config, err)
	}
}
