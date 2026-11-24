package dashboard

import (
	"strings"

	"github.com/rivo/tview"
)

type projectSetupNotice struct {
	inboxIndex int
	runIndex   int
}

func (d *Dashboard) addProjectSetupNotice() {
	var setupFailed bool
	for _, project := range d.projectRows {
		if project.ID == d.projectID {
			setupFailed = project.Status == "error"
			break
		}
	}
	if !setupFailed {
		return
	}
	// Setup and repository-path health are independent. Use the selected
	// project's stored setup outcome, not an old failure from another project.
	text := "Setup failed"
	if path, ok := d.projectHealth[d.projectID]; ok {
		text += " (Git path: " + path.Status + ")"
	}
	runIndex := -1
	for i, row := range d.runRows {
		if row.operation == nil || row.operation.Kind != "project_setup" || row.operation.Stage != "failed" {
			continue
		}
		runIndex = i
		if reason := strings.Join(strings.Fields(row.operation.Error), " "); reason != "" {
			text += ": " + reason
		}
		break
	}
	d.setupNotice = &projectSetupNotice{inboxIndex: d.inbox.GetItemCount(), runIndex: runIndex}
	d.inbox.AddItem(tview.Escape(text+" · Enter for details"), "", 0, nil)
	d.inboxTaskIDs = append(d.inboxTaskIDs, "")
	d.inboxMessages = append(d.inboxMessages, text)
}

func (d *Dashboard) openProjectSetupNotice(index int) bool {
	notice := d.setupNotice
	if notice == nil || index != notice.inboxIndex {
		return false
	}
	if notice.runIndex >= 0 {
		d.runs.SetCurrentItem(notice.runIndex)
		d.showRunLogs(notice.runIndex)
		return true
	}
	view := tview.NewTextView().SetText("Project setup failed. No failed setup operation is retained. Inspect Runs or the supervisor logs before retrying.")
	view.SetBorder(true).SetTitle(" Project setup · Esc to close ")
	d.modal = view
	d.SetRoot(view, true).SetFocus(view)
	return true
}
