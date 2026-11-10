package dashboard

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/rivo/tview"
	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

type runRow struct {
	id      string
	attempt model.Attempt
	started time.Time
	order   int64
}

func (d *Dashboard) refreshRuns() {
	rows := make([]runRow, 0, len(d.snapshot["sessions"]))
	for _, record := range d.snapshot["sessions"] {
		attempt, err := store.Decode[model.Attempt](record)
		if err != nil {
			d.updating = true
			d.runRows = nil
			d.runID = ""
			d.runs.Clear().AddItem("Cannot read run history", "", 0, nil)
			d.updating = false
			d.detail.SetText("Cannot read run history: " + err.Error())
			return
		}
		projectID := attempt.ProjectID
		if projectID == "" {
			_, task, ok := d.planRecord(attempt.Task)
			if ok {
				projectID = task.ProjectID
			}
		}
		if d.projectID != "" && projectID != d.projectID {
			continue
		}
		started, err := time.Parse(time.RFC3339Nano, attempt.StartedAt)
		if err != nil {
			started, _ = time.Parse(time.RFC3339Nano, record.UpdatedAt)
		}
		rows = append(rows, runRow{id: record.ID, attempt: attempt, started: started, order: record.CreatedOrder})
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if !rows[i].started.Equal(rows[j].started) {
			return rows[i].started.After(rows[j].started)
		}
		if rows[i].order != rows[j].order {
			return rows[i].order > rows[j].order
		}
		return rows[i].id > rows[j].id
	})
	d.updating = true
	d.runs.Clear()
	d.runRows = rows
	selected := 0
	for i, row := range rows {
		if row.id == d.runID {
			selected = i
		}
		attempt := row.attempt
		name := attempt.ProviderName
		if name == "" {
			name = attempt.Provider
		}
		modelName := attempt.ResolvedModel
		if modelName == "" {
			modelName = attempt.Model
		}
		if modelName == "" {
			modelName = "unknown model"
		}
		thinking := attempt.EffectiveThinking
		if thinking == nil {
			thinking = attempt.Thinking
		}
		effort := "not recorded"
		if thinking != nil {
			effort = thinking.Value
		}
		when := "time not recorded"
		if !row.started.IsZero() {
			when = row.started.Local().Format("2006-01-02 15:04")
		}
		d.runs.AddItem(tview.Escape(fmt.Sprintf("%s · %s · %s", attempt.Role, name, runStatus(attempt))), tview.Escape(fmt.Sprintf("%s · %s · %s", modelName, effort, when)), 0, nil)
	}
	if len(rows) == 0 {
		d.runID = ""
		d.runs.AddItem("No runs", "", 0, nil)
	} else {
		d.runs.SetCurrentItem(selected)
	}
	d.updating = false
	if !d.taskView {
		d.selectRun(selected, "", "", 0)
	}
}

func runStatus(attempt model.Attempt) string {
	if attempt.Status == "complete" {
		return "completed"
	}
	if attempt.Status == "" {
		return "unknown"
	}
	return attempt.Status
}

func (d *Dashboard) selectRun(index int, _, _ string, _ rune) {
	if d.updating || index < 0 || index >= len(d.runRows) {
		return
	}
	d.taskView = false
	row := d.runRows[index]
	d.runID = row.id
	d.planID = ""
	taskDetails := ""
	for i, task := range d.planRows {
		if task.ID == row.attempt.Task {
			d.plans.SetCurrentItem(i)
			d.selectPlan(i, "", "", 0)
			taskDetails = d.detail.GetText(false)
			break
		}
	}
	attempt := row.attempt
	var text strings.Builder
	fmt.Fprintf(&text, "Run: %s\nAgent: %s\nRole: %s\nModel: %s\nThinking: %s\nStatus: %s\nStarted: %s\nFinished: %s\n", row.id, attempt.Provider, attempt.Role, attemptModelDisplay(attempt), attemptThinkingDisplay(attempt), runStatus(attempt), recordedRuntimeValue(attempt.StartedAt), recordedRuntimeValue(attempt.FinishedAt))
	if attempt.Error != "" {
		fmt.Fprintf(&text, "Error: %s\n", attempt.Error)
	}
	if taskDetails != "" {
		text.WriteString("\n" + taskDetails)
	}
	d.detail.SetText(text.String())
}

func (d *Dashboard) chooseTask() {
	if len(d.planRows) == 0 {
		d.status.SetText("Whirlwind | no tasks for this project")
		return
	}
	d.modal = d.plans
	d.plans.SetTitle(" Tasks · Enter to inspect · Esc to close ")
	d.plans.SetSelectedFunc(func(index int, _, _ string, _ rune) {
		d.focus = 3
		d.closeModal()
		d.selectPlan(index, "", "", 0)
		d.taskView = true
		d.detail.SetTitle(" Task details · Esc to close ")
		d.modal = d.detail
		d.SetRoot(d.detail, true).SetFocus(d.detail)
	})
	d.SetRoot(d.plans, true).SetFocus(d.plans)
}
