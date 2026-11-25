package dashboard

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/rivo/tview"

	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

type runRow struct {
	id        string
	attempt   model.Attempt
	started   time.Time
	order     int64
	operation *ideationRunView
}

type ideationRunView struct {
	model.Operation
	Directions []model.IdeationDirectionProgress `json:"ideation_directions"`
}

func (d *Dashboard) refreshRuns() {
	rows := d.appendOperationRuns(make([]runRow, 0, len(d.snapshot["sessions"])))
	rows, err := d.appendAttemptRuns(rows)
	if err != nil {
		d.updating = true
		d.runRows = nil
		d.runID = ""
		d.runs.Clear().AddItem("Cannot read run history", "", 0, nil)
		d.updating = false
		d.detail.SetText("Cannot read run history: " + err.Error())
		return
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
	d.renderRunRows(rows)
}

func (d *Dashboard) appendOperationRuns(rows []runRow) []runRow {
	for _, record := range d.snapshot["operations"] {
		var operation ideationRunView
		if err := json.Unmarshal(record.Data, &operation); err != nil {
			d.detail.SetText("Cannot read ideation history: " + err.Error())
			continue
		}
		if (operation.Kind != "ideation" && operation.Kind != "project_setup") || !d.runProjectMatches(operation.Subject) {
			continue
		}
		rows = append(rows, runRow{id: record.ID, operation: &operation, started: runStart(operation.StartedAt, record.UpdatedAt), order: record.CreatedOrder})
	}
	return rows
}

func (d *Dashboard) appendAttemptRuns(rows []runRow) ([]runRow, error) {
	for _, record := range d.snapshot["sessions"] {
		attempt, err := store.Decode[model.Attempt](record)
		if err != nil {
			return nil, err
		}
		projectID := attempt.ProjectID
		if projectID == "" {
			_, task, ok := d.planRecord(attempt.Task)
			if ok {
				projectID = task.ProjectID
			}
		}
		if !d.runProjectMatches(projectID) {
			continue
		}
		rows = append(rows, runRow{id: record.ID, attempt: attempt, started: runStart(attempt.StartedAt, record.UpdatedAt), order: record.CreatedOrder})
	}
	return rows, nil
}

func (d *Dashboard) runProjectMatches(projectID string) bool {
	return d.projectID == "" || d.checkoutProjectID(projectID) == d.projectID
}

func runStart(start, updated string) time.Time {
	started, err := time.Parse(time.RFC3339Nano, start)
	if err != nil {
		started, _ = time.Parse(time.RFC3339Nano, updated)
	}
	return started
}

func (d *Dashboard) renderRunRows(rows []runRow) {
	d.updating = true
	d.runs.Clear()
	d.runRows = rows
	selected := 0
	for index, row := range rows {
		if row.id == d.runID {
			selected = index
		}
		label, details := runRowText(row)
		d.runs.AddItem(tview.Escape(label), tview.Escape(details), 0, nil)
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

func runRowText(row runRow) (string, string) {
	if row.operation != nil {
		return operationRunLabel(*row.operation), row.id + " · " + recordedRuntimeValue(row.operation.StartedAt)
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
	return fmt.Sprintf("%s · %s · %s", attempt.Role, name, runStatus(attempt)), fmt.Sprintf("%s · %s · %s", modelName, effort, when)
}

func operationRunLabel(operation ideationRunView) string {
	if operation.Kind == "project_setup" {
		phase := projectSetupPhaseLabel(operation.Input)
		if operation.Stage == "failed" {
			return "Setup failed: " + phase
		}
		return "Project setup: " + phase + " · " + operation.Stage
	}
	if operation.RegistrationIdeation() {
		return "Auto ideation · " + operation.Stage
	}
	return "Ideation · " + operation.Stage
}

func runStatus(attempt model.Attempt) string {
	if attempt.Status == model.AttemptAwaitingPlanPublication {
		return "awaiting plan publication"
	}
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
	if row.operation != nil {
		d.detail.SetText(operationRunDetails(row.id, *row.operation))
		return
	}
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
	fmt.Fprintf(&text, "ACP activity: %s\n", providerActivity(attempt, time.Now()))
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
