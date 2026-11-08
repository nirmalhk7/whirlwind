package dashboard

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
	"whirlwind/internal/control"
	"whirlwind/internal/model"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

const (
	dashboardProjectOne = "project-1"
	dashboardPlanOne    = "plan-1"
	dashboardHashOne    = "hash-1"
	dashboardReviewHash = "reviewed-hash"
)

func screenText(screen tcell.SimulationScreen) string {
	cells, width, _ := screen.GetContents()
	var text strings.Builder
	for i, cell := range cells {
		text.WriteString(string(cell.Runes))
		if (i+1)%width == 0 {
			text.WriteByte('\n')
		}
	}
	return text.String()
}

func TestSlowRefreshDoesNotBlockTerminalInput(t *testing.T) {
	d := New()
	screen := tcell.NewSimulationScreen("UTF-8")
	d.SetScreen(screen)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	loading := make(chan struct{})
	watch := d.Watch(ctx, func(ctx context.Context) (control.Snapshot, error) {
		close(loading)
		<-ctx.Done()
		return nil, ctx.Err()
	})
	finished := make(chan error, 1)
	go func() { finished <- d.Run() }()
	t.Cleanup(d.Stop)
	select {
	case <-loading:
	case <-time.After(time.Second):
		t.Fatal("dashboard never requested supervisor state")
	}
	screen.InjectKey(tcell.KeyRune, 'q', tcell.ModNone)
	select {
	case err := <-finished:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("blocked state read froze terminal quit")
	}
	cancel()
	select {
	case <-watch:
	case <-time.After(time.Second):
		t.Fatal("dashboard leaked state reader")
	}
}

func record(id string, value any) model.Record {
	data, _ := json.Marshal(value)
	return model.Record{ID: id, Version: 1, Data: data}
}

func TestDashboardInspectsSavedPlansAndPendingQuestions(t *testing.T) {
	d := New()
	screen := tcell.NewSimulationScreen("UTF-8")
	d.SetScreen(screen)
	t.Cleanup(screen.Fini)
	screen.SetSize(140, 45)
	d.Present(control.Snapshot{
		"projects":  {record("p", model.Project{ID: "p", Name: "Saved repository"})},
		"tasks":     {record("plan", model.Task{ID: "plan", ProjectID: "p", Stage: "plan_approval", PlanVersion: 2, Branch: "delivery/saved", Plan: &model.Plan{Objective: "Preserve operator choices", AcceptanceCriteria: []string{"Pending question remains answerable"}}})},
		"questions": {record("q", model.QuestionSet{TaskID: "plan", Status: "pending", Questions: []model.Question{{Text: "Which directory should receive output?"}}})},
		"sessions":  {record("a", model.Attempt{Task: "plan", Role: "plan", Provider: "cursor", ResolvedModel: "chosen-model", Status: "paused"})},
	})
	d.ForceDraw()
	text := screenText(screen)
	for _, expected := range []string{"Saved repository", "Preserve operator choices", "plan_approval", "delivery/saved", "Which directory", "cursor", "chosen-model", "paused"} {
		if !strings.Contains(text, expected) {
			t.Errorf("saved state missing %q", expected)
		}
	}
}

func TestAttemptInspectionRendersPersistedHealthQueueAndEffectiveSettings(t *testing.T) {
	now := time.Now().UTC()
	heartbeat := now.Add(-2 * time.Second).Format(time.RFC3339Nano)
	validLease := now.Add(20 * time.Second).Format(time.RFC3339Nano)
	expiredAt := now.Add(-time.Second).Format(time.RFC3339Nano)
	futureDeadline := now.Add(2 * time.Minute).Format(time.RFC3339Nano)
	active := model.Attempt{AttemptID: "active", Task: "plan", Role: "code", Provider: "acp", Model: "configured-model", ResolvedModel: "effective-model", Thinking: &model.ThinkingChoice{ConfigID: "effort", Value: "medium"}, EffectiveThinking: &model.ThinkingChoice{ConfigID: "effort", Value: "high"}, Status: "running", WorkerHeartbeatAt: heartbeat, LeaseExpiresAt: validLease, ProcessState: "running", SandboxHealth: "healthy", SandboxPhase: "Ready", DeadlineAt: futureDeadline, PreviousAttemptID: "previous-attempt"}
	activeData, err := json.Marshal(active)
	if err != nil {
		t.Fatal(err)
	}
	var activeFields map[string]any
	if err := json.Unmarshal(activeData, &activeFields); err != nil {
		t.Fatal(err)
	}
	activeFields["queue_position"], activeFields["queue_length"] = 2, 3
	activeRecord := record("active", activeFields)
	expiredRecord := record("expired", model.Attempt{AttemptID: "expired", Task: "plan", Role: "review", Provider: "acp", Model: "review-model", Status: "running", WorkerHeartbeatAt: expiredAt, LeaseExpiresAt: expiredAt, ProcessState: "exited", SandboxHealth: "stale", SandboxPhase: "Ready", DeadlineAt: expiredAt})
	legacyRecord := record("legacy", model.Attempt{AttemptID: "legacy", Task: "plan", Role: "plan", Provider: "codex", Model: "legacy-model", Thinking: &model.ThinkingChoice{ConfigID: "effort", Value: "low"}, Status: "complete", Finished: true, DeadlineAt: futureDeadline})
	d := New()
	d.Present(control.Snapshot{
		"projects": {record("project", model.Project{ID: "project", Name: "Repo"})},
		"tasks":    {record("plan", model.Task{ID: "plan", ProjectID: "project", Stage: "coding"})},
		"sessions": {activeRecord, expiredRecord, legacyRecord},
		// Current configuration changed after the active attempt was recorded.
		"agents": {record("code", model.AgentConfig{Role: "code", Chain: []model.AgentChoice{{Provider: "other", Model: "changed-model"}}})},
	})
	view := d.detail.GetText(false)
	for _, expected := range []string{
		"heartbeat: lease valid", "process: running", "sandbox: healthy (Ready)",
		"model: effective effective-model", "thinking: effective effort=high", "queue entry: 2/3",
		"remaining: inactive", "heartbeat: expired", "sandbox: stale (Ready)",
		"queue entry: unknown (not recorded)", "model: configured legacy-model (effective not recorded)",
		"thinking: configured effort=low (effective not recorded)",
	} {
		if !strings.Contains(view, expected) {
			t.Errorf("attempt inspection omitted truthful saved status %q: %s", expected, view)
		}
	}
	if strings.Contains(view, "heartbeat: unknown (not recorded)") {
		t.Fatal("recorded active worker heartbeat was rendered as unknown")
	}
}

func TestRefreshPreservesSelectedProjectAndPlan(t *testing.T) {
	d := New()
	snapshot := control.Snapshot{
		"projects": {record("first", model.Project{ID: "first", Name: "First"}), record("second", model.Project{ID: "second", Name: "Second"})},
		"tasks":    {record("one", model.Task{ID: "one", ProjectID: "second", Plan: &model.Plan{Objective: "One"}}), record("two", model.Task{ID: "two", ProjectID: "second", Plan: &model.Plan{Objective: "Two"}})},
	}
	d.Present(snapshot)
	d.projects.SetCurrentItem(1)
	d.plans.SetCurrentItem(1)
	d.Present(snapshot)
	project, _ := d.projects.GetItemText(d.projects.GetCurrentItem())
	plan, _ := d.plans.GetItemText(d.plans.GetCurrentItem())
	if project != "Second" || !strings.Contains(plan, "Two") {
		t.Fatalf("refresh lost selection: %q / %q", project, plan)
	}
}

func TestDashboardOpensProjectAndPlanNavigation(t *testing.T) {
	screen := tcell.NewSimulationScreen("UTF-8")
	app := New().SetScreen(screen)
	t.Cleanup(screen.Fini)
	screen.SetSize(120, 32)
	app.ForceDraw()
	text := screenText(screen)
	for _, expected := range []string{"Whirlwind", "Projects", "Plans", "No projects", "Tab", "quit"} {
		if !strings.Contains(text, expected) {
			t.Errorf("dashboard missing %q:\n%s", expected, text)
		}
	}
}

func TestFourDirectionsAreNavigableAndApprovalRequiresSeparateConfirmation(t *testing.T) {
	d := New()
	screen := tcell.NewSimulationScreen("UTF-8")
	d.SetScreen(screen)
	t.Cleanup(screen.Fini)
	d.Present(control.Snapshot{
		"projects": {record("p", model.Project{ID: "p", Name: "Local repo"})},
		"directions": {
			record("d1", model.Direction{ID: "d1", ProjectID: "p", Category: "expand", Status: "proposed", Title: "Expand"}),
			record("d2", model.Direction{ID: "d2", ProjectID: "p", Category: "optimize", Status: "proposed", Title: "Optimize"}),
			record("d3", model.Direction{ID: "d3", ProjectID: "p", Category: "harden", Status: "proposed", Title: "Harden"}),
			record("d4", model.Direction{ID: "d4", ProjectID: "p", Category: "simplify", Status: "proposed", Title: "Simplify"}),
		},
	})
	if len(d.directionRows) != 4 {
		t.Fatalf("direction list has %d entries", len(d.directionRows))
	}
	d.focus = 1
	d.input(tcell.NewEventKey(tcell.KeyRune, 'a', tcell.ModNone))
	if d.modal == nil {
		t.Fatal("direction approval did not require an explicit confirmation")
	}
	q := tcell.NewEventKey(tcell.KeyRune, 'q', tcell.ModNone)
	if got := d.input(q); got != q || d.modal == nil {
		t.Fatal("q quit while a decision form was active")
	}
	d.input(tcell.NewEventKey(tcell.KeyEscape, 0, tcell.ModNone))
	if d.modal != nil {
		t.Fatal("escape did not cancel the decision form")
	}
}

func TestApprovalRequestsRetainVersionsReviewedWhenFormsOpened(t *testing.T) {
	d := New()
	requests := make(chan control.Request, 2)
	d.SetCommandHandler(func(_ context.Context, request control.Request) (json.RawMessage, error) {
		requests <- request
		return json.RawMessage(`{}`), nil
	})
	direction := model.Direction{ID: "direction-1", ProjectID: dashboardProjectOne, Title: "Improve the repo", Status: "proposed"}
	plan := model.Task{ID: dashboardPlanOne, ProjectID: dashboardProjectOne, PlanDocument: &model.PlanDocument{Path: "PLAN.md", Hash: dashboardReviewHash}}
	snapshot := control.Snapshot{
		"projects":   {record(dashboardProjectOne, model.Project{ID: dashboardProjectOne, Name: "Repo"})},
		"directions": {record(direction.ID, direction)},
		"tasks":      {record(plan.ID, plan)},
	}
	d.Present(snapshot)
	d.focus = 1
	d.confirmDirection()
	modal := d.modal.(*tview.Modal)
	modal.SetFocus(0)
	modal.Focus(func(primitive tview.Primitive) { primitive.Focus(func(tview.Primitive) {}) })
	modal.InputHandler()(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone), func(tview.Primitive) {})
	snapshot["directions"][0].Version = 4
	d.Present(snapshot)
	approved := <-requests
	if approved.ExpectedVersion != 1 || approved.ResourceID != direction.ID {
		t.Fatalf("direction approval adopted a refreshed record version: %+v", approved)
	}

	d.showPlanReview(plan, "reviewed text", dashboardReviewHash, 1)
	snapshot["tasks"][0].Version = 2
	d.Present(snapshot)
	form := d.modal.(*tview.Form)
	form.GetButton(0).InputHandler()(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone), nil)
	review := <-requests
	if review.ExpectedVersion != 1 || review.ContentHash != dashboardReviewHash {
		t.Fatalf("plan review adopted a refreshed record version: %+v", review)
	}
}

func TestPermissionConfirmationShowsSavedActionAndScope(t *testing.T) {
	d := New()
	screen := tcell.NewSimulationScreen("UTF-8")
	d.SetScreen(screen)
	t.Cleanup(screen.Fini)
	screen.SetSize(120, 35)
	d.Present(control.Snapshot{
		"projects": {record(dashboardProjectOne, model.Project{ID: dashboardProjectOne, Name: "Repo"})},
		"tasks":    {record(dashboardPlanOne, model.Task{ID: dashboardPlanOne, ProjectID: dashboardProjectOne, Stage: "paused", Wait: &model.TaskWait{AttemptID: "attempt-1"}, Issue: model.Issue{Title: "Saved plan"}})},
		"permissions": {record("permission-1", map[string]any{
			"task": dashboardPlanOne, "decision": "pending", "provider": "codex", "attempt_id": "attempt-1", "plan_version": 4,
			"request": map[string]any{"tool": "fs.write", "command": []string{"git", "status", "--short"}, "paths": []string{"internal/control/ipc.go"}},
		})},
	})
	d.permissionForm()
	d.ForceDraw()
	text := screenText(screen)
	for _, expected := range []string{"fs.write", "git status --short", "internal/control/ipc.go", "codex", "attempt-1", "Plan version: 4"} {
		if !strings.Contains(text, expected) {
			t.Errorf("permission confirmation omitted saved action detail %q:\n%s", expected, text)
		}
	}
}

func TestPlanReviewKeepsExactUntrustedContentAndCapturedVersion(t *testing.T) {
	d := New()
	plan := model.Task{ID: dashboardPlanOne, ProjectID: dashboardProjectOne, PlanDocument: &model.PlanDocument{Path: "PLAN.md", Hash: dashboardHashOne}}
	content := "# Exact heading [red] literal\n\nSecond line"
	d.showPlanReview(plan, content, dashboardHashOne, 6)
	form, ok := d.modal.(*tview.Form)
	if !ok {
		t.Fatalf("expected review form, got %T", d.modal)
	}
	text, ok := form.GetFormItem(0).(*tview.TextView)
	if !ok || text.GetText(false) != content {
		t.Fatalf("plan review changed displayed content: %#v", form.GetFormItem(0))
	}
	if request := planReviewRequest(plan, 6, dashboardHashOne, json.RawMessage(`{"action":"approve"}`)); request.ExpectedVersion != 6 {
		t.Fatalf("review request adopted a newer snapshot version: %+v", request)
	}
}

func TestInspectionResponseIsDiscardedAfterSelectionOrRecordChanges(t *testing.T) {
	d := New()
	snapshot := control.Snapshot{
		"projects": {record(dashboardProjectOne, model.Project{ID: dashboardProjectOne, Name: "Repo"})},
		"tasks": {
			record(dashboardPlanOne, model.Task{ID: dashboardPlanOne, ProjectID: dashboardProjectOne, PlanDocument: &model.PlanDocument{Hash: dashboardHashOne}}),
			record("plan-2", model.Task{ID: "plan-2", ProjectID: dashboardProjectOne, PlanDocument: &model.PlanDocument{Hash: "hash-2"}}),
		},
	}
	d.Present(snapshot)
	if !d.planStillCurrent(dashboardPlanOne, 1, dashboardHashOne) {
		t.Fatal("current inspected plan was marked stale")
	}
	d.plans.SetCurrentItem(1)
	if d.planStillCurrent(dashboardPlanOne, 1, dashboardHashOne) {
		t.Fatal("response for prior selection remained current")
	}
	d.Present(snapshot)
	snapshot["tasks"][0] = record(dashboardPlanOne, model.Task{ID: dashboardPlanOne, ProjectID: dashboardProjectOne, PlanDocument: &model.PlanDocument{Hash: "changed"}})
	d.Present(snapshot)
	if d.planStillCurrent(dashboardPlanOne, 1, dashboardHashOne) {
		t.Fatal("response survived a changed plan hash")
	}
}

func TestHumanDecisionFormsSelectOnlyCurrentPausedAttempt(t *testing.T) {
	d := New()
	screen := tcell.NewSimulationScreen("UTF-8")
	d.SetScreen(screen)
	t.Cleanup(screen.Fini)
	screen.SetSize(120, 35)
	current := model.Task{ID: dashboardPlanOne, ProjectID: dashboardProjectOne, Stage: "paused", Wait: &model.TaskWait{AttemptID: "attempt-current"}}
	d.Present(control.Snapshot{
		"projects": {record(dashboardProjectOne, model.Project{ID: dashboardProjectOne, Name: "Repo"})},
		"tasks":    {record(dashboardPlanOne, current)},
		"questions": {
			record("question-old", model.QuestionSet{ID: "question-old", TaskID: dashboardPlanOne, AttemptID: "attempt-old", Status: "pending", Questions: []model.Question{{ID: "old", Text: "Old attempt question"}}}),
			record("question-current", model.QuestionSet{ID: "question-current", TaskID: dashboardPlanOne, AttemptID: "attempt-current", Status: "pending", Questions: []model.Question{{ID: "current", Text: "Current attempt question"}}}),
		},
		"permissions": {
			record("permission-old", map[string]any{"task": dashboardPlanOne, "attempt_id": "attempt-old", "decision": "pending", "request": map[string]any{"tool": "old-tool"}}),
			record("permission-current", map[string]any{"task": dashboardPlanOne, "attempt_id": "attempt-current", "decision": "pending", "request": map[string]any{"tool": "current-tool"}}),
		},
	})
	d.answerQuestionsForm()
	d.ForceDraw()
	text := screenText(screen)
	if !strings.Contains(text, "Current attempt question") || strings.Contains(text, "Old attempt question") {
		t.Fatalf("clarification form did not bind to current paused attempt:\n%s", text)
	}
	d.closeModal()
	d.permissionForm()
	d.ForceDraw()
	text = screenText(screen)
	if !strings.Contains(text, "current-tool") || strings.Contains(text, "old-tool") {
		t.Fatalf("permission form did not bind to current paused attempt:\n%s", text)
	}
}

func TestDashboardLabelsRawScoresAndShowsComputedAcceptanceEvidence(t *testing.T) {
	d := New()
	d.Present(control.Snapshot{
		"projects": {record(dashboardProjectOne, model.Project{ID: dashboardProjectOne, Name: "Repo"})},
		"tasks": {record(dashboardPlanOne, model.Task{ID: dashboardPlanOne, ProjectID: dashboardProjectOne, Score: 9, Delivery: &model.LocalDelivery{
			Stage: "accepted", Score: 9, Adherence: &model.Adherence{Score: 92.5}, UnmetRequirements: []string{"WW-055"},
		}})},
	})
	view := d.detail.GetText(false)
	for _, expected := range []string{"Stored task score (raw; not acceptance evidence): 9", "Computed adherence: 92.5%", "WW-055"} {
		if !strings.Contains(view, expected) {
			t.Errorf("inspection omitted correct acceptance score provenance %q: %s", expected, view)
		}
	}
	if strings.Contains(view, "score: 9%") || strings.Contains(view, "Adherence: 9%") {
		t.Fatalf("raw legacy score was presented as adherence: %s", view)
	}
}

func TestCriticalApprovalShowsMatchingSavedReviewAdherenceBeforeDelivery(t *testing.T) {
	provenance := &model.Provenance{Task: dashboardPlanOne, PlanVersion: 3, Head: "candidate-commit", Base: "base-commit", PolicyHash: "policy-hash", VerificationID: "verification-1", ReviewID: "review-1"}
	task := model.Task{ID: dashboardPlanOne, ProjectID: dashboardProjectOne, Stage: "approval", PlanVersion: 3, ReviewedCommit: provenance.Head, VerificationID: provenance.VerificationID, ReviewID: provenance.ReviewID, Score: 9}
	adherenceProvenance := *provenance
	review := model.Review{Provenance: provenance, Adherence: &model.Adherence{Score: 87.5, Total: 2, Provenance: adherenceProvenance, Results: []model.RequirementResult{{ID: "WW-001", Status: "pass"}, {ID: "WW-055", Status: "fail"}}}}
	d := New()
	d.Present(control.Snapshot{
		"projects": {record(dashboardProjectOne, model.Project{ID: dashboardProjectOne, Name: "Repo", PolicyHash: provenance.PolicyHash})},
		"tasks":    {record(dashboardPlanOne, task)},
		"reviews":  {record(provenance.ReviewID, review)},
	})
	view := d.detail.GetText(false)
	for _, expected := range []string{"Computed adherence: 87.5%", "WW-055"} {
		if !strings.Contains(view, expected) {
			t.Errorf("critical approval inspection omitted saved review evidence %q: %s", expected, view)
		}
	}
	if strings.Contains(view, "Computed adherence: 9%") {
		t.Fatalf("legacy score was presented as review adherence: %s", view)
	}
	stale := review
	stale.Adherence = &model.Adherence{Score: 100, Total: 2, Provenance: model.Provenance{Task: dashboardPlanOne, PlanVersion: 2, Head: "older-candidate", PolicyHash: provenance.PolicyHash, VerificationID: provenance.VerificationID, ReviewID: provenance.ReviewID}, Results: []model.RequirementResult{{ID: "WW-055", Status: "pass"}}}
	d.Present(control.Snapshot{
		"projects": {record(dashboardProjectOne, model.Project{ID: dashboardProjectOne, Name: "Repo", PolicyHash: provenance.PolicyHash})},
		"tasks":    {record(dashboardPlanOne, task)},
		"reviews":  {record(provenance.ReviewID, stale)},
	})
	if strings.Contains(d.detail.GetText(false), "Computed adherence: 100%") {
		t.Fatal("stale review report provenance was displayed as current acceptance evidence")
	}
}

func TestFinalizationApprovalRequestBindsExactCandidateAndReview(t *testing.T) {
	provenance := model.Provenance{Task: dashboardPlanOne, PlanVersion: 4, Head: "reviewed-candidate", PolicyHash: "policy-current", VerificationID: "verification-current", ReviewID: "review-current"}
	plan := model.Task{ID: dashboardPlanOne, ProjectID: dashboardProjectOne, Stage: "approval", Risk: "critical", PlanVersion: 4, ReviewedCommit: provenance.Head, ReviewID: provenance.ReviewID, VerificationID: provenance.VerificationID, PlanDocument: &model.PlanDocument{ApprovedHash: "approved-plan"}}
	view := finalizationReport{Task: plan, Review: model.Review{Provenance: &provenance, Adherence: &model.Adherence{Score: 90, Total: 1, Accepted: true, Provenance: provenance, Results: []model.RequirementResult{{ID: "WW-001", Status: "pass"}}}}, Adherence: model.Adherence{Score: 90, Total: 1, Accepted: true, Provenance: provenance, Results: []model.RequirementResult{{ID: "WW-001", Status: "pass"}}}, ApprovedPlanHash: "approved-plan", PolicyHash: "policy-current", Diff: finalizationDiffPage{Content: "diff --git a/file b/file\n+approved candidate\n"}, Accepted: true}
	request, err := finalizationApprovalRequest(plan, 17, "policy-current", view)
	if err != nil {
		t.Fatal(err)
	}
	if request.Operation != "finalization.approve" || request.PlanID != plan.ID || request.ExpectedVersion != 17 || request.IdempotencyKey == "" {
		t.Fatalf("finalization action lost task CAS/receipt binding: %+v", request)
	}
	var approval finalizationApprovalPayload
	if err := json.Unmarshal(request.Payload, &approval); err != nil {
		t.Fatal(err)
	}
	if approval.PlanVersion != plan.PlanVersion || approval.ApprovedPlanHash != "approved-plan" || approval.ReviewedCommit != plan.ReviewedCommit || approval.ReviewID != plan.ReviewID || approval.VerificationID != plan.VerificationID || approval.PolicyHash != "policy-current" {
		t.Fatalf("approval request did not bind the exact reviewed candidate: %+v", approval)
	}
	view.Task.ReviewedCommit = "newer-candidate"
	if _, err := finalizationApprovalRequest(plan, 17, "policy-current", view); err == nil {
		t.Fatal("changed candidate report was approved")
	}
}

func TestCriticalFinalizationFormShowsExactDiffReportAndLocalOnlyAction(t *testing.T) {
	provenance := model.Provenance{Task: dashboardPlanOne, PlanVersion: 4, Head: "reviewed-candidate", PolicyHash: "policy-current", VerificationID: "verification-current", ReviewID: "review-current"}
	plan := model.Task{ID: dashboardPlanOne, ProjectID: dashboardProjectOne, Stage: "approval", Risk: "critical", PlanVersion: 4, ReviewedCommit: provenance.Head, ReviewID: provenance.ReviewID, VerificationID: provenance.VerificationID, PlanDocument: &model.PlanDocument{ApprovedHash: "approved-plan"}}
	report := finalizationReport{Task: plan, Review: model.Review{Summary: "Candidate review summary", Provenance: &provenance, Adherence: &model.Adherence{Score: 82, Total: 2, Accepted: true, Provenance: provenance, Results: []model.RequirementResult{{ID: "WW-055", Status: "fail"}}}}, Adherence: model.Adherence{Score: 82, Total: 2, Accepted: true, Provenance: provenance, Results: []model.RequirementResult{{ID: "WW-055", Status: "fail"}}}, ApprovedPlanHash: "approved-plan", PolicyHash: "policy-current", Diff: finalizationDiffPage{Content: "diff --git a/file b/file\n+exact reviewed code\n"}, Accepted: true}
	d := New()
	screen := tcell.NewSimulationScreen("UTF-8")
	d.SetScreen(screen)
	t.Cleanup(screen.Fini)
	screen.SetSize(140, 45)
	d.Present(control.Snapshot{
		"projects": {record(dashboardProjectOne, model.Project{ID: dashboardProjectOne, PolicyHash: "policy-current"})},
		"tasks":    {record(dashboardPlanOne, plan)},
	})
	d.showFinalizationApproval(plan, 1, report, []int64{0})
	d.ForceDraw()
	text := screenText(screen)
	for _, expected := range []string{"Approve local finalization only", "Computed adherence: 82%", "WW-055", "Exact reviewed candidate diff", "+exact reviewed code", "No pull request or branch push"} {
		if !strings.Contains(text, expected) {
			t.Errorf("critical approval form omitted %q: %s", expected, text)
		}
	}
}
