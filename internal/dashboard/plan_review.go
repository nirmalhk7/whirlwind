package dashboard

import (
	"context"
	"encoding/json"
	"strconv"
	"time"

	"github.com/rivo/tview"
	"whirlwind/internal/control"
	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

const (
	planChangedSinceInspectionMessage = "Whirlwind | plan changed since inspection; refresh before approval"
	planReviewUnavailableMessage      = "Whirlwind | local supervisor unavailable; plan remains unchanged"
)

func (d *Dashboard) reviewPlanForm() {
	plan, version, ok := d.selectedPlanRecord()
	if !ok {
		return
	}
	_, currentPlan, ok := d.planRecord(plan.ID)
	if !ok || currentPlan.PlanDocument == nil {
		d.status.SetText(planChangedSinceInspectionMessage)
		return
	}
	if !d.hasCommandHandler(planReviewUnavailableMessage) {
		return
	}
	d.openPlanContentReview(currentPlan, version, currentPlan.PlanDocument.Hash)
}

func (d *Dashboard) openPlanContentReview(plan model.ExecutionPlan, version int, hash string) {
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
				d.status.SetText(planChangedSinceInspectionMessage)
				return
			}
			d.showPlanReview(plan, view.Content, view.Hash, version)
		})
	}()
}

func (d *Dashboard) showPlanReview(plan model.ExecutionPlan, content, hash string, expectedVersion int) {
	d.showPlanReviewControls(plan, content, hash, expectedVersion)
}

func (d *Dashboard) showPlanReviewControls(plan model.ExecutionPlan, content, hash string, expectedVersion int) {
	view := tview.NewTextView().SetDynamicColors(false).SetScrollable(true).SetWrap(true)
	view.SetText("Plan: " + plan.PlanDocument.Path + "\nHash: " + hash + "\nVersion: " + strconv.Itoa(expectedVersion) + "\n\n" + content)
	form := tview.NewForm()
	feedback := ""
	form.AddInputField("Feedback", "", 36, nil, func(value string) { feedback = value })
	form.AddButton("Approve", func() { d.submitPlanReview(plan, "approve", feedback, hash, expectedVersion) })
	form.AddButton("Request changes", func() { d.submitPlanReview(plan, "revise", feedback, hash, expectedVersion) })
	form.AddButton("Reject", func() { d.submitPlanReview(plan, "reject", feedback, hash, expectedVersion) })
	form.AddButton("Cancel", d.closeModal)
	modal := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(view, 0, 1, false).
		AddItem(form, 5, 0, true)
	modal.SetBorder(true).SetTitle(" Plan review · F6 plan/controls · v Vim editor from plan · PgUp/PgDn scroll · Esc cancel ")
	d.modal = form
	d.planReviewView = view
	d.planReviewForm = form
	d.planReviewVim = func() {
		if !d.planStillCurrent(plan.ID, expectedVersion, hash) {
			d.closeModal()
			d.status.SetText(planChangedSinceInspectionMessage)
			return
		}
		if !d.hasCommandHandler("Whirlwind | local supervisor unavailable; plan remains unchanged") {
			return
		}
		edited, ok := d.editMarkdown(content)
		if ok {
			payload, _ := json.Marshal(map[string]string{"content": edited})
			d.closeModal()
			d.submit(control.Request{Operation: "plan.document.save", PlanID: plan.ID, ProjectID: plan.ProjectID, ExpectedVersion: expectedVersion, ContentHash: hash, Payload: payload})
		}
	}
	d.SetRoot(modal, true).SetFocus(form)
}

func (d *Dashboard) submitPlanReview(plan model.ExecutionPlan, action, feedback, hash string, expectedVersion int) {
	if action != "approve" && action != "revise" && action != "reject" {
		d.status.SetText("Whirlwind | invalid plan review action")
		return
	}
	if !d.planStillCurrent(plan.ID, expectedVersion, hash) {
		d.closeModal()
		d.status.SetText("Whirlwind | plan changed since inspection; refresh before approval")
		return
	}
	if !d.hasCommandHandler(planReviewUnavailableMessage) {
		d.closeModal()
		return
	}
	payload, _ := json.Marshal(map[string]string{"action": action, "hash": hash, "feedback": feedback})
	d.closeModal()
	d.submit(planReviewRequest(plan, expectedVersion, hash, payload))
}
