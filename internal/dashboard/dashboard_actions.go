package dashboard

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
	"whirlwind/internal/control"
	"whirlwind/internal/model"
	"whirlwind/internal/store"

	"github.com/rivo/tview"
)

func (d *Dashboard) confirmDirection() {
	index := d.directions.GetCurrentItem()
	if index < 0 || index >= len(d.directionRows) {
		return
	}
	direction := d.directionRows[index]
	version := d.directionRecordVersion(direction.ID)

	action, reason, ok := d.editDecision("# Proposal: "+direction.Title+"\n\n"+messageText(direction), []string{"approve", "reject"})
	if !ok || action == "cancel" {
		return
	}
	if action == "approve" && reason != "" {
		d.status.SetText("Whirlwind | invalid decision: proposal reason applies to reject only")
		return
	}
	request := directionApprovalRequest(direction, version)
	if action == "reject" {
		request.Operation = "direction.reject"
		request.Payload, _ = json.Marshal(map[string]string{"reason": reason})
	}
	d.closeModal()
	d.submit(request)
}

func (d *Dashboard) directionRecordVersion(id string) int {
	for _, record := range d.snapshot["directions"] {
		if record.ID == id {
			return record.Version
		}
	}
	return 0
}

func (d *Dashboard) reviewPlanForm() {
	plan, version, ok := d.selectedPlanRecord()
	if !ok {
		return
	}
	_, currentPlan, ok := d.planRecord(plan.ID)
	if !ok || currentPlan.PlanDocument == nil {
		d.status.SetText("Whirlwind | plan changed since inspection; refresh before approval")
		return
	}
	if !d.hasCommandHandler("Whirlwind | local supervisor unavailable; plan remains unchanged") {
		return
	}
	d.openPlanContentReview(currentPlan, version, currentPlan.PlanDocument.Hash)
}

func (d *Dashboard) openPlanContentReview(plan model.Task, version int, hash string) {
	d.status.SetText("Whirlwind | loading exact plan for review…")
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		response, err := d.commands(ctx, control.Request{RequestID: store.ID(), Operation: "inspect.plan", PlanID: plan.ID})
		var view struct {
			Path    string `json:"path"`
			Hash    string `json:"hash"`
			Content string `json:"content"`
		}
		if err == nil {
			err = json.Unmarshal(response, &view)
		}
		d.QueueUpdateDraw(func() {
			if !d.planStillCurrent(plan.ID, version, hash) {
				return
			}
			if err != nil {
				d.status.SetText("Whirlwind | could not load plan: " + tview.Escape(err.Error()))
				return
			}
			if view.Hash == "" || view.Hash != hash {
				d.status.SetText("Whirlwind | plan changed since inspection; refresh before approval")
				return
			}
			d.showPlanReview(plan, view.Content, view.Hash, version)
		})
	}()
}

func (d *Dashboard) showPlanReview(plan model.Task, content, hash string, expectedVersion int) {
	action, feedback, ok := d.editDecision("# Plan: "+plan.PlanDocument.Path+"\nHash: "+hash+"\nVersion: "+fmt.Sprint(expectedVersion)+"\n\n"+content, []string{"approve", "revise", "reject"})
	if !ok || action == "cancel" {
		return
	}
	payload, _ := json.Marshal(map[string]string{"action": action, "hash": hash, "feedback": feedback})
	d.closeModal()
	d.submit(planReviewRequest(plan, expectedVersion, hash, payload))
}

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
			Task     string `json:"task"`
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

func currentPausedAttempt(plan model.Task) (string, bool) {
	if plan.Stage != "paused" || plan.Wait == nil || plan.Wait.AttemptID == "" {
		return "", false
	}
	return plan.Wait.AttemptID, true
}

func (d *Dashboard) supervisorStatus(snapshot control.Snapshot) string {
	for _, record := range snapshot["supervisor"] {
		var value struct {
			Online bool `json:"online"`
		}
		if json.Unmarshal(record.Data, &value) == nil && value.Online {
			return "Whirlwind | local supervisor connected"
		}
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

func (d *Dashboard) loadLatestLog() {
	plan, version, ok := d.selectedPlanRecord()
	if !ok {
		return
	}
	if !d.hasCommandHandler("Whirlwind | local supervisor unavailable; logs remain in saved artifacts") {
		return
	}
	artifactID := latestOutputArtifact(d.snapshot["sessions"], plan.ID)
	if artifactID == "" {
		d.status.SetText("Whirlwind | no saved output artifact for this plan")
		return
	}
	d.fetchArtifact(plan.ID, version, artifactID)
}

func (d *Dashboard) fetchArtifact(planID string, version int, artifactID string) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		response, err := d.commands(ctx, control.Request{RequestID: store.ID(), Operation: "inspect.artifact", PlanID: planID, ResourceID: artifactID})
		d.QueueUpdateDraw(func() {
			if !d.planStillCurrent(planID, version, "") {
				return
			}
			if err != nil {
				d.status.SetText("Whirlwind | log retrieval failed: " + tview.Escape(err.Error()))
				return
			}
			var view struct {
				Content   string `json:"content"`
				Truncated bool   `json:"truncated"`
			}
			if json.Unmarshal(response, &view) != nil {
				d.status.SetText("Whirlwind | saved log is invalid")
				return
			}
			d.detail.SetText(view.Content)
			if view.Truncated {
				d.status.SetText("Whirlwind | showing bounded artifact prefix")
			}
		})
	}()
}

func (d *Dashboard) selectedPlanRecord() (model.Task, int, bool) {
	index := d.plans.GetCurrentItem()
	if d.planID == "" || index < 0 || index >= len(d.planRows) {
		return model.Task{}, 0, false
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

func latestOutputArtifact(records []model.Record, planID string) string {
	artifact := ""
	for _, record := range records {
		attempt, err := store.Decode[model.Attempt](record)
		if err == nil && attempt.Task == planID && attempt.OutputArtifact != "" {
			artifact = attempt.OutputArtifact
		}
	}
	return artifact
}
