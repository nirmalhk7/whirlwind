package dashboard

import (
	"context"
	"encoding/json"
	"maps"
	"strings"
	"testing"
	"time"

	"whirlwind/internal/control"
	"whirlwind/internal/model"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

const (
	dashboardFixtureApprovedPolicy       = "policy-current"
	dashboardFixturePrimaryRequirement   = "WW-001"
	dashboardFixtureSecondaryRequirement = "WW-055"
)

const (
	dashboardFixtureAttemptID        = "attempt-1"
	dashboardFixtureCurrentAttemptID = "attempt-current"
	dashboardFixtureApprovedPlanID   = "approved-plan"
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
		"projects":        {record("project", model.Project{ID: "project", Name: "Repo"})},
		"execution_plans": {record("plan", model.ExecutionPlan{ID: "plan", ProjectID: "project", Stage: "coding"})},
		"sessions":        {activeRecord, expiredRecord, legacyRecord},
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

func TestFourDirectionsAreNavigableAndUnchangedVimDoesNotApprove(t *testing.T) {
	decisionVim(t, "cancel", "")
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
	if d.modal != nil || !strings.Contains(d.status.GetText(false), "cancelled") {
		t.Fatal("unchanged decision did not cancel approval")
	}

}

func TestApprovalRequestsRetainCapturedVersions(t *testing.T) {
	decisionVim(t, "approve", "")
	d := New()
	dashboardScreen(t, d)
	requests := make(chan control.Request, 2)
	d.SetCommandHandler(func(_ context.Context, request control.Request) (json.RawMessage, error) {
		requests <- request
		return json.RawMessage(`{}`), nil
	})
	direction := model.Direction{ID: "direction-1", ProjectID: dashboardProjectOne, Title: "Improve the repo", Status: "proposed"}
	plan := model.ExecutionPlan{ID: dashboardPlanOne, ProjectID: dashboardProjectOne, PlanDocument: &model.PlanDocument{Path: "PLAN.md", Hash: dashboardReviewHash}}
	snapshot := control.Snapshot{
		"projects":        {record(dashboardProjectOne, model.Project{ID: dashboardProjectOne, Name: "Repo"})},
		"directions":      {record(direction.ID, direction)},
		"execution_plans": {record(plan.ID, plan)},
	}
	d.Present(snapshot)
	d.focus = 1
	d.confirmDirection()
	snapshot["directions"][0].Version = 4
	d.Present(snapshot)
	approved := <-requests
	if approved.ExpectedVersion != 1 || approved.ResourceID != direction.ID {
		t.Fatalf("direction approval adopted a refreshed record version: %+v", approved)
	}

	d.showPlanReview(plan, "reviewed text", dashboardReviewHash, 1)
	form, ok := d.modal.(*tview.Form)
	if !ok {
		t.Fatalf("plan review did not open decision controls: %T", d.modal)
	}
	form.InputHandler()(tcell.NewEventKey(tcell.KeyTab, 0, tcell.ModNone), func(p tview.Primitive) { d.SetFocus(p) })
	form.InputHandler()(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone), func(p tview.Primitive) { d.SetFocus(p) })
	review := <-requests
	if review.ExpectedVersion != 1 || review.ContentHash != dashboardReviewHash {
		t.Fatalf("plan review did not retain the inspected ID/hash/version: %+v", review)
	}
	snapshot["execution_plans"][0].Version = 2
	d.Present(snapshot)
}

func TestPermissionConfirmationShowsSavedActionAndScope(t *testing.T) {
	d := New()
	screen := tcell.NewSimulationScreen("UTF-8")
	d.SetScreen(screen)
	t.Cleanup(screen.Fini)
	screen.SetSize(120, 35)
	d.Present(control.Snapshot{
		"projects":        {record(dashboardProjectOne, model.Project{ID: dashboardProjectOne, Name: "Repo"})},
		"execution_plans": {record(dashboardPlanOne, model.ExecutionPlan{ID: dashboardPlanOne, ProjectID: dashboardProjectOne, Stage: "paused", Wait: &model.ExecutionPlanWait{AttemptID: dashboardFixtureAttemptID}, Issue: model.Issue{Title: "Saved plan"}})},
		"permissions": {record("permission-1", map[string]any{
			"plan_id": dashboardPlanOne, "decision": "pending", "provider": "codex", "attempt_id": dashboardFixtureAttemptID, "plan_version": 4,
			"request": map[string]any{"tool": "fs.write", "command": []string{"git", "status", "--short"}, "paths": []string{"internal/control/ipc.go"}},
		})},
	})
	d.permissionForm()
	d.ForceDraw()
	text := screenText(screen)
	for _, expected := range []string{"fs.write", "git status --short", "internal/control/ipc.go", "codex", dashboardFixtureAttemptID, "Plan version: 4"} {
		if !strings.Contains(text, expected) {
			t.Errorf("permission confirmation omitted saved action detail %q:\n%s", expected, text)
		}
	}
}

func TestPlanReviewKeepsExactUntrustedContentAndCapturedVersion(t *testing.T) {
	decisionVim(t, "cancel", "")
	d := New()
	screen := dashboardScreen(t, d)
	screen.SetSize(120, 35)
	plan := model.ExecutionPlan{ID: dashboardPlanOne, ProjectID: dashboardProjectOne, PlanDocument: &model.PlanDocument{Path: "PLAN.md", Hash: dashboardHashOne}}
	content := "# Exact heading [red] literal\n\nSecond line"
	d.showPlanReview(plan, content, dashboardHashOne, 6)
	d.ForceDraw()
	view := screenText(screen)
	if !strings.Contains(view, "# Exact heading [red] literal") || !strings.Contains(view, "Second line") {
		t.Fatal("plan review did not display exact saved plan content")
	}

	if request := planReviewRequest(plan, 6, dashboardHashOne, json.RawMessage(`{"action":"approve"}`)); request.ExpectedVersion != 6 {
		t.Fatalf("review request adopted a newer snapshot version: %+v", request)
	}
}

func TestInspectionResponseIsDiscardedAfterSelectionOrRecordChanges(t *testing.T) {
	d := New()
	snapshot := control.Snapshot{
		"projects": {record(dashboardProjectOne, model.Project{ID: dashboardProjectOne, Name: "Repo"})},
		"execution_plans": {
			record(dashboardPlanOne, model.ExecutionPlan{ID: dashboardPlanOne, ProjectID: dashboardProjectOne, PlanDocument: &model.PlanDocument{Hash: dashboardHashOne}}),
			record("plan-2", model.ExecutionPlan{ID: "plan-2", ProjectID: dashboardProjectOne, PlanDocument: &model.PlanDocument{Hash: "hash-2"}}),
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
	snapshot["execution_plans"][0] = record(dashboardPlanOne, model.ExecutionPlan{ID: dashboardPlanOne, ProjectID: dashboardProjectOne, PlanDocument: &model.PlanDocument{Hash: "changed"}})
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
	current := model.ExecutionPlan{ID: dashboardPlanOne, ProjectID: dashboardProjectOne, Stage: "paused", Wait: &model.ExecutionPlanWait{AttemptID: dashboardFixtureCurrentAttemptID}}
	d.Present(control.Snapshot{
		"projects":        {record(dashboardProjectOne, model.Project{ID: dashboardProjectOne, Name: "Repo"})},
		"execution_plans": {record(dashboardPlanOne, current)},
		"questions": {
			record("question-old", model.QuestionSet{ID: "question-old", TaskID: dashboardPlanOne, AttemptID: "attempt-old", Status: "pending", Questions: []model.Question{{ID: "old", Text: "Old attempt question"}}}),
			record("question-current", model.QuestionSet{ID: "question-current", TaskID: dashboardPlanOne, AttemptID: dashboardFixtureCurrentAttemptID, Status: "pending", Questions: []model.Question{{ID: "current", Text: "Current attempt question"}}}),
		},
		"permissions": {
			record("permission-old", map[string]any{"plan_id": dashboardPlanOne, "attempt_id": "attempt-old", "decision": "pending", "request": map[string]any{"tool": "old-tool"}}),
			record("permission-current", map[string]any{"plan_id": dashboardPlanOne, "attempt_id": dashboardFixtureCurrentAttemptID, "decision": "pending", "request": map[string]any{"tool": "current-tool"}}),
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
		"execution_plans": {record(dashboardPlanOne, model.ExecutionPlan{ID: dashboardPlanOne, ProjectID: dashboardProjectOne, Score: 9, Delivery: &model.LocalDelivery{
			Stage: "accepted", Score: 9, Adherence: &model.Adherence{Score: 92.5}, UnmetRequirements: []string{dashboardFixtureSecondaryRequirement},
		}})},
	})
	view := d.detail.GetText(false)
	for _, expected := range []string{"Stored plan score (raw; not acceptance evidence): 9", "Computed adherence: 92.5%", dashboardFixtureSecondaryRequirement} {
		if !strings.Contains(view, expected) {
			t.Errorf("inspection omitted correct acceptance score provenance %q: %s", expected, view)
		}
	}
	if strings.Contains(view, "score: 9%") || strings.Contains(view, "Adherence: 9%") {
		t.Fatalf("raw legacy score was presented as adherence: %s", view)
	}
}

func TestParityFinalizerInspectionDistinguishesDeliveryFromAcceptedCommit(t *testing.T) {
	d := New()
	d.Present(control.Snapshot{"projects": {record(dashboardProjectOne, model.Project{ID: dashboardProjectOne, Name: "Repo"})}, "execution_plans": {record(dashboardPlanOne, model.ExecutionPlan{ID: dashboardPlanOne, ProjectID: dashboardProjectOne, PlanVersion: 2, Stage: "complete", PlanDocument: &model.PlanDocument{Path: "/plans/repo.md", ApprovedHash: "approved-document"}, Delivery: &model.LocalDelivery{Stage: "complete", Branch: "codex/delivery/plan", ReviewedCommit: "accepted-internal-commit", DeliveryCommit: "final-delivery-commit", Tree: "accepted-tree", VerificationID: "complete-verification", ReviewID: "accepted-review", Adherence: &model.Adherence{Score: 83.33333333333333}, UnmetRequirements: []string{"REQ-6"}, Diff: "+accepted source"}})}})
	view := d.detail.GetText(false)
	for _, expected := range []string{"commit: final-delivery-commit", "Accepted source commit: accepted-internal-commit", "Accepted tree: accepted-tree", "Verification: complete-verification", "Review: accepted-review", "/plans/repo.md", "approved-document", "REQ-6", "+accepted source"} {
		if !strings.Contains(view, expected) {
			t.Errorf("delivery inspection omitted %q: %s", expected, view)
		}
	}
}

func TestCriticalApprovalShowsMatchingSavedReviewAdherenceBeforeDelivery(t *testing.T) {
	provenance := &model.Provenance{Task: dashboardPlanOne, PlanVersion: 3, Head: "candidate-commit", Base: "base-commit", PolicyHash: "policy-hash", VerificationID: "verification-1", ReviewID: "review-1"}
	task := model.ExecutionPlan{ID: dashboardPlanOne, ProjectID: dashboardProjectOne, Stage: "approval", PlanVersion: 3, ReviewedCommit: provenance.Head, VerificationID: provenance.VerificationID, ReviewID: provenance.ReviewID, Score: 9}
	adherenceProvenance := *provenance
	review := model.Review{Provenance: provenance, Adherence: &model.Adherence{Score: 87.5, Total: 2, Provenance: adherenceProvenance, Results: []model.RequirementResult{{ID: dashboardFixturePrimaryRequirement, Status: "pass"}, {ID: dashboardFixtureSecondaryRequirement, Status: "fail"}}}}
	d := New()
	d.Present(control.Snapshot{
		"projects":        {record(dashboardProjectOne, model.Project{ID: dashboardProjectOne, Name: "Repo", PolicyHash: provenance.PolicyHash})},
		"execution_plans": {record(dashboardPlanOne, task)},
		"reviews":         {record(provenance.ReviewID, review)},
	})
	view := d.detail.GetText(false)
	for _, expected := range []string{"Computed adherence: 87.5%", dashboardFixtureSecondaryRequirement} {
		if !strings.Contains(view, expected) {
			t.Errorf("critical approval inspection omitted saved review evidence %q: %s", expected, view)
		}
	}
	if strings.Contains(view, "Computed adherence: 9%") {
		t.Fatalf("legacy score was presented as review adherence: %s", view)
	}
	stale := review
	stale.Adherence = &model.Adherence{Score: 100, Total: 2, Provenance: model.Provenance{Task: dashboardPlanOne, PlanVersion: 2, Head: "older-candidate", PolicyHash: provenance.PolicyHash, VerificationID: provenance.VerificationID, ReviewID: provenance.ReviewID}, Results: []model.RequirementResult{{ID: dashboardFixtureSecondaryRequirement, Status: "pass"}}}
	d.Present(control.Snapshot{
		"projects":        {record(dashboardProjectOne, model.Project{ID: dashboardProjectOne, Name: "Repo", PolicyHash: provenance.PolicyHash})},
		"execution_plans": {record(dashboardPlanOne, task)},
		"reviews":         {record(provenance.ReviewID, stale)},
	})
	if strings.Contains(d.detail.GetText(false), "Computed adherence: 100%") {
		t.Fatal("stale review report provenance was displayed as current acceptance evidence")
	}
}

func TestFinalizationApprovalRequestBindsExactCandidateAndReview(t *testing.T) {
	provenance := model.Provenance{Task: dashboardPlanOne, PlanVersion: 4, Head: "reviewed-candidate", PolicyHash: dashboardFixtureApprovedPolicy, VerificationID: "verification-current", ReviewID: "review-current"}
	plan := model.ExecutionPlan{ID: dashboardPlanOne, ProjectID: dashboardProjectOne, Stage: "approval", Risk: "critical", PlanVersion: 4, ReviewedCommit: provenance.Head, ReviewID: provenance.ReviewID, VerificationID: provenance.VerificationID, PlanDocument: &model.PlanDocument{ApprovedHash: dashboardFixtureApprovedPlanID}}
	view := control.FinalizationReport{Task: plan, Review: model.Review{Provenance: &provenance, Adherence: &model.Adherence{Score: 90, Total: 1, Accepted: true, Provenance: provenance, Results: []model.RequirementResult{{ID: dashboardFixturePrimaryRequirement, Status: "pass"}}}}, Adherence: model.Adherence{Score: 90, Total: 1, Accepted: true, Provenance: provenance, Results: []model.RequirementResult{{ID: dashboardFixturePrimaryRequirement, Status: "pass"}}}, ApprovedPlanHash: dashboardFixtureApprovedPlanID, PolicyHash: dashboardFixtureApprovedPolicy, Diff: control.FinalizationDiffPage{Content: "diff --git a/file b/file\n+approved candidate\n"}, Accepted: true}
	request, err := finalizationApprovalRequest(plan, 17, dashboardFixtureApprovedPolicy, view)
	if err != nil {
		t.Fatal(err)
	}
	if request.Operation != "finalization.approve" || request.PlanID != plan.ID || request.ExpectedVersion != 17 || request.IdempotencyKey == "" {
		t.Fatalf("finalization action lost task CAS/receipt binding: %+v", request)
	}
	var approval map[string]any
	if err := json.Unmarshal(request.Payload, &approval); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"plan_version": float64(plan.PlanVersion), "approved_plan_hash": dashboardFixtureApprovedPlanID, "reviewed_commit": provenance.Head, "review_id": provenance.ReviewID, "verification_id": provenance.VerificationID, "policy_hash": provenance.PolicyHash}
	if !maps.Equal(approval, want) {
		t.Fatalf("approval request did not bind the exact reviewed candidate: %+v", approval)
	}
	view.Task.ReviewedCommit = "newer-candidate"
	if _, err := finalizationApprovalRequest(plan, 17, dashboardFixtureApprovedPolicy, view); err == nil {
		t.Fatal("changed candidate report was approved")
	}
}

func TestCriticalFinalizationFormShowsExactDiffReportAndLocalOnlyAction(t *testing.T) {
	provenance := model.Provenance{Task: dashboardPlanOne, PlanVersion: 4, Head: "reviewed-candidate", PolicyHash: dashboardFixtureApprovedPolicy, VerificationID: "verification-current", ReviewID: "review-current"}
	plan := model.ExecutionPlan{ID: dashboardPlanOne, ProjectID: dashboardProjectOne, Stage: "approval", Risk: "critical", PlanVersion: 4, ReviewedCommit: provenance.Head, ReviewID: provenance.ReviewID, VerificationID: provenance.VerificationID, PlanDocument: &model.PlanDocument{ApprovedHash: dashboardFixtureApprovedPlanID}}
	report := control.FinalizationReport{Task: plan, Review: model.Review{Summary: "Candidate review summary", Provenance: &provenance, Adherence: &model.Adherence{Score: 82, Total: 2, Accepted: true, Provenance: provenance, Results: []model.RequirementResult{{ID: dashboardFixtureSecondaryRequirement, Status: "fail"}}}}, Adherence: model.Adherence{Score: 82, Total: 2, Accepted: true, Provenance: provenance, Results: []model.RequirementResult{{ID: dashboardFixtureSecondaryRequirement, Status: "fail"}}}, ApprovedPlanHash: dashboardFixtureApprovedPlanID, PolicyHash: dashboardFixtureApprovedPolicy, Diff: control.FinalizationDiffPage{Content: "diff --git a/file b/file\n+exact reviewed code\n"}, Accepted: true}
	d := New()
	screen := tcell.NewSimulationScreen("UTF-8")
	d.SetScreen(screen)
	t.Cleanup(screen.Fini)
	screen.SetSize(140, 45)
	d.Present(control.Snapshot{
		"projects":        {record(dashboardProjectOne, model.Project{ID: dashboardProjectOne, PolicyHash: dashboardFixtureApprovedPolicy})},
		"execution_plans": {record(dashboardPlanOne, plan)},
	})
	d.showFinalizationApproval(plan, 1, report, []int64{0})
	d.ForceDraw()
	text := screenText(screen)
	for _, expected := range []string{"Approve local finalization only", "Computed adherence: 82%", dashboardFixtureSecondaryRequirement, "Exact reviewed candidate diff", "+exact reviewed code", "No pull request or branch push"} {
		if !strings.Contains(text, expected) {
			t.Errorf("critical approval form omitted %q: %s", expected, text)
		}
	}
}
