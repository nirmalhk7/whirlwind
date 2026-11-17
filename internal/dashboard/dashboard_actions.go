package dashboard

import (
	"encoding/json"

	"whirlwind/internal/control"
	"whirlwind/internal/model"
	"whirlwind/internal/store"

	"github.com/rivo/tview"
)

func (d *Dashboard) answerQuestionsForm() {
	index := d.plans.GetCurrentItem()
	if d.planID == "" || index < 0 || index >= len(d.planRows) {
		return
	}
	plan := d.planRows[index]
	attemptID, hasCurrentAttempt := currentPausedAttempt(plan)
	if !hasCurrentAttempt {
		d.status.SetText("Whirlwind | no current paused attempt owns a clarification question")
		return
	}
	for _, record := range d.snapshot["questions"] {
		questions, err := store.Decode[model.QuestionSet](record)
		if err != nil || questions.TaskID != plan.ID || questions.Status != "pending" || questions.AttemptID != attemptID {
			continue
		}
		form := tview.NewForm()
		form.AddTextView("Questions", "Answer each saved question. Answers are stored once against this question set.", 0, 3, true, false)
		answers := map[string]string{}
		for _, question := range questions.Questions {
			q := question
			if q.Secret {
				form.AddPasswordField(tview.Escape(q.Text), "", 40, '*', func(value string) { answers[q.ID] = value })
			} else {
				form.AddInputField(tview.Escape(q.Text), "", 40, nil, func(value string) { answers[q.ID] = value })
			}
		}
		changesSpecification := false
		form.AddCheckbox("Changes approved specification", false, func(value bool) { changesSpecification = value })
		form.AddButton("Save answers", func() {
			payload, _ := json.Marshal(map[string]any{"answers": answers, "changes_specification": changesSpecification})
			d.closeModal()
			d.submit(control.Request{Operation: "question.answer", ResourceID: questions.ID, PlanID: plan.ID, ExpectedVersion: record.Version, IdempotencyKey: store.ID(), Payload: payload})
		})
		d.showForm(form)
		return
	}
	d.status.SetText("Whirlwind | no pending clarification questions for this plan")
}

func (d *Dashboard) permissionForm() {
	index := d.plans.GetCurrentItem()
	if d.planID == "" || index < 0 || index >= len(d.planRows) {
		return
	}
	plan := d.planRows[index]
	attemptID, hasCurrentAttempt := currentPausedAttempt(plan)
	if !hasCurrentAttempt {
		d.status.SetText("Whirlwind | no current paused attempt owns a permission request")
		return
	}
	for _, record := range d.snapshot["permissions"] {
		var value struct {
			Task     string `json:"plan_id"`
			Attempt  string `json:"attempt_id"`
			Decision string `json:"decision"`
		}
		if json.Unmarshal(record.Data, &value) != nil || value.Task != plan.ID || value.Attempt != attemptID || value.Decision != "pending" {
			continue
		}
		modal := tview.NewModal().SetText(tview.Escape(permissionSummary(record))).AddButtons([]string{"Allow", "Deny", "Cancel"})
		modal.SetDoneFunc(func(button int, _ string) {
			d.closeModal()
			if button < 2 {
				decision := []string{"allow", "deny"}[button]
				payload, _ := json.Marshal(map[string]string{"decision": decision})
				d.submit(control.Request{Operation: "permission.decide", ResourceID: record.ID, PlanID: plan.ID, ExpectedVersion: record.Version, IdempotencyKey: store.ID(), Payload: payload})
			}
		})
		d.modal = modal
		d.SetRoot(modal, true).SetFocus(modal)
		return
	}
	d.status.SetText("Whirlwind | no pending permission request for this plan")
}

func currentPausedAttempt(plan model.ExecutionPlan) (string, bool) {
	if plan.Stage != "paused" || plan.Wait == nil || plan.Wait.AttemptID == "" {
		return "", false
	}
	return plan.Wait.AttemptID, true
}

func (d *Dashboard) supervisorStatus(snapshot control.Snapshot) string {
	if supervisorOnline(snapshot) {
		return "Whirlwind | local supervisor connected"
	}
	return "Whirlwind | supervisor offline · read-only database inspection"
}

func (d *Dashboard) finalizationView() {
	plan, version, ok := d.selectedPlanRecord()
	if !ok {
		return
	}
	if !d.hasCommandHandler("Whirlwind | local supervisor unavailable; completion remains inspectable from saved records") {
		return
	}
	d.loadFinalizationReport(plan, version, 0, []int64{0})
}

func (d *Dashboard) selectedPlanRecord() (model.ExecutionPlan, int, bool) {
	index := d.plans.GetCurrentItem()
	if d.planID == "" || index < 0 || index >= len(d.planRows) {
		return model.ExecutionPlan{}, 0, false
	}
	plan := d.planRows[index]
	record, _, ok := d.planRecord(plan.ID)
	return plan, record.Version, ok
}

func (d *Dashboard) hasCommandHandler(message string) bool {
	if d.commands != nil {
		return true
	}
	d.status.SetText(message)
	return false
}
