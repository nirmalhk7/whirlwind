package dashboard

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/rivo/tview"
	"whirlwind/internal/control"
	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

func (d *Dashboard) ideaRecord(id string) (model.Idea, int, bool) {
	for _, record := range d.snapshot["ideas"] {
		if record.ID == id {
			idea, err := store.Decode[model.Idea](record)
			return idea, record.Version, err == nil && idea.ProjectID == d.projectID
		}
	}
	return model.Idea{}, 0, false
}

func (d *Dashboard) hasCanonicalIdeas() bool {
	for _, record := range d.snapshot["ideas"] {
		if _, _, ok := d.ideaRecord(record.ID); ok {
			return true
		}
	}
	return false
}

func (d *Dashboard) refreshIdeas() bool {
	if !d.hasCanonicalIdeas() {
		return false
	}
	d.updating = true
	defer func() { d.updating = false }()
	d.directions.Clear()
	d.directionRows = nil
	selected := 0
	for _, record := range d.snapshot["ideas"] {
		idea, _, ok := d.ideaRecord(record.ID)
		if !ok || idea.ArchivedAt != "" {
			continue
		}
		if idea.ID == d.directionID {
			selected = len(d.directionRows)
		}
		d.directionRows = append(d.directionRows, model.Direction{ID: idea.ID, ProjectID: idea.ProjectID, Category: idea.Direction, Title: idea.Title, Description: idea.HowItHelps, Problem: idea.Why, Mechanism: idea.Mechanism, Components: idea.Components, Evidence: idea.Evidence, Status: idea.DecisionStatus})
		d.directions.AddItem(tview.Escape(fmt.Sprintf("Idea (%s) · %s · %s", idea.Direction, idea.DecisionStatus, idea.Title)), "", 0, nil)
	}
	if len(d.directionRows) == 0 {
		d.directionID = ""
		d.directions.AddItem("No active ideas", "", 0, nil)
	} else {
		d.directionID = d.directionRows[selected].ID
		d.directions.SetCurrentItem(selected)
	}
	return true
}

func ideaDetails(idea model.Idea) string {
	planning := map[string]string{"draft": "Not queued", "approved": "Awaiting Planner", "in_process": "Planner running", "complete": "Planner completed", "error": "Planner error: " + idea.ErrorReason}[idea.DecisionStatus]
	body := fmt.Sprintf("Idea: %s\nSource run: %s\nDirection: %s\nLifecycle: %s\nPlanning: %s\nLinked plan: %s\nNovelty: Temporary ACP/LLM novelty\n\nWhat\n%s\n\nWhy\n%s\n\nWhere\n%s\n\nHow it helps\n%s", idea.ID, idea.RunID, idea.Direction, idea.DecisionStatus, planning, idea.PlanID, idea.What, idea.Why, idea.Where, idea.HowItHelps)
	if idea.ArchivedAt != "" {
		body += "\n\nArchived: " + idea.ArchivedAt + "\nReason: " + idea.ArchiveReason
	}
	if idea.PredecessorID != "" {
		body += "\nPredecessor: " + idea.PredecessorID + "\nReconsideration reason: " + idea.RevisitReason
	}
	return body
}

func (d *Dashboard) showIdeaRecord(idea model.Idea, version int) {
	form, original, draft := ideaRecordEditor(idea)
	d.addIdeaDecisionButtons(form, idea, version, original, draft)
	form.AddButton("Close", d.closeModal)
	form.SetBorder(true).SetTitle(" Idea ")
	details := ideaDetails(idea)
	if idea.PlanID != "" {
		details += "\n\nPlan approval: " + d.ideaPlanApprovalState(idea)
	}
	d.ideaDetailView = tview.NewTextView().SetText(details).SetScrollable(true)
	d.ideaDetailView.SetBorder(true).SetTitle(" Saved idea and provenance ")
	root := tview.NewFlex().SetDirection(tview.FlexRow).AddItem(d.ideaDetailView, 0, 1, false).AddItem(form, 0, 2, true).AddItem(d.status, 1, 0, false)
	d.modal = form
	d.SetRoot(root, true).SetFocus(form)
}

func ideaRecordEditor(idea model.Idea) (*tview.Form, model.IdeaDraft, func() model.IdeaDraft) {
	form := tview.NewForm()
	original := model.IdeaDraft{What: idea.What, Why: idea.Why, Where: idea.Where, HowItHelps: idea.HowItHelps}
	draft := func() model.IdeaDraft { return original }
	if idea.DecisionStatus == "draft" && idea.ArchivedAt == "" {
		for _, field := range []struct{ label, value string }{{"What", idea.What}, {"Why", idea.Why}, {"Where", idea.Where}, {"How it helps", idea.HowItHelps}} {
			form.AddTextArea(field.label, field.value, 0, 3, 0, nil)
		}
		draft = func() model.IdeaDraft {
			return model.IdeaDraft{What: form.GetFormItem(0).(*tview.TextArea).GetText(), Why: form.GetFormItem(1).(*tview.TextArea).GetText(), Where: form.GetFormItem(2).(*tview.TextArea).GetText(), HowItHelps: form.GetFormItem(3).(*tview.TextArea).GetText()}
		}
	}
	return form, original, draft
}

func (d *Dashboard) sendIdeaDecision(idea model.Idea, version int, operation string, payload any) {
	_, current, ok := d.ideaRecord(idea.ID)
	if !ok || current != version {
		d.status.SetText("Whirlwind | idea changed; reopen before deciding")
		return
	}
	if d.commands == nil {
		d.status.SetText("Whirlwind | local supervisor unavailable; saved idea unchanged")
		return
	}
	body, err := json.Marshal(payload)
	if err != nil {
		d.status.SetText(err.Error())
		return
	}
	d.closeModal()
	d.submit(control.Request{Operation: operation, ResourceID: idea.ID, ProjectID: idea.ProjectID, ExpectedVersion: version, Payload: body})
}

func (d *Dashboard) addIdeaDecisionButtons(form *tview.Form, idea model.Idea, version int, original model.IdeaDraft, draft func() model.IdeaDraft) {
	send := func(operation string, payload any) { d.sendIdeaDecision(idea, version, operation, payload) }
	if idea.ArchivedAt != "" {
		form.AddButton("Reconsider", func() { d.reconsiderIdeaForm(idea, version) })
		return
	}
	if idea.DecisionStatus == "draft" {
		form.AddButton("Save", func() { send("idea.update", draft()) })
		form.AddButton("Approve", func() {
			if draft() != original {
				d.status.SetText("Whirlwind | Save changes before approval")
				return
			}
			send("idea.approve", struct{}{})
		})
	}
	if idea.DecisionStatus == "in_process" {
		form.AddButton("Cancel planning", func() { send("planner.cancel", struct{}{}) })
	}
	if idea.DecisionStatus == "error" {
		form.AddButton("Retry planning", func() { send("idea.retry_planning", struct{}{}) })
	}
	if idea.DecisionStatus != "in_process" {
		form.AddButton("Archive", func() { d.archiveIdeaForm(idea, version) })
	}
}

func (d *Dashboard) ideaPlanApprovalState(idea model.Idea) string {
	for _, record := range d.snapshot["execution_plans"] {
		if record.ID != idea.PlanID {
			continue
		}
		plan, err := store.Decode[model.ExecutionPlan](record)
		if err != nil || plan.ProjectID != idea.ProjectID {
			return "Linked plan unavailable; refresh inspection"
		}
		if plan.Stage == "cancelled" {
			return "Plan rejected or cancelled"
		}
		if plan.PlanDocument != nil && plan.PlanDocument.ApprovedHash != "" && plan.PlanDocument.ApprovedHash == plan.PlanDocument.Hash {
			return "Plan Approved"
		}
		return "Awaiting approval"
	}
	return "Linked plan unavailable; refresh inspection"
}

func (d *Dashboard) archiveIdeaForm(idea model.Idea, version int) {
	form := tview.NewForm().AddTextArea("Reason", "", 0, 4, 0, nil)
	form.AddButton("Archive", func() {
		reason := strings.TrimSpace(form.GetFormItem(0).(*tview.TextArea).GetText())
		if reason == "" {
			d.status.SetText("Whirlwind | archival reason required")
			return
		}
		_, current, ok := d.ideaRecord(idea.ID)
		if !ok || current != version {
			d.status.SetText("Whirlwind | idea changed; reopen before deciding")
			return
		}
		payload, _ := json.Marshal(map[string]string{"reason": reason})
		d.closeModal()
		d.submit(control.Request{Operation: "idea.archive", ResourceID: idea.ID, ProjectID: idea.ProjectID, ExpectedVersion: version, Payload: payload})
	})
	d.showForm(form)
}
