package dashboard

import (
	"encoding/json"
	"github.com/rivo/tview"
	"strings"
	"whirlwind/internal/control"
	"whirlwind/internal/model"
)

func (d *Dashboard) showIdeaHistory(archivedOnly bool) {
	list := tview.NewList().ShowSecondaryText(false)
	list.SetBorder(true).SetTitle(" Idea history · Enter to inspect · Esc to close ")
	for _, record := range d.snapshot["ideas"] {
		idea, version, ok := d.ideaRecord(record.ID)
		if !ok || (archivedOnly && idea.ArchivedAt == "") {
			continue
		}
		label := idea.DecisionStatus
		if idea.ArchivedAt != "" {
			label += " · archived"
		}
		list.AddItem(tview.Escape(idea.What+" · "+label), "", 0, func() { d.showIdeaRecord(idea, version) })
	}
	if list.GetItemCount() == 0 {
		d.status.SetText("Whirlwind | no archived ideas for this project")
		return
	}
	d.modal = list
	d.SetRoot(list, true).SetFocus(list)
}

func (d *Dashboard) reconsiderIdeaForm(idea model.Idea, version int) {
	form := tview.NewForm().AddTextArea("Reason", "", 0, 4, 0, nil)
	form.AddButton("Request reconsideration", func() {
		reason := strings.TrimSpace(form.GetFormItem(0).(*tview.TextArea).GetText())
		if reason == "" {
			form.SetTitle(" Human reason required ")
			return
		}
		_, current, ok := d.ideaRecord(idea.ID)
		if !ok || current != version {
			form.SetTitle(" Idea changed; reopen history ")
			return
		}
		payload, _ := json.Marshal(map[string]string{"reason": reason})
		d.closeModal()
		d.submit(control.Request{Operation: "idea.reconsider", ProjectID: idea.ProjectID, ResourceID: idea.ID, ExpectedVersion: version, Payload: payload})
	})
	d.showForm(form)
}
