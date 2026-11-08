package dashboard

import (
	"encoding/json"
	"strings"

	"github.com/rivo/tview"
	"whirlwind/internal/control"
	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

type reconsiderationTarget struct {
	direction model.Direction
	version   int
}

func (d *Dashboard) reconsiderDirectionHistory() {
	projectID := d.projectID
	if projectID == "" {
		d.status.SetText("Whirlwind | select a project before opening direction history")
		return
	}
	targets := reconsiderableDirections(d.snapshot["directions"], projectID)
	if len(targets) == 0 {
		d.status.SetText("Whirlwind | no dismissed or rejected directions for this project")
		return
	}
	d.showDirectionHistory(projectID, targets)
}

func reconsiderableDirections(records []model.Record, projectID string) []reconsiderationTarget {
	var targets []reconsiderationTarget
	for _, record := range records {
		direction, err := store.Decode[model.Direction](record)
		if err != nil || !eligibleForReconsideration(direction, record, projectID) {
			continue
		}
		targets = append(targets, reconsiderationTarget{direction: direction, version: record.Version})
	}
	return targets
}

func eligibleForReconsideration(direction model.Direction, record model.Record, projectID string) bool {
	return record.Version > 0 && record.ID != "" && direction.ID == record.ID && direction.ProjectID == projectID && !direction.Selected &&
		(direction.Status == "dismissed" || direction.Status == "rejected")
}

func (d *Dashboard) showDirectionHistory(projectID string, targets []reconsiderationTarget) {
	list := tview.NewList().ShowSecondaryText(false)
	list.SetBorder(true).SetTitle(" Project direction history · Enter to request reconsideration · Esc to close ")
	for _, target := range targets {
		captured := target
		label := tview.Escape(captured.direction.Title + " · " + captured.direction.Status)
		list.AddItem(label, "", 0, func() { d.showReconsiderationForm(projectID, captured) })
	}
	d.modal = list
	d.SetRoot(list, true).SetFocus(list)
}

func (d *Dashboard) showReconsiderationForm(projectID string, target reconsiderationTarget) {
	direction := target.direction
	form := tview.NewForm()
	details := "Title: " + direction.Title + "\nStatus: " + direction.Status
	if direction.RejectionReason != "" {
		details += "\nPrevious rejection: " + direction.RejectionReason
	}
	form.AddTextView("Saved direction", tview.Escape(details), 0, 5, true, false)
	reason := tview.NewInputField().SetLabel("Reason for reconsideration").SetFieldWidth(48)
	form.AddFormItem(reason)
	form.AddButton("Request reconsideration", func() {
		value := strings.TrimSpace(reason.GetText())
		if value == "" {
			form.SetTitle(" Reconsideration reason required ")
			d.status.SetText("Whirlwind | enter a reason before requesting reconsideration")
			return
		}
		payload, err := json.Marshal(struct {
			ProjectID string `json:"project_id"`
			Reason    string `json:"reason"`
		}{ProjectID: projectID, Reason: value})
		if err != nil {
			d.status.SetText("Whirlwind | could not prepare reconsideration request")
			return
		}
		d.closeModal()
		d.submit(control.Request{Operation: "direction.reconsider", ProjectID: projectID, ResourceID: direction.ID, ExpectedVersion: target.version, Payload: payload})
	})
	d.showForm(form)
}
