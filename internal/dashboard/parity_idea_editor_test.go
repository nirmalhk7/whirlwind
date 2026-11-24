package dashboard

import (
	"context"
	"encoding/json"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"strings"
	"testing"
	"time"
	"whirlwind/internal/control"
	"whirlwind/internal/model"
)

func parityIdeaDashboard(t *testing.T, status string) (*Dashboard, model.Idea, chan control.Request) {
	t.Helper()
	d := New(t.TempDir())
	dashboardScreen(t, d)
	i := model.Idea{Title: "Invoice export", ID: "idea", ProjectID: "p", ProjectName: "Project", RunID: "source-run", Direction: "expand", What: "Export invoices", Why: "Reconcile monthly totals", Where: "billing", HowItHelps: "Keep audit history", DecisionStatus: status, NoveltyMethod: "legacy_llm"}
	r := record(i.ID, i)
	r.Version = 4
	d.Present(control.Snapshot{"projects": {record("p", model.Project{ID: "p", Name: "Project"})}, "ideas": {r}})
	requests := make(chan control.Request, 2)
	d.SetCommandHandler(func(_ context.Context, r control.Request) (json.RawMessage, error) {
		requests <- r
		return json.RawMessage(`{}`), nil
	})
	return d, i, requests
}

func pressIdeaButton(t *testing.T, form *tview.Form, label string) {
	t.Helper()
	for index := 0; index < form.GetButtonCount(); index++ {
		button := form.GetButton(index)
		if button.GetLabel() == label {
			button.InputHandler()(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone), nil)
			return
		}
	}
	t.Fatalf("missing %s button", label)
}

func TestParityIdeaEditorSavesFourFieldsWithCapturedVersion(t *testing.T) {
	d, i, requests := parityIdeaDashboard(t, "draft")
	if len(d.directionRows) != 1 || d.directionRows[0].ID != i.ID {
		t.Fatal("canonical idea registry missing from Inbox")
	}
	d.confirmDirection()
	form, ok := d.modal.(*tview.Form)
	if !ok || form.GetFormItemCount() != 4 {
		t.Fatalf("four-field draft editor unavailable: %T", d.modal)
	}
	what, ok := form.GetFormItem(0).(*tview.TextArea)
	if !ok {
		t.Fatal("draft field cannot preserve multiline content")
	}
	what.SetText("Export monthly invoices\nPreserve totals", false)
	pressIdeaButton(t, form, "Save")
	select {
	case request := <-requests:
		var draft model.IdeaDraft
		if err := json.Unmarshal(request.Payload, &draft); err != nil {
			t.Fatal(err)
		}
		if request.Operation != "idea.update" || request.ResourceID != i.ID || request.ProjectID != i.ProjectID || request.ExpectedVersion != 4 || draft.What != "Export monthly invoices\nPreserve totals" || draft.Why != i.Why || draft.Where != i.Where || draft.HowItHelps != i.HowItHelps {
			t.Fatalf("editor lost captured identity/content: %+v %+v", request, draft)
		}
	case <-time.After(time.Second):
		t.Fatal("draft save was not sent to supervisor")
	}
}

func TestParityCompletedPlannerShowsSeparatePlanApproval(t *testing.T) {
	d, idea, _ := parityIdeaDashboard(t, "complete")
	idea.PlanID = "linked-plan"
	d.snapshot["ideas"] = []model.Record{record(idea.ID, idea)}
	plan := model.ExecutionPlan{ID: idea.PlanID, ProjectID: idea.ProjectID, Stage: "plan_approval", PlanDocument: &model.PlanDocument{Hash: "document"}}
	d.snapshot["execution_plans"] = []model.Record{record(plan.ID, plan)}
	d.showIdeaRecord(idea, 1)
	if text := d.ideaDetailView.GetText(false); !strings.Contains(text, "Planner completed") || !strings.Contains(text, "Awaiting approval") {
		t.Fatalf("authoring/approval states conflated: %s", text)
	}
	d.closeModal()
	plan.Stage = "coding"
	plan.PlanDocument.ApprovedHash = "document"
	d.snapshot["execution_plans"] = []model.Record{record(plan.ID, plan)}
	d.showIdeaRecord(idea, 1)
	if text := d.ideaDetailView.GetText(false); !strings.Contains(text, "Planner completed") || !strings.Contains(text, "Plan Approved") {
		t.Fatalf("approval state unavailable: %s", text)
	}
}

func TestParityIdeaDirtyDraftCannotBeApproved(t *testing.T) {
	d, _, requests := parityIdeaDashboard(t, "draft")
	d.confirmDirection()
	form, ok := d.modal.(*tview.Form)
	if !ok {
		t.Fatal("idea decision form missing")
	}
	form.GetFormItem(0).(*tview.TextArea).SetText("Unsaved new proposal", false)
	pressIdeaButton(t, form, "Approve")
	select {
	case request := <-requests:
		t.Fatalf("unsaved content was approved: %+v", request)
	default:
	}
	if !strings.Contains(strings.ToLower(d.status.GetText(false)), "save") {
		t.Fatal("approval did not explain unsaved draft")
	}
}

func TestParityApprovedIdeaIsReadOnlyAndShowsProvenance(t *testing.T) {
	d, _, _ := parityIdeaDashboard(t, "approved")
	d.confirmDirection()
	form, ok := d.modal.(*tview.Form)
	if !ok || form.GetFormItemCount() != 0 {
		t.Fatalf("non-draft idea remained editable: %T", d.modal)
	}
	if d.ideaDetailView == nil {
		t.Fatal("idea details unavailable")
	}
	for _, text := range []string{"source-run", "approved", "Awaiting Planner", "Recorded legacy assessment ()", "billing"} {
		if !strings.Contains(d.ideaDetailView.GetText(false), text) {
			t.Fatalf("missing provenance %q", text)
		}
	}
}

func TestParityDeferredIdeaShowsUniquenessWasNotChecked(t *testing.T) {
	body := ideaDetails(model.Idea{NoveltyMethod: "not_checked", NoveltyOutcome: "deferred"})
	if !strings.Contains(body, "Uniqueness: Not checked (deferred)") {
		t.Fatalf("deferred comparison status is unclear: %s", body)
	}
}

func TestParityLegacyIdeaShowsItsRecordedAssessment(t *testing.T) {
	body := ideaDetails(model.Idea{NoveltyMethod: "legacy_llm", NoveltyOutcome: "accepted"})
	if !strings.Contains(body, "Uniqueness: Recorded legacy assessment (accepted)") {
		t.Fatalf("legacy assessment was reinterpreted: %s", body)
	}
}

func TestParityIdeaStaleFormCannotApprove(t *testing.T) {
	d, _, requests := parityIdeaDashboard(t, "draft")
	d.confirmDirection()
	form := d.modal.(*tview.Form)
	d.snapshot["ideas"][0].Version++
	pressIdeaButton(t, form, "Approve")
	select {
	case request := <-requests:
		t.Fatalf("stale decision submitted: %+v", request)
	default:
	}
	if !strings.Contains(d.status.GetText(false), "reopen") {
		t.Fatal("stale decision unexplained")
	}
}
func TestParityIdeaOfflineDraftRemainsInspectable(t *testing.T) {
	d, _, requests := parityIdeaDashboard(t, "draft")
	d.SetCommandHandler(nil)
	d.confirmDirection()
	form := d.modal.(*tview.Form)
	pressIdeaButton(t, form, "Save")
	if d.modal != form || !strings.Contains(d.status.GetText(false), "unavailable") {
		t.Fatal("offline draft lost")
	}
	select {
	case request := <-requests:
		t.Fatalf("offline command: %+v", request)
	default:
	}
}
func TestParityIdeaDeleteArchivesWithReason(t *testing.T) {
	d, _, requests := parityIdeaDashboard(t, "draft")
	d.deleteInboxProposal()
	form, ok := d.modal.(*tview.Form)
	if !ok {
		t.Fatal("archival reason missing")
	}
	form.GetFormItem(0).(*tview.TextArea).SetText("No longer needed", false)
	pressIdeaButton(t, form, "Archive")
	select {
	case request := <-requests:
		if request.Operation != "idea.archive" || request.ExpectedVersion != 4 || !strings.Contains(string(request.Payload), "No longer needed") {
			t.Fatalf("wrong archive: %+v", request)
		}
	case <-time.After(time.Second):
		t.Fatal("archive missing")
	}
}
func TestParityIdeaMessageOpensCanonicalEditor(t *testing.T) {
	d, _, _ := parityIdeaDashboard(t, "draft")
	d.viewMessage(0)
	form, ok := d.modal.(*tview.Form)
	if !ok || form.GetFormItemCount() != 4 {
		t.Fatal("idea message bypassed canonical content")
	}
}

func TestParityActiveIdeaOffersOnlyPlannerCancellation(t *testing.T) {
	d, i, requests := parityIdeaDashboard(t, "in_process")
	d.confirmDirection()
	form := d.modal.(*tview.Form)
	if form.GetFormItemCount() != 0 {
		t.Fatal("active idea became editable")
	}
	pressIdeaButton(t, form, "Cancel planning")
	select {
	case request := <-requests:
		if request.Operation != "planner.cancel" || request.ResourceID != i.ID || request.ExpectedVersion != 4 {
			t.Fatalf("wrong cancellation: %+v", request)
		}
	case <-time.After(time.Second):
		t.Fatal("cancel missing")
	}
}

func TestParityArchivedIdeaHistoryOffersReasonedReconsideration(t *testing.T) {
	d, i, requests := parityIdeaDashboard(t, "draft")
	i.ArchivedAt, i.ArchiveReason = "2026-11-11T00:00:00Z", "Priorities changed"
	r := record(i.ID, i)
	r.Version = 4
	d.snapshot["ideas"] = []model.Record{r}
	d.refreshDirections()
	d.reconsiderDirectionHistory()
	list, ok := d.modal.(*tview.List)
	if !ok || list.GetItemCount() != 1 {
		t.Fatal("archived canonical idea missing from history")
	}
	list.InputHandler()(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone), nil)
	form, ok := d.modal.(*tview.Form)
	if !ok || form.GetFormItemCount() != 0 {
		t.Fatal("archived idea editable")
	}
	pressIdeaButton(t, form, "Reconsider")
	reason := d.modal.(*tview.Form)
	reason.GetFormItem(0).(*tview.TextArea).SetText("Accounting requirements changed", false)
	pressIdeaButton(t, reason, "Request reconsideration")
	select {
	case request := <-requests:
		if request.Operation != "idea.reconsider" || request.ExpectedVersion != 4 || !strings.Contains(string(request.Payload), "Accounting requirements changed") {
			t.Fatalf("wrong reconsideration: %+v", request)
		}
	case <-time.After(time.Second):
		t.Fatal("reconsideration missing")
	}
}

func TestParityCompletedIdeaShowsPlannerCompletion(t *testing.T) {
	body := ideaDetails(model.Idea{DecisionStatus: "complete", PlanID: "plan"})
	if !strings.Contains(body, "Planning: Planner completed") || !strings.Contains(body, "Linked plan: plan") {
		t.Fatalf("completed idea lost planning result: %s", body)
	}
}

func TestParityIdeaNavigationShowsSeparateDisplayTitle(t *testing.T) {
	d, idea, _ := parityIdeaDashboard(t, "draft")
	if len(d.directionRows) != 1 || d.directionRows[0].Title != idea.Title {
		t.Fatalf("navigation lost short idea title: %+v", d.directionRows)
	}
	main, _ := d.directions.GetItemText(0)
	if !strings.Contains(main, idea.Title) {
		t.Fatalf("navigation replaced title with What: %q", main)
	}
}

func TestParityApprovedIdeaOffersCancellationBeforeProviderStarts(t *testing.T) {
	d, idea, requests := parityIdeaDashboard(t, "approved")
	d.confirmDirection()
	form, ok := d.modal.(*tview.Form)
	if !ok || form.GetFormItemCount() != 0 {
		t.Fatal("approved idea did not open a read-only view")
	}
	pressIdeaButton(t, form, "Cancel planning")
	select {
	case request := <-requests:
		if request.Operation != "planner.cancel" || request.ResourceID != idea.ID || request.ProjectID != idea.ProjectID || request.ExpectedVersion != 4 {
			t.Fatalf("wrong queued cancellation: %+v", request)
		}
	case <-time.After(time.Second):
		t.Fatal("queued cancellation unavailable")
	}
}
