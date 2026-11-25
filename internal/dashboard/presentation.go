package dashboard

import (
	"strings"

	"github.com/rivo/tview"

	"whirlwind/internal/control"
	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

// Present updates widgets on the terminal event loop, keeping durable identities
// selected even when polling changes the order of repository records.
func (d *Dashboard) Present(snapshot control.Snapshot) {
	d.updateRunnerStatus(snapshot)
	projects, err := decodeRecords[model.Project](snapshot["projects"])
	if err != nil {
		d.detail.SetText("Cannot read project state: " + err.Error())
		return
	}
	projects, err = d.configuredProjects(projects, snapshot)
	if err != nil {
		d.detail.SetText("Cannot read project health: " + err.Error())
		return
	}
	d.snapshot, d.projectRows, d.updating = snapshot, projects, true
	d.refreshAgents()
	d.projects.Clear()
	d.projectAliases = make(map[string]string)
	for _, project := range projects {
		for _, alias := range project.CheckoutAliases {
			d.projectAliases[alias] = project.ID
		}
	}
	selectedProject := d.checkoutProjectID(d.projectID)
	selected := 0
	for i, project := range projects {
		d.projects.AddItem(d.projectLabel(project), "", 0, nil)
		if project.ID == selectedProject {
			selected = i
		}
	}
	if len(projects) == 0 {
		d.projects.AddItem("No projects", "", 0, nil)
		d.projectID = ""
	} else {
		d.projectID = projects[selected].ID
		d.projects.SetCurrentItem(selected)
	}
	d.updating = false
	d.refreshDirections()
	d.refreshPlans()
	d.refreshRuns()
	d.refreshAgentInspection()
	d.refreshCurrentInbox()
	d.showProjectHealth()
}

func decodeRecords[T any](records []model.Record) ([]T, error) {
	values := make([]T, 0, len(records))
	for _, record := range records {
		value, err := store.Decode[T](record)
		if err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, nil
}

func (d *Dashboard) selectProject(index int, _, _ string, _ rune) {
	if d.updating || index < 0 || index >= len(d.projectRows) {
		return
	}
	d.taskView = false
	d.projectID, d.directionID, d.planID, d.runID = d.projectRows[index].ID, "", "", ""
	d.refreshDirections()
	d.refreshPlans()
	d.refreshRuns()
	d.refreshCurrentInbox()
	d.showProjectHealth()
}

func (d *Dashboard) refreshDirections() {
	if d.refreshIdeas() {
		return
	}
	state, err := directionGenerationState(d.snapshot["directions"], d.snapshot["inbox"], d.projectRows, d.projectID)
	if err != nil {
		d.detail.SetText("Cannot read direction state: " + err.Error())
		return
	}
	d.updating = true
	d.directions.Clear()
	d.directionRows = nil
	selected := d.appendDirectionRows(state)
	if len(d.directionRows) == 0 {
		d.directions.AddItem("No current directions", "", 0, nil)
		d.directionID = ""
	} else {
		d.directionID = d.directionRows[selected].ID
		d.directions.SetCurrentItem(selected)
	}
	d.updating = false
}

func (d *Dashboard) appendDirectionRows(state directionGenerationView) int {
	if state.current == nil {
		return 0
	}
	selected := 0
	for _, entry := range state.current.entries {
		direction := entry.direction
		if !isCurrentProposal(direction) {
			continue
		}
		if direction.ID == d.directionID {
			selected = len(d.directionRows)
		}
		d.directionRows = append(d.directionRows, direction)
		d.directions.AddItem(tview.Escape(directionLabel(direction, state.current.legacy)), "", 0, nil)
	}
	return selected
}

func isCurrentProposal(direction model.Direction) bool {
	return direction.Status == "" || direction.Status == "proposed" || direction.Status == "selected"
}

func directionLabel(direction model.Direction, legacy bool) string {
	label := "Ideation"
	if direction.Category != "" {
		label += " (" + strings.ToUpper(direction.Category[:1]) + direction.Category[1:] + ")"
	}
	if legacy {
		label = "Legacy · generation unknown · " + label
	}
	label += " - " + direction.Title
	if direction.Status == "selected" {
		label += " · selected"
	}
	return label
}

func (d *Dashboard) refreshCurrentInbox() {
	d.refreshInbox()
	d.updateDirectionInboxMessages()
	if d.hasCanonicalIdeas() {
		return
	}
	state, err := directionGenerationState(d.snapshot["directions"], d.snapshot["inbox"], d.projectRows, d.projectID)
	if err != nil || state.marker == "" {
		return
	}
	selected := d.inbox.GetCurrentItem()
	if d.inbox.GetItemCount() == 1 {
		first, _ := d.inbox.GetItemText(0)
		if first == "No inbox items" {
			d.inbox.RemoveItem(0)
			selected = 0
		}
	}
	insertAt := d.inbox.GetItemCount()
	d.inbox.InsertItem(insertAt, state.marker, "", 0, nil)
	d.inboxTaskIDs = append(d.inboxTaskIDs, "")
	copy(d.inboxTaskIDs[insertAt+1:], d.inboxTaskIDs[insertAt:len(d.inboxTaskIDs)-1])
	d.inboxTaskIDs[insertAt] = ""
	message := state.marker
	if state.paused != nil {
		message = state.paused.Message
	}
	d.inboxMessages = append(d.inboxMessages, "")
	copy(d.inboxMessages[insertAt+1:], d.inboxMessages[insertAt:len(d.inboxMessages)-1])
	d.inboxMessages[insertAt] = message
	if selected >= insertAt {
		selected++
	}
	if selected < d.inbox.GetItemCount() {
		d.inbox.SetCurrentItem(selected)
	}
}

func (d *Dashboard) updateDirectionInboxMessages() {
	for index, direction := range d.directionRows {
		if index < len(d.inboxMessages) {
			if idea, _, ok := d.ideaRecord(direction.ID); ok {
				d.inboxMessages[index] = ideaDetails(idea)
			} else {
				d.inboxMessages[index] = directionRationale(direction)
			}
		}
	}
}

func (d *Dashboard) selectDirection(index int, _, _ string, _ rune) {
	if d.updating || index < 0 || index >= len(d.directionRows) {
		return
	}
	d.directionID = d.directionRows[index].ID
}

func (d *Dashboard) refreshPlans() {
	plans, err := decodeRecords[model.ExecutionPlan](d.snapshot["execution_plans"])
	if err != nil {
		d.detail.SetText("Cannot read plan state: " + err.Error())
		return
	}
	d.updating = true
	d.plans.Clear()
	d.planRows = nil
	selected := 0
	for _, plan := range plans {
		if plan.ProjectID != d.projectID {
			continue
		}
		if plan.ID == d.planID {
			selected = len(d.planRows)
		}
		d.planRows = append(d.planRows, plan)
		d.plans.AddItem(tview.Escape(planTitle(plan)), "", 0, nil)
	}
	if len(d.planRows) == 0 {
		d.plans.AddItem("No plans", "", 0, nil)
		d.detail.SetText("No runs for this project.")
	} else {
		d.plans.SetCurrentItem(selected)
	}
	d.updating = false
	d.selectPlan(selected, "", "", 0)
}

func planTitle(plan model.ExecutionPlan) string {
	if plan.Plan != nil && plan.Plan.Objective != "" {
		return plan.Plan.Objective
	}
	if plan.Issue.Title != "" {
		return plan.Issue.Title
	}
	return plan.ID
}
